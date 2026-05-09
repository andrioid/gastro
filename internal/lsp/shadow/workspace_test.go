package shadow_test

import (
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andrioid/gastro/internal/lsp/shadow"
)

// --- structural / lifecycle tests --------------------------------

func TestWorkspace_CreatesDirectory(t *testing.T) {
	projectDir := createTestProject(t)
	ws, err := shadow.NewWorkspace(projectDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer ws.Close()

	if _, err := os.Stat(ws.Dir()); os.IsNotExist(err) {
		t.Fatal("shadow workspace directory was not created")
	}
}

// TestWorkspace_GoModPresentAndPatched verifies that go.mod is mirrored
// into the shadow workspace and any relative `replace` directives are
// rewritten to absolute paths so the file resolves correctly from the
// temp directory. Today this is a copy-and-patch (was symlink before
// R6) — the test asserts presence and patching, not link-vs-copy.
func TestWorkspace_GoModPresentAndPatched(t *testing.T) {
	projectDir := t.TempDir()
	// Project go.mod with a relative `replace` directive (common in
	// monorepo / multi-module setups).
	external := t.TempDir()
	goMod := "module testproject\n\ngo 1.22\n\nreplace example.com/dep => ../external\n"
	if err := os.WriteFile(filepath.Join(projectDir, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatal(err)
	}
	// Also a sibling "../external" directory so the absolute path is
	// resolvable (tests assert the rewrite happened, not that go can
	// reach the dependency).
	if err := os.MkdirAll(filepath.Join(filepath.Dir(projectDir), "external"), 0o755); err != nil {
		_ = external // unused on platforms that fail Mkdir; harmless
	}

	ws, err := shadow.NewWorkspace(projectDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer ws.Close()

	got, err := os.ReadFile(filepath.Join(ws.Dir(), "go.mod"))
	if err != nil {
		t.Fatalf("go.mod not present in shadow workspace: %v", err)
	}
	gotS := string(got)
	if strings.Contains(gotS, "../external") {
		t.Errorf("expected `../external` to be rewritten to an absolute path, got:\n%s", gotS)
	}
	if !strings.Contains(gotS, "replace example.com/dep =>") {
		t.Errorf("replace directive should still be present, got:\n%s", gotS)
	}
}

func TestWorkspace_SymlinksSourceDirs(t *testing.T) {
	projectDir := createTestProject(t)
	ws, err := shadow.NewWorkspace(projectDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer ws.Close()

	// db/ should be reachable through the shadow workspace.
	if _, err := os.Stat(filepath.Join(ws.Dir(), "db")); os.IsNotExist(err) {
		t.Fatal("db/ directory not accessible in shadow workspace")
	}
}

func TestWorkspace_WritesVirtualFile(t *testing.T) {
	projectDir := createTestProject(t)
	ws, err := shadow.NewWorkspace(projectDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer ws.Close()

	gastroContent := `---
import "fmt"

Title := fmt.Sprint("hi")
---
<h1>{{ .Title }}</h1>`

	vf, err := ws.UpdateFile("pages/index.gastro", gastroContent)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := os.Stat(ws.VirtualFilePath("pages/index.gastro")); os.IsNotExist(err) {
		t.Fatal("virtual .go file was not written to disk")
	}
	if vf.SourceMap == nil {
		t.Fatal("source map should not be nil")
	}
}

func TestWorkspace_CloseRemovesDirectory(t *testing.T) {
	projectDir := createTestProject(t)
	ws, err := shadow.NewWorkspace(projectDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	dir := ws.Dir()
	ws.Close()
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Error("shadow workspace directory should be removed on Close")
	}
}

func TestWorkspace_MultipleFilesCoexist(t *testing.T) {
	projectDir := createTestProject(t)
	ws, err := shadow.NewWorkspace(projectDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer ws.Close()

	if _, err := ws.UpdateFile("pages/index.gastro", "---\nTitle := \"A\"\n---\n<h1>A</h1>"); err != nil {
		t.Fatal(err)
	}
	if _, err := ws.UpdateFile("pages/about.gastro", "---\nTitle := \"B\"\n---\n<h1>B</h1>"); err != nil {
		t.Fatal(err)
	}

	p1 := ws.VirtualFilePath("pages/index.gastro")
	p2 := ws.VirtualFilePath("pages/about.gastro")
	if p1 == p2 {
		t.Errorf("virtual files should have different paths: %s", p1)
	}
	if _, err := os.Stat(p1); os.IsNotExist(err) {
		t.Error("virtual file 1 does not exist")
	}
	if _, err := os.Stat(p2); os.IsNotExist(err) {
		t.Error("virtual file 2 does not exist")
	}
}

// --- generated source structure tests ----------------------------

// TestWorkspace_VirtualFileIsValidGo verifies that the generated source
// is parseable. Any layered assertions (imports preserved, frontmatter
// preserved, etc.) are tested separately as behaviour assertions.
func TestWorkspace_VirtualFileIsValidGo(t *testing.T) {
	projectDir := createTestProject(t)
	ws, err := shadow.NewWorkspace(projectDir)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()

	cases := map[string]string{
		"single import": "---\nimport \"fmt\"\n\nTitle := fmt.Sprint(\"hi\")\n---\n<h1>{{ .Title }}</h1>",
		"grouped imports": `---
import (
	"fmt"
	"strings"
)

Title := fmt.Sprintf("Hi %s", strings.ToUpper("there"))
---
<h1>{{ .Title }}</h1>`,
		"component with hoisted Props": `---
type Props struct {
	Title string
}

p := gastro.Props()
Heading := p.Title
---
<h2>{{ .Heading }}</h2>`,
	}

	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			gastroFile := "pages/test_" + strings.ReplaceAll(name, " ", "_") + ".gastro"
			vf, err := ws.UpdateFile(gastroFile, content)
			if err != nil {
				t.Fatalf("UpdateFile: %v", err)
			}
			if _, err := parser.ParseFile(token.NewFileSet(), vf.Filename, vf.GoSource, 0); err != nil {
				t.Fatalf("virtual file is not valid Go: %v\n--- source ---\n%s", err, vf.GoSource)
			}
		})
	}
}

// --- source map tests --------------------------------------------

// TestWorkspace_SourceMapMapsFrontmatterContent verifies that gastro
// line numbers within the frontmatter region map to virtual lines that
// contain the corresponding (possibly codegen-rewritten) content.
//
// The mapping is line-stable: codegen rewrites preserve line breaks
// but may shift columns (e.g. `gastro.Context()` →
// `gastroRuntime.NewContext(w, r)` changes column positions but not
// the line). The shadow accepts this trade-off in exchange for full
// type fidelity.
func TestWorkspace_SourceMapMapsFrontmatterContent(t *testing.T) {
	projectDir := createTestProject(t)
	ws, err := shadow.NewWorkspace(projectDir)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()

	gastroContent := "---\nimport \"fmt\"\n\nctx := gastro.Context()\nTitle := fmt.Sprintf(\"Hello %s\", \"World\")\n---\n<h1>{{ .Title }}</h1>"
	//                  ^ line 1 (---)
	//                       ^ line 2 (import "fmt")
	//                                  ^ line 3 (blank — leading blank in frontmatter, stripped by codegen)
	//                                            ^ line 4 (ctx := gastro.Context())
	//                                                                   ^ line 5 (Title := fmt.Sprintf...)

	vf, err := ws.UpdateFile("pages/index.gastro", gastroContent)
	if err != nil {
		t.Fatal(err)
	}
	virtualLines := strings.Split(vf.GoSource, "\n")

	// Gastro line 4 is `ctx := gastro.Context()` — codegen rewrites it
	// to `ctx := gastroRuntime.NewContext(w, r)`.
	vLine4 := vf.SourceMap.GastroToVirtual(4)
	if vLine4 < 1 || vLine4 > len(virtualLines) {
		t.Fatalf("gastro line 4 mapped to out-of-bounds virtual line %d", vLine4)
	}
	if !strings.Contains(virtualLines[vLine4-1], "gastroRuntime.NewContext") {
		t.Errorf("gastro line 4 should map to gastroRuntime.NewContext line, got: %q", virtualLines[vLine4-1])
	}

	// Gastro line 5 is `Title := fmt.Sprintf(...)`.
	vLine5 := vf.SourceMap.GastroToVirtual(5)
	if !strings.Contains(virtualLines[vLine5-1], "Title := fmt.Sprintf") {
		t.Errorf("gastro line 5 should map to Title assignment, got: %q", virtualLines[vLine5-1])
	}
}

// TestWorkspace_SourceMapAccuracy_Component covers the component path
// where codegen rewrites `gastro.Props()` to `__props`. The mapping
// must still land on the correct gastro line.
func TestWorkspace_SourceMapAccuracy_Component(t *testing.T) {
	projectDir := createTestProject(t)
	ws, err := shadow.NewWorkspace(projectDir)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()

	gastroContent := `---
type Props struct {
    Title string
}

Title := gastro.Props().Title
---
<h1>{{ .Title }}</h1>`
	//   1 (---)
	//   2 (type Props struct {)
	//   3 (    Title string)
	//   4 (})
	//   5 (blank)
	//   6 (Title := gastro.Props().Title)

	vf, err := ws.UpdateFile("components/card.gastro", gastroContent)
	if err != nil {
		t.Fatal(err)
	}
	virtualLines := strings.Split(vf.GoSource, "\n")

	// Codegen rewrites `gastro.Props()` to `__props`.
	vLine6 := vf.SourceMap.GastroToVirtual(6)
	if vLine6 < 1 || vLine6 > len(virtualLines) {
		t.Fatalf("gastro line 6 mapped to out-of-bounds virtual line %d", vLine6)
	}
	got := virtualLines[vLine6-1]
	if !strings.Contains(got, "Title := __props.Title") {
		t.Errorf("gastro line 6 should map to `Title := __props.Title`, got: %q", got)
	}
}

func TestWorkspace_FrontmatterEndLine(t *testing.T) {
	projectDir := createTestProject(t)
	ws, err := shadow.NewWorkspace(projectDir)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()

	tests := []struct {
		name     string
		content  string
		wantLine int // 1-indexed line of the closing ---
	}{
		{"simple", "---\nTitle := \"Hi\"\n---\n<h1>hi</h1>", 3},
		{"with imports", "---\nimport \"fmt\"\n\nTitle := fmt.Sprintf(\"Hi\")\n---\n<h1>hi</h1>", 5},
		{"with blank lines", "---\nTitle := \"Hi\"\n\n\n---\n<h1>hi</h1>", 5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vf, err := ws.UpdateFile("pages/test.gastro", tt.content)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if vf.FrontmatterEndLine != tt.wantLine {
				t.Errorf("FrontmatterEndLine = %d, want %d", vf.FrontmatterEndLine, tt.wantLine)
			}
		})
	}
}

func TestWorkspace_NoFrontmatter(t *testing.T) {
	projectDir := createTestProject(t)
	ws, err := shadow.NewWorkspace(projectDir)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()

	vf, err := ws.UpdateFile("components/divider.gastro", "<hr class=\"divider\" />")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(vf.GoSource, "package main") {
		t.Errorf("no-frontmatter file should produce a minimal package main shell, got:\n%s", vf.GoSource)
	}
	if vf.FrontmatterEndLine != 0 {
		t.Errorf("FrontmatterEndLine = %d, want 0", vf.FrontmatterEndLine)
	}
	if vf.SourceMap == nil {
		t.Fatal("source map should not be nil")
	}
}

// --- behavioural / type-checking tests ---------------------------

// TestWorkspace_PropsCallTypeChecks verifies that codegen's Props
// rewrite (gastro.Props() → __props) preserves type information for
// gopls. We assert the rewrite happened (so that __props.Field is
// available for hover/completion) and that no `gastro.Props()` call
// leaks through unrewritten.
func TestWorkspace_PropsCallTypeChecks(t *testing.T) {
	projectDir := createTestProject(t)
	ws, err := shadow.NewWorkspace(projectDir)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()

	gastroContent := `---
type Props struct {
    Title string
}

Title := gastro.Props().Title
---
<h1>{{ .Title }}</h1>`

	vf, err := ws.UpdateFile("components/card.gastro", gastroContent)
	if err != nil {
		t.Fatal(err)
	}

	// Codegen rewrite must produce __props (not preserve gastro.Props()).
	if !strings.Contains(vf.GoSource, "__props.Title") {
		t.Errorf("expected __props.Title in shadow source, got:\n%s", vf.GoSource)
	}
	if strings.Contains(vf.GoSource, "gastro.Props()") {
		t.Error("gastro.Props() should be rewritten by codegen, not preserved")
	}

	// __props is declared by codegen as a typed local var pointing at
	// the user's hoisted Props struct. Under MangleHoisted=false (the
	// shadow's mode) the type keeps its user-written name `Props`,
	// since each shadow file lives in its own subpackage and does not
	// need cross-component collision protection.
	if !strings.Contains(vf.GoSource, "type Props struct") {
		t.Errorf("expected unmangled `type Props struct` in shadow source, got:\n%s", vf.GoSource)
	}
	if strings.Contains(vf.GoSource, "__component_") {
		t.Errorf("shadow source should not contain __component_ prefix under MangleHoisted=false, got:\n%s", vf.GoSource)
	}
}

// TestWorkspace_TypeChecksAgainstRealRuntime is the keystone test:
// after the workspace generates a shadow file, `go build` runs against
// the temp directory and reports zero errors. This is the parity
// guarantee R6 buys — anything that breaks here would also break the
// editor's diagnostics.
func TestWorkspace_TypeChecksAgainstRealRuntime(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping: invokes go build")
	}
	projectDir := createGastroLinkedProject(t)
	ws, err := shadow.NewWorkspace(projectDir)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()

	cases := []struct {
		name    string
		path    string
		content string
	}{
		{"page with frontmatter", "pages/test_page.gastro",
			"---\nimport \"fmt\"\n\nTitle := fmt.Sprint(\"hi\")\n---\n<h1>{{ .Title }}</h1>"},
		{"component with Props", "components/test_card.gastro",
			"---\ntype Props struct {\n\tTitle string\n}\n\nHeading := gastro.Props().Title\n---\n<h2>{{ .Heading }}</h2>"},
		{"component with template.HTML field", "components/test_layout.gastro",
			"---\ntype Props struct {\n\tTitle  string\n\tDetail template.HTML\n}\n\np := gastro.Props()\nTitle := p.Title\n---\n<h2>{{ .Title }}</h2>"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gastroFile := tc.path
			content := tc.content
			vf, err := ws.UpdateFile(gastroFile, content)
			if err != nil {
				t.Fatalf("UpdateFile: %v", err)
			}
			pkgDir := filepath.Dir(ws.VirtualFilePath(gastroFile))
			rel, err := filepath.Rel(ws.Dir(), pkgDir)
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("go", "build", "-o", os.DevNull, "./"+rel)
			cmd.Dir = ws.Dir()
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("shadow does not type-check:\n%s\n--- shadow source ---\n%s", out, vf.GoSource)
			}
		})
	}
}

