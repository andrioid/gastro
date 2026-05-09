package shadow_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andrioid/gastro/internal/lsp/shadow"
)

// TestShadow_HoistedVar_Unmangled verifies that a frontmatter
// `var Foo = expr` lands in the virtual .go file at package scope
// with its user-written name (no __page_ prefix), since the shadow
// runs codegen with MangleHoisted=false.
func TestShadow_HoistedVar_Unmangled(t *testing.T) {
	projectDir := createTestProject(t)
	gastroSrc := `---
import "regexp"

var SlugRE = regexp.MustCompile(` + "`^[a-z]+$`" + `)
Title := "Hello"
---
<h1>{{ .Title }}</h1>
`
	pagePath := filepath.Join(projectDir, "pages", "index.gastro")
	if err := os.WriteFile(pagePath, []byte(gastroSrc), 0o644); err != nil {
		t.Fatal(err)
	}

	ws, err := shadow.NewWorkspace(projectDir)
	if err != nil {
		t.Fatalf("NewWorkspace: %v", err)
	}
	defer ws.Close()

	vf, err := ws.UpdateFile("pages/index.gastro", gastroSrc)
	if err != nil {
		t.Fatalf("UpdateFile: %v", err)
	}

	if !strings.Contains(vf.GoSource, "var SlugRE = regexp.MustCompile") {
		t.Errorf("expected unmangled var SlugRE in shadow source, got:\n%s", vf.GoSource)
	}
	if strings.Contains(vf.GoSource, "__page_") {
		t.Errorf("shadow should not contain __page_ prefix, got:\n%s", vf.GoSource)
	}
	if !strings.Contains(vf.GoSource, `"SlugRE": SlugRE`) {
		t.Errorf("expected unmangled __data binding, got:\n%s", vf.GoSource)
	}
}

// TestShadow_HoistedFunc_Unmangled verifies that a hoisted func is
// emitted at package scope with its user-written name.
func TestShadow_HoistedFunc_Unmangled(t *testing.T) {
	projectDir := createTestProject(t)
	gastroSrc := `---
import "strings"

func slug(s string) string {
	return strings.ToLower(s)
}

Slug := slug("Hello")
---
<p>{{ .Slug }}</p>
`
	pagePath := filepath.Join(projectDir, "pages", "index.gastro")
	if err := os.WriteFile(pagePath, []byte(gastroSrc), 0o644); err != nil {
		t.Fatal(err)
	}

	ws, err := shadow.NewWorkspace(projectDir)
	if err != nil {
		t.Fatalf("NewWorkspace: %v", err)
	}
	defer ws.Close()

	vf, err := ws.UpdateFile("pages/index.gastro", gastroSrc)
	if err != nil {
		t.Fatalf("UpdateFile: %v", err)
	}

	if !strings.Contains(vf.GoSource, "func slug(s string) string") {
		t.Errorf("expected unmangled func slug in shadow source, got:\n%s", vf.GoSource)
	}
	if !strings.Contains(vf.GoSource, `slug("Hello")`) {
		t.Errorf("expected body to keep slug(...) call, got:\n%s", vf.GoSource)
	}
	if strings.Contains(vf.GoSource, "__page_") {
		t.Errorf("shadow should not contain __page_ prefix, got:\n%s", vf.GoSource)
	}
}

