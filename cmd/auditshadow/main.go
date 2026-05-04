// auditshadow exercises the LSP shadow workspace against every
// .gastro file in a project and reports any non-import diagnostics
// that gopls / go-build would surface to a user editing those files.
//
// Used as a CI gate: the residual diagnostic count for examples/gastro
// (and any future fixture trees) should stay at zero. A non-zero count
// means the shadow is producing Go that doesn't compile against the
// real runtime — the exact failure mode the audit document
// (tmp/lsp-shadow-audit.md) was tracking.
//
// Usage:
//
//	auditshadow [<projectDir>]
//
// Default: examples/gastro relative to the current working directory.
//
// Exit codes:
//
//	0  — all files clean
//	1  — at least one file produced a non-import diagnostic
//	2  — setup error (couldn't open project, etc.)
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/andrioid/gastro/internal/lsp/shadow"
)

func main() {
	verbose := flag.Bool("v", false, "print every file's status, not just failures")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: auditshadow [-v] [<projectDir>]\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	projectDir := "examples/gastro"
	if flag.NArg() >= 1 {
		projectDir = flag.Arg(0)
	}

	exit := run(projectDir, *verbose)
	os.Exit(exit)
}

func run(projectDir string, verbose bool) int {
	absProject, err := filepath.Abs(projectDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "auditshadow: %v\n", err)
		return 2
	}

	files, err := walkGastro(absProject)
	if err != nil {
		fmt.Fprintf(os.Stderr, "auditshadow: walking %s: %v\n", absProject, err)
		return 2
	}
	if len(files) == 0 {
		fmt.Fprintf(os.Stderr, "auditshadow: no .gastro files under %s\n", absProject)
		return 2
	}

	ws, err := shadow.NewWorkspace(absProject)
	if err != nil {
		fmt.Fprintf(os.Stderr, "auditshadow: creating workspace: %v\n", err)
		return 2
	}
	defer ws.Close()

	classCounts := make(map[string]int)
	totalReal := 0
	failedFiles := 0

	for _, rel := range files {
		content, err := os.ReadFile(filepath.Join(absProject, rel))
		if err != nil {
			fmt.Fprintf(os.Stderr, "  %s: read error: %v\n", rel, err)
			failedFiles++
			continue
		}

		if _, err := ws.UpdateFile(rel, string(content)); err != nil {
			fmt.Fprintf(os.Stderr, "  %s: shadow update error: %v\n", rel, err)
			failedFiles++
			continue
		}

		diags, err := goBuild(ws.Dir(), filepath.Dir(ws.VirtualFilePath(rel)))
		if err != nil {
			fmt.Fprintf(os.Stderr, "  %s: build invocation error: %v\n", rel, err)
			failedFiles++
			continue
		}

		fileReal := 0
		for _, d := range diags {
			if d.cls == "import" {
				continue
			}
			classCounts[d.cls]++
			fileReal++
		}
		totalReal += fileReal

		switch {
		case fileReal > 0:
			fmt.Printf("  %-60s  %d\n", rel, fileReal)
			for _, d := range diags {
				if d.cls == "import" {
					continue
				}
				fmt.Printf("      %s:%d: %s\n", filepath.Base(d.file), d.line, d.msg)
			}
		case verbose:
			fmt.Printf("  %-60s  ok\n", rel)
		}
	}

	fmt.Println()
	fmt.Printf("=== auditshadow summary ===\n")
	fmt.Printf("  project:          %s\n", projectDir)
	fmt.Printf("  files processed:  %d\n", len(files)-failedFiles)
	fmt.Printf("  setup errors:     %d\n", failedFiles)
	fmt.Printf("  real diagnostics: %d (excluding import resolution)\n", totalReal)

	if len(classCounts) > 0 {
		fmt.Println()
		fmt.Println("  by class:")
		var classes []string
		for c := range classCounts {
			classes = append(classes, c)
		}
		sort.Slice(classes, func(i, j int) bool {
			return classCounts[classes[i]] > classCounts[classes[j]]
		})
		for _, c := range classes {
			fmt.Printf("    %-26s  %d\n", c, classCounts[c])
		}
	}

	if totalReal > 0 || failedFiles > 0 {
		return 1
	}
	return 0
}

// goBuild runs `go build` on a single package directory inside the
// shadow workspace and returns parsed diagnostics. The toolchain
// itself enforces module-aware resolution, so this matches what the
// editor's gopls instance would surface.
func goBuild(shadowRoot, pkgDir string) ([]diag, error) {
	rel, err := filepath.Rel(shadowRoot, pkgDir)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command("go", "build", "-o", os.DevNull, "./"+rel)
	cmd.Dir = shadowRoot
	out, _ := cmd.CombinedOutput() // non-zero on diagnostics is expected
	return parseGoOutput(string(out)), nil
}

type diag struct {
	file string
	line int
	msg  string
	cls  string
}

func parseGoOutput(out string) []diag {
	var diags []diag
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, ":", 4)
		if len(parts) < 4 {
			diags = append(diags, diag{msg: line, cls: classify(line)})
			continue
		}
		var ln int
		fmt.Sscanf(parts[1], "%d", &ln)
		msg := strings.TrimSpace(parts[3])
		diags = append(diags, diag{
			file: parts[0],
			line: ln,
			msg:  msg,
			cls:  classify(msg),
		})
	}
	return diags
}

func classify(msg string) string {
	switch {
	case strings.Contains(msg, "could not import"),
		strings.Contains(msg, "cannot find package"),
		strings.Contains(msg, "no required module provides"),
		strings.Contains(msg, "is not in std"),
		strings.Contains(msg, "missing go.sum entry"):
		return "import"
	case strings.HasPrefix(msg, "undefined:"):
		return "undefined-identifier"
	case strings.Contains(msg, "has no field or method"),
		strings.Contains(msg, "undefined (type"):
		return "missing-method-or-field"
	case strings.Contains(msg, "declared and not used"),
		strings.Contains(msg, "declared but not used"):
		return "unused-local"
	case strings.Contains(msg, "imported and not used"):
		return "unused-import"
	case strings.Contains(msg, "cannot use"):
		return "type-mismatch"
	default:
		return "other"
	}
}

func walkGastro(root string) ([]string, error) {
	var out []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			name := info.Name()
			if name == ".gastro" || strings.HasPrefix(name, ".") || name == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".gastro") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		out = append(out, rel)
		return nil
	})
	sort.Strings(out)
	return out, err
}
