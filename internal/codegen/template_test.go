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

func TestTransformTemplate_BareFunctionCallPassthrough(t *testing.T) {
	body := `{{ Card (dict "Title" .Name "Urgent" .IsHot) }}`
	uses := []parser.UseDeclaration{
		{Name: "Card", Path: "components/card.gastro"},
	}

	result, err := codegen.TransformTemplate(body, uses)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result != body {
		t.Errorf("bare function call should pass through unchanged:\ngot:  %q\nwant: %q", result, body)
	}
}

func TestTransformTemplate_BareFunctionCallNoProps(t *testing.T) {
	body := `{{ Hero (dict) }}`
	uses := []parser.UseDeclaration{
		{Name: "Hero", Path: "components/hero.gastro"},
	}

	result, err := codegen.TransformTemplate(body, uses)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result != body {
		t.Errorf("bare function call should pass through unchanged:\ngot:  %q\nwant: %q", result, body)
	}
}

func TestTransformTemplate_WrapWithChildren(t *testing.T) {
	body := `{{ wrap Layout (dict "Title" .Title) }}
    <p>Hello</p>
{{ end }}`
	uses := []parser.UseDeclaration{
		{Name: "Layout", Path: "components/layout.gastro"},
	}

	result, err := codegen.TransformTemplate(body, uses)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertContains(t, result, `{{ Layout`)
	assertNotContains(t, result, `__gastro_Layout`)
	assertContains(t, result, `"Title" .Title`)
	assertContains(t, result, `__gastro_render_children`)
	assertContains(t, result, `{{define "layout_children_0"}}`)
	assertContains(t, result, `<p>Hello</p>`)
	assertContains(t, result, `{{end}}`)
}

func TestTransformTemplate_WrapEmptyChildren(t *testing.T) {
	body := `{{ wrap Layout (dict "Title" .Title) }}{{ end }}`
	uses := []parser.UseDeclaration{
		{Name: "Layout", Path: "components/layout.gastro"},
	}

	result, err := codegen.TransformTemplate(body, uses)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertContains(t, result, `{{ Layout`)
	assertNotContains(t, result, `__gastro_Layout`)
	assertContains(t, result, `{{define "layout_children_0"}}`)
}

func TestTransformTemplate_MixedHTMLAndComponents(t *testing.T) {
	body := `<h1>{{ .Title }}</h1>
{{ range .Items }}
    {{ Card (dict "Title" .Name) }}
{{ end }}`
	uses := []parser.UseDeclaration{
		{Name: "Card", Path: "components/card.gastro"},
	}

	result, err := codegen.TransformTemplate(body, uses)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result != body {
		t.Errorf("mixed HTML with bare calls should pass through unchanged:\ngot:  %q\nwant: %q", result, body)
	}
}

func TestTransformTemplate_NestedWraps(t *testing.T) {
	body := `{{ wrap A (dict) }}{{ wrap B (dict) }}inner{{ end }}{{ end }}`
	uses := []parser.UseDeclaration{
		{Name: "A", Path: "components/a.gastro"},
		{Name: "B", Path: "components/b.gastro"},
	}

	result, err := codegen.TransformTemplate(body, uses)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertContains(t, result, `{{ A`)
	assertNotContains(t, result, `__gastro_A`)
	assertContains(t, result, `{{ B`)
	assertNotContains(t, result, `__gastro_B`)
	assertContains(t, result, `{{define "a_children_0"}}`)
	assertContains(t, result, `{{define "b_children_1"}}`)
	assertContains(t, result, `inner`)
}

func TestTransformTemplate_WrapWithInnerRange(t *testing.T) {
	body := `{{ wrap Layout (dict "Title" .Title) }}{{ range .Items }}{{ Card (dict "Title" .Name) }}{{ end }}{{ end }}`
	uses := []parser.UseDeclaration{
		{Name: "Layout", Path: "components/layout.gastro"},
		{Name: "Card", Path: "components/card.gastro"},
	}

	result, err := codegen.TransformTemplate(body, uses)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertContains(t, result, `{{ Layout`)
	assertNotContains(t, result, `__gastro_Layout`)
	assertContains(t, result, `{{ Card (dict "Title" .Name) }}`)
	assertContains(t, result, `{{ range .Items }}`)
}

func TestTransformTemplate_WrapWithInnerIfElse(t *testing.T) {
	body := `{{ wrap Layout (dict) }}{{ if .X }}yes{{ else }}no{{ end }}{{ end }}`
	uses := []parser.UseDeclaration{
		{Name: "Layout", Path: "components/layout.gastro"},
	}

	result, err := codegen.TransformTemplate(body, uses)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertContains(t, result, `{{ Layout`)
	assertNotContains(t, result, `__gastro_Layout`)
	assertContains(t, result, `{{ if .X }}yes{{ else }}no{{ end }}`)
}

