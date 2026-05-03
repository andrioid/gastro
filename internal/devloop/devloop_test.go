package devloop_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/andrioid/gastro/internal/devloop"
)

// Test timing. All tests use compressed intervals so the suite stays fast.
// Debounce is set lower than the historical 200ms because every test waits
// on event channels rather than wall-clock sleeps.
const (
	testPoll     = 20 * time.Millisecond
	testDebounce = 30 * time.Millisecond
	// waitTimeout is the upper bound for any single channel receive.
	// Generous because CI filesystems can be slow.
	waitTimeout = 5 * time.Second
)

// recorder collects OnRestart / OnReload invocations. Channels are buffered
// generously so tests don't deadlock if more events than expected fire —
// the assertion reads the count and decides whether to fail.
type recorder struct {
	restartCh chan struct{}
	reloadCh  chan struct{}
	genCh     chan struct{}
	genFunc   atomic.Value // func() ([]string, error)
}

func newRecorder() *recorder {
	r := &recorder{
		restartCh: make(chan struct{}, 64),
		reloadCh:  make(chan struct{}, 64),
		genCh:     make(chan struct{}, 64),
	}
	r.genFunc.Store(func() ([]string, error) { return nil, nil })
	return r
}

func (r *recorder) generate() ([]string, error) {
	r.genCh <- struct{}{}
	fn := r.genFunc.Load().(func() ([]string, error))
	return fn()
}

func (r *recorder) onRestart(_ context.Context) error {
	r.restartCh <- struct{}{}
	return nil
}

func (r *recorder) onReload() {
	r.reloadCh <- struct{}{}
}

// waitN drains exactly n events from ch within waitTimeout. Fails the
// test if fewer than n arrive in time. Extra events past n are left in
// the channel so the caller can assert on the steady state.
func waitN(t *testing.T, name string, ch <-chan struct{}, n int) {
	t.Helper()
	deadline := time.NewTimer(waitTimeout)
	defer deadline.Stop()
	for i := 0; i < n; i++ {
		select {
		case <-ch:
		case <-deadline.C:
			t.Fatalf("timeout waiting for %s event %d/%d", name, i+1, n)
		}
	}
}

// drain reads any pending events without blocking. Used to assert "no
// further events" after a wait.
func drain(ch <-chan struct{}) int {
	n := 0
	for {
		select {
		case <-ch:
			n++
		default:
			return n
		}
	}
}

// setupProject creates a minimal gastro project layout in a tempdir and
// returns the path. Pages/components have one .gastro file each so
// modify-based tests have something to touch. Returns absolute path.
func setupProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	mustMkdir(t, filepath.Join(root, "pages"))
	mustMkdir(t, filepath.Join(root, "components"))
	mustMkdir(t, filepath.Join(root, "static"))

	mustWrite(t, filepath.Join(root, "pages", "index.gastro"), `---
---
<h1>hello</h1>
`)
	mustWrite(t, filepath.Join(root, "components", "card.gastro"), `---
---
<div>card</div>
`)

	return root
}

func mustMkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", p, err)
	}
}

func mustWrite(t *testing.T, p, content string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
}

// withChdir runs fn with the working directory set to dir, restoring the
// original on return. devloop.Run currently watches relative paths
// ("pages", "components", "static") so tests need to chdir into the temp
// project before calling Run. A package-level mutex serialises chdirs
// across parallel tests in the same binary.
var chdirMu sync.Mutex

func withChdir(t *testing.T, dir string, fn func()) {
	t.Helper()
	chdirMu.Lock()
	defer chdirMu.Unlock()

	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir %s: %v", dir, err)
	}
	defer func() {
		if err := os.Chdir(orig); err != nil {
			t.Logf("chdir restore: %v", err)
		}
	}()
	fn()
}

// runLoop spawns devloop.Run in a goroutine and returns a cancel func and
// a wait func that blocks until Run returns. Tests should defer both so
// the loop is always cleaned up even on assertion failure.
func runLoop(t *testing.T, cfg devloop.Config) (cancel func(), wait func()) {
	t.Helper()
	ctx, cancelCtx := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := devloop.Run(ctx, cfg); err != nil {
			t.Logf("devloop.Run returned: %v", err)
		}
	}()
	return cancelCtx, func() {
		select {
		case <-done:
		case <-time.After(waitTimeout):
			t.Fatal("devloop.Run did not exit within timeout")
		}
	}
}

