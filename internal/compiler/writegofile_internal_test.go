package compiler

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// TestWriteGoFile_SkipsWhenIdentical asserts that calling writeGoFile twice
// with the same source bytes leaves the on-disk mod-time untouched on the
// second call. This is the property that lets external watchers (and the
// in-tree dev watcher) avoid spurious change events on no-op regenerations,
// and that keeps committed-.gastro/ trees free of spurious git diffs.
func TestWriteGoFile_SkipsWhenIdentical(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.go")

	src := []byte("package x\n\nvar X = 1\n")

	if err := writeGoFile(path, src); err != nil {
		t.Fatalf("first write: %v", err)
	}
	first, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat after first write: %v", err)
	}

	// Force the mod-time backwards so any new write would be obviously
	// detectable. Without this we'd be racing the filesystem timer
	// resolution (1s on some filesystems / OSes), which is exactly the
	// bug that motivated the fix.
	past := first.ModTime().Add(-2 * time.Hour)
	if err := os.Chtimes(path, past, past); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	if err := writeGoFile(path, src); err != nil {
		t.Fatalf("second write: %v", err)
	}
	second, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat after second write: %v", err)
	}

	if !second.ModTime().Equal(past) {
		t.Errorf("identical content rewrote the file: mod-time changed from %v to %v",
			past, second.ModTime())
	}

	// And the contents must still match (sanity check).
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != "package x\n\nvar X = 1\n" {
		t.Errorf("contents changed unexpectedly: %q", got)
	}
}

// TestWriteGoFile_WritesWhenDifferent is the positive complement to
// TestWriteGoFile_SkipsWhenIdentical: changed bytes must reach disk.
func TestWriteGoFile_WritesWhenDifferent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.go")

	if err := writeGoFile(path, []byte("package x\n\nvar X = 1\n")); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if err := writeGoFile(path, []byte("package x\n\nvar X = 2\n")); err != nil {
		t.Fatalf("second write: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != "package x\n\nvar X = 2\n" {
		t.Errorf("expected updated contents, got: %q", got)
	}
}

// TestWriteGoFile_AtomicOnFailure asserts that when the rename step fails,
// no half-written file (neither the temp sibling nor a corrupt destination)
// is left behind. We trigger a rename failure by making the destination
// directory read-only after the temp write succeeds — but because os.WriteFile
// to the temp itself would also fail in a read-only dir, we use a different
// strategy: set the destination path to be a directory, so os.Rename(tmp, dir)
// fails with "file exists" / "is a directory" depending on platform, and verify
// the temp file is cleaned up.
func TestWriteGoFile_AtomicOnFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		// Rename semantics differ on Windows; gastro watch v1 doesn't support
		// it anyway. Skip rather than maintain a parallel assertion.
		t.Skip("skipping on windows: rename-over-directory semantics differ")
	}

	dir := t.TempDir()
	// Create a directory at the destination path so os.Rename(tmp, dest)
	// fails — POSIX rename refuses to replace a non-empty directory with
	// a regular file.
	dest := filepath.Join(dir, "x.go")
	if err := os.Mkdir(dest, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dest, "blocker"), []byte("x"), 0o644); err != nil {
		t.Fatalf("seed blocker: %v", err)
	}

	err := writeGoFile(dest, []byte("package x\n"))
	if err == nil {
		t.Fatal("expected writeGoFile to fail when destination is a non-empty directory")
	}

	// The temp file must not have been left behind.
	tmp := dest + ".tmp"
	if _, statErr := os.Stat(tmp); !os.IsNotExist(statErr) {
		t.Errorf("expected temp file %q to be cleaned up, stat err: %v", tmp, statErr)
	}

	// And the original destination (the directory) must still be intact —
	// no partial overwrite.
	info, err := os.Stat(dest)
	if err != nil {
		t.Fatalf("stat dest: %v", err)
	}
	if !info.IsDir() {
		t.Errorf("expected dest to remain a directory, got mode %v", info.Mode())
	}
}
