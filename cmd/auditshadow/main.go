// auditshadow exercises the LSP shadow workspace against every
// .gastro file in every gastro project under a given path, and reports
// any non-import diagnostics that gopls / go-build would surface to a
// user editing those files.
//
// Used as a CI gate: the residual diagnostic count across all examples
// (and any future fixture trees) should stay at zero. A non-zero count
// means the shadow is producing Go that doesn't compile against the
// real runtime — the exact failure mode the audit document
// (docs/history/lsp-shadow-audit.md) was tracking.
//
// Usage:
//
//	auditshadow [<rootDir>]
//
// Default: the repository root (`.`). The supplied rootDir is walked
// for any directory containing pages/ or components/; each is treated
// as its own gastro project. This makes `auditshadow` work both for
// flat projects (point it at the root, audits that one project) and
// repo-shaped trees (point it at the repo root, audits every example
// or workspace under it).
//
// Exit codes:
//
//	0  — all files clean across all projects
//	1  — at least one file produced a non-import diagnostic
//	2  — setup error (couldn't open root, no projects found, etc.)
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/andrioid/gastro/internal/codegen"
	"github.com/andrioid/gastro/internal/lsp/shadow"
)

func main() {
	verbose := flag.Bool("v", false, "print every file's status, not just failures")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: auditshadow [-v] [<rootDir>]\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	rootDir := "."
	if flag.NArg() >= 1 {
		rootDir = flag.Arg(0)
	}

	os.Exit(runAll(rootDir, *verbose))
}

// runAll discovers every gastro project under rootDir and runs the
// per-project audit on each. Aggregate exit codes follow the
// most-severe-wins rule: a setup error in any project pins the
// process exit to 2; a real diagnostic in any project pins it to 1.
func runAll(rootDir string, verbose bool) int {
	projects, err := codegen.DiscoverProjects(rootDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "auditshadow: discovering projects under %s: %v\n", rootDir, err)
		return 2
	}
	if len(projects) == 0 {
		fmt.Fprintf(os.Stderr, "auditshadow: no gastro projects (containing pages/ or components/) found under %s\n", rootDir)
		return 2
	}

	overall := 0
	totalProjects := len(projects)
	totalFiles := 0
	totalReal := 0
	totalSetupErr := 0
	aggregateClasses := make(map[string]int)

	for i, p := range projects {
		if i > 0 {
			fmt.Println()
		}
		rel, _ := filepath.Rel(rootDir, p)
		if rel == "" || rel == "." {
			rel = p
		}
		fmt.Printf("--- project: %s ---\n", rel)
		code, summary := run(p, verbose)
		totalFiles += summary.files
		totalReal += summary.realDiags
		totalSetupErr += summary.setupErrs
		for cls, n := range summary.classCounts {
			aggregateClasses[cls] += n
		}
		if code > overall {
			overall = code
		}
	}

	if totalProjects > 1 {
		fmt.Println()
		fmt.Println("=== auditshadow aggregate summary ===")
		fmt.Printf("  projects:         %d\n", totalProjects)
		fmt.Printf("  files processed:  %d\n", totalFiles)
		fmt.Printf("  setup errors:     %d\n", totalSetupErr)
		fmt.Printf("  real diagnostics: %d (excluding import resolution)\n", totalReal)
		if len(aggregateClasses) > 0 {
			fmt.Println()
			fmt.Println("  by class:")
			var classes []string
			for c := range aggregateClasses {
				classes = append(classes, c)
			}
			sort.Slice(classes, func(i, j int) bool {
				return aggregateClasses[classes[i]] > aggregateClasses[classes[j]]
			})
			for _, c := range classes {
				fmt.Printf("    %-26s  %d\n", c, aggregateClasses[c])
			}
		}
	}
	return overall
}

// projectSummary collects the per-project numbers run() reports back
// to the aggregator. Kept private to this binary; callers outside
// auditshadow should not depend on these counts.
type projectSummary struct {
	files       int
	realDiags   int
	setupErrs   int
	classCounts map[string]int
}

func run(projectDir string, verbose bool) (int, projectSummary) {
	summary := projectSummary{classCounts: make(map[string]int)}
	absProject, err := filepath.Abs(projectDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "auditshadow: %v\n", err)
		return 2, summary
	}

	files, err := walkGastro(absProject)
	if err != nil {
		fmt.Fprintf(os.Stderr, "auditshadow: walking %s: %v\n", absProject, err)
		return 2, summary
	}
	if len(files) == 0 {
		fmt.Fprintf(os.Stderr, "auditshadow: no .gastro files under %s\n", absProject)
		return 2, summary
	}

	ws, err := shadow.NewWorkspace(absProject)
	if err != nil {
		fmt.Fprintf(os.Stderr, "auditshadow: creating workspace: %v\n", err)
		return 2, summary
	}
	defer ws.Close()

	failedFiles := 0
	totalReal := 0

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
			summary.classCounts[d.cls]++
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

	if len(summary.classCounts) > 0 {
		fmt.Println()
		fmt.Println("  by class:")
		var classes []string
		for c := range summary.classCounts {
			classes = append(classes, c)
		}
		sort.Slice(classes, func(i, j int) bool {
			return summary.classCounts[classes[i]] > summary.classCounts[classes[j]]
		})
		for _, c := range classes {
			fmt.Printf("    %-26s  %d\n", c, summary.classCounts[c])
		}
	}

	summary.files = len(files) - failedFiles
	summary.realDiags = totalReal
	summary.setupErrs = failedFiles
	if totalReal > 0 || failedFiles > 0 {
		return 1, summary
	}
	return 0, summary
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
