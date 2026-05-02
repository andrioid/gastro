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

	output, err := codegen.GenerateHandler(file, info, info.IsComponent)
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
props := gastro.Props()
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

	output, err := codegen.GenerateHandler(file, info, info.IsComponent)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have render method on *Router (so it can read the per-router
	// template registry and dependency map), not a free function or an HTTP handler.
	assertContains(t, output, `func (__router *Router) componentCard(propsMap map[string]any) template.HTML`)
	assertNotContains(t, output, `http.ResponseWriter`)

	// Should extract Children from props map (renamed from "__children" in A5)
	assertContains(t, output, `propsMap["Children"]`)
	assertContains(t, output, `"Children": __children`)

	// Should unpack props via MapToStruct with unique type name
	assertContains(t, output, `MapToStruct[componentCardProps](propsMap)`)

	// Should alias the props variable
	assertContains(t, output, `props := __props`) // gastro.Props() is rewritten to __props by codegen

	// Should contain the data map with exported vars
	assertContains(t, output, `"CSSClass": CSSClass`)

	// Should render into a buffer and return template.HTML
	assertContains(t, output, `bytes.Buffer`)
	assertContains(t, output, `return template.HTML`)

	// Should hoist type declarations with unique name to avoid collisions
	assertContains(t, output, "type componentCardProps struct")

	// A5: the exported XProps type now lives in render.go (see compiler_test
	// TestCompile_GeneratesRenderFile), not in the per-component file.
	assertNotContains(t, output, "type CardProps =")
	assertNotContains(t, output, "type CardProps struct")

	// Should NOT contain gastroRuntime.NewContext (components don't have context)
	assertNotContains(t, output, `gastroRuntime.NewContext(w, r)`)
}

func TestGenerate_ComponentWithUses(t *testing.T) {
	file := &parser.File{
		Filename: "components/card.gastro",
		Frontmatter: `type Props struct {
    Title string
    Tag   string
}
props := gastro.Props()
Title := props.Title
Tag := props.Tag`,
		TemplateBody: `<div class="card"><h2>{{ .Title }}</h2>{{ __gastro_Badge (dict "Label" .Tag) }}</div>`,
		Uses: []parser.UseDeclaration{
			{Name: "Badge", Path: "components/badge.gastro"},
		},
	}

	info := &codegen.FrontmatterInfo{
		ExportedVars:  []codegen.VarInfo{{Name: "Title"}, {Name: "Tag"}},
		PrivateVars:   []codegen.VarInfo{{Name: "props"}},
		IsComponent:   true,
		PropsTypeName: "Props",
	}

	output, err := codegen.GenerateHandler(file, info, info.IsComponent)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Component wiring (FuncMap, render_children) is now centralised in
	// the generated routes.go, not in each component file.
	assertNotContains(t, output, `func init()`)
	assertNotContains(t, output, `template.New("componentCard")`)

	// Should use the per-router registry-based template lookup
	assertContains(t, output, `__router.__gastro_getTemplate("componentCard")`)

	// Should still have component render method (not HTTP handler)
	assertContains(t, output, `func (__router *Router) componentCard(propsMap map[string]any) template.HTML`)
	assertNotContains(t, output, `http.ResponseWriter`)

	// Should still handle props and children (A5 renamed the dict key from
	// "__children" to "Children"; the component-internal local variable name
	// __children is unchanged).
	assertContains(t, output, `MapToStruct[componentCardProps](propsMap)`)
	assertContains(t, output, `propsMap["Children"]`)
	assertContains(t, output, `"Children": __children`)
}

func TestGenerate_NewPropsDirectAccess(t *testing.T) {
	file := &parser.File{
		Filename: "components/card.gastro",
		Frontmatter: `type Props struct {
    Title string
}
Title := gastro.Props().Title
CSSClass := "card"`,
		TemplateBody: `<div class="{{ .CSSClass }}">{{ .Title }}</div>`,
	}

	info := &codegen.FrontmatterInfo{
		ExportedVars:  []codegen.VarInfo{{Name: "Title"}, {Name: "CSSClass"}},
		IsComponent:   true,
		PropsTypeName: "Props",
	}

	output, err := codegen.GenerateHandler(file, info, info.IsComponent)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// gastro.Props() should be rewritten to __props
	assertContains(t, output, `Title := __props.Title`)
	assertNotContains(t, output, `gastro.Props`)

	// Should still have component method
	assertContains(t, output, `func (__router *Router) componentCard(propsMap map[string]any) template.HTML`)
	assertContains(t, output, `MapToStruct[componentCardProps](propsMap)`)
}