// TestShadow_HoistedType_Unmangled verifies that hoisted types stay
// unmangled in shadow output. Pages can declare arbitrary helper
// types — this exercises the page-level type hoisting path (not
// component Props).
func TestShadow_HoistedType_Unmangled(t *testing.T) {
	projectDir := createTestProject(t)
	gastroSrc := `---
type Comment struct {
	Author string
	Text   string
}

Comments := []Comment{}
---
<p>{{ .Comments }}</p>
`
	pagePath := filepath.Join(projectDir, "pages", "index.gastro")
	if err := os.WriteFile(pagePath, []byte(gastroSrc), 0o644); err != nil {
		t.Fatal(err)
	}

	ws, err := shadow.NewWorkspace(projectDir)
	if err != nil {
		t.Fatalf("NewWorkspace: %v", err)
	}
	defer ws.Close()

	vf, err := ws.UpdateFile("pages/index.gastro", gastroSrc)
	if err != nil {
		t.Fatalf("UpdateFile: %v", err)
	}

	if !strings.Contains(vf.GoSource, "type Comment struct") {
		t.Errorf("expected unmangled type Comment in shadow source, got:\n%s", vf.GoSource)
	}
	// Body's reference to []Comment is the user-written name, not mangled.
	if !strings.Contains(vf.GoSource, "[]Comment{}") {
		t.Errorf("expected body to reference []Comment (unmangled), got:\n%s", vf.GoSource)
	}
}

// TestShadow_TwoPagesWithSameVarName verifies that two pages declaring
// `var Title = ...` don't collide in the shadow workspace, because
// each shadow file lives in its own subpackage. This is the structural
// invariant that makes MangleHoisted=false safe in the shadow.
func TestShadow_TwoPagesWithSameVarName(t *testing.T) {
	projectDir := createTestProject(t)
	pageA := `---
var Title = "A"
---
<h1>{{ .Title }}</h1>
`
	pageB := `---
var Title = "B"
---
<h1>{{ .Title }}</h1>
`
	if err := os.MkdirAll(filepath.Join(projectDir, "pages"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "pages", "a.gastro"), []byte(pageA), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "pages", "b.gastro"), []byte(pageB), 0o644); err != nil {
		t.Fatal(err)
	}

	ws, err := shadow.NewWorkspace(projectDir)
	if err != nil {
		t.Fatalf("NewWorkspace: %v", err)
	}
	defer ws.Close()

	vfA, err := ws.UpdateFile("pages/a.gastro", pageA)
	if err != nil {
		t.Fatalf("UpdateFile a: %v", err)
	}
	vfB, err := ws.UpdateFile("pages/b.gastro", pageB)
	if err != nil {
		t.Fatalf("UpdateFile b: %v", err)
	}

	// Both shadow files declare `var Title = ...` at package scope —
	// allowed because each lives in its own Go subpackage.
	if !strings.Contains(vfA.GoSource, `var Title = "A"`) {
		t.Errorf("page A shadow missing unmangled var Title:\n%s", vfA.GoSource)
	}
	if !strings.Contains(vfB.GoSource, `var Title = "B"`) {
		t.Errorf("page B shadow missing unmangled var Title:\n%s", vfB.GoSource)
	}

	// And confirmed: their virtual file paths put them in different
	// subpackages, so the duplicate `Title` decls do not collide
	// when gopls type-checks each shadow.
	pathA := ws.VirtualFilePath("pages/a.gastro")
	pathB := ws.VirtualFilePath("pages/b.gastro")
	if filepath.Dir(pathA) == filepath.Dir(pathB) {
		t.Errorf("shadow files share a subpackage; collision possible: %s vs %s", pathA, pathB)
	}
}

// TestShadow_HoistedVarReferencingR_Errors verifies that a per-request
// reference inside a hoisted decl produces a HoistError that surfaces
// through UpdateFile. This is the LSP equivalent of the Phase 3
// migration-hint check — users should see the canonical error in the
// editor diagnostics rather than a confusing downstream parse error.
func TestShadow_HoistedVarReferencingR_Errors(t *testing.T) {
	projectDir := createTestProject(t)
	gastroSrc := `---
var Path = r.URL.Path
---
<p>{{ .Path }}</p>
`
	if err := os.WriteFile(filepath.Join(projectDir, "pages", "bad.gastro"), []byte(gastroSrc), 0o644); err != nil {
		t.Fatal(err)
	}

	ws, err := shadow.NewWorkspace(projectDir)
	if err != nil {
		t.Fatalf("NewWorkspace: %v", err)
	}
	defer ws.Close()

	_, err = ws.UpdateFile("pages/bad.gastro", gastroSrc)
	if err == nil {
		t.Fatal("expected error on per-request capture in hoisted decl")
	}
	if !strings.Contains(err.Error(), "r.URL") {
		t.Errorf("expected error to mention r.URL, got: %v", err)
	}
}

