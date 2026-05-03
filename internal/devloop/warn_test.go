package devloop

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// chdirMu serialises chdir-based tests across the package — duplicated
// from devloop_test.go (which is in the *_test package) because warn_test
// is internal and can't reach into that file's helpers.
var warnTestChdirMu sync.Mutex

// TestWarnEmptySources_BothMissing: when neither pages/ nor components/
// has .gastro files, both warnings fire and Run continues running.
func TestWarnEmptySources_BothMissing(t *testing.T) {
	root := t.TempDir()
	// Create the directories empty (no .gastro files inside).
	if err := os.MkdirAll(filepath.Join(root, "pages"), 0o755); err != nil {
		t.Fatalf("mkdir pages: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "components"), 0o755); err != nil {
		t.Fatalf("mkdir components: %v", err)
	}

	var buf bytes.Buffer
	warnEmptySources(&buf, root)

	out := buf.String()
	if !strings.Contains(out, "pages/ has no .gastro files yet") {
		t.Errorf("expected pages/ warning in output, got:\n%s", out)
	}
	if !strings.Contains(out, "components/ has no .gastro files yet") {
		t.Errorf("expected components/ warning in output, got:\n%s", out)
	}
}

// TestWarnEmptySources_OneMissing: only the empty dir gets a warning.
func TestWarnEmptySources_OneMissing(t *testing.T) {
	root := t.TempDir()
	pagesDir := filepath.Join(root, "pages")
	if err := os.MkdirAll(pagesDir, 0o755); err != nil {
		t.Fatalf("mkdir pages: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pagesDir, "index.gastro"),
		[]byte("---\n---\n<h1>x</h1>\n"), 0o644); err != nil {
		t.Fatalf("write index: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "components"), 0o755); err != nil {
		t.Fatalf("mkdir components: %v", err)
	}

	var buf bytes.Buffer
	warnEmptySources(&buf, root)

	out := buf.String()
	if strings.Contains(out, "pages/ has no .gastro") {
		t.Errorf("pages/ has a .gastro file but warning fired anyway:\n%s", out)
	}
	if !strings.Contains(out, "components/ has no .gastro files yet") {
		t.Errorf("expected components/ warning, got:\n%s", out)
	}
}

// TestWarnEmptySources_BothPopulated: no warnings fire when both have
// at least one .gastro file.
func TestWarnEmptySources_BothPopulated(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"pages", "components"} {
		full := filepath.Join(root, dir)
		if err := os.MkdirAll(full, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
		if err := os.WriteFile(filepath.Join(full, "x.gastro"),
			[]byte("---\n---\n<x/>\n"), 0o644); err != nil {
			t.Fatalf("write x.gastro: %v", err)
		}
	}

	var buf bytes.Buffer
	warnEmptySources(&buf, root)

	out := buf.String()
	if strings.Contains(out, "has no .gastro files yet") {
		t.Errorf("no warnings expected when both dirs are populated, got:\n%s", out)
	}
}

// TestRun_EmptyProjectStillRuns asserts the integration property the
// plan calls out: with empty pages/ and components/, devloop.Run does
// not exit early — it keeps watching so newly-added files are picked up
// by the polling watcher's "new file" branch. Run also writes the
// expected warning to stderr (captured via the swappable stderrSink).
func TestRun_EmptyProjectStillRuns(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"pages", "components", "static"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}

	// Capture warning output via the package-level sink.
	var buf bytes.Buffer
	origSink := stderrSink
	stderrSink = &buf
	t.Cleanup(func() { stderrSink = origSink })

	warnTestChdirMu.Lock()
	defer warnTestChdirMu.Unlock()
	orig, _ := os.Getwd()
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer os.Chdir(orig)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	genCh := make(chan struct{}, 4)
	restartCh := make(chan struct{}, 4)
	done := make(chan struct{})

	go func() {
		defer close(done)
		_ = Run(ctx, Config{
			PollInterval:  20 * time.Millisecond,
			DebounceDelay: 30 * time.Millisecond,
			Generate: func() ([]string, error) {
				genCh <- struct{}{}
				return nil, nil
			},
			OnRestart: func(_ context.Context) error {
				restartCh <- struct{}{}
				return nil
			},
		})
	}()

	// Initial Generate + OnRestart should fire even though there are
	// no .gastro files to compile.
	select {
	case <-genCh:
	case <-time.After(2 * time.Second):
		t.Fatal("initial Generate never fired against empty project")
	}
	select {
	case <-restartCh:
	case <-time.After(2 * time.Second):
		t.Fatal("initial OnRestart never fired against empty project")
	}

	// Run should still be alive — give it a moment then cancel.
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not exit after cancel")
	}

	out := buf.String()
	if !strings.Contains(out, "pages/ has no .gastro files yet") {
		t.Errorf("expected pages/ warning in stderr, got:\n%s", out)
	}
	if !strings.Contains(out, "components/ has no .gastro files yet") {
		t.Errorf("expected components/ warning in stderr, got:\n%s", out)
	}
}