// TestWorkspace_RenderAPIResolvesForKnownComponents verifies the B2
// behaviour: when a project contains components/, calling
// gastro.Render.X(XProps{...}) from a page shadow type-checks against
// a synthetic *renderAPI method generated from the component's Props
// schema.
func TestWorkspace_RenderAPIResolvesForKnownComponents(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping: invokes go build")
	}
	projectDir := createGastroLinkedProject(t)
	// Add a component with a Props struct so the workspace scan picks
	// it up and emits Render.Card(CardProps{...}).
	if err := os.MkdirAll(filepath.Join(projectDir, "components"), 0o755); err != nil {
		t.Fatal(err)
	}
	cardSrc := `---
type Props struct {
	Title string
}

p := gastro.Props()
Heading := p.Title
---
<article>{{ .Heading }}</article>`
	if err := os.WriteFile(filepath.Join(projectDir, "components", "card.gastro"), []byte(cardSrc), 0o644); err != nil {
		t.Fatal(err)
	}

	ws, err := shadow.NewWorkspace(projectDir)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()

	pageSrc := `---
html, err := gastro.Render.Card(CardProps{Title: "hi"})
if err != nil {
	return
}
Body := html
---
<div>{{ .Body }}</div>`

	if _, err := ws.UpdateFile("pages/index.gastro", pageSrc); err != nil {
		t.Fatal(err)
	}

	pkgDir := filepath.Dir(ws.VirtualFilePath("pages/index.gastro"))
	rel, _ := filepath.Rel(ws.Dir(), pkgDir)
	cmd := exec.Command("go", "build", "-o", os.DevNull, "./"+rel)
	cmd.Dir = ws.Dir()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Render.Card call should type-check, got error:\n%s", out)
	}
}

