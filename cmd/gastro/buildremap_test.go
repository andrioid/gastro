package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andrioid/gastro/internal/compiler"
)

// genLineOf returns the 1-indexed line of the first generated line containing
// needle, or 0.
func genLineOf(t *testing.T, path, needle string) int {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	for i, l := range strings.Split(string(b), "\n") {
		if strings.Contains(l, needle) {
			return i + 1
		}
	}
	return 0
}

// TestRemapBuildOutput covers issue #38: go build diagnostics that point at
// generated .gastro/*.go must be rewritten back to the .gastro source —
// exactly for frontmatter-region lines, and with a file pointer + breadcrumb
// otherwise.
func TestRemapBuildOutput(t *testing.T) {
	dir := t.TempDir()
	pages := filepath.Join(dir, "pages")
	if err := os.MkdirAll(pages, 0o755); err != nil {
		t.Fatal(err)
	}
	// Source lines: 1 `---`, 2 import, 3 Title, 4 Count, 5 Bad (offender),
	// 6 strconv use, 7 `---`, 8 body.
	src := "---\n" +
		"import \"strconv\"\n" +
		"Title := \"Hello\"\n" +
		"Count := 0\n" +
		"Bad := Title + Count\n" +
		"_ = strconv.Itoa(Count)\n" +
		"---\n" +
		"<main>{{ .Title }}</main>\n"
	if err := os.WriteFile(filepath.Join(pages, "index.gastro"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module ex\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := compiler.Compile(dir, filepath.Join(dir, ".gastro"), compiler.CompileOptions{}); err != nil {
		t.Fatalf("compile: %v", err)
	}

	genPath := filepath.Join(dir, ".gastro", "pages_index.go")
	offenderLine := genLineOf(t, genPath, "Bad := Title + Count")
	if offenderLine == 0 {
		t.Fatal("offending statement not found in generated output")
	}

	// 1. Frontmatter-region line: exact remap to source line 5, col preserved.
	in := fmt.Sprintf("# ex/.gastro\n.gastro/pages_index.go:%d:9: invalid operation: Title + Count (mismatched types string and int)\n", offenderLine)
	got := remapBuildOutput(in, dir)
	if !strings.Contains(got, "pages/index.gastro:5:9:") {
		t.Errorf("frontmatter remap: got %q, want source line 5:9", got)
	}
	if strings.Contains(got, ".gastro/pages_index.go:") {
		t.Errorf("frontmatter remap should drop the generated path: %q", got)
	}

	// 2. Out-of-region line (package clause, line 2): file pointer + breadcrumb.
	got2 := remapBuildOutput(".gastro/pages_index.go:2:1: some error\n", dir)
	if !strings.Contains(got2, "pages/index.gastro [generated .gastro/pages_index.go:2:1]") {
		t.Errorf("out-of-region remap: got %q, want file pointer + breadcrumb", got2)
	}

	// 3. Unknown generated file (no source): left untouched.
	got3 := remapBuildOutput(".gastro/routes.go:5:1: boom\n", dir)
	if !strings.Contains(got3, ".gastro/routes.go:5:1:") {
		t.Errorf("unknown generated file should be left untouched: %q", got3)
	}

	// 4. Output with no generated reference: returned unchanged.
	plain := "all good\n"
	if remapBuildOutput(plain, dir) != plain {
		t.Errorf("plain output should be unchanged")
	}
}
