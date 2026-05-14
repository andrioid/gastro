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
	"github.com/andrioid/gastro/internal/devloop"
	"github.com/andrioid/gastro/internal/format"
	"github.com/andrioid/gastro/internal/lsp"
	"github.com/andrioid/gastro/internal/scaffold"
)

// Set at build time via -ldflags "-X main.version=..."
var version = "dev"

// Watch / debounce timing now lives in internal/devloop (defaultDebounce,
// defaultPollInterval). runDev leaves them at the package defaults so the
// observable behaviour of `gastro dev` is unchanged from before the
// extraction.

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
		applyGastroProject()
		if _, err := runGenerate(true); err != nil {
			fmt.Fprintf(os.Stderr, "gastro generate: %v\n", err)
			os.Exit(1)
		}
	case "build":
		applyGastroProject()
		if err := runBuild(); err != nil {
			fmt.Fprintf(os.Stderr, "gastro build: %v\n", err)
			os.Exit(1)
		}
	case "dev":
		devWatch, err := parseDevWatchFlags(os.Args[2:])
		if err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			os.Exit(1)
		}
		applyGastroProject()
		if err := runDev(devWatch); err != nil {
			fmt.Fprintf(os.Stderr, "gastro dev: %v\n", err)
			os.Exit(1)
		}
	case "watch":
		applyGastroProject()
		if err := runWatch(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "gastro watch: %v\n", err)
			os.Exit(1)
		}
	case "new":
		// `new` takes a target dir as an argument; GASTRO_PROJECT does not apply.
		if err := runNew(); err != nil {
			fmt.Fprintf(os.Stderr, "gastro new: %v\n", err)
			os.Exit(1)
		}
	case "fmt":
		// `fmt` honours GASTRO_PROJECT only when no targets are given (handled inside runFmt).
		if err := runFmt(); err != nil {
			fmt.Fprintf(os.Stderr, "gastro fmt: %v\n", err)
			os.Exit(1)
		}
	case "list":
		applyGastroProject()
		if err := runList(); err != nil {
			fmt.Fprintf(os.Stderr, "gastro list: %v\n", err)
			os.Exit(1)
		}
	case "check":
		applyGastroProject()
		if err := runCheck(); err != nil {
			if err == errDrift {
				// Drift: the diff has already been printed by runCheck.
				os.Exit(1)
			}
			fmt.Fprintf(os.Stderr, "gastro check: %v\n", err)
			os.Exit(2)
		}
	case "lsp":
		// LSP applies GASTRO_PROJECT internally per-file (see internal/lsp/server).
		lsp.Serve(version)
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

