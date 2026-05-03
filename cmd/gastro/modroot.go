package main

import (
	"os"
	"path/filepath"
)

// findGoModuleRoot walks up from startAbs looking for a go.mod file.
// Returns the absolute directory containing go.mod, or "" if none is
// found before hitting a .git/ directory, the home boundary, or the
// filesystem root.
//
// startAbs must already be an absolute path. home is the upper bound
// (typically os.UserHomeDir's result); if "" the bound is disabled.
//
// Search order at each level:
//  1. If <dir>/go.mod exists → return dir.
//  2. Else if <dir>/.git exists → stop, return "" (we crossed a repo
//     boundary without finding a module root above us).
//  3. Else if dir == home → stop, return "" (don't walk past $HOME).
//  4. Else climb to filepath.Dir(dir). If unchanged (filesystem root),
//     stop, return "".
//
// The .git boundary is checked AFTER go.mod so a project that ships
// both at the same level (the common case) still resolves correctly.
// The home boundary is checked AFTER .git so a stray .git/ inside $HOME
// still terminates the walk early.
func findGoModuleRoot(startAbs, home string) string {
	dir := startAbs
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return ""
		}
		if home != "" && dir == home {
			return ""
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached filesystem root.
			return ""
		}
		dir = parent
	}
}
