package main

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/andrioid/gastro/internal/compiler"
	"github.com/andrioid/gastro/internal/format"
	"github.com/andrioid/gastro/internal/lsp"
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
		if err := runGenerate(true); err != nil {
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
	case "fmt":
		if err := runFmt(); err != nil {
			fmt.Fprintf(os.Stderr, "gastro fmt: %v\n", err)
			os.Exit(1)
		}
	case "lsp":
		lsp.Serve(version)
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
	fmt.Fprintln(os.Stderr, "  fmt         Format .gastro files")
	fmt.Fprintln(os.Stderr, "  version     Print version")
}

var skipDirs = map[string]bool{
	".gastro":      true,
	".git":         true,
	"node_modules": true,
	"vendor":       true,
}

func runFmt() error {
	args := os.Args[2:]

	check := false
	stdinFilepath := "<stdin>"
	var targets []string

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--check":
			check = true
		case "--stdin-filepath":
			if i+1 < len(args) {
				i++
				stdinFilepath = args[i]
			}
		default:
			targets = append(targets, args[i])
		}
	}

	// Stdin mode: no targets and stdin is a pipe
	if len(targets) == 0 {
		stat, _ := os.Stdin.Stat()
		if (stat.Mode() & os.ModeCharDevice) == 0 {
			return fmtStdin(stdinFilepath, check)
		}
		// No targets and no stdin — default to current directory
		targets = []string{"."}
	}

	// Collect all .gastro files from targets
	var files []string
	for _, target := range targets {
		info, err := os.Stat(target)
		if err != nil {
			return fmt.Errorf("cannot access %s: %w", target, err)
		}
		if info.IsDir() {
			found, err := collectGastroFiles(target)
			if err != nil {
				return err
			}
			files = append(files, found...)
		} else if strings.HasSuffix(target, ".gastro") {
			files = append(files, target)
		}
	}

	if len(files) == 0 {
		return nil
	}

	return fmtFiles(files, check)
}

func fmtStdin(filepath string, check bool) error {
	content, err := io.ReadAll(os.Stdin)
	if err != nil {
		return fmt.Errorf("read stdin: %w", err)
	}

	formatted, changed, err := format.FormatFile(filepath, string(content))
	if err != nil {
		return err
	}

	if check {
		if changed {
			fmt.Fprintln(os.Stderr, filepath)
			return fmt.Errorf("not formatted")
		}
		return nil
	}

	_, err = os.Stdout.WriteString(formatted)
	return err
}

func fmtFiles(files []string, check bool) error {
	maxWorkers := runtime.NumCPU()
	if maxWorkers > 8 {
		maxWorkers = 8
	}

	type result struct {
		file    string
		changed bool
		err     error
	}

	results := make(chan result, len(files))
	sem := make(chan struct{}, maxWorkers)

	var wg sync.WaitGroup
	for _, file := range files {
		wg.Add(1)
		go func(f string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			content, err := os.ReadFile(f)
			if err != nil {
				results <- result{file: f, err: err}
				return
			}

			formatted, changed, err := format.FormatFile(f, string(content))
			if err != nil {
				results <- result{file: f, err: err}
				return
			}

			if !changed {
				results <- result{file: f}
				return
			}

			if !check {
				if err := atomicWriteFile(f, []byte(formatted)); err != nil {
					results <- result{file: f, err: err}
					return
				}
			}

			results <- result{file: f, changed: true}
		}(file)
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	var hadErrors int32
	var changedCount int32
	var errMsgs []string

	for res := range results {
		if res.err != nil {
			atomic.AddInt32(&hadErrors, 1)
			errMsgs = append(errMsgs, fmt.Sprintf("%s: %v", res.file, res.err))
			fmt.Fprintf(os.Stderr, "Error: %s: %v\n", res.file, res.err)
			continue
		}
		if res.changed {
			atomic.AddInt32(&changedCount, 1)
			if check {
				fmt.Fprintln(os.Stderr, res.file)
			} else {
				fmt.Printf("formatted %s\n", res.file)
			}
		}
	}

	if hadErrors > 0 {
		return fmt.Errorf("%d file(s) had errors", hadErrors)
	}

	if check && changedCount > 0 {
		return fmt.Errorf("%d file(s) not formatted", changedCount)
	}

	return nil
}

// atomicWriteFile writes data to a file atomically using a temp file + rename.
func atomicWriteFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".gastro-fmt-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()

	defer func() {
		tmp.Close()
		os.Remove(tmpPath)
	}()

	if _, err := tmp.Write(data); err != nil {
		return err
	}

	// Preserve original file permissions
	if info, statErr := os.Stat(path); statErr == nil {
		tmp.Chmod(info.Mode())
	}

	if err := tmp.Close(); err != nil {
		return err
	}

	return os.Rename(tmpPath, path)
}

