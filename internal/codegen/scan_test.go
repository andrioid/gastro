package codegen_test

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/andrioid/gastro/internal/codegen"
)

// writeTreeT is a small filesystem helper used to set up component
// scan fixtures inline. Keeps tests self-contained without pulling in
// a fixture corpus this early.
func writeTreeT(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, body := range files {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(full), err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", full, err)
		}
	}
}

func TestScanComponents_NoComponentsDir(t *testing.T) {
	tmp := t.TempDir()
	got, err := codegen.ScanComponents(tmp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil for missing components dir, got %v", got)
	}
}

func TestScanComponents_EmptyComponentsDir(t *testing.T) {
	tmp := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmp, "components"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	got, err := codegen.ScanComponents(tmp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty slice for empty components dir, got %v", got)
	}
}

func TestScanComponents_ParsesPropsAndChildren(t *testing.T) {
	tmp := t.TempDir()
	writeTreeT(t, tmp, map[string]string{
		"components/card.gastro": `---
type Props struct {
	Title string
	Count int
}
Title := gastro.Props().Title
Count := gastro.Props().Count
---
<div>{{ .Title }} ({{ .Count }})</div>
`,
		"components/layout.gastro": `---
type Props struct {
	Heading string
}
Heading := gastro.Props().Heading
---
<main><h1>{{ .Heading }}</h1>{{ .Children }}</main>
`,
		"components/bare.gastro": `<p>no props, no children</p>
`,
	})

	got, err := codegen.ScanComponents(tmp)
	if err != nil {
		t.Fatalf("ScanComponents: %v", err)
	}

	byPath := make(map[string]codegen.ComponentSchema)
	for _, s := range got {
		byPath[s.RelPath] = s
	}

	card, ok := byPath["components/card.gastro"]
	if !ok {
		t.Fatalf("expected components/card.gastro in results, got %v", got)
	}
	if card.ExportedName != "Card" {
		t.Errorf("Card.ExportedName = %q, want %q", card.ExportedName, "Card")
	}
	if card.FuncName != "componentCard" {
		t.Errorf("Card.FuncName = %q, want %q", card.FuncName, "componentCard")
	}
	if !card.HasProps {
		t.Error("Card.HasProps = false, want true")
	}
	if card.HasChildren {
		t.Error("Card.HasChildren = true, want false")
	}
	wantFields := []codegen.StructField{
		{Name: "Title", Type: "string"},
		{Name: "Count", Type: "int"},
	}
	if !equalFields(card.PropsFields, wantFields) {
		t.Errorf("Card.PropsFields = %v, want %v", card.PropsFields, wantFields)
	}

	layout, ok := byPath["components/layout.gastro"]
	if !ok {
		t.Fatalf("expected components/layout.gastro in results")
	}
	if !layout.HasChildren {
		t.Error("Layout.HasChildren = false, want true")
	}
	if !layout.HasProps {
		t.Error("Layout.HasProps = false, want true")
	}

	bare, ok := byPath["components/bare.gastro"]
	if !ok {
		t.Fatalf("expected components/bare.gastro in results")
	}
	if bare.HasProps {
		t.Error("Bare.HasProps = true, want false")
	}
	if len(bare.PropsFields) != 0 {
		t.Errorf("Bare.PropsFields = %v, want empty", bare.PropsFields)
	}
	if bare.HasChildren {
		t.Error("Bare.HasChildren = true, want false")
	}
}

func TestScanComponents_KebabAndNestedNames(t *testing.T) {
	tmp := t.TempDir()
	writeTreeT(t, tmp, map[string]string{
		// Kebab-case at top level — codegen folds segments to PascalCase.
		"components/post-card.gastro": `<p>x</p>
`,
		// Nested directories — codegen joins path segments
		// (post/foo-bar.gastro -> "componentPostFooBar" / "PostFooBar").
		// This is a real bug source: the LSP server's old
		// discoverComponentsIn computed only "FooBar" here, then
		// auto-import would suggest a name that doesn't compile.
		// Scanning through codegen.HandlerFuncName fixes that.
		"components/post/foo-bar.gastro": `<p>y</p>
`,
	})

	got, err := codegen.ScanComponents(tmp)
	if err != nil {
		t.Fatalf("ScanComponents: %v", err)
	}

	byPath := make(map[string]codegen.ComponentSchema)
	for _, s := range got {
		byPath[s.RelPath] = s
	}

	if pc, ok := byPath["components/post-card.gastro"]; !ok {
		t.Fatalf("missing post-card; got: %v", got)
	} else if pc.ExportedName != "PostCard" {
		t.Errorf("post-card.ExportedName = %q, want %q", pc.ExportedName, "PostCard")
	}

	if pf, ok := byPath["components/post/foo-bar.gastro"]; !ok {
		t.Fatalf("missing post/foo-bar; got: %v", got)
	} else if pf.ExportedName != "PostFooBar" {
		t.Errorf("post/foo-bar.ExportedName = %q, want %q (path segments must round-trip through HandlerFuncName)", pf.ExportedName, "PostFooBar")
	}
}

func TestScanComponents_SkipsHiddenDirs(t *testing.T) {
	tmp := t.TempDir()
	writeTreeT(t, tmp, map[string]string{
		"components/visible.gastro": `<p>v</p>
`,
		// Hidden directories like .gastro/ (the codegen output dir)
		// must not be re-scanned as components.
		"components/.gastro/cached.gastro": `<p>c</p>
`,
	})

	got, err := codegen.ScanComponents(tmp)
	if err != nil {
		t.Fatalf("ScanComponents: %v", err)
	}
	if len(got) != 1 || got[0].RelPath != "components/visible.gastro" {
		paths := make([]string, len(got))
		for i, s := range got {
			paths[i] = s.RelPath
		}
		sort.Strings(paths)
		t.Errorf("expected only [components/visible.gastro], got %v", paths)
	}
}

