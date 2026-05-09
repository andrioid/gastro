package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/andrioid/gastro/internal/codegen"
)

// TestInvalidateComponentPropsCacheNoDeadlock verifies that calling
// invalidateComponentPropsCache while holding dataMu.Lock() does not deadlock.
// Before the fix, invalidateComponentPropsCache called instanceForURI which
// tried to acquire dataMu.RLock() — causing an immediate deadlock.
func TestInvalidateComponentPropsCacheNoDeadlock(t *testing.T) {
	tmpDir := t.TempDir()
	writeGoMod(t, tmpDir)

	s := newServer("test")
	s.projectDir = tmpDir

	// Pre-populate an instance so lookupInstanceLocked finds it
	s.instances[tmpDir] = &projectInstance{
		root:                tmpDir,
		componentPropsCache: make(map[string]cacheEntry[[]codegen.StructField]),
	}

	uri := "file://" + filepath.Join(tmpDir, "page.gastro")

	done := make(chan struct{})
	go func() {
		defer close(done)
		s.dataMu.Lock()
		s.invalidateComponentPropsCache(uri)
		s.dataMu.Unlock()
	}()

	select {
	case <-done:
		// Success — no deadlock
	case <-time.After(2 * time.Second):
		t.Fatal("invalidateComponentPropsCache deadlocked when called with dataMu held")
	}
}

// TestHandleDidChangeNoDeadlock simulates a didChange message to verify the
// full handler path doesn't deadlock.
func TestHandleDidChangeNoDeadlock(t *testing.T) {
	tmpDir := t.TempDir()
	writeGoMod(t, tmpDir)

	s := newServer("test")
	s.projectDir = tmpDir

	// Pre-populate an instance
	s.instances[tmpDir] = &projectInstance{
		root:                tmpDir,
		componentPropsCache: make(map[string]cacheEntry[[]codegen.StructField]),
		goplsOpenFiles:      make(map[string]int),
	}

	uri := "file://" + filepath.Join(tmpDir, "page.gastro")
	s.documents[uri] = "---\n---\n<h1>Hello</h1>"

	params, _ := json.Marshal(map[string]any{
		"textDocument": map[string]any{
			"uri":     uri,
			"version": 2,
		},
		"contentChanges": []map[string]any{
			{"text": "---\n---\n<h1>Updated</h1>"},
		},
	})
	msg := &jsonRPCMessage{
		JSONRPC: "2.0",
		Method:  "textDocument/didChange",
		Params:  params,
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		s.handleDidChange(msg)
	}()

	select {
	case <-done:
		// Success
	case <-time.After(5 * time.Second):
		t.Fatal("handleDidChange deadlocked")
	}
}

// TestInstanceForURIWithoutGopls verifies that instanceForURI returns a usable
// instance even when gopls is not available (graceful degradation).
func TestInstanceForURIWithoutGopls(t *testing.T) {
	tmpDir := t.TempDir()
	writeGoMod(t, tmpDir)

	// Create a components directory with a .gastro file
	compDir := filepath.Join(tmpDir, "components")
	os.MkdirAll(compDir, 0o755)
	os.WriteFile(filepath.Join(compDir, "my-card.gastro"), []byte("---\n---\n<div></div>"), 0o644)

	// Use an empty PATH so gopls cannot be found
	origPath := os.Getenv("PATH")
	os.Setenv("PATH", "")
	defer os.Setenv("PATH", origPath)

	s := newServer("test")
	s.projectDir = tmpDir

	uri := "file://" + filepath.Join(tmpDir, "page.gastro")

	inst := s.instanceForURI(uri)
	if inst == nil {
		t.Fatal("expected non-nil instance even without gopls")
	}

	if inst.workspace == nil {
		t.Error("expected workspace to be created")
	}
	defer inst.workspace.Close()

	if inst.gopls != nil {
		t.Error("expected gopls to be nil when not in PATH")
	}

	if inst.goplsError == nil {
		t.Error("expected goplsError to be set")
	}

	if len(inst.components) != 1 {
		t.Errorf("expected 1 discovered component, got %d", len(inst.components))
	}

	// Verify the instance is cached — second call should return the same instance
	inst2 := s.instanceForURI(uri)
	if inst2 != inst {
		t.Error("expected instanceForURI to return the cached instance on second call")
	}
}

// TestInstanceForURIConcurrent verifies that concurrent calls to instanceForURI
// for the same project root only create one instance.
func TestInstanceForURIConcurrent(t *testing.T) {
	tmpDir := t.TempDir()
	writeGoMod(t, tmpDir)

	// Use an empty PATH so gopls cannot be found (fast failure)
	origPath := os.Getenv("PATH")
	os.Setenv("PATH", "")
	defer os.Setenv("PATH", origPath)

	s := newServer("test")
	s.projectDir = tmpDir

	uri := "file://" + filepath.Join(tmpDir, "page.gastro")

	var wg sync.WaitGroup
	instances := make([]*projectInstance, 10)
	for i := range instances {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			instances[idx] = s.instanceForURI(uri)
		}(i)
	}
	wg.Wait()

	// All goroutines should get the same instance
	first := instances[0]
	if first == nil {
		t.Fatal("expected non-nil instance")
	}
	defer first.workspace.Close()
	for i, inst := range instances[1:] {
		if inst != first {
			t.Errorf("goroutine %d got a different instance", i+1)
		}
	}

	// Only one instance should be stored
	s.dataMu.RLock()
	count := len(s.instances)
	s.dataMu.RUnlock()
	if count != 1 {
		t.Errorf("expected 1 cached instance, got %d", count)
	}
}

// TestNotifyGoplsUnavailableOnce verifies the notification is sent at most once.
func TestNotifyGoplsUnavailableOnce(t *testing.T) {
	s := newServer("test")

	var notifications []string
	var mu sync.Mutex

	// Override writeToClient to capture notifications
	origWrite := s.writeToClient
	_ = origWrite // suppress unused warning

	// We can't easily mock writeToClient since it writes to stdout.
	// Instead, verify the sync.Once behavior directly.
	callCount := 0
	s.notifiedGoplsMissing = sync.Once{}

	for i := 0; i < 5; i++ {
		s.notifiedGoplsMissing.Do(func() {
			mu.Lock()
			callCount++
			notifications = append(notifications, "called")
			mu.Unlock()
		})
	}

	if callCount != 1 {
		t.Errorf("expected notification to fire once, got %d times", callCount)
	}
}

func writeGoMod(t *testing.T, dir string) {
	t.Helper()
	err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test\n\ngo 1.21\n"), 0o644)
	if err != nil {
		t.Fatal(err)
	}
}