func TestTransformTemplate_DuplicateWrapSameComponent(t *testing.T) {
	body := `{{ wrap Layout (dict "Title" "A") }}First{{ end }}
{{ wrap Layout (dict "Title" "B") }}Second{{ end }}`
	uses := []parser.UseDeclaration{
		{Name: "Layout", Path: "components/layout.gastro"},
	}

	result, err := codegen.TransformTemplate(body, uses)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertContains(t, result, `{{define "layout_children_0"}}`)
	assertContains(t, result, `{{define "layout_children_1"}}`)
	assertContains(t, result, `First`)
	assertContains(t, result, `Second`)
}

func TestTransformTemplate_UnknownComponentWrap(t *testing.T) {
	body := `{{ wrap Unknown (dict) }}content{{ end }}`

	_, err := codegen.TransformTemplate(body, nil)
	if err == nil {
		t.Fatal("expected error for unknown component, got nil")
	}
	assertContains(t, err.Error(), "Unknown")
}

func TestTransformTemplate_UnclosedWrap(t *testing.T) {
	body := `{{ wrap Layout (dict) }}content`
	uses := []parser.UseDeclaration{
		{Name: "Layout", Path: "components/layout.gastro"},
	}

	_, err := codegen.TransformTemplate(body, uses)
	if err == nil {
		t.Fatal("expected error for unclosed wrap, got nil")
	}
	assertContains(t, err.Error(), "missing {{ end }}")
}

func TestTransformTemplate_CommentWithWrapInside(t *testing.T) {
	body := `{{/* Example: {{ wrap Layout }} */}}{{ Card (dict) }}`
	uses := []parser.UseDeclaration{
		{Name: "Card", Path: "components/card.gastro"},
	}

	result, err := codegen.TransformTemplate(body, uses)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertContains(t, result, `{{ Card (dict) }}`)
	assertContains(t, result, `{{/* Example: {{ wrap Layout }} */}}`)
}

func TestTransformTemplate_BareFunctionCallWithPipeline(t *testing.T) {
	body := `{{ Card (dict "Date" (.CreatedAt | timeFormat "Jan 2, 2006")) }}`
	uses := []parser.UseDeclaration{
		{Name: "Card", Path: "components/card.gastro"},
	}

	result, err := codegen.TransformTemplate(body, uses)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result != body {
		t.Errorf("bare function call with pipeline should pass through unchanged:\ngot:  %q\nwant: %q", result, body)
	}
}

