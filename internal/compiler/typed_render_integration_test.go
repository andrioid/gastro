package compiler_test

// Integration tests for typed rendering (plan v0.4 §4.5, Phase 4):
// Router.Render[P](gastro.Component[P], P).
//
//   - Render output is byte-identical to the same component rendered
//     from a template dict.
//   - Render passes the typed struct straight through: no props map, so
//     it allocates less than the dict path (in-package benchmark/test).
//   - Passing another component's Props type fails to compile.
//   - Frontmatter can call gastro.Render(Card, CardProps{…}).
//
// The project lives under tmp/test-projects/ because Go ignores go.mod
// files in the system temp root.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andrioid/gastro/internal/compiler"
)

// newBuildableProject creates an empty project directory under
// <repo>/tmp/test-projects that is removed when the test ends.
func newBuildableProject(t *testing.T, prefix string) (projectDir, repoRoot string) {
	t.Helper()
	repoRoot = findRepoRoot(t)
	base := filepath.Join(repoRoot, "tmp", "test-projects")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", base, err)
	}
	dir, err := os.MkdirTemp(base, prefix+"-*")
	if err != nil {
		t.Fatalf("mkdir-temp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir, repoRoot
}

func writeProjectFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		mustWriteFile(t, path, content)
	}
}

func runGo(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOFLAGS=")
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestTypedRender(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping typed-render integration test in -short mode")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not in PATH")
	}

	projectDir, repoRoot := newBuildableProject(t, "typed-render")
	writeProjectFiles(t, projectDir, map[string]string{
		"components/card.gastro": "---\n" +
			"type Props struct {\n\tTitle string\n\tCount int\n}\n\n" +
			"p := gastro.Props()\n" +
			"Title := p.Title\n" +
			"Count := p.Count\n" +
			"---\n" +
			"<div class=\"card\"><h2>{{ .Title }}</h2><span>{{ .Count }}</span></div>\n",
		"components/badge.gastro": "---\n" +
			"type Props struct {\n\tLabel string\n}\n\n" +
			"Label := gastro.Props().Label\n" +
			"---\n" +
			"<em>{{ .Label }}</em>\n",
		"components/panel.gastro": "---\n" +
			"type Props struct {\n\tHeading string\n}\n\n" +
			"Heading := gastro.Props().Heading\n" +
			"---\n" +
			"<section><h3>{{ .Heading }}</h3>{{ .Children }}</section>\n",
		"components/divider.gastro": "<hr class=\"divider\">\n",
		"pages/index.gastro": "---\n" +
			"import (\n\tCard \"components/card.gastro\"\n\tPanel \"components/panel.gastro\"\n\tDivider \"components/divider.gastro\"\n)\n" +
			"---\n" +
			"<main>[CARD]{{ Card (dict \"Title\" \"Hello <world>\" \"Count\" 3) }}[/CARD]" +
			"[PANEL]{{ wrap Panel (dict \"Heading\" \"H\") }}<p>kids</p>{{ end }}[/PANEL]" +
			"[DIVIDER]{{ Divider }}[/DIVIDER]</main>\n",
		"pages/fm.gastro": "---\n" +
			"html, err := gastro.Render(Card, CardProps{Title: \"from frontmatter\", Count: 7})\n" +
			"if err != nil {\n\thttp.Error(w, err.Error(), http.StatusInternalServerError)\n\treturn\n}\n" +
			"Snippet := html\n" +
			"---\n" +
			"<main>{{ .Snippet }}</main>\n",
		"go.mod": "module typed_render_repro\n\ngo 1.27.2\n\n" +
			"require github.com/andrioid/gastro v0.0.0\n\n" +
			"replace github.com/andrioid/gastro => " + repoRoot + "\n",
		"render_test.go": typedRenderExternalTest,
		// Compile-fail fixture: wrong Props type for Card.
		"badrender/main.go": `package main

import gastro "typed_render_repro/.gastro"

func main() {
	r := gastro.New()
	_, _ = r.Render(gastro.Card, gastro.BadgeProps{})
}
`,
	})

	if _, err := compiler.Compile(projectDir, filepath.Join(projectDir, ".gastro"), compiler.CompileOptions{}); err != nil {
		t.Fatalf("compile: %v", err)
	}
	// In-package test: may call the unexported dict-path method directly.
	mustWriteFile(t, filepath.Join(projectDir, ".gastro", "zz_alloc_test.go"), typedRenderInternalTest)

	out, err := runGo(t, projectDir, "test", "-race", "-count=1", "-v", "-run", "Test", ".", "./.gastro")
	if err != nil {
		t.Fatalf("go test -race failed: %v\n%s", err, out)
	}
	for _, sub := range []string{
		"TestRenderMatchesDict", "TestRenderChildren", "TestRenderPropless",
		"TestRenderFromFrontmatter", "TestRenderAllocatesLessThanDict",
	} {
		if !strings.Contains(out, "--- PASS: "+sub) {
			t.Errorf("subprocess did not pass %s; output:\n%s", sub, out)
		}
	}

	// The benchmark is part of the acceptance evidence; make sure it runs.
	bench, err := runGo(t, projectDir, "test", "-run", "^$", "-bench", "BenchmarkRender", "-benchmem", "-benchtime", "200x", "./.gastro")
	if err != nil {
		t.Fatalf("benchmark failed: %v\n%s", err, bench)
	}
	t.Logf("benchmark:\n%s", bench)

	// Wrong Props type must not compile.
	out, err = runGo(t, projectDir, "build", "-o", os.DevNull, "./badrender")
	if err == nil {
		t.Fatalf("expected build failure for Render(Card, BadgeProps{}), got success:\n%s", out)
	}
	if !strings.Contains(out, "BadgeProps") {
		t.Errorf("build error should mention BadgeProps, got:\n%s", out)
	}
}