func TestScanComponents_CapturesImports(t *testing.T) {
	tmp := t.TempDir()
	writeTreeT(t, tmp, map[string]string{
		"components/withimport.gastro": `---
import (
	"fmt"
	"example.com/myapp/db"
)
type Props struct {
	Item db.Item
}
Item := gastro.Props().Item
_ = fmt.Sprintf
---
<p>{{ .Item }}</p>
`,
	})

	got, err := codegen.ScanComponents(tmp)
	if err != nil {
		t.Fatalf("ScanComponents: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 component, got %d", len(got))
	}
	imports := got[0].Imports
	want := map[string]bool{"fmt": true, "example.com/myapp/db": true}
	if len(imports) != len(want) {
		t.Errorf("Imports len = %d, want %d (got %v)", len(imports), len(want), imports)
	}
	for _, i := range imports {
		if !want[i] {
			t.Errorf("unexpected import %q in %v", i, imports)
		}
	}
}

func TestPropsByPath_OmitsComponentsWithoutFields(t *testing.T) {
	schemas := []codegen.ComponentSchema{
		{RelPath: "components/a.gastro", PropsFields: []codegen.StructField{{Name: "X", Type: "int"}}},
		{RelPath: "components/b.gastro", HasProps: false},
		{RelPath: "components/c.gastro", PropsFields: []codegen.StructField{{Name: "Y", Type: "string"}}},
	}
	got := codegen.PropsByPath(schemas)
	if _, ok := got["components/b.gastro"]; ok {
		t.Error("PropsByPath should omit components without fields")
	}
	if len(got) != 2 {
		t.Errorf("PropsByPath len = %d, want 2", len(got))
	}
	if got["components/a.gastro"][0].Name != "X" {
		t.Errorf("a.gastro fields wrong: %v", got["components/a.gastro"])
	}
}

func TestDiscoverProjects_SingleRoot(t *testing.T) {
	tmp := t.TempDir()
	writeTreeT(t, tmp, map[string]string{
		"pages/index.gastro":     `<p>x</p>`,
		"components/card.gastro": `<div>card</div>`,
	})

	got, err := codegen.DiscoverProjects(tmp)
	if err != nil {
		t.Fatalf("DiscoverProjects: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 project, got %d: %v", len(got), got)
	}
	// Resolve symlinks for the comparison to handle macOS /var → /private/var.
	wantAbs, _ := filepath.EvalSymlinks(tmp)
	gotAbs, _ := filepath.EvalSymlinks(got[0])
	if gotAbs != wantAbs {
		t.Errorf("got project %q, want %q", gotAbs, wantAbs)
	}
}

func TestDiscoverProjects_NestedProject(t *testing.T) {
	// Mirrors the git-pm shape: a Go module root with the gastro
	// project sitting under internal/web. DiscoverProjects must
	// descend through internal/ to find it.
	tmp := t.TempDir()
	writeTreeT(t, tmp, map[string]string{
		"go.mod":                           "module example\ngo 1.26\n",
		"internal/web/pages/index.gastro":  `<p>x</p>`,
		"internal/web/components/x.gastro": `<p>x</p>`,
	})

	got, err := codegen.DiscoverProjects(tmp)
	if err != nil {
		t.Fatalf("DiscoverProjects: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 project, got %d: %v", len(got), got)
	}
	wantSuffix := filepath.Join("internal", "web")
	if !strings.HasSuffix(got[0], wantSuffix) {
		t.Errorf("got %q, want path ending with %q", got[0], wantSuffix)
	}
}

func TestDiscoverProjects_MultipleProjects(t *testing.T) {
	// Mimics this repo's examples/ tree where multiple sibling gastro
	// projects coexist. Every one should be reported.
	tmp := t.TempDir()
	writeTreeT(t, tmp, map[string]string{
		"examples/blog/pages/index.gastro":      `<p>x</p>`,
		"examples/dashboard/pages/index.gastro": `<p>x</p>`,
		"examples/sse/pages/index.gastro":       `<p>x</p>`,
		"examples/sse/components/widget.gastro": `<p>x</p>`,
	})

	got, err := codegen.DiscoverProjects(tmp)
	if err != nil {
		t.Fatalf("DiscoverProjects: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 projects, got %d: %v", len(got), got)
	}
}

func TestDiscoverProjects_SkipsHiddenAndTestdata(t *testing.T) {
	tmp := t.TempDir()
	writeTreeT(t, tmp, map[string]string{
		"pages/index.gastro": `<p>x</p>`,
		// Codegen output: must not be reported as a separate project.
		".gastro/pages/leak.gastro": `<p>x</p>`,
		// Test fixtures: must not be reported.
		"testdata/proj/pages/leak.gastro": `<p>x</p>`,
		// node_modules nesting: same.
		"node_modules/foo/pages/leak.gastro": `<p>x</p>`,
	})

	got, err := codegen.DiscoverProjects(tmp)
	if err != nil {
		t.Fatalf("DiscoverProjects: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 project, got %d: %v", len(got), got)
	}
}

func TestDiscoverProjects_NoProjectsReturnsEmpty(t *testing.T) {
	tmp := t.TempDir()
	writeTreeT(t, tmp, map[string]string{
		"src/main.go": "package main\nfunc main(){}\n",
	})
	got, err := codegen.DiscoverProjects(tmp)
	if err != nil {
		t.Fatalf("DiscoverProjects: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected no projects, got %v", got)
	}
}

func equalFields(a, b []codegen.StructField) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Name != b[i].Name || a[i].Type != b[i].Type {
			return false
		}
	}
	return true
}