// touchLater overwrites path with newContent and bumps mod-time forward
// to ensure the polling watcher (which only triggers on info.ModTime().After(prev))
// notices the change even if the test runs faster than filesystem
// timestamp resolution.
func touchLater(t *testing.T, path, newContent string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(newContent), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatalf("chtimes %s: %v", path, err)
	}
}

// --- TESTS ---

// TestRun_CleanCancellation: Run returns nil promptly when ctx is cancelled.
func TestRun_CleanCancellation(t *testing.T) {
	root := setupProject(t)
	rec := newRecorder()

	withChdir(t, root, func() {
		cancel, wait := runLoop(t, devloop.Config{
			PollInterval:  testPoll,
			DebounceDelay: testDebounce,
			Generate:      rec.generate,
			OnRestart:     rec.onRestart,
			OnReload:      rec.onReload,
		})

		// Wait for the initial Generate + OnRestart to fire.
		waitN(t, "generate", rec.genCh, 1)
		waitN(t, "restart", rec.restartCh, 1)

		cancel()
		wait()
	})
}

// TestRun_OnRestartCalledOnceAtStartup: per the plan's sharpened contract,
// OnRestart fires exactly once before the watcher starts processing events,
// so callers don't need to bootstrap the binary themselves.
func TestRun_OnRestartCalledOnceAtStartup(t *testing.T) {
	root := setupProject(t)
	rec := newRecorder()

	withChdir(t, root, func() {
		cancel, wait := runLoop(t, devloop.Config{
			PollInterval:  testPoll,
			DebounceDelay: testDebounce,
			Generate:      rec.generate,
			OnRestart:     rec.onRestart,
			OnReload:      rec.onReload,
		})
		defer wait()
		defer cancel()

		waitN(t, "restart", rec.restartCh, 1)

		// Give the watcher a few poll cycles to confirm no spurious
		// events fire when nothing changes.
		time.Sleep(testPoll * 5)
		if extra := drain(rec.restartCh); extra > 0 {
			t.Errorf("unexpected %d extra OnRestart events after startup", extra)
		}
		if extra := drain(rec.reloadCh); extra > 0 {
			t.Errorf("unexpected %d OnReload events after startup", extra)
		}
	})
}

// TestRun_FrontmatterChangeTriggersRestart: editing the frontmatter section
// of a .gastro file (between --- markers) escalates to a restart, not a
// reload. This is the smart-classification property `gastro dev` has
// always had — covered now in devloop after the refactor.
func TestRun_FrontmatterChangeTriggersRestart(t *testing.T) {
	root := setupProject(t)
	rec := newRecorder()

	withChdir(t, root, func() {
		cancel, wait := runLoop(t, devloop.Config{
			PollInterval:  testPoll,
			DebounceDelay: testDebounce,
			Generate:      rec.generate,
			OnRestart:     rec.onRestart,
			OnReload:      rec.onReload,
		})
		defer wait()
		defer cancel()

		// Drain startup events.
		waitN(t, "initial generate", rec.genCh, 1)
		waitN(t, "initial restart", rec.restartCh, 1)

		// Modify the frontmatter region (add a Go variable).
		touchLater(t, filepath.Join(root, "pages", "index.gastro"), `---
var x = 1
---
<h1>hello</h1>
`)

		waitN(t, "regen after frontmatter change", rec.genCh, 1)
		waitN(t, "restart after frontmatter change", rec.restartCh, 1)

		// No reload should fire when the change escalated to a restart.
		if extra := drain(rec.reloadCh); extra > 0 {
			t.Errorf("expected reload escalated to restart; saw %d reload events", extra)
		}
	})
}