// TestWorkspace_RenderAPIRejectsUnknownComponents verifies that the
// stub does NOT silently accept arbitrary method names — it surfaces
// "no method X" so users notice typos.
func TestWorkspace_RenderAPIRejectsUnknownComponents(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping: invokes go build")
	}
	projectDir := createGastroLinkedProject(t)
	ws, err := shadow.NewWorkspace(projectDir)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()

	pageSrc := `---
_, _ = gastro.Render.NoSuchComponent("anything")
---
<p>x</p>`

	if _, err := ws.UpdateFile("pages/x.gastro", pageSrc); err != nil {
		t.Fatal(err)
	}

	pkgDir := filepath.Dir(ws.VirtualFilePath("pages/x.gastro"))
	rel, _ := filepath.Rel(ws.Dir(), pkgDir)
	cmd := exec.Command("go", "build", "-o", os.DevNull, "./"+rel)
	cmd.Dir = ws.Dir()
	out, _ := cmd.CombinedOutput()
	if !strings.Contains(string(out), "NoSuchComponent") {
		t.Errorf("expected build error mentioning NoSuchComponent, got:\n%s", out)
	}
}

// TestWorkspace_NestedProjectFindsModuleRoot verifies the git-pm-shaped
// case: a gastro project at <module>/internal/web/ where go.mod lives
// at <module>/, not at the gastro project root. The shadow workspace
// must walk up to find go.mod or the codegen output won't type-check
// (it imports gastroRuntime, which only resolves under the module
// graph).
func TestWorkspace_NestedProjectFindsModuleRoot(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping: invokes go build")
	}
	moduleRoot := createGastroLinkedProject(t)

	// Place the gastro project two levels deep, mimicking
	// git-pm/internal/web/ structure.
	gastroProject := filepath.Join(moduleRoot, "internal", "web")
	if err := os.MkdirAll(filepath.Join(gastroProject, "pages"), 0o755); err != nil {
		t.Fatal(err)
	}

	ws, err := shadow.NewWorkspace(gastroProject)
	if err != nil {
		t.Fatalf("NewWorkspace: %v", err)
	}
	defer ws.Close()

	// Even though projectDir = <module>/internal/web (no go.mod
	// there), the shadow workspace should have go.mod at its root.
	if _, err := os.Stat(filepath.Join(ws.Dir(), "go.mod")); err != nil {
		t.Fatalf("shadow workspace should have go.mod at root: %v", err)
	}

	// Round-trip: a simple page should type-check end to end.
	if _, err := ws.UpdateFile("pages/index.gastro", "---\nimport \"fmt\"\n\nTitle := fmt.Sprint(\"hi\")\n---\n<h1>{{ .Title }}</h1>"); err != nil {
		t.Fatal(err)
	}
	pkgDir := filepath.Dir(ws.VirtualFilePath("pages/index.gastro"))
	rel, _ := filepath.Rel(ws.Dir(), pkgDir)
	cmd := exec.Command("go", "build", "-o", os.DevNull, "./"+rel)
	cmd.Dir = ws.Dir()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("nested-project shadow should type-check, got:\n%s", out)
	}
}

