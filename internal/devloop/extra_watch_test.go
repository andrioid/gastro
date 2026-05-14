package devloop

// Tests for Config.ExtraWatch: glob patterns that participate in the
// dev-watch surface. Changes / creates / deletes on matched files are
// classified as ChangeRestart (the use case is files baked into the
// binary via //go:embed where stale memory state requires a rebuild).
//
// The tests run against the same poll loop that ships in production —
// no mocks. The poll interval is tightened to keep the tests fast.

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// startLoop spins up devloop.Run with a 25ms poll interval and short
// debounce; returns the cancel func plus counters incremented by the
// hooks. The Generate hook is a no-op; OnRestart and OnReload increment
// their respective counters. The test is responsible for cancelling.
type loopHooks struct {
	restarts int64
	reloads  int64
	cancel   context.CancelFunc
	done     chan struct{}
}

func startLoop(t *testing.T, cfg Config) *loopHooks {
	t.Helper()
	if cfg.PollInterval == 0 {
		cfg.PollInterval = 25 * time.Millisecond
	}
	if cfg.DebounceDelay == 0 {
		cfg.DebounceDelay = 30 * time.Millisecond
	}
	if cfg.Generate == nil {
		cfg.Generate = func() ([]string, error) { return nil, nil }
	}
	h := &loopHooks{done: make(chan struct{})}
	cfg.OnRestart = func(ctx context.Context) error {
		atomic.AddInt64(&h.restarts, 1)
		return nil
	}
	cfg.OnReload = func() {
		atomic.AddInt64(&h.reloads, 1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	go func() {
		_ = Run(ctx, cfg)
		close(h.done)
	}()
	return h
}

// awaitRestart blocks until the restart count reaches at least n (with a
// timeout); reports a test failure on timeout.
func awaitRestart(t *testing.T, h *loopHooks, n int64, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if atomic.LoadInt64(&h.restarts) >= n {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("expected at least %d restarts within %v; got %d", n, timeout, atomic.LoadInt64(&h.restarts))
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// TestExtraWatch_FileChangeTriggersRestart: editing a matched file
// escalates to ChangeRestart (the documented contract).
func TestExtraWatch_FileChangeTriggersRestart(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "i18n"), 0o755); err != nil {
		t.Fatal(err)
	}
	po := filepath.Join(dir, "i18n", "en.po")
	writeFile(t, po, "# initial\nmsgid \"hi\"\nmsgstr \"hi\"\n")

	t.Chdir(dir)

	h := startLoop(t, Config{
		ProjectRoot: ".",
		ExtraWatch:  []string{"i18n/*.po"},
	})
	defer h.cancel()

	// Initial OnRestart is fired synchronously by Run() before the
	// watcher goroutine starts. Wait for it so the count baseline is
	// clean.
	awaitRestart(t, h, 1, 1*time.Second)

	// Bump the file's modtime so the next poll observes a change. Just
	// rewriting content isn't always enough on filesystems with second-
	// level granularity; explicit Chtimes guarantees the bump.
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(po, future, future); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	awaitRestart(t, h, 2, 2*time.Second)
}

// TestExtraWatch_NewFileMatchingGlobTriggersRestart: a file that didn't
// exist at seed time but matches the glob on a subsequent poll triggers
// a restart. This is the "adopter adds a new locale mid-session"
// scenario.
func TestExtraWatch_NewFileMatchingGlobTriggersRestart(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "i18n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	h := startLoop(t, Config{
		ProjectRoot: ".",
		ExtraWatch:  []string{"i18n/*.po"},
	})
	defer h.cancel()

	awaitRestart(t, h, 1, 1*time.Second)

	// Drop a new PO file into i18n/. The next poll should see it.
	writeFile(t, filepath.Join(dir, "i18n", "de.po"), "# new\n")
	awaitRestart(t, h, 2, 2*time.Second)
}

// TestExtraWatch_NonMatchingFileIgnored: a file outside the glob doesn't
// trigger anything.
func TestExtraWatch_NonMatchingFileIgnored(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "i18n"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "i18n", "en.po"), "# initial\n")
	t.Chdir(dir)

	h := startLoop(t, Config{
		ProjectRoot: ".",
		ExtraWatch:  []string{"i18n/*.po"},
	})
	defer h.cancel()
	awaitRestart(t, h, 1, 1*time.Second)
	baseline := atomic.LoadInt64(&h.restarts)

	// Edit a file that doesn't match the glob.
	other := filepath.Join(dir, "i18n", "README.md")
	writeFile(t, other, "# unrelated\n")
	future := time.Now().Add(2 * time.Second)
	_ = os.Chtimes(other, future, future)

	// Give the poller a few cycles. No restart should follow.
	time.Sleep(200 * time.Millisecond)
	if got := atomic.LoadInt64(&h.restarts); got != baseline {
		t.Errorf("non-matching file triggered restart; baseline=%d got=%d", baseline, got)
	}
}

// TestExtraWatch_MultipleGlobs: multiple patterns compose; a match
// against any one triggers restart.
func TestExtraWatch_MultipleGlobs(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "i18n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "i18n", "en.po"), "# en\n")
	writeFile(t, filepath.Join(dir, "config", "app.toml"), "name = \"x\"\n")
	t.Chdir(dir)

	h := startLoop(t, Config{
		ProjectRoot: ".",
		ExtraWatch:  []string{"i18n/*.po", "config/*.toml"},
	})
	defer h.cancel()
	awaitRestart(t, h, 1, 1*time.Second)

	future := time.Now().Add(2 * time.Second)
	_ = os.Chtimes(filepath.Join(dir, "config", "app.toml"), future, future)
	awaitRestart(t, h, 2, 2*time.Second)
}

// TestCollectExtraWatchFiles_EmptyAndMissing: collectExtraWatchFiles is
// resilient to empty globs (returns nil) and to globs whose directories
// don't exist (filepath.Glob returns no matches, no error).
func TestCollectExtraWatchFiles_EmptyAndMissing(t *testing.T) {
	if got := collectExtraWatchFiles(".", nil); got != nil {
		t.Errorf("empty globs: got %v, want nil", got)
	}
	dir := t.TempDir()
	if got := collectExtraWatchFiles(dir, []string{"missing/*.po"}); len(got) != 0 {
		t.Errorf("missing dir: got %v, want empty", got)
	}
}
