package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/andrioid/gastro/internal/devloop"
	shlexLib "github.com/google/shlex"
)

// devFlagRejectionMessage is the canonical multi-line error printed when
// `gastro dev` is invoked with any flag. The plan (\u00a73 Q5 + \u00a74) defines
// this as the single source of truth so the test (TestDev_RejectsUnknownFlags)
// asserts against the exact constant the production code uses.
//
// First line is the colon-prefixed error so it groups with the rest of the
// CLI's "gastro <cmd>: ..." output. The follow-up lines spell out the
// alternative for users who landed here from a habit of passing flags to
// `air` / `wgo` / similar.
func devFlagRejectionMessage(flag string) string {
	return fmt.Sprintf("gastro dev: unknown flag %s\n"+
		"gastro dev takes no flags. For custom build/asset/run commands, use:\n"+
		"  gastro watch --asset '...' --build '...' --run '...'", flag)
}

// validateDevArgs implements the Q5 flag-rejection contract. Returns nil
// when args is empty; an error containing devFlagRejectionMessage otherwise.
// The first looks-like-a-flag arg drives the message because reporting more
// than one would obscure the suggested fix.
//
// Exception: --watch GLOB is allowed (and only this flag). It registers an
// additional glob pattern with the dev-mode watcher (Phase 4, see
// docs/dev-mode.md). The flag is repeatable; comma-separated values are
// also accepted.
func validateDevArgs(args []string) error {
	_, err := parseDevWatchFlags(args)
	return err
}

// parseDevWatchFlags extracts `--watch GLOB[,GLOB...]` repetitions from a
// `gastro dev` argv. Any other flag-like or positional argument is
// rejected with the devFlagRejectionMessage. Returns the deduplicated
// list of glob patterns to forward to devloop.Config.ExtraWatch.
func parseDevWatchFlags(args []string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	i := 0
	for i < len(args) {
		a := args[i]
		var value string
		var hasValue bool

		if eq := strings.IndexByte(a, '='); eq > 0 && strings.HasPrefix(a, "--") {
			value = a[eq+1:]
			a = a[:eq]
			hasValue = true
		}
		if a != "--watch" {
			return nil, errors.New(devFlagRejectionMessage(a))
		}

		if !hasValue {
			if i+1 >= len(args) {
				return nil, errors.New("--watch needs a value")
			}
			value = args[i+1]
			i++
		}
		i++

		for _, g := range strings.Split(value, ",") {
			g = strings.TrimSpace(g)
			if g == "" || seen[g] {
				continue
			}
			seen[g] = true
			out = append(out, g)
		}
	}
	return out, nil
}

// watchFlags is the parsed result of `gastro watch ...`'s argv. Build is
// repeatable to support multi-step pipelines (the canonical example is
// tailwindcss-then-go-build); Run is single because exactly one
// long-running process makes sense per watch session.
type watchFlags struct {
	Run        string
	Build      []string
	Asset      []string
	Excludes   []string
	Project    string
	WatchRoot  string
	Debounce   time.Duration
	Quiet      bool
	ExtraWatch []string
}

