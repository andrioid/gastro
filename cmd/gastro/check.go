package main

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/andrioid/gastro/internal/compiler"
)

// runCheck verifies that the on-disk .gastro/ directory matches what
// `gastro generate` would produce right now. Intended for CI:
//
//	gastro check
//
// Exit codes:
//
//	0 — .gastro/ is up to date
//	1 — drift detected (a list of differing files is printed to stderr)
//	2 — the check itself failed (cannot generate, cannot read .gastro/, etc.)
//
// Drift detection is byte-level: files are compared verbatim. Missing files
// (in either direction) are reported. The generated tree is produced in a
// temporary directory and discarded after the comparison.
func runCheck() error {
	projectDir := "."
	existingDir := filepath.Join(projectDir, ".gastro")

	if _, err := os.Stat(existingDir); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("no .gastro/ directory found — run `gastro generate` first")
		}
		return fmt.Errorf("stat %s: %w", existingDir, err)
	}

	tmpDir, err := os.MkdirTemp("", "gastro-check-")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	result, err := compiler.Compile(projectDir, tmpDir, compiler.CompileOptions{Strict: true})
	if err != nil {
		return fmt.Errorf("generate to temp dir: %w", err)
	}
	for _, w := range result.Warnings {
		fmt.Fprintf(os.Stderr, "gastro: warning: %s:%d: %s\n", w.File, w.Line, w.Message)
	}

	diffs, err := compareTrees(existingDir, tmpDir)
	if err != nil {
		return fmt.Errorf("compare trees: %w", err)
	}

	if len(diffs) == 0 {
		fmt.Println("gastro check: .gastro/ is up to date")
		return nil
	}

	fmt.Fprintln(os.Stderr, "gastro check: .gastro/ is out of date")
	fmt.Fprintln(os.Stderr)
	for _, d := range diffs {
		fmt.Fprintln(os.Stderr, "  "+d)
	}
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Run `gastro generate` and commit the result.")

	// Sentinel value the caller checks for to set exit code 1 (drift) vs 2 (error).
	return errDrift
}

// errDrift is returned by runCheck when the existing .gastro/ tree differs
// from what fresh code generation would produce. main.go treats this as
// exit code 1.
var errDrift = fmt.Errorf("gastro check: drift detected")

// compareTrees returns a sorted list of human-readable difference descriptions
// between two directory trees. Entries are paths relative to the supplied
// roots. Returns an empty slice when the trees are byte-identical.
//
// Excluded from the comparison:
//
//   - dev-server binaries (left over from `gastro dev`)
//   - .reload signal files (transient dev-mode IPC)
//   - directories themselves (only file content matters)
func compareTrees(have, want string) ([]string, error) {
	haveFiles, err := walkFiles(have)
	if err != nil {
		return nil, fmt.Errorf("walk %s: %w", have, err)
	}
	wantFiles, err := walkFiles(want)
	if err != nil {
		return nil, fmt.Errorf("walk %s: %w", want, err)
	}

	var diffs []string
	for rel := range wantFiles {
		if _, ok := haveFiles[rel]; !ok {
			diffs = append(diffs, "missing: "+rel)
			continue
		}
		hbytes, err := os.ReadFile(filepath.Join(have, rel))
		if err != nil {
			return nil, err
		}
		wbytes, err := os.ReadFile(filepath.Join(want, rel))
		if err != nil {
			return nil, err
		}
		if !bytes.Equal(hbytes, wbytes) {
			diffs = append(diffs, "differs: "+rel)
		}
	}
	for rel := range haveFiles {
		if _, ok := wantFiles[rel]; !ok {
			diffs = append(diffs, "stale:   "+rel)
		}
	}

	sort.Strings(diffs)
	return diffs, nil
}

// walkFiles returns a set of file paths under root, relative to root.
// Skipped: directories, dev-server binaries, transient IPC files.
func walkFiles(root string) (map[string]struct{}, error) {
	out := make(map[string]struct{})
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		switch rel {
		case "dev-server", ".reload":
			return nil
		}
		out[rel] = struct{}{}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
