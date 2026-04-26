package main

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func TestCompareTrees_Identical(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	mustWrite(t, filepath.Join(a, "x.go"), "package x\n")
	mustWrite(t, filepath.Join(b, "x.go"), "package x\n")

	diffs, err := compareTrees(a, b)
	if err != nil {
		t.Fatalf("compareTrees: %v", err)
	}
	if len(diffs) != 0 {
		t.Errorf("expected no diffs, got %v", diffs)
	}
}

func TestCompareTrees_DetectsAllThreeKinds(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	// Same file:
	mustWrite(t, filepath.Join(a, "same.go"), "ok\n")
	mustWrite(t, filepath.Join(b, "same.go"), "ok\n")
	// File present in a but not in b -> stale.
	mustWrite(t, filepath.Join(a, "stale.go"), "stale\n")
	// File present in b but not in a -> missing.
	mustWrite(t, filepath.Join(b, "new.go"), "new\n")
	// File in both but differing content -> differs.
	mustWrite(t, filepath.Join(a, "drift.go"), "old\n")
	mustWrite(t, filepath.Join(b, "drift.go"), "new\n")

	diffs, err := compareTrees(a, b)
	if err != nil {
		t.Fatalf("compareTrees: %v", err)
	}
	want := []string{
		"differs: drift.go",
		"missing: new.go",
		"stale:   stale.go",
	}
	sort.Strings(diffs)
	sort.Strings(want)
	if !equalSlices(diffs, want) {
		t.Errorf("diffs = %v, want %v", diffs, want)
	}
}

func TestCompareTrees_SkipsDevServerAndReloadSignal(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	// dev-server binary lives only in a (ignored).
	mustWrite(t, filepath.Join(a, "dev-server"), "binary content")
	// .reload IPC file lives only in a (ignored).
	mustWrite(t, filepath.Join(a, ".reload"), "1234")

	// Other files must be reported.
	mustWrite(t, filepath.Join(a, "real.go"), "x")

	diffs, err := compareTrees(a, b)
	if err != nil {
		t.Fatalf("compareTrees: %v", err)
	}
	if len(diffs) != 1 || diffs[0] != "stale:   real.go" {
		t.Errorf("diffs = %v, want only [stale: real.go]", diffs)
	}
}

func TestCompareTrees_RecursesSubdirectories(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	mustMkdirAll(t, filepath.Join(a, "templates"))
	mustMkdirAll(t, filepath.Join(b, "templates"))
	mustWrite(t, filepath.Join(a, "templates", "x.html"), "<p>old</p>")
	mustWrite(t, filepath.Join(b, "templates", "x.html"), "<p>new</p>")

	diffs, err := compareTrees(a, b)
	if err != nil {
		t.Fatalf("compareTrees: %v", err)
	}
	want := "differs: " + filepath.Join("templates", "x.html")
	if len(diffs) != 1 || diffs[0] != want {
		t.Errorf("diffs = %v, want [%s]", diffs, want)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func mustMkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
}

func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