// applyGastroProject changes the working directory to GASTRO_PROJECT if the
// env var is set. It exits with a clear error if the value is invalid.
// Called by project-level commands (generate, build, dev, list, check) and
// optionally by fmt when no targets are supplied.
func applyGastroProject() {
	root := os.Getenv("GASTRO_PROJECT")
	if root == "" {
		return
	}

	abs, err := filepath.Abs(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gastro: GASTRO_PROJECT %q: %v\n", root, err)
		os.Exit(1)
	}

	info, err := os.Stat(abs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gastro: GASTRO_PROJECT %q: %v\n", root, err)
		os.Exit(1)
	}
	if !info.IsDir() {
		fmt.Fprintf(os.Stderr, "gastro: GASTRO_PROJECT %q is not a directory\n", root)
		os.Exit(1)
	}

	if err := os.Chdir(abs); err != nil {
		fmt.Fprintf(os.Stderr, "gastro: cannot chdir to GASTRO_PROJECT %q: %v\n", root, err)
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
	fmt.Fprintln(os.Stderr, "  watch       Watch mode for library-mode projects (gastro embedded in an existing Go service)")
	fmt.Fprintln(os.Stderr, "  fmt         Format .gastro files")
	fmt.Fprintln(os.Stderr, "  check       Verify .gastro/ matches the source (CI gate)")
	fmt.Fprintln(os.Stderr, "  list        List all components and pages with their props (--json for machine output)")
	fmt.Fprintln(os.Stderr, "  version     Print version")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Environment:")
	fmt.Fprintln(os.Stderr, "  GASTRO_PROJECT   Path to the gastro project root. When set, the CLI")
	fmt.Fprintln(os.Stderr, "                   chdir's here before running project-level commands.")
	fmt.Fprintln(os.Stderr, "                   Useful for nested projects (e.g. internal/web/).")
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
		// No targets and no stdin — honour GASTRO_PROJECT (if set) before
		// defaulting to current directory. User-supplied targets are sacred
		// (relative to the user's cwd), so we only chdir when no targets are
		// given. Stdin mode also skips it because the path is conceptual.
		applyGastroProject()
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

func runGenerate(strict bool) (*compiler.CompileResult, error) {
	projectDir := "."
	outputDir := filepath.Join(projectDir, ".gastro")

	start := time.Now()
	result, err := compiler.Compile(projectDir, outputDir, compiler.CompileOptions{Strict: strict})
	if err != nil {
		return nil, err
	}

	for _, w := range result.Warnings {
		fmt.Fprintf(os.Stderr, "gastro: warning: %s:%d: %s\n", w.File, w.Line, w.Message)
	}

	elapsed := time.Since(start).Round(time.Millisecond)
	fmt.Printf("gastro: done (%d components, %d pages, %s)\n",
		result.ComponentCount, result.PageCount, elapsed)
	return result, nil
}

func runBuild() error {
	if _, err := runGenerate(true); err != nil {
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

	arg := os.Args[2]
	targetDir := arg

	// When the user passes a path like /tmp/foo or ./examples/foo, use the
	// final path component as the Go module name. Module paths cannot start
	// with a slash, so passing the full path would generate an invalid
	// go.mod. Users who want a fully-qualified module path
	// (github.com/user/repo) can edit go.mod afterwards or pass the basename
	// and rename the module manually.
	name := filepath.Base(filepath.Clean(arg))

	if info, err := os.Stat(targetDir); err == nil && info.IsDir() {
		return fmt.Errorf("directory %q already exists", targetDir)
	}

	fmt.Printf("gastro: creating project %q at %s...\n", name, targetDir)
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

func runDev(extraWatch []string) error {
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

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// SIGINT/SIGTERM — cancel the loop, then the deferred app cleanup
	// below kills the child process.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Println("\ngastro: shutting down...")
		cancel()
	}()

	// App lifecycle. startApp is called by devloop's OnRestart hook —
	// once at startup, then again on every restart-class change.
	var (
		appMu  sync.Mutex
		appCmd *exec.Cmd
	)
	startApp := func(loopCtx context.Context) error {
		appMu.Lock()
		defer appMu.Unlock()

		if appCmd != nil && appCmd.Process != nil {
			appCmd.Process.Signal(syscall.SIGTERM)
			appCmd.Wait()
			appCmd = nil
		}

		fmt.Println("gastro: building...")
		build := exec.Command("go", "build", "-o", ".gastro/dev-server", ".")
		build.Stdout = os.Stdout
		build.Stderr = os.Stderr
		if err := build.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "gastro: build failed: %v\n", err)
			// Match historical behaviour: log and continue. devloop will
			// retry on the next restart-class change.
			return nil
		}

		fmt.Printf("gastro: starting server on :%s\n", port)
		cmd := exec.CommandContext(loopCtx, ".gastro/dev-server")
		cmd.Env = append(os.Environ(), "GASTRO_DEV=1", "PORT="+port)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Start(); err != nil {
			fmt.Fprintf(os.Stderr, "gastro: start failed: %v\n", err)
			return nil
		}
		appCmd = cmd
		return nil
	}

	// Run the watch loop. Generate hooks into the existing runGenerate
	// helper (lenient — warnings don't block dev). OnReload writes the
	// .gastro/.reload signal that pkg/gastro/devreload.go polls. Note
	// that PollInterval and DebounceDelay are left at the package
	// defaults (500ms / 200ms) so observable behaviour matches what
	// `gastro dev` shipped before the devloop extraction.
	loopErr := devloop.Run(ctx, devloop.Config{
		Generate: func() ([]string, error) {
			result, err := runGenerate(false)
			if err != nil {
				return nil, err
			}
			return result.EmbedDeps, nil
		},
		OnRestart:  startApp,
		OnReload:   writeReloadSignal,
		ExtraWatch: extraWatch,
	})

	// Cleanup: kill the running app process. devloop.Run has already
	// returned (ctx is cancelled), so no new OnRestart will race here.
	appMu.Lock()
	if appCmd != nil && appCmd.Process != nil {
		appCmd.Process.Signal(syscall.SIGTERM)
		appCmd.Wait()
	}
	appMu.Unlock()

	return loopErr
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