// TestRun_TemplateOnlyChangeTriggersReload: editing only the template body
// of a .gastro file (after the closing ---) does NOT escalate to a restart.
// OnReload fires; OnRestart does not (beyond the initial startup call).
func TestRun_TemplateOnlyChangeTriggersReload(t *testing.T) {
	root := setupProject(t)
	rec := newRecorder()

	withChdir(t, root, func() {
		cancel, wait := runLoop(t, devloop.Config{
			PollInterval:  testPoll,
			DebounceDelay: testDebounce,
			Generate:      rec.generate,
			OnRestart:     rec.onRestart,
			OnReload:      rec.onReload,
		})
		defer wait()
		defer cancel()

		waitN(t, "initial generate", rec.genCh, 1)
		waitN(t, "initial restart", rec.restartCh, 1)

		// Edit the template body only — frontmatter unchanged.
		touchLater(t, filepath.Join(root, "pages", "index.gastro"), `---
---
<h1>HELLO</h1>
`)

		waitN(t, "regen after template change", rec.genCh, 1)
		waitN(t, "reload after template change", rec.reloadCh, 1)

		if extra := drain(rec.restartCh); extra > 0 {
			t.Errorf("template-only change must not trigger restart; saw %d", extra)
		}
	})
}

// TestRun_DebouncesBurstOfChanges: a rapid burst of N file edits within
// the debounce window collapses into a single Generate / restart pair.
// Without debouncing, every poll cycle would compile the world.
func TestRun_DebouncesBurstOfChanges(t *testing.T) {
	root := setupProject(t)
	rec := newRecorder()

	withChdir(t, root, func() {
		cancel, wait := runLoop(t, devloop.Config{
			PollInterval:  testPoll,
			DebounceDelay: 100 * time.Millisecond, // wider than testDebounce so the burst lands inside it
			Generate:      rec.generate,
			OnRestart:     rec.onRestart,
			OnReload:      rec.onReload,
		})
		defer wait()
		defer cancel()

		waitN(t, "initial generate", rec.genCh, 1)
		waitN(t, "initial restart", rec.restartCh, 1)

		// Five frontmatter edits inside one debounce window.
		for i := 0; i < 5; i++ {
			touchLater(t, filepath.Join(root, "pages", "index.gastro"), fmt.Sprintf(`---
var x = %d
---
<h1>hello</h1>
`, i))
		}

		waitN(t, "burst regen", rec.genCh, 1)
		waitN(t, "burst restart", rec.restartCh, 1)

		// Wait one debounce window past the burst, then assert no extras.
		time.Sleep(200 * time.Millisecond)
		if extraGen := drain(rec.genCh); extraGen > 0 {
			t.Errorf("expected exactly 1 regen for 5-change burst; saw %d extras", extraGen)
		}
		if extraRestart := drain(rec.restartCh); extraRestart > 0 {
			t.Errorf("expected exactly 1 restart for 5-change burst; saw %d extras", extraRestart)
		}
	})
}

// TestRun_GenerateErrorDoesNotTerminate: when Generate fails (e.g. the
// user broke a .gastro file), the loop logs and keeps watching. The next
// successful Generate restores normal operation. Importantly, the
// previously-running app stays alive (no restart fires when generate
// failed).
func TestRun_GenerateErrorDoesNotTerminate(t *testing.T) {
	root := setupProject(t)
	rec := newRecorder()

	// Make the second Generate call fail; subsequent calls succeed again.
	var calls atomic.Int32
	rec.genFunc.Store(func() ([]string, error) {
		n := calls.Add(1)
		if n == 2 {
			return nil, errors.New("simulated parse error")
		}
		return nil, nil
	})

	withChdir(t, root, func() {
		cancel, wait := runLoop(t, devloop.Config{
			PollInterval:  testPoll,
			DebounceDelay: testDebounce,
			Generate:      rec.generate,
			OnRestart:     rec.onRestart,
			OnReload:      rec.onReload,
		})
		defer wait()
		defer cancel()

		waitN(t, "initial generate", rec.genCh, 1)
		waitN(t, "initial restart", rec.restartCh, 1)

		// First edit — Generate will fail.
		touchLater(t, filepath.Join(root, "pages", "index.gastro"), `---
broken
---
<h1>x</h1>
`)
		waitN(t, "failing regen", rec.genCh, 1)

		// No restart should fire when generate failed.
		time.Sleep(testDebounce + 50*time.Millisecond)
		if extra := drain(rec.restartCh); extra > 0 {
			t.Errorf("restart fired despite generate error; saw %d", extra)
		}

		// Second edit — Generate succeeds again, restart fires.
		touchLater(t, filepath.Join(root, "pages", "index.gastro"), `---
var ok = true
---
<h1>x</h1>
`)
		waitN(t, "recovered regen", rec.genCh, 1)
		waitN(t, "recovered restart", rec.restartCh, 1)
	})
}