// parseWatchArgs is a hand-rolled parser rather than flag.NewFlagSet
// because flag's repeatable-flag story (the user has to define a custom
// flag.Value type) is more code than just walking argv. Supports both
// long forms (--run X, --run=X) and short forms (-r X) for the small set
// the plan defines.
//
// Errors carry the offending argument so the user sees `--build needs a
// value` rather than `flag provided but not defined: -build`.
func parseWatchArgs(args []string) (watchFlags, error) {
	fl := watchFlags{}
	i := 0
	for i < len(args) {
		a := args[i]
		consumed := 1
		var value string
		var hasValue bool

		// --flag=value form: split on first '=' and treat the suffix as
		// an inline value, no extra consume.
		if eq := strings.IndexByte(a, '='); eq > 0 && strings.HasPrefix(a, "--") {
			value = a[eq+1:]
			a = a[:eq]
			hasValue = true
		}

		switch a {
		case "--run", "-r":
			if !hasValue {
				if i+1 >= len(args) {
					return fl, fmt.Errorf("%s needs a value", a)
				}
				value = args[i+1]
				consumed = 2
			}
			if fl.Run != "" {
				return fl, errors.New("--run can only be specified once")
			}
			fl.Run = value
		case "--build", "-b":
			if !hasValue {
				if i+1 >= len(args) {
					return fl, fmt.Errorf("%s needs a value", a)
				}
				value = args[i+1]
				consumed = 2
			}
			fl.Build = append(fl.Build, value)
		case "--asset", "-a":
			// --asset is the reload-aware sibling of --build. Runs on
			// every change (both reload-class and restart-class), before
			// the --build chain on restart. Use for content-derived
			// asset generators (Tailwind, esbuild, image opt) that need
			// to re-run whenever a template body changes -- RELOAD-class
			// changes skip --build entirely and would otherwise miss
			// asset regeneration. See docs/dev-mode.md.
			if !hasValue {
				if i+1 >= len(args) {
					return fl, fmt.Errorf("%s needs a value", a)
				}
				value = args[i+1]
				consumed = 2
			}
			fl.Asset = append(fl.Asset, value)
		case "--exclude":
			if !hasValue {
				if i+1 >= len(args) {
					return fl, fmt.Errorf("%s needs a value", a)
				}
				value = args[i+1]
				consumed = 2
			}
			fl.Excludes = append(fl.Excludes, value)
		case "--project", "-p":
			if !hasValue {
				if i+1 >= len(args) {
					return fl, fmt.Errorf("%s needs a value", a)
				}
				value = args[i+1]
				consumed = 2
			}
			fl.Project = value
		case "--watch-root":
			if !hasValue {
				if i+1 >= len(args) {
					return fl, fmt.Errorf("%s needs a value", a)
				}
				value = args[i+1]
				consumed = 2
			}
			if fl.WatchRoot != "" {
				return fl, errors.New("--watch-root can only be specified once")
			}
			fl.WatchRoot = value
		case "--debounce":
			if !hasValue {
				if i+1 >= len(args) {
					return fl, fmt.Errorf("%s needs a value", a)
				}
				value = args[i+1]
				consumed = 2
			}
			d, err := time.ParseDuration(value)
			if err != nil {
				return fl, fmt.Errorf("--debounce %q: %w", value, err)
			}
			fl.Debounce = d
		case "--watch":
			if !hasValue {
				if i+1 >= len(args) {
					return fl, fmt.Errorf("%s needs a value", a)
				}
				value = args[i+1]
				consumed = 2
			}
			for _, g := range strings.Split(value, ",") {
				if g = strings.TrimSpace(g); g != "" {
					fl.ExtraWatch = append(fl.ExtraWatch, g)
				}
			}
		case "--quiet", "-q":
			fl.Quiet = true
		case "--help", "-h":
			return fl, errHelp
		default:
			return fl, fmt.Errorf("unknown flag %s\nrun `gastro watch --help` for usage", a)
		}
		i += consumed
	}
	return fl, nil
}

// errHelp signals that the user requested help — handled by the caller
// printing the usage block and exiting cleanly.
var errHelp = errors.New("help requested")