// collectGastroFiles recursively finds all .gastro files in a directory,
// skipping generated/hidden/vendor directories.
func collectGastroFiles(root string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && skipDirs[d.Name()] {
			return filepath.SkipDir
		}
		if !d.IsDir() && strings.HasSuffix(path, ".gastro") {
			files = append(files, path)
		}
		return nil
	})
	return files, err
}

func runGenerate(strict bool) error {
	projectDir := "."
	outputDir := filepath.Join(projectDir, ".gastro")

	fmt.Println("gastro: generating code...")
	result, err := compiler.Compile(projectDir, outputDir, compiler.CompileOptions{Strict: strict})
	if err != nil {
		return err
	}

	for _, w := range result.Warnings {
		fmt.Fprintf(os.Stderr, "gastro: warning: %s:%d: %s\n", w.File, w.Line, w.Message)
	}

	fmt.Println("gastro: done")
	return nil
}

func runBuild() error {
	if err := runGenerate(true); err != nil {
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
	if err := scaffold.Generate(name, targetDir, version); err != nil {
		return err
	}

	// Run code generation so the project is immediately runnable.
	projectDir := targetDir
	outputDir := filepath.Join(projectDir, ".gastro")
	if _, err := compiler.Compile(projectDir, outputDir, compiler.CompileOptions{}); err != nil {
		fmt.Fprintf(os.Stderr, "gastro: initial generate failed (run 'gastro generate' in the project dir): %v\n", err)
	}

	// Populate go.sum so the project builds without extra steps.
	tidy := exec.Command("go", "mod", "tidy")
	tidy.Dir = projectDir
	if out, err := tidy.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "gastro: go mod tidy failed (run manually later): %v\n%s", err, out)
	}

	fmt.Println("gastro: done")
	fmt.Println("")
	fmt.Printf("  cd %s\n", name)
	fmt.Println("  gastro dev")
	fmt.Println("")
	return nil
}

// findAvailablePort tries to bind to each port in [startPort, startPort+10)
// and returns the first one that is available.
func findAvailablePort(startPort int) (string, error) {
	for port := startPort; port < startPort+10; port++ {
		ln, err := net.Listen("tcp", ":"+strconv.Itoa(port))
		if err != nil {
			continue
		}
		ln.Close()
		return strconv.Itoa(port), nil
	}
	return "", fmt.Errorf("no available port found in range %d-%d", startPort, startPort+9)
}

func runDev() error {
	port := os.Getenv("PORT")
	if port == "" {
		p, err := findAvailablePort(4242)
		if err != nil {
			return err
		}
		if p != "4242" {
			fmt.Printf("gastro: port 4242 is in use, using http://localhost:%s\n", p)
		}
		port = p
	}

	// Initial generation (non-strict in dev mode — warnings don't block)
	if err := runGenerate(false); err != nil {
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
		if err := runGenerate(false); err != nil {
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

		// Markdown files can live anywhere in the project, so walking the
		// whole tree on every poll is wasteful. Cache the discovered list
		// and rewalk only every markdownRescanEvery ticks; stat the cached
		// list every tick for fast change detection.
		var markdownCache []string
		var markdownTick uint
		const markdownRescanEvery = 10

		rescanMarkdown := func() {
			files, err := watcher.CollectMarkdownFiles(".")
			if err != nil {
				return
			}
			markdownCache = files
		}

		seedMarkdown := func() {
			rescanMarkdown()
			for _, f := range markdownCache {
				info, err := os.Stat(f)
				if err != nil {
					continue
				}
				modTimes[f] = info.ModTime()
			}
		}

		for _, dir := range []string{"pages", "components"} {
			seedFiles(dir, true)
		}
		seedFiles("static", false)
		seedMarkdown()

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

			// Watch markdown files anywhere in the project (referenced by
			// {{ markdown "..." }} directives in .gastro files). Rewalking
			// the whole tree every tick is expensive, so rescan only every
			// markdownRescanEvery ticks; in between, stat the cached list.
			markdownTick++
			if markdownTick%markdownRescanEvery == 0 {
				rescanMarkdown()
			}
			for _, f := range markdownCache {
				currentFiles[f] = true
				info, err := os.Stat(f)
				if err != nil {
					// Stale cache entry (file deleted between rewalks).
					// Unmark so the deletion loop below drops it from
					// modTimes.
					delete(currentFiles, f)
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
					fmt.Printf("gastro: %s changed (markdown)\n", f)
					modTimes[f] = info.ModTime()
					escalate(watcher.ChangeReload)
					changed = true
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