func TestTransformTemplate_HTMLTagsPassThrough(t *testing.T) {
	// PascalCase HTML tags that are NOT imported should pass through
	// because with the new syntax, HTML is just HTML
	body := `<Badge Label="hello" /><MyComponent>content</MyComponent>`

	result, err := codegen.TransformTemplate(body, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result != body {
		t.Errorf("HTML should pass through unchanged:\ngot:  %q\nwant: %q", result, body)
	}
}

func TestTransformTemplate_OldPropSyntaxErrors(t *testing.T) {
	body := `<Card Title={.Name} />`
	uses := []parser.UseDeclaration{
		{Name: "Card", Path: "components/card.gastro"},
	}

	_, err := codegen.TransformTemplate(body, uses)
	if err == nil {
		t.Fatal("expected error for old prop syntax")
	}
	assertContains(t, err.Error(), "old component syntax")
}

func TestTransformTemplate_OutputParseable(t *testing.T) {
	body := `<h1>{{ .Title }}</h1>
{{ range .Items }}
    {{ Card (dict "Title" .Name) }}
{{ end }}`
	uses := []parser.UseDeclaration{
		{Name: "Card", Path: "components/card.gastro"},
	}

	result, err := codegen.TransformTemplate(body, uses)
	if err != nil {
		t.Fatalf("TransformTemplate error: %v", err)
	}

	stubFuncs := make(map[string]any)
	for name := range gastro.DefaultFuncs() {
		stubFuncs[name] = ""
	}
	for _, u := range uses {
		stubFuncs[u.Name] = ""
	}
	stubFuncs["__gastro_render_children"] = ""

	trees, err := parse.Parse("test", result, "{{", "}}", stubFuncs)
	if err != nil {
		t.Fatalf("transformed output is not parseable: %v\noutput:\n%s", err, result)
	}
	if trees["test"] == nil {
		t.Fatal("expected parse tree for 'test', got nil")
	}
}

func TestTransformTemplate_WrapOutputParseable(t *testing.T) {
	body := `{{ wrap Layout (dict "Title" .Title) }}
    <h1>Welcome</h1>
    {{ range .Posts }}
        {{ Card (dict "Title" .Name) }}
    {{ end }}
{{ end }}`
	uses := []parser.UseDeclaration{
		{Name: "Layout", Path: "components/layout.gastro"},
		{Name: "Card", Path: "components/card.gastro"},
	}

	result, err := codegen.TransformTemplate(body, uses)
	if err != nil {
		t.Fatalf("TransformTemplate error: %v", err)
	}

	stubFuncs := make(map[string]any)
	for name := range gastro.DefaultFuncs() {
		stubFuncs[name] = ""
	}
	for _, u := range uses {
		stubFuncs[u.Name] = ""
	}
	stubFuncs["__gastro_render_children"] = ""

	trees, err := parse.Parse("test", result, "{{", "}}", stubFuncs)
	if err != nil {
		t.Fatalf("output not parseable: %v\noutput:\n%s", err, result)
	}
	if trees["test"] == nil {
		t.Fatal("expected parse tree for 'test', got nil")
	}
}

// --- Raw block tests ---

func TestTransformTemplate_RawBlockBasic(t *testing.T) {
	body := `{{ raw }}{{ .X }}{{ endraw }}`
	result, err := codegen.TransformTemplate(body, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := `{{ "{{" }} .X {{ "}}" }}`
	if result != want {
		t.Errorf("got:  %q\nwant: %q", result, want)
	}
}

func TestTransformTemplate_RawBlockMultiLine(t *testing.T) {
	body := "{{ raw }}\n{{ .A }}\n{{ .B }}\n{{ endraw }}"
	result, err := codegen.TransformTemplate(body, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Raw blocks trim by default, so leading/trailing \n are removed
	want := "{{ \"{{\" }} .A {{ \"}}\" }}\n{{ \"{{\" }} .B {{ \"}}\" }}"
	if result != want {
		t.Errorf("got:  %q\nwant: %q", result, want)
	}
}

func TestTransformTemplate_RawBlockNonTemplateContent(t *testing.T) {
	body := `{{ raw }}<h1>Hello</h1>{{ endraw }}`
	result, err := codegen.TransformTemplate(body, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// HTML tags are escaped inside raw blocks
	want := `&lt;h1&gt;Hello&lt;/h1&gt;`
	if result != want {
		t.Errorf("got:  %q\nwant: %q", result, want)
	}
}

func TestTransformTemplate_RawBlockTrimsByDefault(t *testing.T) {
	body := "before {{ raw }} content {{ endraw }} after"
	result, err := codegen.TransformTemplate(body, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Raw blocks always trim whitespace around markers and content
	want := "beforecontentafter"
	if result != want {
		t.Errorf("got:  %q\nwant: %q", result, want)
	}
}

func TestTransformTemplate_RawBlockPreCode(t *testing.T) {
	// Simulates the <pre><code> pattern used in examples
	body := "<pre><code>\n{{ raw }}\n---\n<h1>{{ .Title }}</h1>\n{{ endraw }}\n</code></pre>"
	result, err := codegen.TransformTemplate(body, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "<pre><code>---\n&lt;h1&gt;{{ \"{{\" }} .Title {{ \"}}\" }}&lt;/h1&gt;</code></pre>"
	if result != want {
		t.Errorf("got:  %q\nwant: %q", result, want)
	}
}

func TestTransformTemplate_RawBlockHTMLEscaping(t *testing.T) {
	body := `{{ raw }}<div class="test">&amp; more</div>{{ endraw }}`
	result, err := codegen.TransformTemplate(body, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// <, >, and & are all escaped
	want := `&lt;div class="test"&gt;&amp;amp; more&lt;/div&gt;`
	if result != want {
		t.Errorf("got:  %q\nwant: %q", result, want)
	}
}

func TestTransformTemplate_RawBlockUnmatchedRaw(t *testing.T) {
	body := `{{ raw }}no endraw here`
	_, err := codegen.TransformTemplate(body, nil)
	if err == nil {
		t.Fatal("expected error for unclosed raw block")
	}
	assertContains(t, err.Error(), "unclosed {{ raw }}")
}

func TestTransformTemplate_RawBlockOrphanEndraw(t *testing.T) {
	body := `{{ endraw }}`
	_, err := codegen.TransformTemplate(body, nil)
	if err == nil {
		t.Fatal("expected error for orphan endraw")
	}
	assertContains(t, err.Error(), "unexpected {{ endraw }}")
}

func TestTransformTemplate_RawBlockOutsideUntouched(t *testing.T) {
	body := `{{ .Title }}{{ raw }}{{ .X }}{{ endraw }}`
	result, err := codegen.TransformTemplate(body, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := `{{ .Title }}{{ "{{" }} .X {{ "}}" }}`
	if result != want {
		t.Errorf("got:  %q\nwant: %q", result, want)
	}
}

func TestTransformTemplate_RawBlockWrapInsideIsEscaped(t *testing.T) {
	body := `{{ raw }}{{ wrap Layout (dict) }}{{ endraw }}`
	result, err := codegen.TransformTemplate(body, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The {{ wrap ... }} inside raw should be escaped, not transformed
	assertContains(t, result, `{{ "{{" }} wrap Layout (dict) {{ "}}" }}`)
}

func TestTransformTemplate_RawBlockMultipleBlocks(t *testing.T) {
	body := `A{{ raw }}{{ .X }}{{ endraw }}B{{ raw }}{{ .Y }}{{ endraw }}C`
	result, err := codegen.TransformTemplate(body, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := `A{{ "{{" }} .X {{ "}}" }}B{{ "{{" }} .Y {{ "}}" }}C`
	if result != want {
		t.Errorf("got:  %q\nwant: %q", result, want)
	}
}

func TestTransformTemplate_RawBlockEmpty(t *testing.T) {
	body := `{{ raw }}{{ endraw }}`
	result, err := codegen.TransformTemplate(body, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "" {
		t.Errorf("empty raw block should produce empty output, got: %q", result)
	}
}

func TestTransformTemplate_RawBlockAtFileStart(t *testing.T) {
	body := `{{ raw }}{{ .X }}{{ endraw }}rest`
	result, err := codegen.TransformTemplate(body, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := `{{ "{{" }} .X {{ "}}" }}rest`
	if result != want {
		t.Errorf("got:  %q\nwant: %q", result, want)
	}
}

func TestTransformTemplate_RawBlockAdjacentDelimiters(t *testing.T) {
	body := `{{ raw }}}}{{{{ endraw }}`
	result, err := codegen.TransformTemplate(body, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := `{{ "}}" }}{{ "{{" }}`
	if result != want {
		t.Errorf("got:  %q\nwant: %q", result, want)
	}
}

func TestTransformTemplate_RawInsideWrap(t *testing.T) {
	body := `{{ wrap Layout (dict "Title" "Test") }}{{ raw }}{{ .X }}{{ endraw }}{{ end }}`
	uses := []parser.UseDeclaration{
		{Name: "Layout", Path: "components/layout.gastro"},
	}
	result, err := codegen.TransformTemplate(body, uses)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Raw content should be escaped, wrap should be transformed
	assertContains(t, result, `{{ "{{" }} .X {{ "}}" }}`)
	assertContains(t, result, `{{ Layout`)
}

func TestTransformTemplate_RawBlockOutputParseable(t *testing.T) {
	body := "<h1>{{ .Title }}</h1>\n{{ raw }}\nHello {{ .Example }}\n{{ endraw }}\n<p>Footer</p>"

	result, err := codegen.TransformTemplate(body, nil)
	if err != nil {
		t.Fatalf("TransformTemplate error: %v", err)
	}

	stubFuncs := make(map[string]any)
	for name := range gastro.DefaultFuncs() {
		stubFuncs[name] = ""
	}
	stubFuncs["__gastro_render_children"] = ""

	trees, err := parse.Parse("test", result, "{{", "}}", stubFuncs)
	if err != nil {
		t.Fatalf("output not parseable: %v\noutput:\n%s", err, result)
	}
	if trees["test"] == nil {
		t.Fatal("expected parse tree for 'test', got nil")
	}
}

func TestTransformTemplate_DuplicateWrapParseable(t *testing.T) {
	body := `{{ wrap Layout (dict "Title" "A") }}<p>One</p>{{ end }}
{{ wrap Layout (dict "Title" "B") }}<p>Two</p>{{ end }}
{{ Card (dict "Title" .Name) }}`
	uses := []parser.UseDeclaration{
		{Name: "Layout", Path: "components/layout.gastro"},
		{Name: "Card", Path: "components/card.gastro"},
	}

	result, err := codegen.TransformTemplate(body, uses)
	if err != nil {
		t.Fatalf("TransformTemplate error: %v", err)
	}

	stubFuncs := make(map[string]any)
	for name := range gastro.DefaultFuncs() {
		stubFuncs[name] = ""
	}
	for _, u := range uses {
		stubFuncs[u.Name] = ""
	}
	stubFuncs["__gastro_render_children"] = ""

	trees, err := parse.Parse("test", result, "{{", "}}", stubFuncs)
	if err != nil {
		t.Fatalf("output not parseable: %v\noutput:\n%s", err, result)
	}
	if trees["test"] == nil {
		t.Fatal("expected parse tree for 'test', got nil")
	}
}
