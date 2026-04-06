package server

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/andrioid/gastro/internal/codegen"
)

func TestDiscoverComponentsIn_Recursive(t *testing.T) {
	tmpDir := t.TempDir()

	// Create nested component structure
	dirs := []string{
		filepath.Join(tmpDir, "components"),
		filepath.Join(tmpDir, "components", "ui"),
		filepath.Join(tmpDir, "components", "ui", "forms"),
	}
	for _, d := range dirs {
		os.MkdirAll(d, 0o755)
	}

	files := map[string]string{
		"components/card.gastro":           "---\n---\n<div></div>",
		"components/ui/button.gastro":      "---\n---\n<button></button>",
		"components/ui/forms/input.gastro": "---\n---\n<input />",
		"components/ui/post-card.gastro":   "---\n---\n<article></article>",
		"components/not-a-component.txt":   "ignored",
		"components/ui/also-ignored.html":  "<p>nope</p>",
	}
	for rel, content := range files {
		os.WriteFile(filepath.Join(tmpDir, rel), []byte(content), 0o644)
	}

	components := discoverComponentsIn(tmpDir)

	want := map[string]string{
		"Card":     "components/card.gastro",
		"Button":   "components/ui/button.gastro",
		"Input":    "components/ui/forms/input.gastro",
		"PostCard": "components/ui/post-card.gastro",
	}

	if len(components) != len(want) {
		t.Fatalf("expected %d components, got %d: %+v", len(want), len(components), components)
	}

	found := make(map[string]string, len(components))
	for _, c := range components {
		found[c.Name] = c.Path
	}

	for name, path := range want {
		got, ok := found[name]
		if !ok {
			t.Errorf("missing component %q", name)
			continue
		}
		if got != path {
			t.Errorf("component %q: got path %q, want %q", name, got, path)
		}
	}
}

func TestDiscoverComponentsIn_SkipsHiddenDirs(t *testing.T) {
	tmpDir := t.TempDir()
	os.MkdirAll(filepath.Join(tmpDir, "components", ".hidden"), 0o755)
	os.MkdirAll(filepath.Join(tmpDir, "components", "visible"), 0o755)

	os.WriteFile(filepath.Join(tmpDir, "components", ".hidden", "secret.gastro"), []byte("---\n---\n"), 0o644)
	os.WriteFile(filepath.Join(tmpDir, "components", "visible", "card.gastro"), []byte("---\n---\n"), 0o644)

	components := discoverComponentsIn(tmpDir)

	if len(components) != 1 {
		t.Fatalf("expected 1 component, got %d: %+v", len(components), components)
	}
	if components[0].Name != "Card" {
		t.Errorf("expected Card, got %q", components[0].Name)
	}
}

func TestDiscoverComponentsIn_NoComponentsDir(t *testing.T) {
	tmpDir := t.TempDir()
	components := discoverComponentsIn(tmpDir)
	if len(components) != 0 {
		t.Errorf("expected 0 components, got %d", len(components))
	}
}

func TestDiscoverComponentsIn_Empty(t *testing.T) {
	tmpDir := t.TempDir()
	os.MkdirAll(filepath.Join(tmpDir, "components"), 0o755)
	components := discoverComponentsIn(tmpDir)
	if len(components) != 0 {
		t.Errorf("expected 0 components, got %d", len(components))
	}
}

func TestGetComponents_PicksUpNewFiles(t *testing.T) {
	tmpDir := t.TempDir()
	compDir := filepath.Join(tmpDir, "components")
	os.MkdirAll(compDir, 0o755)

	os.WriteFile(filepath.Join(compDir, "card.gastro"), []byte("---\n---\n"), 0o644)

	inst := &projectInstance{
		root:                tmpDir,
		components:          discoverComponentsIn(tmpDir),
		componentsScannedAt: time.Now().Add(-10 * time.Second), // expired cache
		componentPropsCache: make(map[string][]codegen.StructField),
	}

	if len(inst.getComponents()) != 1 {
		t.Fatalf("expected 1 component initially")
	}

	// Add a new component file
	os.WriteFile(filepath.Join(compDir, "button.gastro"), []byte("---\n---\n"), 0o644)

	// Force cache expiry by backdating
	inst.componentsMu.Lock()
	inst.componentsScannedAt = time.Now().Add(-10 * time.Second)
	inst.componentsMu.Unlock()

	components := inst.getComponents()
	if len(components) != 2 {
		t.Fatalf("expected 2 components after adding file, got %d", len(components))
	}
}

func TestGetComponents_CacheHit(t *testing.T) {
	tmpDir := t.TempDir()
	compDir := filepath.Join(tmpDir, "components")
	os.MkdirAll(compDir, 0o755)

	os.WriteFile(filepath.Join(compDir, "card.gastro"), []byte("---\n---\n"), 0o644)

	inst := &projectInstance{
		root:                tmpDir,
		components:          discoverComponentsIn(tmpDir),
		componentsScannedAt: time.Now(), // fresh cache
		componentPropsCache: make(map[string][]codegen.StructField),
	}

	// Add a new file — but cache should still be fresh
	os.WriteFile(filepath.Join(compDir, "button.gastro"), []byte("---\n---\n"), 0o644)

	components := inst.getComponents()
	if len(components) != 1 {
		t.Fatalf("expected 1 component (cache hit), got %d", len(components))
	}
}

func TestGetComponents_Concurrent(t *testing.T) {
	tmpDir := t.TempDir()
	compDir := filepath.Join(tmpDir, "components")
	os.MkdirAll(compDir, 0o755)

	os.WriteFile(filepath.Join(compDir, "card.gastro"), []byte("---\n---\n"), 0o644)

	inst := &projectInstance{
		root:                tmpDir,
		components:          discoverComponentsIn(tmpDir),
		componentsScannedAt: time.Now().Add(-10 * time.Second), // expired
		componentPropsCache: make(map[string][]codegen.StructField),
	}

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			components := inst.getComponents()
			if len(components) < 1 {
				t.Errorf("expected at least 1 component, got %d", len(components))
			}
		}()
	}
	wg.Wait()
}
