// Package devloop owns the gastro dev/watch file-watching loop: polling
// the project tree for changes, debouncing bursts, classifying each change
// as restart-class or reload-class, regenerating .gastro/, and dispatching
// the resulting action to caller-provided hooks.
//
// The package is consumed by two CLI subcommands today:
//
//   - `gastro dev` (framework mode) — passes an OnRestart hook that builds
//     the scaffold's main.go and execs the resulting binary. WatchGoFiles
//     is left false; only .gastro / .md / static/ are watched, matching
//     the scaffold's directory layout where the user never edits Go files
//     by hand.
//
//   - `gastro watch` (library mode, ships in Phase 4) — passes an
//     OnRestart hook that runs user-supplied --build commands then execs
//     a user-supplied --run command, and sets WatchGoFiles=true so edits
//     to the user's *.go sources also trigger rebuilds.
//
// The runtime API stays identical between modes; what differs is which
// hooks the caller provides and whether *.go watching is enabled.
//
// The package intentionally does not own:
//
//   - the build/exec lifecycle of the user's app (caller-owned via OnRestart)
//   - the live-reload signal mechanism (caller-owned via OnReload — the CLI
//     writes .gastro/.reload, which pkg/gastro/devreload.go polls)
//   - the strict/lenient toggle on Generate (caller decides — both dev and
//     watch use lenient today)
//
// Concurrency: Run is intended to be called once per process. It spawns a
// single watcher goroutine and blocks until ctx is cancelled. OnRestart and
// OnReload are invoked from the watcher goroutine, never concurrently with
// each other or with themselves.
package devloop

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/andrioid/gastro/internal/watcher"
)

// Default timing values, chosen to preserve the observable behaviour of
// `gastro dev` as it shipped before the devloop extraction (debounce 200ms,
// poll 500ms). The plan's draft values (100ms debounce) were aspirational;
// matching the historical defaults keeps Phase 2 a true refactor.
const (
	defaultDebounce     = 200 * time.Millisecond
	defaultPollInterval = 500 * time.Millisecond
)

// Config configures a Run. All fields except Generate are optional; Generate
// is required because the loop has no opinion about what compilation means.
type Config struct {
	// ProjectRoot is the directory whose pages/, components/, and static/
	// subtrees are watched. If empty, "." is used. The caller is expected
	// to have already chdir'd here (or set GASTRO_PROJECT) before calling
	// Run; ProjectRoot is currently informational and reserved for future
	// path resolution work.
	ProjectRoot string

	// DebounceDelay coalesces bursts of file events into a single
	// regen+restart cycle. Zero falls back to defaultDebounce.
	DebounceDelay time.Duration

	// PollInterval is the gap between filesystem scans. Zero falls back
	// to defaultPollInterval.
	PollInterval time.Duration

	// Quiet suppresses per-change logs ("gastro: pages/x.gastro changed
	// (frontmatter)") but leaves errors and the restart/reload action
	// logs in place. Used by `gastro watch -q`.
	Quiet bool

	// Generate is called once at startup and again after each debounced
	// change burst. It returns the markdown deps the compiler discovered
	// (used to drive the markdown-watching path); a nil slice is fine.
	// On error, the loop logs and skips this regen cycle — the previous
	// .gastro/ tree continues to serve.
	Generate func() (markdownDeps []string, err error)

	// OnRestart is called once at startup (with the parent ctx) so the
	// caller can boot its binary without a separate bootstrap call, and
	// again on every restart-class change. The ctx argument is the parent
	// ctx today; in Phase 4 it becomes a per-call child that is cancelled
	// when a newer change arrives mid-build (R3). Errors are logged but
	// do not terminate Run — the caller decides whether to keep the
	// previous binary alive (R4).
	//
	// Required for `gastro dev` and `gastro watch`. If nil, the loop
	// still runs but performs no app management — useful for tests that
	// only exercise the watcher.
	OnRestart func(ctx context.Context) error

	// OnReload is called after each successful regen that did not
	// escalate to a restart. The CLI writes the .gastro/.reload signal
	// here so pkg/gastro/devreload.go can broadcast a browser reload.
	// Optional — if nil, reload-class changes still recompile but no
	// signal is written.
	OnReload func()

	// WatchGoFiles enables polling of *.go files under GoWatchRoot
	// (or ProjectRoot if GoWatchRoot is empty), excluding the hardcoded
	// basename set in defaultGoExcludeBasenames plus ExtraExcludes. All
	// .go changes are restart-class; the smart classification machinery
	// only applies to .gastro files.
	//
	// Set true by `gastro watch` (R1). Left false by `gastro dev` so
	// scaffolded projects keep their .gastro-only watch surface.
	WatchGoFiles bool

	// GoWatchRoot is the directory walked for *.go files when
	// WatchGoFiles is true. If empty, falls back to ProjectRoot. Used
	// by `gastro watch` (R5) to watch a Go module root that sits above
	// GASTRO_PROJECT, so edits to e.g. cmd/myapp/main.go in the parent
	// trigger restarts.
	//
	// Has no effect when WatchGoFiles is false. The .gastro / pages /
	// components / static surfaces always stay rooted at ProjectRoot.
	GoWatchRoot string

	// ExtraExcludes appends caller-supplied exclude paths to the
	// hardcoded defaults. Each entry is matched as a path prefix
	// relative to GoWatchRoot (or ProjectRoot if GoWatchRoot is empty).
	// Used by `gastro watch --exclude` (R2).
	ExtraExcludes []string
}