func TestGenerate_NewPropsWholeStruct(t *testing.T) {
	file := &parser.File{
		Filename: "components/kpi.gastro",
		Frontmatter: `type Props struct {
    X     int
    Value string
}
p := gastro.Props()
Value := p.Value
CX := fmt.Sprintf("%d", p.X+135)`,
		TemplateBody: `<text x="{{ .CX }}">{{ .Value }}</text>`,
		Imports:      []string{"fmt"},
	}

	info := &codegen.FrontmatterInfo{
		ExportedVars:  []codegen.VarInfo{{Name: "Value"}, {Name: "CX"}},
		PrivateVars:   []codegen.VarInfo{{Name: "p"}},
		IsComponent:   true,
		PropsTypeName: "Props",
	}

	output, err := codegen.GenerateHandler(file, info, info.IsComponent)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// gastro.Props() should be rewritten to __props
	assertContains(t, output, `p := __props`)
	assertContains(t, output, `Value := p.Value`)
	assertContains(t, output, `CX := fmt.Sprintf("%d", p.X+135)`)
	assertNotContains(t, output, `gastro.Props`)
}

func TestGenerate_NewPropsInExpression(t *testing.T) {
	file := &parser.File{
		Filename: "components/badge.gastro",
		Frontmatter: `type Props struct {
    Label string
    Count int
}
Label := gastro.Props().Label
Summary := fmt.Sprintf("%s (%d)", gastro.Props().Label, gastro.Props().Count)`,
		TemplateBody: `<span>{{ .Summary }}</span>`,
		Imports:      []string{"fmt"},
	}

	info := &codegen.FrontmatterInfo{
		ExportedVars:  []codegen.VarInfo{{Name: "Label"}, {Name: "Summary"}},
		IsComponent:   true,
		PropsTypeName: "Props",
	}

	output, err := codegen.GenerateHandler(file, info, info.IsComponent)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// All gastro.Props() calls should be rewritten
	assertContains(t, output, `Label := __props.Label`)
	assertContains(t, output, `Summary := fmt.Sprintf("%s (%d)", __props.Label, __props.Count)`)
	assertNotContains(t, output, `gastro.Props`)
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

	output, err := codegen.GenerateHandler(file, info, info.IsComponent)
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

	output, err := codegen.GenerateHandler(file, info, info.IsComponent)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should still work with an empty or nil data map
	assertContains(t, output, "Execute")
}

func TestGenerate_PageHandlerLogsExecuteError(t *testing.T) {
	file := &parser.File{
		Filename:     "pages/index.gastro",
		Frontmatter:  `Title := "Hello"`,
		TemplateBody: `<h1>{{ .Title }}</h1>`,
	}

	info := &codegen.FrontmatterInfo{
		ExportedVars: []codegen.VarInfo{{Name: "Title"}},
		IsPage:       true,
	}

	output, err := codegen.GenerateHandler(file, info, info.IsComponent)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Page handlers should log template execution errors
	assertContains(t, output, `log.Printf`)
	assertContains(t, output, `template execution failed`)
}

func TestGenerate_ComponentLogsExecuteError(t *testing.T) {
	file := &parser.File{
		Filename: "components/card.gastro",
		Frontmatter: `type Props struct {
    Title string
}
props := gastro.Props()
CSSClass := "card"`,
		TemplateBody: `<div class="{{ .CSSClass }}">{{ .Children }}</div>`,
	}

	info := &codegen.FrontmatterInfo{
		ExportedVars:  []codegen.VarInfo{{Name: "CSSClass"}},
		PrivateVars:   []codegen.VarInfo{{Name: "props"}},
		IsComponent:   true,
		PropsTypeName: "Props",
	}

	output, err := codegen.GenerateHandler(file, info, info.IsComponent)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Component render functions should log template execution errors
	assertContains(t, output, `template execution failed`)
}