// TestShadow_ExportedVarsHaveSuppressionLines confirms that the shadow
// virtual file emits `_ = X` lines for every exported frontmatter var,
// in source order. queryVariableTypes scans these lines to resolve
// types via gopls hover; without them, template-body hover and
// completion silently fall back to "no type info".
func TestShadow_ExportedVarsHaveSuppressionLines(t *testing.T) {
	projectDir := createTestProject(t)
	gastroSrc := `---
import "fmt"

Title := "Hello"
Posts := []string{"a", "b"}
fmt.Println("side effect")
---
<h1>{{ .Title }}</h1>
{{ range .Posts }}<p>{{ . }}</p>{{ end }}
`
	pagePath := filepath.Join(projectDir, "pages", "index.gastro")
	if err := os.WriteFile(pagePath, []byte(gastroSrc), 0o644); err != nil {
		t.Fatal(err)
	}

	ws, err := shadow.NewWorkspace(projectDir)
	if err != nil {
		t.Fatalf("NewWorkspace: %v", err)
	}
	defer ws.Close()

	vf, err := ws.UpdateFile("pages/index.gastro", gastroSrc)
	if err != nil {
		t.Fatalf("UpdateFile: %v", err)
	}

	for _, name := range []string{"Title", "Posts"} {
		needle := "_ = " + name
		if !strings.Contains(vf.GoSource, needle) {
			t.Errorf("shadow source missing %q (queryVariableTypes anchor):\n%s", needle, vf.GoSource)
		}
	}

	// Order: Title before Posts (source order).
	titleIdx := strings.Index(vf.GoSource, "_ = Title")
	postsIdx := strings.Index(vf.GoSource, "_ = Posts")
	if titleIdx < 0 || postsIdx < 0 || titleIdx >= postsIdx {
		t.Errorf("expected `_ = Title` before `_ = Posts` (source order); titleIdx=%d postsIdx=%d", titleIdx, postsIdx)
	}
}

// TestShadow_ComponentPropsType_Unmangled verifies that a component's
// `type Props struct{}` stays unmangled in shadow output.
func TestShadow_ComponentPropsType_Unmangled(t *testing.T) {
	projectDir := createTestProject(t)
	if err := os.MkdirAll(filepath.Join(projectDir, "components"), 0o755); err != nil {
		t.Fatal(err)
	}
	gastroSrc := `---
type Props struct {
	Title string
}

Title := gastro.Props().Title
---
<h1>{{ .Title }}</h1>
`
	if err := os.WriteFile(filepath.Join(projectDir, "components", "card.gastro"), []byte(gastroSrc), 0o644); err != nil {
		t.Fatal(err)
	}

	ws, err := shadow.NewWorkspace(projectDir)
	if err != nil {
		t.Fatalf("NewWorkspace: %v", err)
	}
	defer ws.Close()

	vf, err := ws.UpdateFile("components/card.gastro", gastroSrc)
	if err != nil {
		t.Fatalf("UpdateFile: %v", err)
	}

	if !strings.Contains(vf.GoSource, "type Props struct") {
		t.Errorf("expected unmangled type Props, got:\n%s", vf.GoSource)
	}
	if !strings.Contains(vf.GoSource, "MapToStruct[Props]") {
		t.Errorf("expected MapToStruct[Props] (no mangling), got:\n%s", vf.GoSource)
	}
	if strings.Contains(vf.GoSource, "__component_") {
		t.Errorf("shadow should not contain __component_ prefix, got:\n%s", vf.GoSource)
	}
}