// defaultGoExcludeBasenames is the always-on exclude set for *.go
// watching when WatchGoFiles is enabled. Entries are basenames matched
// anywhere in the tree (so a nested vendor/ or node_modules/ inside a
// monorepo is also skipped, not just one at the root). Not user-removable
// in v1 (R2); removing any of them would either loop forever (.gastro/),
// watch the world (vendor/, node_modules/), or violate user expectations
// (.git/, tmp/).
var defaultGoExcludeBasenames = []string{
	".gastro",
	"vendor",
	"node_modules",
	".git",
	"tmp",
}

// Run blocks until ctx is cancelled. It performs an initial Generate +
// OnRestart synchronously, then spawns a watcher goroutine and waits.
// On clean cancellation the watcher exits and Run returns nil. The first
// Generate error is returned (since callers typically want to fail loudly
// on a broken initial compile); subsequent Generate errors are logged and
// the loop continues.
func Run(ctx context.Context, cfg Config) error {
	if cfg.Generate == nil {
		return fmt.Errorf("devloop: Config.Generate is required")
	}
	if cfg.DebounceDelay <= 0 {
		cfg.DebounceDelay = defaultDebounce
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = defaultPollInterval
	}

	extDeps := &watcher.ExternalDeps{}

	// Initial generate (lenient — warnings don't block dev).
	deps, err := cfg.Generate()
	if err != nil {
		return err
	}
	extDeps.Set(deps)

	// Pending change tracking — restart wins over reload across the
	// debounce window.
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

	debounced := watcher.Debounce(cfg.DebounceDelay, func() {
		fmt.Println("gastro: changes detected, regenerating...")
		deps, err := cfg.Generate()
		if err != nil {
			fmt.Fprintf(os.Stderr, "gastro: generate failed: %v\n", err)
			// Retain last-known-good extDeps so .md edits keep reloading
			// while the user fixes a broken .gastro file.
			return
		}
		extDeps.Set(deps)

		ct := consumePending()
		if ct == watcher.ChangeRestart {
			if cfg.OnRestart != nil {
				if err := cfg.OnRestart(ctx); err != nil {
					fmt.Fprintf(os.Stderr, "gastro: restart failed: %v\n", err)
				}
			}
		} else {
			if cfg.OnReload != nil {
				cfg.OnReload()
			}
		}
	})

	// Empty-source warnings (Phase 3). Non-fatal: the polling watcher
	// already detects new files via existing logic, so a project that
	// starts with empty pages/ or components/ will pick up files added
	// later. The warning just makes "why doesn't anything happen?" a
	// non-mystery for users who've forgotten to scaffold a page.
	warnEmptySources(stderrSink, cfg.ProjectRoot)

	// Seed the watcher state synchronously, BEFORE the initial
	// OnRestart, so any caller that observes the initial OnRestart can
	// trust that the seed is complete. The seed scans files; if the
	// caller (e.g. an integration test) edits a file between observing
	// OnRestart and the watcher's first poll, the seed already captured
	// the pre-edit baseline and the change is classified correctly.
	//
	// This ordering also matches what `gastro dev` did pre-extraction:
	// the inline goroutine seeded BEFORE entering the poll loop, and
	// startApp ran outside the goroutine. After this refactor the seed
	// runs in the calling goroutine of Run, before OnRestart — stricter
	// than the old behaviour (which let OnRestart and seed race) and
	// strictly safer for all known callers.
	state := newWatcherState(cfg, extDeps, escalate, debounced)

	// Initial OnRestart (boots the user's binary). Errors are logged
	// but do not terminate Run — the next change triggers another
	// attempt. Matches the historical `gastro dev` behaviour where
	// startApp() set appCmd=nil on failure and pressed on.
	if cfg.OnRestart != nil {
		if err := cfg.OnRestart(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "gastro: initial start failed: %v\n", err)
		}
	}

	// Spawn the watcher goroutine. It reads extDeps via Snapshot() and
	// owns the state struct; no cross-goroutine sharing beyond extDeps
	// and the escalate/debounced closures (themselves serialised by the
	// mutex above).
	go state.runLoop(ctx)

	<-ctx.Done()
	return nil
}