// watchUsage is printed on `gastro watch --help` or on a usage error.
const watchUsage = `gastro watch — watch .gastro and Go sources, regenerate on change, signal
browser reloads, and manage your application binary with smart classification.
For projects where 'gastro new' conventions don't apply.

We built this so you don't have to install air, wgo, or watchexec just to
get a hot-reload loop for a gastro-in-Go-project setup. For more advanced
or composed workflows, see the watchexec recipe in docs/dev-mode.md.

Usage: gastro watch --run COMMAND [--build COMMAND]... [flags]

Required:
  -r, --run COMMAND       Command to run your binary
                          (e.g. "go run ./cmd/myapp" or "tmp/app --port 8080")

Optional:
  -b, --build COMMAND     Command to compile before each (re)start.
                          Repeat for multi-step pipelines:
                            --build "go build -o tmp/app ./cmd/myapp"
                          On build failure, the previous --run keeps running.
                          NOTE: --build runs ONLY on restart-class changes
                          (frontmatter, Go sources, embedded deps).
                          Template-body edits are reload-class and skip
                          --build entirely. Put asset generators in --asset.
  -a, --asset COMMAND     Command to regenerate content-derived assets
                          (Tailwind, esbuild, image opt) before every change
                          -- both reload-class and restart-class.
                          Repeat for multi-step pipelines:
                            --asset "tailwindcss -i in.css -o out.css"
                          On asset-chain failure, the browser reload is
                          suppressed so it doesn't render onto stale assets,
                          and the previous --run keeps running. Errors are
                          surfaced via .gastro/.build-error.
                          On restart-class changes, --asset runs before
                          --build.
  -p, --project PATH      Path to the gastro project root
                          (defaults to GASTRO_PROJECT env, then cwd)
      --watch-root PATH   Override the directory walked for *.go changes.
                          Defaults to the nearest enclosing go.mod
                          (walking up from --project / GASTRO_PROJECT,
                          stopping at .git/ or $HOME). Falls back to the
                          project root when no go.mod is found.
      --exclude PATH      Path to ignore when watching .go files.
                          Repeat for multiple. Hardcoded defaults already
                          exclude any directory named .gastro, vendor,
                          node_modules, .git, or tmp (matched anywhere
                          in the watched tree).
      --debounce DUR      Coalesce burst changes (default 200ms)
  -q, --quiet             Suppress per-change logs
  -h, --help              Show this help

Example:
  gastro watch --run 'go run ./cmd/myapp'
  gastro watch --build 'go build -o tmp/app ./cmd/myapp' --run 'tmp/app'
  gastro watch \
    --asset 'tailwindcss -i tailwind.css -o static/styles.css' \
    --build 'go build -o tmp/app .' \
    --run   'tmp/app'
`

