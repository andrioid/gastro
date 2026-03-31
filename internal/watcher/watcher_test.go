package watcher_test

import (
	"os"
	"path/filepath"
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
	got := watcher.ClassifyChange("public/styles.css", watcher.SectionUnknown)
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
	count := 0
	fn := watcher.Debounce(50*time.Millisecond, func() {
		count++
	})

	// Fire rapidly
	fn()
	fn()
	fn()

	// Wait for debounce to settle
	time.Sleep(100 * time.Millisecond)

	if count != 1 {
		t.Errorf("expected debounced function to fire once, got %d", count)
	}
}