func TestExportedComponentName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"componentPostCard", "PostCard"},
		{"componentLayout", "Layout"},
		{"componentBadge", "Badge"},
		{"componentDashboardBody", "DashboardBody"},
	}

	for _, tt := range tests {
		got := codegen.ExportedComponentName(tt.input)
		if got != tt.want {
			t.Errorf("ExportedComponentName(%q): got %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestParseStructFields(t *testing.T) {
	hoisted := `type Props struct {
    Title  string
    Count  int
    Active bool
}
`
	fields := codegen.ParseStructFields(hoisted)

	if len(fields) != 3 {
		t.Fatalf("expected 3 fields, got %d", len(fields))
	}

	want := []codegen.StructField{
		{Name: "Title", Type: "string"},
		{Name: "Count", Type: "int"},
		{Name: "Active", Type: "bool"},
	}

	for i, f := range fields {
		if f.Name != want[i].Name || f.Type != want[i].Type {
			t.Errorf("field %d: got {%s %s}, want {%s %s}", i, f.Name, f.Type, want[i].Name, want[i].Type)
		}
	}
}

func TestParseStructFields_Empty(t *testing.T) {
	fields := codegen.ParseStructFields("")
	if len(fields) != 0 {
		t.Errorf("expected 0 fields, got %d", len(fields))
	}
}

func TestParseStructFields_InlineComments(t *testing.T) {
	hoisted := "type Props struct {\n\tID    string // ulid\n\tTitle string // task title with `code`\n\tCount int    // contains { brace\n}"
	fields := codegen.ParseStructFields(hoisted)

	want := []codegen.StructField{
		{Name: "ID", Type: "string"},
		{Name: "Title", Type: "string"},
		{Name: "Count", Type: "int"},
	}
	if len(fields) != len(want) {
		t.Fatalf("expected %d fields, got %d: %v", len(want), len(fields), fields)
	}
	for i, f := range fields {
		if f.Name != want[i].Name || f.Type != want[i].Type {
			t.Errorf("field %d: got {%s %s}, want {%s %s}", i, f.Name, f.Type, want[i].Name, want[i].Type)
		}
	}
}

func TestParseStructFields_CompositeTypes(t *testing.T) {
	hoisted := "type Props struct {\n\tTags    []string\n\tLookup  map[string]int\n\tHandler func(string) error\n}"
	fields := codegen.ParseStructFields(hoisted)

	want := []codegen.StructField{
		{Name: "Tags", Type: "[]string"},
		{Name: "Lookup", Type: "map[string]int"},
		{Name: "Handler", Type: "func(string) error"},
	}
	if len(fields) != len(want) {
		t.Fatalf("expected %d fields, got %d: %v", len(want), len(fields), fields)
	}
	for i, f := range fields {
		if f.Name != want[i].Name || f.Type != want[i].Type {
			t.Errorf("field %d: got {%s %s}, want {%s %s}", i, f.Name, f.Type, want[i].Name, want[i].Type)
		}
	}
}

func TestParseStructFields_QualifiedTypes(t *testing.T) {
	hoisted := "type Props struct {\n\tHTML template.HTML\n\tWhen time.Time\n}"
	fields := codegen.ParseStructFields(hoisted)

	want := []codegen.StructField{
		{Name: "HTML", Type: "template.HTML"},
		{Name: "When", Type: "time.Time"},
	}
	if len(fields) != len(want) {
		t.Fatalf("expected %d fields, got %d: %v", len(want), len(fields), fields)
	}
	for i, f := range fields {
		if f.Name != want[i].Name || f.Type != want[i].Type {
			t.Errorf("field %d: got {%s %s}, want {%s %s}", i, f.Name, f.Type, want[i].Name, want[i].Type)
		}
	}
}

func TestParseStructFields_MultipleNamesPerField(t *testing.T) {
	hoisted := "type Props struct {\n\tA, B string\n\tC    int\n}"
	fields := codegen.ParseStructFields(hoisted)

	want := []codegen.StructField{
		{Name: "A", Type: "string"},
		{Name: "B", Type: "string"},
		{Name: "C", Type: "int"},
	}
	if len(fields) != len(want) {
		t.Fatalf("expected %d fields, got %d: %v", len(want), len(fields), fields)
	}
	for i, f := range fields {
		if f.Name != want[i].Name || f.Type != want[i].Type {
			t.Errorf("field %d: got {%s %s}, want {%s %s}", i, f.Name, f.Type, want[i].Name, want[i].Type)
		}
	}
}

func TestParseStructFields_StructTags(t *testing.T) {
	hoisted := "type Props struct {\n\tTitle string `json:\"title,omitempty\"`\n\tCount int    `json:\"count\"`\n}"
	fields := codegen.ParseStructFields(hoisted)

	want := []codegen.StructField{
		{Name: "Title", Type: "string"},
		{Name: "Count", Type: "int"},
	}
	if len(fields) != len(want) {
		t.Fatalf("expected %d fields, got %d: %v", len(want), len(fields), fields)
	}
	for i, f := range fields {
		if f.Name != want[i].Name || f.Type != want[i].Type {
			t.Errorf("field %d: got {%s %s}, want {%s %s}", i, f.Name, f.Type, want[i].Name, want[i].Type)
		}
	}
}

func TestParseStructFields_SkipsEmbedded(t *testing.T) {
	hoisted := "type Props struct {\n\tBaseProps\n\tTitle string\n}"
	fields := codegen.ParseStructFields(hoisted)

	want := []codegen.StructField{
		{Name: "Title", Type: "string"},
	}
	if len(fields) != len(want) {
		t.Fatalf("expected %d fields, got %d: %v", len(want), len(fields), fields)
	}
	if fields[0].Name != "Title" || fields[0].Type != "string" {
		t.Errorf("got %v, want %v", fields[0], want[0])
	}
}

func TestParseStructFields_FirstStructWins(t *testing.T) {
	// When multiple types are hoisted (auxiliary types + Props), the
	// first struct's fields are returned. Preserves existing behaviour.
	hoisted := "type Item struct {\n\tID string\n}\n\ntype Props struct {\n\tItems []Item\n}"
	fields := codegen.ParseStructFields(hoisted)

	if len(fields) != 1 {
		t.Fatalf("expected 1 field from first struct (Item), got %d: %v", len(fields), fields)
	}
	if fields[0].Name != "ID" || fields[0].Type != "string" {
		t.Errorf("first struct fields: got %v, want {ID string}", fields[0])
	}
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