// runWatch is the entry point for `gastro watch`. Parses argv, sets up
// the build/run lifecycle, and drives devloop.Run with WatchGoFiles=true.
//
// The lifecycle is:
//  1. Initial onRestart: run all --build commands, then start --run.
//     Build failure → log + write build-error signal + don't start run.
//  2. On each restart-class change: cancel any in-flight build (R3),
//     stop the previous --run (R4 keep-alive only on FAILURE; on
//     success we replace), run builds in order, start fresh --run.
func runWatch(args []string) error {
	flags, err := parseWatchArgs(args)
	if err != nil {
		if errors.Is(err, errHelp) {
			fmt.Fprint(os.Stderr, watchUsage)
			return nil
		}
		fmt.Fprint(os.Stderr, watchUsage)
		return err
	}
	if flags.Run == "" {
		fmt.Fprint(os.Stderr, watchUsage)
		return errors.New("--run is required")
	}

	// --project overrides GASTRO_PROJECT for this invocation. Mirrors
	// applyGastroProject's behaviour but routes through the same chdir
	// so all downstream code (devloop, runGenerate, signals) sees a
	// consistent cwd.
	if flags.Project != "" {
		abs, err := absDir(flags.Project)
		if err != nil {
			return fmt.Errorf("--project %q: %w", flags.Project, err)
		}
		if err := os.Chdir(abs); err != nil {
			return fmt.Errorf("chdir to --project %q: %w", flags.Project, err)
		}
	}

	// Resolve the Go-watch root (R5). Priority:
	//   1. --watch-root explicit override.
	//   2. Walk up from cwd looking for go.mod, stopping at .git/ or $HOME.
	//   3. Fall back to cwd (= project root).
	//
	// Computed once here, before runWatchLoop, so the startup log line
	// can name both the resolved path and how it was chosen. The
	// resolved path is then passed through devloop.Config.GoWatchRoot.
	goWatchRoot, goWatchSource, err := resolveGoWatchRoot(flags.WatchRoot)
	if err != nil {
		return err
	}
	flags.WatchRoot = goWatchRoot
	fmt.Fprintf(os.Stderr, "gastro: watching *.go under %s (%s)\n", goWatchRoot, goWatchSource)

	// Build-output collision warning (\u00a74a). If any --build or --asset
	// command's argv looks like it writes into a watched directory
	// other than tmp/, emit a heads-up. Non-fatal. Asset outputs into
	// static/ are by design (the app serves them) and the suspect-path
	// helper already exempts that path, so legitimate Tailwind setups
	// don't trip the warning.
	for _, b := range flags.Build {
		if dst := suspectedBuildOutputCollision(b); dst != "" {
			fmt.Fprintf(os.Stderr,
				"gastro: --build writes to %q which is under a watched path; "+
					"this can cause reload loops. Consider writing to tmp/.\n", dst)
		}
	}
	for _, a := range flags.Asset {
		if dst := suspectedAssetOutputCollision(a); dst != "" {
			fmt.Fprintf(os.Stderr,
				"gastro: --asset writes to %q which is under a watched source path; "+
					"this can cause reload loops. Asset outputs belong in static/.\n", dst)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Fprintln(os.Stderr, "\ngastro: shutting down...")
		cancel()
	}()

	return runWatchLoop(ctx, flags)
}

// runWatchLoop is the core build/run/watch loop, factored out of
// runWatch so integration tests can drive it with a controlled context
// instead of a process-wide signal handler.
func runWatchLoop(ctx context.Context, flags watchFlags) error {
	// Build-and-run state. The build context is regenerated on each
	// restart so cancel-restart (R3) cancels the in-flight build's
	// child processes when a newer change arrives. The app pointer is
	// guarded so the deferred shutdown can stop a partially-started
	// child even if OnRestart panics.
	var (
		appMu       sync.Mutex
		app         *App
		buildCancel context.CancelFunc
		buildMu     sync.Mutex
	)

	// runChain executes a sequence of shell commands in order. Returns
	// true on full success; false (with the build-error signal already
	// written and a stderr log) on the first failure, so the caller can
	// decide whether to skip downstream work. ctx-cancelled mid-run is
	// silent (newer change won the race; the new cycle will redo it).
	runChain := func(ctx context.Context, label string, cmds []string) bool {
		for _, c := range cmds {
			if ctx.Err() != nil {
				return false
			}
			fmt.Fprintf(os.Stderr, "gastro: %s\n", c)
			out, runErr := runShellCommand(ctx, c)
			if ctx.Err() != nil {
				return false
			}
			if runErr != nil {
				msg := fmt.Sprintf("$ %s\n%s%v", c, out, runErr)
				fmt.Fprintf(os.Stderr, "gastro: %s failed; previous version still serving\n", label)
				if werr := writeBuildErrorSignal(msg); werr != nil {
					fmt.Fprintf(os.Stderr, "gastro: failed to write build-error signal: %v\n", werr)
				}
				return false
			}
		}
		return true
	}

	startApp := func(loopCtx context.Context) error {
		// R3: cancel any in-flight build before starting a new one.
		buildMu.Lock()
		if buildCancel != nil {
			buildCancel()
		}
		buildCtx, cancelBuild := context.WithCancel(loopCtx)
		buildCancel = cancelBuild
		buildMu.Unlock()

		// Asset chain first (Tailwind etc.), then build chain. On any
		// failure we DO NOT stop the previously-running app (R4) and
		// we surface the error to the browser via .gastro/.build-error.
		if !runChain(buildCtx, "asset", flags.Asset) {
			return nil
		}
		if !runChain(buildCtx, "build", flags.Build) {
			return nil
		}

		// Builds succeeded \u2014 swap the running app.
		appMu.Lock()
		old := app
		app = nil
		appMu.Unlock()
		if old != nil {
			_ = old.Stop()
		}

		fmt.Fprintf(os.Stderr, "gastro: %s\n", flags.Run)
		newApp, err := Start(loopCtx, flags.Run, append(os.Environ(), "GASTRO_DEV=1"))
		if err != nil {
			fmt.Fprintf(os.Stderr, "gastro: --run failed: %v\n", err)
			return nil
		}
		appMu.Lock()
		app = newApp
		appMu.Unlock()

		return nil
	}

	// runAssets is wired to devloop.PreReload so the asset chain runs
	// on RELOAD-class changes too -- not just RESTART-class -- which is
	// the whole point of distinguishing --asset from --build. Failure
	// is signalled by returning an error; devloop will then suppress
	// the OnReload signal so the browser doesn't refresh onto a
	// half-rebuilt asset state. The error itself is informational; the
	// build-error signal has already been written inside runChain.
	runAssets := func(loopCtx context.Context) error {
		if len(flags.Asset) == 0 {
			return nil
		}
		if !runChain(loopCtx, "asset", flags.Asset) {
			return errors.New("asset chain failed")
		}
		return nil
	}

	debounce := flags.Debounce
	loopErr := devloop.Run(ctx, devloop.Config{
		ProjectRoot:   ".",
		GoWatchRoot:   flags.WatchRoot,
		DebounceDelay: debounce,
		Quiet:         flags.Quiet,
		WatchGoFiles:  true,
		ExtraExcludes: flags.Excludes,
		ExtraWatch:    flags.ExtraWatch,
		Generate: func() ([]string, error) {
			result, err := runGenerate(false)
			if err != nil {
				return nil, err
			}
			return result.EmbedDeps, nil
		},
		OnRestart: startApp,
		PreReload: runAssets,
		OnReload:  writeReloadSignal,
	})

	// Cleanup. Stop the running app and cancel any in-flight build.
	buildMu.Lock()
	if buildCancel != nil {
		buildCancel()
	}
	buildMu.Unlock()
	appMu.Lock()
	if app != nil {
		_ = app.Stop()
	}
	appMu.Unlock()

	return loopErr
}

// runShellCommand executes one --build entry. Captures combined output
// so build failures land in .gastro/.build-error with full context, and
// also streams it to the parent's stderr so the user sees it in the
// terminal as it happens. The command is parsed with shlex (same rules
// as --run) so quoted args like `-ldflags '-X main.version=...'` work.
func runShellCommand(ctx context.Context, command string) (string, error) {
	argv, err := shlexSplit(command)
	if err != nil {
		return "", err
	}
	if len(argv) == 0 {
		return "", errors.New("empty command")
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	// Build steps run as plain children, not in their own process group:
	// they're short-lived and exec.CommandContext's default cancellation
	// (SIGKILL on ctx.Done) is sufficient for R3 (cancel-restart) since
	// the parent build itself doesn't fork long-lived grandchildren in
	// the common case (`go build`, `tailwindcss`).
	var buf bytes.Buffer
	cmd.Stdout = io.MultiWriter(&buf, os.Stderr)
	cmd.Stderr = io.MultiWriter(&buf, os.Stderr)
	err = cmd.Run()
	return buf.String(), err
}

// shlexSplit indirects through the dependency so the test can swap it
// if a future shlex version surprises us. Today it's a thin pass-through.
var shlexSplit = func(s string) ([]string, error) { return shlexLib.Split(s) }

// writeBuildErrorSignal atomically writes msg to .gastro/.build-error so
// the running app's DevReloader picks it up on its next poll tick. Atomic
// (write-to-tmp + rename) so the reader never sees a half-written file.
//
// .gastro/ is created if missing. The signal file stays on disk until
// the DevReloader consumes it, so a build failure that happens before
// the browser is connected will still reach the next page load.
func writeBuildErrorSignal(msg string) error {
	if err := os.MkdirAll(".gastro", 0o755); err != nil {
		return err
	}
	tmp := ".gastro/.build-error.tmp"
	if err := os.WriteFile(tmp, []byte(msg), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, ".gastro/.build-error"); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// suspectedAssetOutputCollision is the --asset-flavoured sibling of
// suspectedBuildOutputCollision. Same parser, narrower exclusion list:
// asset generators are *meant* to write into static/ (Tailwind, esbuild,
// image opt, fingerprint hashing), so flagging that case would warn on
// every legitimate setup. We still flag writes into pages/ or components/
// because those are source directories \u2014 an --asset producing files
// there would feed back into Generate and almost certainly loop.
func suspectedAssetOutputCollision(assetCmd string) string {
	dst := parseOutputPath(assetCmd)
	if dst == "" {
		return ""
	}
	for _, watched := range []string{"pages/", "components/"} {
		if strings.HasPrefix(dst, watched) {
			return dst
		}
	}
	return ""
}

// suspectedBuildOutputCollision returns the destination path if the build
// command appears to write to a directory the watcher is polling. Used
// for the heads-up startup warning in \u00a74a. Substring match keeps the
// check cheap and forgiving \u2014 false positives are non-fatal.
func suspectedBuildOutputCollision(buildCmd string) string {
	rest := parseOutputPath(buildCmd)
	if rest == "" {
		return ""
	}
	// Anything else under a watched path is suspicious. We can't know
	// the exact watch set without duplicating devloop's logic, so we
	// flag any output under pages/, components/, or static/ \u2014 the
	// directories the watcher always monitors.
	for _, watched := range []string{"pages/", "components/", "static/"} {
		if strings.HasPrefix(rest, watched) {
			return rest
		}
	}
	return ""
}

// parseOutputPath extracts the argument to `-o <path>` from a shell
// command, returning "" if no such argument is present or if it points
// at tmp/ (conventional and intentionally allowed). Shared by the
// build- and asset-output collision checks.
func parseOutputPath(cmd string) string {
	// Look for `-o <path>` style outputs (go build, tailwindcss, ko,
	// custom Makefiles).
	idx := strings.Index(cmd, "-o ")
	if idx < 0 {
		return ""
	}
	rest := strings.TrimSpace(cmd[idx+3:])
	end := strings.IndexAny(rest, " \t")
	if end > 0 {
		rest = rest[:end]
	}
	rest = strings.TrimPrefix(rest, "./")
	// tmp/ is conventional and intentionally allowed.
	if rest == "" || strings.HasPrefix(rest, "tmp/") || rest == "tmp" {
		return ""
	}
	return rest
}

// absDir resolves p to an absolute directory path and verifies it
// exists and is a directory. Mirrors applyGastroProject's checks.
//
// Uses filepath.Abs (cross-platform, handles ".." normalisation) rather
// than the unix-only string-prefix check the original implementation
// shipped with.
func absDir(p string) (string, error) {
	info, err := os.Stat(p)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%q is not a directory", p)
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	return abs, nil
}

// resolveGoWatchRoot picks the directory walked for *.go changes when
// gastro watch is running. Returns the absolute path and a one-word
// source label for the startup log:
//
//   - "--watch-root" — explicit override; we just validate the path.
//   - "go.mod"       — found by walking up from cwd; stops at .git/,
//     $HOME, or the filesystem root.
//   - "no go.mod found" — fallback to cwd; behaviour matches the v1
//     watcher (rooted at GASTRO_PROJECT).
//
// override is the raw --watch-root flag value ("" when absent). cwd at
// call time is assumed to already be the GASTRO_PROJECT (runWatch
// chdirs before calling this).
func resolveGoWatchRoot(override string) (path, source string, err error) {
	if override != "" {
		abs, aerr := filepath.Abs(override)
		if aerr != nil {
			return "", "", fmt.Errorf("--watch-root %q: %w", override, aerr)
		}
		info, serr := os.Stat(abs)
		if serr != nil {
			return "", "", fmt.Errorf("--watch-root %q: %w", override, serr)
		}
		if !info.IsDir() {
			return "", "", fmt.Errorf("--watch-root %q: not a directory", override)
		}
		return abs, "--watch-root", nil
	}

	cwd, gerr := os.Getwd()
	if gerr != nil {
		return "", "", fmt.Errorf("getwd: %w", gerr)
	}
	home, _ := os.UserHomeDir() // empty home disables the bound; that's fine.
	if mod := findGoModuleRoot(cwd, home); mod != "" {
		return mod, "go.mod", nil
	}
	return cwd, "no go.mod found", nil
}
