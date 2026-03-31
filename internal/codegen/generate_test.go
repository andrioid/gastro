package codegen_test

import (
	"strings"
	"testing"

	"github.com/andrioid/gastro/internal/codegen"
	"github.com/andrioid/gastro/internal/parser"
)

func TestGenerate_PageHandler(t *testing.T) {
	file := &parser.File{
		Filename: "pages/index.gastro",
		Frontmatter: `ctx := gastro.Context()
Title := "Hello"`,
		TemplateBody: `<h1>{{ .Title }}</h1>`,
		Imports:      []string{"fmt"},
	}

	info := &codegen.FrontmatterInfo{
		ExportedVars: []codegen.VarInfo{{Name: "Title"}},
		PrivateVars:  []codegen.VarInfo{{Name: "ctx"}},
		IsPage:       true,
	}

	output, err := codegen.GenerateHandler(file, info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should contain a package declaration
	assertContains(t, output, "package gastro")

	// Should import the user's imports
	assertContains(t, output, `"fmt"`)

	// Should contain the frontmatter code (gastro runtime is aliased)
	assertContains(t, output, `gastroRuntime.NewContext(w, r)`)

	// Should build a data map with only exported (uppercase) variables
	assertContains(t, output, `"Title": Title`)

	// Should NOT contain private variables in the data map
	assertNotContains(t, output, `"ctx": ctx`)

	// Should call template execution
	assertContains(t, output, `Execute`)
}

func TestGenerate_ComponentRenderFunc(t *testing.T) {
	file := &parser.File{
		Filename: "components/card.gastro",
		Frontmatter: `type Props struct {
    Title string
}
props := gastro.Props[Props]()
CSSClass := "card"`,
		TemplateBody: `<div class="{{ .CSSClass }}">{{ .Children }}</div>`,
		Uses:         []parser.UseDeclaration{},
	}

	info := &codegen.FrontmatterInfo{
		ExportedVars:  []codegen.VarInfo{{Name: "CSSClass"}},
		PrivateVars:   []codegen.VarInfo{{Name: "props"}},
		IsComponent:   true,
		PropsTypeName: "Props",
	}

	output, err := codegen.GenerateHandler(file, info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have render function signature, not HTTP handler
	assertContains(t, output, `func componentCard(propsMap map[string]any) template.HTML`)
	assertNotContains(t, output, `http.ResponseWriter`)

	// Should extract __children from props map
	assertContains(t, output, `propsMap["__children"]`)
	assertContains(t, output, `"Children": __children`)

	// Should unpack props via MapToStruct with unique type name
	assertContains(t, output, `MapToStruct[componentCardProps](propsMap)`)

	// Should alias the props variable
	assertContains(t, output, `props := __props`)

	// Should contain the data map with exported vars
	assertContains(t, output, `"CSSClass": CSSClass`)

	// Should render into a buffer and return template.HTML
	assertContains(t, output, `bytes.Buffer`)
	assertContains(t, output, `return template.HTML`)

	// Should hoist type declarations with unique name to avoid collisions
	assertContains(t, output, "type componentCardProps struct")

	// Should NOT contain gastroRuntime.NewContext (components don't have context)
	assertNotContains(t, output, `gastroRuntime.NewContext(w, r)`)
}

func TestGenerate_PageWithComponents(t *testing.T) {
	file := &parser.File{
		Filename:    "pages/index.gastro",
		Frontmatter: `ctx := gastro.Context()` + "\n" + `Title := "Hello"`,
		TemplateBody: `{{ __gastro_Layout (dict "Title" .Title "__children" (__gastro_render_children "layout_children" .)) }}
{{define "layout_children"}}<h1>{{ .Title }}</h1>{{end}}`,
		Imports: []string{},
		Uses: []parser.UseDeclaration{
			{Name: "Layout", Path: "components/layout.gastro"},
		},
	}

	info := &codegen.FrontmatterInfo{
		ExportedVars: []codegen.VarInfo{{Name: "Title"}},
		PrivateVars:  []codegen.VarInfo{{Name: "ctx"}},
		IsPage:       true,
	}

	output, err := codegen.GenerateHandler(file, info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should use init-based template parsing
	assertContains(t, output, `func init()`)
	assertContains(t, output, `template.New("pageIndex")`)

	// Should register component functions
	assertContains(t, output, `__fm["__gastro_Layout"] = componentLayout`)

	// Should register __gastro_render_children as a closure
	assertContains(t, output, `__fm["__gastro_render_children"]`)
	assertContains(t, output, `ExecuteTemplate`)

	// Should still have HTTP handler signature
	assertContains(t, output, `func pageIndex(w http.ResponseWriter, r *http.Request)`)

	// Should import bytes
	assertContains(t, output, `"bytes"`)
}

func TestGenerate_MultipleExportedVars(t *testing.T) {
	file := &parser.File{
		Filename:     "pages/index.gastro",
		Frontmatter:  `Title := "Hello"` + "\n" + `Year := 2026`,
		TemplateBody: `<h1>{{ .Title }} {{ .Year }}</h1>`,
	}

	info := &codegen.FrontmatterInfo{
		ExportedVars: []codegen.VarInfo{
			{Name: "Title"},
			{Name: "Year"},
		},
		IsPage: true,
	}

	output, err := codegen.GenerateHandler(file, info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertContains(t, output, `"Title": Title`)
	assertContains(t, output, `"Year": Year`)
}

func TestGenerate_NoExportedVars(t *testing.T) {
	file := &parser.File{
		Filename:     "pages/index.gastro",
		Frontmatter:  `ctx := gastro.Context()`,
		TemplateBody: `<h1>Hello</h1>`,
	}

	info := &codegen.FrontmatterInfo{
		PrivateVars: []codegen.VarInfo{{Name: "ctx"}},
		IsPage:      true,
	}

	output, err := codegen.GenerateHandler(file, info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should still work with an empty or nil data map
	assertContains(t, output, "Execute")
}

func assertContains(t *testing.T, haystack, needle string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Errorf("expected output to contain %q, but it didn't.\noutput:\n%s", needle, haystack)
	}
}

func assertNotContains(t *testing.T, haystack, needle string) {
	t.Helper()
	if strings.Contains(haystack, needle) {
		t.Errorf("expected output NOT to contain %q, but it did.\noutput:\n%s", needle, haystack)
	}
}
