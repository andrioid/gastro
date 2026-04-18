package watcher_test

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/andrioid/gastro/internal/watcher"
)

func TestClassifyChange_FrontmatterChange(t *testing.T) {
	got := watcher.ClassifyChange("pages/index.gastro", watcher.SectionFrontmatter)
	if got != watcher.ChangeRestart {
		t.Errorf("frontmatter change should require restart, got %v", got)
	}
}

func TestClassifyChange_TemplateChange(t *testing.T) {
	got := watcher.ClassifyChange("pages/index.gastro", watcher.SectionTemplate)
	if got != watcher.ChangeReload {
		t.Errorf("template change should only need reload, got %v", got)
	}
}

func TestClassifyChange_StaticAsset(t *testing.T) {
	got := watcher.ClassifyChange("static/styles.css", watcher.SectionUnknown)
	if got != watcher.ChangeReload {
		t.Errorf("static asset change should only need reload, got %v", got)
	}
}

func TestCollectGastroFiles(t *testing.T) {
	dir := t.TempDir()
	// Create some .gastro files
	os.MkdirAll(filepath.Join(dir, "pages", "blog"), 0o755)
	os.WriteFile(filepath.Join(dir, "pages", "index.gastro"), []byte("---\n---\n<h1>hi</h1>"), 0o644)
	os.WriteFile(filepath.Join(dir, "pages", "blog", "index.gastro"), []byte("---\n---\n<h1>blog</h1>"), 0o644)
	os.WriteFile(filepath.Join(dir, "pages", "ignore.txt"), []byte("not gastro"), 0o644)

	files, err := watcher.CollectGastroFiles(filepath.Join(dir, "pages"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(files) != 2 {
		t.Fatalf("expected 2 .gastro files, got %d: %v", len(files), files)
	}
}

func TestExternalDeps_SetSnapshot(t *testing.T) {
	var deps watcher.ExternalDeps

	if paths, ver := deps.Snapshot(); len(paths) != 0 || ver != 0 {
		t.Fatalf("zero value: want empty/0, got %v/%d", paths, ver)
	}

	deps.Set([]string{"/tmp/b.md", "/tmp/a.md"})
	paths, ver := deps.Snapshot()
	if ver != 1 {
		t.Errorf("version after first Set = %d, want 1", ver)
	}
	// Sorted & preserved.
	if len(paths) != 2 || paths[0] != "/tmp/a.md" || paths[1] != "/tmp/b.md" {
		t.Errorf("paths = %v, want sorted [a,b]", paths)
	}
}

func TestExternalDeps_Dedupe(t *testing.T) {
	var deps watcher.ExternalDeps
	deps.Set([]string{"/tmp/a.md", "/tmp/a.md", "/tmp/b.md"})
	paths, _ := deps.Snapshot()
	if len(paths) != 2 {
		t.Errorf("expected dedupe to 2 paths, got %d: %v", len(paths), paths)
	}
}

func TestExternalDeps_VersionUnchangedOnEqualSet(t *testing.T) {
	var deps watcher.ExternalDeps
	deps.Set([]string{"/tmp/a.md", "/tmp/b.md"})
	_, v1 := deps.Snapshot()

	// Same set, different order — should not bump version.
	deps.Set([]string{"/tmp/b.md", "/tmp/a.md"})
	_, v2 := deps.Snapshot()
	if v1 != v2 {
		t.Errorf("version bumped for equal set: %d -> %d", v1, v2)
	}

	// Actual change — should bump.
	deps.Set([]string{"/tmp/a.md"})
	_, v3 := deps.Snapshot()
	if v3 != v2+1 {
		t.Errorf("version after real change: %d, want %d", v3, v2+1)
	}
}

func TestExternalDeps_Symlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "real.md")
	link := filepath.Join(dir, "link.md")
	if err := os.WriteFile(target, []byte("# real"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}

	var deps watcher.ExternalDeps
	deps.Set([]string{target, link})
	paths, _ := deps.Snapshot()
	if len(paths) != 1 {
		t.Errorf("symlink + target should dedupe to 1 path, got %d: %v", len(paths), paths)
	}
}

func TestExternalDeps_ConcurrentAccess(t *testing.T) {
	var deps watcher.ExternalDeps
	done := make(chan struct{})
	go func() {
		for i := 0; i < 1000; i++ {
			deps.Set([]string{"/tmp/a.md", "/tmp/b.md"})
		}
		close(done)
	}()
	for i := 0; i < 1000; i++ {
		_, _ = deps.Snapshot()
	}
	<-done
}

func TestDetectChangedSection_TemplateOnly(t *testing.T) {
	old := "---\nTitle := \"Hello\"\n---\n<h1>Old</h1>"
	new_ := "---\nTitle := \"Hello\"\n---\n<h1>New</h1>"

	section := watcher.DetectChangedSection(old, new_)
	if section != watcher.SectionTemplate {
		t.Errorf("expected SectionTemplate, got %v", section)
	}
}

func TestDetectChangedSection_FrontmatterChanged(t *testing.T) {
	old := "---\nTitle := \"Hello\"\n---\n<h1>Hello</h1>"
	new_ := "---\nTitle := \"World\"\n---\n<h1>Hello</h1>"

	section := watcher.DetectChangedSection(old, new_)
	if section != watcher.SectionFrontmatter {
		t.Errorf("expected SectionFrontmatter, got %v", section)
	}
}

func TestDetectChangedSection_BothChanged(t *testing.T) {
	old := "---\nTitle := \"Hello\"\n---\n<h1>Old</h1>"
	new_ := "---\nTitle := \"World\"\n---\n<h1>New</h1>"

	section := watcher.DetectChangedSection(old, new_)
	if section != watcher.SectionFrontmatter {
		// Frontmatter change takes priority — needs restart
		t.Errorf("expected SectionFrontmatter when both changed, got %v", section)
	}
}

func TestDebounce(t *testing.T) {
	var count atomic.Int32
	fn := watcher.Debounce(50*time.Millisecond, func() {
		count.Add(1)
	})

	// Fire rapidly
	fn()
	fn()
	fn()

	// Wait for debounce to settle
	time.Sleep(100 * time.Millisecond)

	if got := count.Load(); got != 1 {
		t.Errorf("expected debounced function to fire once, got %d", got)
	}
}