// TestRun_MarkdownDepsTracked: when Generate reports markdown deps, the
// watcher polls those paths (even when they live outside the project
// tree) and emits reload events on change. This is the regression test
// for the extDeps wiring after the package extraction.
func TestRun_MarkdownDepsTracked(t *testing.T) {
	root := setupProject(t)
	rec := newRecorder()

	// Place the markdown file outside the project tree to prove the
	// watcher follows extDeps absolute paths, not just files under root.
	mdDir := t.TempDir()
	mdPath := filepath.Join(mdDir, "shared.md")
	mustWrite(t, mdPath, "# initial\n")

	rec.genFunc.Store(func() ([]string, error) {
		return []string{mdPath}, nil
	})

	withChdir(t, root, func() {
		cancel, wait := runLoop(t, devloop.Config{
			PollInterval:  testPoll,
			DebounceDelay: testDebounce,
			Generate:      rec.generate,
			OnRestart:     rec.onRestart,
			OnReload:      rec.onReload,
		})
		defer wait()
		defer cancel()

		waitN(t, "initial generate", rec.genCh, 1)
		waitN(t, "initial restart", rec.restartCh, 1)

		// Drain any startup events for the new-md file (depending on
		// timing it may or may not be reported as new).
		time.Sleep(testPoll * 3)
		drain(rec.reloadCh)
		drain(rec.genCh)

		// Edit the external markdown — should regen and reload.
		touchLater(t, mdPath, "# updated\n")
		waitN(t, "regen after md change", rec.genCh, 1)
		waitN(t, "reload after md change", rec.reloadCh, 1)
	})
}

// TestRun_StaticChangeTriggersReload: edits to static/ files trigger
// reload-class events. Smoke-level coverage of the static branch in the
// watcher.
func TestRun_StaticChangeTriggersReload(t *testing.T) {
	root := setupProject(t)
	cssPath := filepath.Join(root, "static", "styles.css")
	mustWrite(t, cssPath, "body { color: black; }")

	rec := newRecorder()

	withChdir(t, root, func() {
		cancel, wait := runLoop(t, devloop.Config{
			PollInterval:  testPoll,
			DebounceDelay: testDebounce,
			Generate:      rec.generate,
			OnRestart:     rec.onRestart,
			OnReload:      rec.onReload,
		})
		defer wait()
		defer cancel()

		waitN(t, "initial generate", rec.genCh, 1)
		waitN(t, "initial restart", rec.restartCh, 1)

		touchLater(t, cssPath, "body { color: red; }")
		waitN(t, "regen after static change", rec.genCh, 1)
		waitN(t, "reload after static change", rec.reloadCh, 1)
	})
}

// --- WatchGoFiles tests (R1+R2) ---

// TestRun_WatchGoFiles_RestartsOnGoFileChange: with WatchGoFiles=true,
// editing any *.go file under the project root triggers a restart-class
// regen + OnRestart cycle. This is the property `gastro watch` relies on
// in library mode.
func TestRun_WatchGoFiles_RestartsOnGoFileChange(t *testing.T) {
	root := setupProject(t)
	mustMkdir(t, filepath.Join(root, "cmd", "myapp"))
	goPath := filepath.Join(root, "cmd", "myapp", "main.go")
	mustWrite(t, goPath, "package main\n\nfunc main() {}\n")

	rec := newRecorder()

	withChdir(t, root, func() {
		cancel, wait := runLoop(t, devloop.Config{
			PollInterval:  testPoll,
			DebounceDelay: testDebounce,
			WatchGoFiles:  true,
			Generate:      rec.generate,
			OnRestart:     rec.onRestart,
			OnReload:      rec.onReload,
		})
		defer wait()
		defer cancel()

		waitN(t, "initial generate", rec.genCh, 1)
		waitN(t, "initial restart", rec.restartCh, 1)

		// Drain any incidental startup events.
		time.Sleep(testPoll * 2)
		drain(rec.genCh)
		drain(rec.restartCh)
		drain(rec.reloadCh)

		touchLater(t, goPath, "package main\n\nfunc main() { println(\"hi\") }\n")
		waitN(t, "regen after .go change", rec.genCh, 1)
		waitN(t, "restart after .go change", rec.restartCh, 1)

		// .go changes are restart-class — never reload.
		if extra := drain(rec.reloadCh); extra > 0 {
			t.Errorf(".go change should not trigger reload; saw %d", extra)
		}
	})
}

