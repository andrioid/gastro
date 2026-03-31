package codegen_test

import (
	"testing"
	"text/template/parse"

	"github.com/andrioid/gastro/internal/codegen"
	"github.com/andrioid/gastro/internal/parser"
	"github.com/andrioid/gastro/pkg/gastro"
)

func TestTransformTemplate_PassthroughPlainHTML(t *testing.T) {
	body := `<h1>Hello</h1>
<p>World</p>`

	result, err := codegen.TransformTemplate(body, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result != body {
		t.Errorf("plain HTML should pass through unchanged:\ngot:  %q\nwant: %q", result, body)
	}
}

func TestTransformTemplate_PassthroughGoTemplateExpressions(t *testing.T) {
	body := `<h1>{{ .Title }}</h1>
{{ range .Items }}
<p>{{ . }}</p>
{{ end }}`

	result, err := codegen.TransformTemplate(body, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result != body {
		t.Errorf("go template expressions should pass through unchanged:\ngot:  %q\nwant: %q", result, body)
	}
}

func TestTransformTemplate_SelfClosingComponent(t *testing.T) {
	body := `<Card Title={.Name} Urgent={.IsHot} />`
	uses := []parser.UseDeclaration{
		{Name: "Card", Path: "components/card.gastro"},
	}

	result, err := codegen.TransformTemplate(body, uses)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := `{{ __gastro_Card (dict "Title" .Name "Urgent" .IsHot) }}`
	if result != want {
		t.Errorf("self-closing component:\ngot:  %q\nwant: %q", result, want)
	}
}

func TestTransformTemplate_ComponentWithStringLiteral(t *testing.T) {
	body := `<Card Title="hello" />`
	uses := []parser.UseDeclaration{
		{Name: "Card", Path: "components/card.gastro"},
	}

	result, err := codegen.TransformTemplate(body, uses)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := `{{ __gastro_Card (dict "Title" "hello") }}`
	if result != want {
		t.Errorf("string literal prop:\ngot:  %q\nwant: %q", result, want)
	}
}

func TestTransformTemplate_ComponentWithChildren(t *testing.T) {
	body := `<Layout Title={.Title}>
    <p>Hello</p>
</Layout>`
	uses := []parser.UseDeclaration{
		{Name: "Layout", Path: "components/layout.gastro"},
	}

	result, err := codegen.TransformTemplate(body, uses)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Children should be captured and passed as __children
	assertContains(t, result, `__gastro_Layout`)
	assertContains(t, result, `"Title" .Title`)
	assertContains(t, result, `__gastro_render_children`)

	// Child content should be extracted into a {{define}} block
	assertContains(t, result, `{{define "layout_children"}}`)
	assertContains(t, result, `<p>Hello</p>`)
	assertContains(t, result, `{{end}}`)
}

func TestTransformTemplate_SlotBecomesChildren(t *testing.T) {
	body := `<div>
    <slot />
</div>`

	result, err := codegen.TransformTemplate(body, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertContains(t, result, `{{ .Children }}`)
	assertNotContains(t, result, `<slot />`)
}

func TestTransformTemplate_MixedHTMLAndComponents(t *testing.T) {
	body := `<h1>{{ .Title }}</h1>
{{ range .Items }}
    <Card Title={.Name} />
{{ end }}`
	uses := []parser.UseDeclaration{
		{Name: "Card", Path: "components/card.gastro"},
	}

	result, err := codegen.TransformTemplate(body, uses)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// HTML and go template expressions should be unchanged
	assertContains(t, result, `<h1>{{ .Title }}</h1>`)
	assertContains(t, result, `{{ range .Items }}`)
	assertContains(t, result, `{{ end }}`)

	// Component should be transformed
	assertContains(t, result, `__gastro_Card`)
	assertNotContains(t, result, `<Card`)
}

func TestTransformTemplate_UnknownComponentReturnsError(t *testing.T) {
	body := `<Unknown Title={.Name} />`

	_, err := codegen.TransformTemplate(body, nil)
	if err == nil {
		t.Fatal("expected an error for unknown component, got nil")
	}
}

func TestTransformTemplate_ComponentNoProps(t *testing.T) {
	body := `<Header />`
	uses := []parser.UseDeclaration{
		{Name: "Header", Path: "components/header.gastro"},
	}

	result, err := codegen.TransformTemplate(body, uses)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := `{{ __gastro_Header (dict) }}`
	if result != want {
		t.Errorf("component with no props:\ngot:  %q\nwant: %q", result, want)
	}
}

func TestTransformTemplate_PipeExpressionInProps(t *testing.T) {
	body := `<Card Title={.Name} Date={.CreatedAt | timeFormat "Jan 2, 2006"} />`
	uses := []parser.UseDeclaration{
		{Name: "Card", Path: "components/card.gastro"},
	}

	result, err := codegen.TransformTemplate(body, uses)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Pipe expressions must be wrapped in parens to avoid precedence issues
	want := `{{ __gastro_Card (dict "Title" .Name "Date" (.CreatedAt | timeFormat "Jan 2, 2006")) }}`
	if result != want {
		t.Errorf("pipe expression in props:\ngot:  %q\nwant: %q", result, want)
	}
}

// TestTransformTemplate_OutputParseable verifies that transformed template
// output can be parsed by Go's text/template/parse package. The AST-based
// diagnostics depend on this.
func TestTransformTemplate_OutputParseable(t *testing.T) {
	body := `<h1>{{ .Title }}</h1>
{{ range .Items }}
    <Card Title={.Name} />
{{ end }}`
	uses := []parser.UseDeclaration{
		{Name: "Card", Path: "components/card.gastro"},
	}

	result, err := codegen.TransformTemplate(body, uses)
	if err != nil {
		t.Fatalf("TransformTemplate error: %v", err)
	}

	// Build a stub FuncMap with all default functions + component functions
	stubFuncs := make(map[string]any)
	for name := range gastro.DefaultFuncs() {
		stubFuncs[name] = ""
	}
	for _, u := range uses {
		stubFuncs["__gastro_"+u.Name] = ""
	}
	stubFuncs["__gastro_render_children"] = ""

	trees, err := parse.Parse("test", result, "{{", "}}", stubFuncs)
	if err != nil {
		t.Fatalf("transformed output is not parseable by text/template/parse: %v\noutput:\n%s", err, result)
	}
	if trees["test"] == nil {
		t.Fatal("expected parse tree for 'test', got nil")
	}
}
