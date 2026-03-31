package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/andrioid/gastro/internal/compiler"
	"github.com/andrioid/gastro/internal/watcher"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "generate":
		if err := runGenerate(); err != nil {
			fmt.Fprintf(os.Stderr, "gastro generate: %v\n", err)
			os.Exit(1)
		}
	case "build":
		if err := runBuild(); err != nil {
			fmt.Fprintf(os.Stderr, "gastro build: %v\n", err)
			os.Exit(1)
		}
	case "dev":
		if err := runDev(); err != nil {
			fmt.Fprintf(os.Stderr, "gastro dev: %v\n", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprintln(os.Stderr, "Usage: gastro <command>")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Commands:")
	fmt.Fprintln(os.Stderr, "  generate    Compile .gastro files to .gastro/ directory")
	fmt.Fprintln(os.Stderr, "  build       Generate + go build -> single binary")
	fmt.Fprintln(os.Stderr, "  dev         Watch mode with hot reload (port 4242 or PORT env)")
}

func runGenerate() error {
	projectDir := "."
	outputDir := filepath.Join(projectDir, ".gastro")

	fmt.Println("gastro: generating code...")
	if err := compiler.Compile(projectDir, outputDir); err != nil {
		return err
	}

	fmt.Println("gastro: done")
	return nil
}

func runBuild() error {
	if err := runGenerate(); err != nil {
		return err
	}

	fmt.Println("gastro: building binary...")
	cmd := exec.Command("go", "build", "-o", "app", ".")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("go build failed: %w", err)
	}

	fmt.Println("gastro: build complete -> ./app")
	return nil
}

func runDev() error {
	port := os.Getenv("PORT")
	if port == "" {
		port = "4242"
	}

	// Initial generation
	if err := runGenerate(); err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle interrupt signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Println("\ngastro: shutting down...")
		cancel()
	}()

	// Build and start the app
	var appCmd *exec.Cmd
	startApp := func() {
		if appCmd != nil && appCmd.Process != nil {
			appCmd.Process.Signal(syscall.SIGTERM)
			appCmd.Wait()
		}

		fmt.Println("gastro: building...")
		build := exec.Command("go", "build", "-o", ".gastro/dev-server", ".")
		build.Stdout = os.Stdout
		build.Stderr = os.Stderr
		if err := build.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "gastro: build failed: %v\n", err)
			return
		}

		fmt.Printf("gastro: starting server on :%s\n", port)
		appCmd = exec.CommandContext(ctx, ".gastro/dev-server")
		appCmd.Env = append(os.Environ(), "GASTRO_DEV=1", "PORT="+port)
		appCmd.Stdout = os.Stdout
		appCmd.Stderr = os.Stderr
		if err := appCmd.Start(); err != nil {
			fmt.Fprintf(os.Stderr, "gastro: start failed: %v\n", err)
		}
	}

	startApp()

	// Watch for changes using polling (no external dependency)
	debounced := watcher.Debounce(200*time.Millisecond, func() {
		fmt.Println("gastro: changes detected, regenerating...")
		if err := runGenerate(); err != nil {
			fmt.Fprintf(os.Stderr, "gastro: generate failed: %v\n", err)
			return
		}
		startApp()
	})

	// Simple polling watcher — checks file mod times
	go func() {
		modTimes := make(map[string]time.Time)
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(500 * time.Millisecond):
			}

			for _, dir := range []string{"pages", "components"} {
				files, err := watcher.CollectGastroFiles(dir)
				if err != nil {
					continue
				}
				for _, f := range files {
					info, err := os.Stat(f)
					if err != nil {
						continue
					}
					if prev, ok := modTimes[f]; ok && info.ModTime().After(prev) {
						debounced()
					}
					modTimes[f] = info.ModTime()
				}
			}

			// Watch all files in static/ (not just .gastro)
			if files, err := watcher.CollectAllFiles("static"); err == nil {
				for _, f := range files {
					info, err := os.Stat(f)
					if err != nil {
						continue
					}
					if prev, ok := modTimes[f]; ok && info.ModTime().After(prev) {
						debounced()
					}
					modTimes[f] = info.ModTime()
				}
			}
		}
	}()

	// Wait for context cancellation
	<-ctx.Done()

	// Clean up
	if appCmd != nil && appCmd.Process != nil {
		appCmd.Process.Signal(syscall.SIGTERM)
		appCmd.Wait()
	}

	return nil
}