// TestRun_WatchGoFiles_IgnoresDefaultExcludes: edits to vendored / tmp /
// .git Go files do not trigger restarts. The exclude set is hardcoded
// and not user-removable in v1.
func TestRun_WatchGoFiles_IgnoresDefaultExcludes(t *testing.T) {
	root := setupProject(t)
	for _, dir := range []string{"vendor/foo", "tmp", ".git", "node_modules/bar"} {
		mustMkdir(t, filepath.Join(root, dir))
		mustWrite(t, filepath.Join(root, dir, "leftover.go"), "package x\n")
	}

	rec := newRecorder()

	withChdir(t, root, func() {
		cancel, wait := runLoop(t, devloop.Config{
			PollInterval:  testPoll,
			DebounceDelay: testDebounce,
			WatchGoFiles:  true,
			Generate:      rec.generate,
			OnRestart:     rec.onRestart,
			OnReload:      rec.onReload,
		})
		defer wait()
		defer cancel()

		waitN(t, "initial generate", rec.genCh, 1)
		waitN(t, "initial restart", rec.restartCh, 1)
		time.Sleep(testPoll * 2)
		drain(rec.genCh)
		drain(rec.restartCh)
		drain(rec.reloadCh)

		// Touch each excluded file. None should produce events.
		for _, dir := range []string{"vendor/foo", "tmp", ".git", "node_modules/bar"} {
			touchLater(t, filepath.Join(root, dir, "leftover.go"), "package x\n\nvar V = 1\n")
		}

		time.Sleep(testPoll*5 + testDebounce)
		if extraGen := drain(rec.genCh); extraGen > 0 {
			t.Errorf("excluded .go edits triggered %d generates", extraGen)
		}
		if extraRestart := drain(rec.restartCh); extraRestart > 0 {
			t.Errorf("excluded .go edits triggered %d restarts", extraRestart)
		}
	})
}

// TestRun_WatchGoFiles_HonoursExtraExcludes: ExtraExcludes adds project-
// specific paths on top of the hardcoded defaults. Edits to Go files
// under those paths are silently ignored.
func TestRun_WatchGoFiles_HonoursExtraExcludes(t *testing.T) {
	root := setupProject(t)
	mustMkdir(t, filepath.Join(root, "custom", "deep"))
	excludedGo := filepath.Join(root, "custom", "deep", "x.go")
	mustWrite(t, excludedGo, "package deep\n")

	// Also create a Go file that should NOT be excluded — proves the
	// exclude is targeted, not "any .go file is ignored".
	mustMkdir(t, filepath.Join(root, "cmd", "app"))
	includedGo := filepath.Join(root, "cmd", "app", "main.go")
	mustWrite(t, includedGo, "package main\n\nfunc main() {}\n")

	rec := newRecorder()

	withChdir(t, root, func() {
		cancel, wait := runLoop(t, devloop.Config{
			PollInterval:  testPoll,
			DebounceDelay: testDebounce,
			WatchGoFiles:  true,
			ExtraExcludes: []string{"custom/"},
			Generate:      rec.generate,
			OnRestart:     rec.onRestart,
			OnReload:      rec.onReload,
		})
		defer wait()
		defer cancel()

		waitN(t, "initial generate", rec.genCh, 1)
		waitN(t, "initial restart", rec.restartCh, 1)
		time.Sleep(testPoll * 2)
		drain(rec.genCh)
		drain(rec.restartCh)
		drain(rec.reloadCh)

		// Touch the excluded file — must not trigger.
		touchLater(t, excludedGo, "package deep\n\nvar X = 1\n")
		time.Sleep(testPoll*5 + testDebounce)
		if extra := drain(rec.restartCh); extra > 0 {
			t.Errorf("custom-excluded .go change triggered %d restarts", extra)
		}

		// Touch the included file — must trigger, proving the watcher
		// is alive and the exclude is targeted rather than blanket.
		touchLater(t, includedGo, "package main\n\nfunc main() { println(\"x\") }\n")
		waitN(t, "regen after included .go change", rec.genCh, 1)
		waitN(t, "restart after included .go change", rec.restartCh, 1)
	})
}

