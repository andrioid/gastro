package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/andrioid/gastro/internal/compiler"
	"github.com/andrioid/gastro/internal/scaffold"
	"github.com/andrioid/gastro/internal/watcher"
)

// Set at build time via -ldflags "-X main.version=..."
var version = "dev"

const (
	fileWatchInterval = 500 * time.Millisecond
	debounceDelay     = 200 * time.Millisecond
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "version", "--version", "-v":
		fmt.Println(version)
		return
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
	case "new":
		if err := runNew(); err != nil {
			fmt.Fprintf(os.Stderr, "gastro new: %v\n", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprintf(os.Stderr, "gastro %s\n", version)
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Usage: gastro <command>")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Commands:")
	fmt.Fprintln(os.Stderr, "  new <name>  Create a new gastro project")
	fmt.Fprintln(os.Stderr, "  generate    Compile .gastro files to .gastro/ directory")
	fmt.Fprintln(os.Stderr, "  build       Generate + go build -> single binary")
	fmt.Fprintln(os.Stderr, "  dev         Watch mode with hot reload (port 4242 or PORT env)")
	fmt.Fprintln(os.Stderr, "  version     Print version")
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

func runNew() error {
	if len(os.Args) < 3 {
		return fmt.Errorf("missing project name\n\nUsage: gastro new <name>")
	}

	name := os.Args[2]
	targetDir := name

	if info, err := os.Stat(targetDir); err == nil && info.IsDir() {
		return fmt.Errorf("directory %q already exists", targetDir)
	}

	fmt.Printf("gastro: creating project %q...\n", name)
	if err := scaffold.Generate(name, targetDir); err != nil {
		return err
	}

	// Run code generation so the project is immediately runnable.
	projectDir := targetDir
	outputDir := filepath.Join(projectDir, ".gastro")
	if err := compiler.Compile(projectDir, outputDir); err != nil {
		fmt.Fprintf(os.Stderr, "gastro: initial generate failed (run 'gastro generate' in the project dir): %v\n", err)
	}

	fmt.Println("gastro: done")
	fmt.Println("")
	fmt.Printf("  cd %s\n", name)
	fmt.Println("  gastro dev")
	fmt.Println("")
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
			appCmd = nil
		}
	}

	startApp()

	// Track the pending change type across the debounce window.
	// ChangeRestart wins over ChangeReload.
	var (
		pendingMu     sync.Mutex
		pendingChange = watcher.ChangeReload
	)

	escalate := func(ct watcher.ChangeType) {
		pendingMu.Lock()
		defer pendingMu.Unlock()
		if ct > pendingChange {
			pendingChange = ct
		}
	}

	consumePending := func() watcher.ChangeType {
		pendingMu.Lock()
		defer pendingMu.Unlock()
		ct := pendingChange
		pendingChange = watcher.ChangeReload
		return ct
	}

	debounced := watcher.Debounce(debounceDelay, func() {
		fmt.Println("gastro: changes detected, regenerating...")
		if err := runGenerate(); err != nil {
			fmt.Fprintf(os.Stderr, "gastro: generate failed: %v\n", err)
			return
		}

		ct := consumePending()
		if ct == watcher.ChangeRestart {
			startApp()
		} else {
			writeReloadSignal()
		}
	})

	// Polling watcher — checks file mod times, classifies changes
	go func() {
		modTimes := make(map[string]time.Time)
		fileContents := make(map[string]string)

		// Seed the initial state so we don't trigger on first scan.
		seedFiles := func(dir string, gastroOnly bool) {
			var files []string
			var err error
			if gastroOnly {
				files, err = watcher.CollectGastroFiles(dir)
			} else {
				files, err = watcher.CollectAllFiles(dir)
			}
			if err != nil {
				return
			}
			for _, f := range files {
				info, err := os.Stat(f)
				if err != nil {
					continue
				}
				modTimes[f] = info.ModTime()
				if gastroOnly {
					if content, err := os.ReadFile(f); err == nil {
						fileContents[f] = string(content)
					}
				}
			}
		}

		for _, dir := range []string{"pages", "components"} {
			seedFiles(dir, true)
		}
		seedFiles("static", false)

		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(fileWatchInterval):
			}

			changed := false

			// Track current files to detect deletions.
			currentFiles := make(map[string]bool)

			for _, dir := range []string{"pages", "components"} {
				files, err := watcher.CollectGastroFiles(dir)
				if err != nil {
					continue
				}
				for _, f := range files {
					currentFiles[f] = true
					info, err := os.Stat(f)
					if err != nil {
						continue
					}

					prev, known := modTimes[f]
					if !known {
						// New file added — needs full restart (new routes).
						fmt.Printf("gastro: new file %s\n", f)
						content, _ := os.ReadFile(f)
						fileContents[f] = string(content)
						modTimes[f] = info.ModTime()
						escalate(watcher.ChangeRestart)
						changed = true
						continue
					}

					if info.ModTime().After(prev) {
						content, err := os.ReadFile(f)
						if err != nil {
							continue
						}
						newContent := string(content)
						oldContent := fileContents[f]

						section := watcher.DetectChangedSection(oldContent, newContent)
						ct := watcher.ClassifyChange(f, section)

						label := "template"
						if ct == watcher.ChangeRestart {
							label = "frontmatter"
						}
						fmt.Printf("gastro: %s changed (%s)\n", f, label)

						fileContents[f] = newContent
						modTimes[f] = info.ModTime()
						escalate(ct)
						changed = true
					}
				}
			}

			// Watch static/ files
			if files, err := watcher.CollectAllFiles("static"); err == nil {
				for _, f := range files {
					currentFiles[f] = true
					info, err := os.Stat(f)
					if err != nil {
						continue
					}

					prev, known := modTimes[f]
					if !known {
						fmt.Printf("gastro: new file %s\n", f)
						modTimes[f] = info.ModTime()
						escalate(watcher.ChangeReload)
						changed = true
						continue
					}

					if info.ModTime().After(prev) {
						fmt.Printf("gastro: %s changed (static)\n", f)
						modTimes[f] = info.ModTime()
						escalate(watcher.ClassifyChange(f, watcher.SectionUnknown))
						changed = true
					}
				}
			}

			// Detect deleted files.
			for f := range modTimes {
				if !currentFiles[f] {
					fmt.Printf("gastro: %s deleted\n", f)
					delete(modTimes, f)
					delete(fileContents, f)
					escalate(watcher.ChangeRestart)
					changed = true
				}
			}

			if changed {
				debounced()
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

// writeReloadSignal writes the .gastro/.reload file that signals the running
// dev server to notify connected browsers via SSE.
func writeReloadSignal() {
	if err := os.MkdirAll(".gastro", 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "gastro: failed to create .gastro dir: %v\n", err)
		return
	}
	if err := os.WriteFile(".gastro/.reload", []byte(time.Now().String()), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "gastro: failed to write reload signal: %v\n", err)
	}
}