const typedRenderExternalTest = `package typed_render_repro

import (
	"html/template"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gastro "typed_render_repro/.gastro"
)

func fetch(t *testing.T, r *gastro.Router, path string) string {
	t.Helper()
	srv := httptest.NewServer(r.Handler())
	defer srv.Close()
	resp, err := http.Get(srv.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("GET %s: %d %s", path, resp.StatusCode, b)
	}
	return string(b)
}

func between(t *testing.T, s, open, close string) string {
	t.Helper()
	i := strings.Index(s, open)
	j := strings.Index(s, close)
	if i < 0 || j < 0 {
		t.Fatalf("markers %s..%s not found in %q", open, close, s)
	}
	return s[i+len(open) : j]
}

func TestRenderMatchesDict(t *testing.T) {
	r := gastro.New()
	page := fetch(t, r, "/")
	want := between(t, page, "[CARD]", "[/CARD]")

	got, err := r.Render(gastro.Card, gastro.CardProps{Title: "Hello <world>", Count: 3})
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Errorf("typed Render differs from dict render:\n typed: %q\n  dict: %q", got, want)
	}
}

func TestRenderChildren(t *testing.T) {
	r := gastro.New()
	page := fetch(t, r, "/")
	want := between(t, page, "[PANEL]", "[/PANEL]")

	got, err := r.Render(gastro.Panel, gastro.PanelProps{Heading: "H", Children: template.HTML("<p>kids</p>")})
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Errorf("typed Render differs from wrap render:\n typed: %q\n  wrap: %q", got, want)
	}
}

func TestRenderPropless(t *testing.T) {
	r := gastro.New()
	page := fetch(t, r, "/")
	want := between(t, page, "[DIVIDER]", "[/DIVIDER]")

	got, err := r.Render(gastro.Divider, gastro.DividerProps{})
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Errorf("typed Render differs from bare call:\n typed: %q\n  bare: %q", got, want)
	}
}

func TestRenderFromFrontmatter(t *testing.T) {
	page := fetch(t, gastro.New(), "/fm")
	if !strings.Contains(page, "<h2>from frontmatter</h2><span>7</span>") {
		t.Errorf("frontmatter gastro.Render output missing:\n%s", page)
	}
}
`

const typedRenderInternalTest = `package gastro

import "testing"

func dictCard(r *Router) {
	_ = r.componentCard(map[string]any{"Title": "Hello", "Count": 3})
}

func typedCard(r *Router) {
	_, _ = r.Render(Card, CardProps{Title: "Hello", Count: 3})
}

// TestRenderAllocatesLessThanDict: the typed path skips the props map and
// MapToStruct's reflection, so it must allocate strictly less than the
// template dict path for the same component and values.
func TestRenderAllocatesLessThanDict(t *testing.T) {
	r := New()
	dict := testing.AllocsPerRun(200, func() { dictCard(r) })
	typed := testing.AllocsPerRun(200, func() { typedCard(r) })
	t.Logf("allocs/op: dict=%.0f typed=%.0f", dict, typed)
	if typed >= dict {
		t.Errorf("typed Render allocs/op = %.0f, want < dict path %.0f", typed, dict)
	}
}

func BenchmarkRenderTyped(b *testing.B) {
	r := New()
	b.ReportAllocs()
	for b.Loop() {
		typedCard(r)
	}
}

func BenchmarkRenderDict(b *testing.B) {
	r := New()
	b.ReportAllocs()
	for b.Loop() {
		dictCard(r)
	}
}
`
