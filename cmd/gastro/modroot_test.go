package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestFindGoModuleRoot covers the walk-up search for go.mod with
// .git/$HOME/filesystem-root upper bounds.
func TestFindGoModuleRoot(t *testing.T) {
	t.Run("go.mod at start dir", func(t *testing.T) {
		root := t.TempDir()
		mustWriteT(t, filepath.Join(root, "go.mod"), "module x\n")
		got := findGoModuleRoot(root, "")
		if got != root {
			t.Errorf("got %q, want %q", got, root)
		}
	})

	t.Run("go.mod two levels up", func(t *testing.T) {
		root := t.TempDir()
		deep := filepath.Join(root, "internal", "web")
		mustMkdirT(t, deep)
		mustWriteT(t, filepath.Join(root, "go.mod"), "module x\n")
		got := findGoModuleRoot(deep, "")
		if got != root {
			t.Errorf("got %q, want %q", got, root)
		}
	})

	t.Run("no go.mod anywhere returns empty", func(t *testing.T) {
		// Use a sandbox under tmp so we never accidentally find the
		// project's own go.mod by walking up.
		repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
		if err != nil {
			t.Fatalf("abs: %v", err)
		}
		base := filepath.Join(repoRoot, "tmp", "test-projects")
		if err := os.MkdirAll(base, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		root, err := os.MkdirTemp(base, "modroot-none-*")
		if err != nil {
			t.Fatalf("mkdtemp: %v", err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(root) })

		// Plant a .git at root so the walk stops there instead of
		// finding the repo's own go.mod above tmp/.
		mustMkdirT(t, filepath.Join(root, ".git"))
		deep := filepath.Join(root, "a", "b", "c")
		mustMkdirT(t, deep)

		got := findGoModuleRoot(deep, "")
		if got != "" {
			t.Errorf("got %q, want empty (no go.mod in tree)", got)
		}
	})

	t.Run(".git stops the walk before go.mod is found", func(t *testing.T) {
		// Layout:
		//   <root>/go.mod          <- this should NOT be returned
		//   <root>/sub/.git/       <- repo boundary
		//   <root>/sub/inner/      <- start here
		root := t.TempDir()
		mustWriteT(t, filepath.Join(root, "go.mod"), "module x\n")
		sub := filepath.Join(root, "sub")
		mustMkdirT(t, filepath.Join(sub, ".git"))
		inner := filepath.Join(sub, "inner")
		mustMkdirT(t, inner)

		got := findGoModuleRoot(inner, "")
		if got != "" {
			t.Errorf("got %q, want empty (.git boundary should block)", got)
		}
	})

	t.Run("go.mod and .git at same level: go.mod wins", func(t *testing.T) {
		root := t.TempDir()
		mustWriteT(t, filepath.Join(root, "go.mod"), "module x\n")
		mustMkdirT(t, filepath.Join(root, ".git"))
		deep := filepath.Join(root, "internal", "web")
		mustMkdirT(t, deep)

		got := findGoModuleRoot(deep, "")
		if got != root {
			t.Errorf("got %q, want %q (go.mod should win at same level)", got, root)
		}
	})

	t.Run("home boundary stops the walk", func(t *testing.T) {
		// home is checked AFTER .git, so we use a layout with no .git
		// to isolate the home-boundary behaviour.
		root := t.TempDir()
		// No go.mod, no .git — the only thing that should stop us is home.
		deep := filepath.Join(root, "a", "b")
		mustMkdirT(t, deep)

		got := findGoModuleRoot(deep, root)
		if got != "" {
			t.Errorf("got %q, want empty (should stop at home %q)", got, root)
		}
	})

	t.Run("home boundary irrelevant when go.mod found below", func(t *testing.T) {
		root := t.TempDir()
		deep := filepath.Join(root, "a", "b")
		mustMkdirT(t, deep)
		mustWriteT(t, filepath.Join(root, "a", "go.mod"), "module x\n")

		got := findGoModuleRoot(deep, root)
		want := filepath.Join(root, "a")
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})
}