// TestRun_GoWatchRoot_WatchesAboveProjectRoot covers R5: when GoWatchRoot
// is set above ProjectRoot (e.g. the enclosing Go module root), edits to
// *.go files in the parent tree trigger restarts. This is the common
// library-mode layout where GASTRO_PROJECT is internal/web/ but the
// binary entrypoint lives at cmd/myapp/main.go.
func TestRun_GoWatchRoot_WatchesAboveProjectRoot(t *testing.T) {
	// Layout:
	//   <module>/go.mod            (presence not required by devloop)
	//   <module>/cmd/myapp/main.go (edited — must trigger restart)
	//   <module>/web/              (= ProjectRoot; gastro project)
	//     pages/index.gastro
	//     components/card.gastro
	module := t.TempDir()
	projectRoot := filepath.Join(module, "web")
	mustMkdir(t, filepath.Join(projectRoot, "pages"))
	mustMkdir(t, filepath.Join(projectRoot, "components"))
	mustMkdir(t, filepath.Join(projectRoot, "static"))
	mustWrite(t, filepath.Join(projectRoot, "pages", "index.gastro"), `---
---
<h1>hi</h1>
`)
	mustWrite(t, filepath.Join(projectRoot, "components", "card.gastro"), `---
---
<div>card</div>
`)

	mustMkdir(t, filepath.Join(module, "cmd", "myapp"))
	parentGo := filepath.Join(module, "cmd", "myapp", "main.go")
	mustWrite(t, parentGo, "package main\n\nfunc main() {}\n")

	rec := newRecorder()

	withChdir(t, projectRoot, func() {
		cancel, wait := runLoop(t, devloop.Config{
			PollInterval:  testPoll,
			DebounceDelay: testDebounce,
			WatchGoFiles:  true,
			GoWatchRoot:   module, // <-- the new behaviour under test
			Generate:      rec.generate,
			OnRestart:     rec.onRestart,
			OnReload:      rec.onReload,
		})
		defer wait()
		defer cancel()

		waitN(t, "initial generate", rec.genCh, 1)
		waitN(t, "initial restart", rec.restartCh, 1)
		time.Sleep(testPoll * 2)
		drain(rec.genCh)
		drain(rec.restartCh)
		drain(rec.reloadCh)

		// Edit the parent *.go file. With GoWatchRoot=module this must
		// trigger a restart even though the file lives outside the
		// gastro project root.
		touchLater(t, parentGo, "package main\n\nfunc main() { println(\"hi\") }\n")
		waitN(t, "regen after parent .go change", rec.genCh, 1)
		waitN(t, "restart after parent .go change", rec.restartCh, 1)
	})
}

// TestRun_GoWatchRoot_DefaultsToProjectRoot guards the backward-compat
// promise: if GoWatchRoot is empty, behaviour is identical to the
// pre-R5 watcher — only *.go files under ProjectRoot are watched.
func TestRun_GoWatchRoot_DefaultsToProjectRoot(t *testing.T) {
	module := t.TempDir()
	projectRoot := filepath.Join(module, "web")
	mustMkdir(t, filepath.Join(projectRoot, "pages"))
	mustMkdir(t, filepath.Join(projectRoot, "components"))
	mustMkdir(t, filepath.Join(projectRoot, "static"))
	mustWrite(t, filepath.Join(projectRoot, "pages", "index.gastro"), `---
---
<h1>hi</h1>
`)
	mustWrite(t, filepath.Join(projectRoot, "components", "card.gastro"), `---
---
<div>card</div>
`)

	mustMkdir(t, filepath.Join(module, "cmd", "myapp"))
	parentGo := filepath.Join(module, "cmd", "myapp", "main.go")
	mustWrite(t, parentGo, "package main\n\nfunc main() {}\n")

	rec := newRecorder()

	withChdir(t, projectRoot, func() {
		cancel, wait := runLoop(t, devloop.Config{
			PollInterval:  testPoll,
			DebounceDelay: testDebounce,
			WatchGoFiles:  true,
			// GoWatchRoot intentionally unset — must fall back to ProjectRoot.
			Generate:  rec.generate,
			OnRestart: rec.onRestart,
			OnReload:  rec.onReload,
		})
		defer wait()
		defer cancel()

		waitN(t, "initial generate", rec.genCh, 1)
		waitN(t, "initial restart", rec.restartCh, 1)
		time.Sleep(testPoll * 2)
		drain(rec.genCh)
		drain(rec.restartCh)
		drain(rec.reloadCh)

		// Edit the parent *.go file. With GoWatchRoot unset it must NOT
		// trigger a restart — we never see it.
		touchLater(t, parentGo, "package main\n\nfunc main() { println(\"hi\") }\n")
		time.Sleep(testPoll*5 + testDebounce)
		if extra := drain(rec.restartCh); extra > 0 {
			t.Errorf("parent .go change should NOT restart when GoWatchRoot is unset; saw %d", extra)
		}
	})
}