// --- helpers -----------------------------------------------------

// createTestProject creates a minimal Go project for tests that don't
// need to invoke `go build`.
func createTestProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module testproject\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "db"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "db", "db.go"), []byte("package db\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "pages"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// createGastroLinkedProject creates a Go project whose go.mod
// (borrowed from examples/gastro) replaces github.com/andrioid/gastro
// with the local checkout, so `go build` can type-check shadow output
// that imports the real gastro runtime — including all transitive
// runtime dependencies (chroma, goldmark, etc.). Copying the example's
// go.mod / go.sum is the cheapest way to get a fully resolved module
// graph without running `go mod tidy` (which is slow and requires
// network in some configurations).
func createGastroLinkedProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	gastroRoot := findGastroRoot(t)
	exampleDir := filepath.Join(gastroRoot, "examples", "gastro")

	goMod, err := os.ReadFile(filepath.Join(exampleDir, "go.mod"))
	if err != nil {
		t.Fatalf("reading examples/gastro/go.mod: %v", err)
	}
	// The example uses `replace github.com/andrioid/gastro => ../..`
	// which resolves from examples/gastro/. Rewrite it to an absolute
	// path so it resolves from our temp dir.
	patched := strings.Replace(string(goMod), "=> ../..", "=> "+gastroRoot, 1)
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(patched), 0o644); err != nil {
		t.Fatal(err)
	}

	if goSum, err := os.ReadFile(filepath.Join(exampleDir, "go.sum")); err == nil {
		_ = os.WriteFile(filepath.Join(dir, "go.sum"), goSum, 0o644)
	}
	return dir
}

// findGastroRoot walks up from the test working directory until it
// finds the gastro repo's go.mod (matched by module path).
func findGastroRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		modPath := filepath.Join(dir, "go.mod")
		if data, err := os.ReadFile(modPath); err == nil {
			if strings.Contains(string(data), "module github.com/andrioid/gastro") {
				return dir
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not locate gastro repo root")
		}
		dir = parent
	}
}