// TestRun_GoWatchRoot_BasenameExcludesPruneNestedDirs covers the new
// basename-match semantics for the always-on default excludes. With
// GoWatchRoot set to a parent dir, an inner .gastro/ (under the gastro
// project itself) and a nested vendor/ inside an arbitrary subtree must
// both be skipped — the prefix-only logic of v1 would have walked into
// the inner .gastro/ because its rel-path doesn't start with ".gastro/".
func TestRun_GoWatchRoot_BasenameExcludesPruneNestedDirs(t *testing.T) {
	module := t.TempDir()
	projectRoot := filepath.Join(module, "web")
	mustMkdir(t, filepath.Join(projectRoot, "pages"))
	mustMkdir(t, filepath.Join(projectRoot, "components"))
	mustMkdir(t, filepath.Join(projectRoot, "static"))
	mustWrite(t, filepath.Join(projectRoot, "pages", "index.gastro"), `---
---
<h1>hi</h1>
`)
	mustWrite(t, filepath.Join(projectRoot, "components", "card.gastro"), `---
---
<div>card</div>
`)

	// .gastro/ under the gastro project (where generate would write).
	// Files here MUST NOT trigger restarts — otherwise we infinite-loop.
	nestedGastroGo := filepath.Join(projectRoot, ".gastro", "router.go")
	mustMkdir(t, filepath.Join(projectRoot, ".gastro"))
	mustWrite(t, nestedGastroGo, "package gastro\n")

	// vendor/ deep inside the module — must also be skipped despite not
	// matching the prefix "vendor/" relative to GoWatchRoot.
	nestedVendorGo := filepath.Join(module, "third_party", "libfoo", "vendor", "foo.go")
	mustMkdir(t, filepath.Dir(nestedVendorGo))
	mustWrite(t, nestedVendorGo, "package foo\n")

	// And a regular .go file that SHOULD trigger — keeps the test honest.
	mustMkdir(t, filepath.Join(module, "cmd", "myapp"))
	includedGo := filepath.Join(module, "cmd", "myapp", "main.go")
	mustWrite(t, includedGo, "package main\n\nfunc main() {}\n")

	rec := newRecorder()

	withChdir(t, projectRoot, func() {
		cancel, wait := runLoop(t, devloop.Config{
			PollInterval:  testPoll,
			DebounceDelay: testDebounce,
			WatchGoFiles:  true,
			GoWatchRoot:   module,
			Generate:      rec.generate,
			OnRestart:     rec.onRestart,
			OnReload:      rec.onReload,
		})
		defer wait()
		defer cancel()

		waitN(t, "initial generate", rec.genCh, 1)
		waitN(t, "initial restart", rec.restartCh, 1)
		time.Sleep(testPoll * 2)
		drain(rec.genCh)
		drain(rec.restartCh)
		drain(rec.reloadCh)

		// Edit both excluded files. Neither should produce events.
		touchLater(t, nestedGastroGo, "package gastro\n\nvar X = 1\n")
		touchLater(t, nestedVendorGo, "package foo\n\nvar Y = 1\n")
		time.Sleep(testPoll*5 + testDebounce)
		if extra := drain(rec.restartCh); extra > 0 {
			t.Errorf("nested-default-exclude .go edits triggered %d restarts", extra)
		}

		// Edit the regular file — must trigger.
		touchLater(t, includedGo, "package main\n\nfunc main() { println(\"x\") }\n")
		waitN(t, "regen after included .go change", rec.genCh, 1)
		waitN(t, "restart after included .go change", rec.restartCh, 1)
	})
}
