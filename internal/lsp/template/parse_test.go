package template_test

import (
	"strings"
	"testing"

	lsptemplate "github.com/andrioid/gastro/internal/lsp/template"
	"github.com/andrioid/gastro/internal/parser"
)

func TestParseTemplateBody_SimpleTemplate(t *testing.T) {
	body := `<h1>{{ .Title }}</h1>
<p>{{ .Description }}</p>`

	tree, err := lsptemplate.ParseTemplateBody(body, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tree == nil {
		t.Fatal("expected non-nil tree")
	}
	if tree.Root == nil {
		t.Fatal("expected non-nil Root")
	}
}

func TestParseTemplateBody_RangeWithIfBlocks(t *testing.T) {
	body := `<h1>{{ .Title }}</h1>
{{ range .Items }}
<p>{{ .Name }}</p>
{{ end }}
{{ with .Author }}
<span>{{ .Name }}</span>
{{ end }}
{{ if .ShowFooter }}
<footer>hi</footer>
{{ end }}`

	tree, err := lsptemplate.ParseTemplateBody(body, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tree == nil {
		t.Fatal("expected non-nil tree")
	}
}

func TestParseTemplateBody_WithCustomFunctions(t *testing.T) {
	body := `<p>{{ .CreatedAt | timeFormat "Jan 2, 2006" }}</p>
<p>{{ .Title | upper }}</p>`

	tree, err := lsptemplate.ParseTemplateBody(body, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tree == nil {
		t.Fatal("expected non-nil tree")
	}
}

func TestParseTemplateBody_WithComponentFunctions(t *testing.T) {
	// Components use bare PascalCase function calls
	body := `{{ Card (dict "Title" .Name) }}`
	uses := []parser.UseDeclaration{
		{Name: "Card", Path: "components/card.gastro"},
	}

	tree, err := lsptemplate.ParseTemplateBody(body, uses)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tree == nil {
		t.Fatal("expected non-nil tree")
	}
}

func TestParseTemplateBody_IncompleteTemplateReturnsError(t *testing.T) {
	// Unclosed action — common while typing
	_, err := lsptemplate.ParseTemplateBody(`{{ .Title`, nil)
	if err == nil {
		t.Fatal("expected error for unclosed action, got nil")
	}

	// Unclosed range block
	_, err = lsptemplate.ParseTemplateBody(`{{ range .Items }}<p>hi</p>`, nil)
	if err == nil {
		t.Fatal("expected error for unclosed range block, got nil")
	}
}

func TestParseTemplateBody_EmptyBody(t *testing.T) {
	tree, err := lsptemplate.ParseTemplateBody("", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tree == nil {
		t.Fatal("expected non-nil tree for empty body")
	}
}

func TestParseTemplateBody_DollarVariable(t *testing.T) {
	body := `{{ range .Items }}{{ $.Title }}{{ end }}`

	tree, err := lsptemplate.ParseTemplateBody(body, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tree == nil {
		t.Fatal("expected non-nil tree")
	}
}

func TestParseTemplateBody_ChainedFieldAccess(t *testing.T) {
	body := `{{ .Post.Title }}`

	tree, err := lsptemplate.ParseTemplateBody(body, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tree == nil {
		t.Fatal("expected non-nil tree")
	}
}

// TestParseTemplateBody_WrapForm verifies that Gastro's `wrap` block
// extension parses successfully via the internal `wrap` → `if  `
// rewrite. Before the rewrite was added, text/template/parse would
// reject the trailing `{{ end }}` as "unexpected" because `wrap` is
// registered as a function rather than a block keyword — leaving the
// whole body unparseable and forcing diagnostics through a regex
// fallback path with key/value pairing bugs (see
// TestDiagnoseComponentProps_WrapForm_PascalCaseValue).
func TestParseTemplateBody_WrapForm(t *testing.T) {
	uses := []parser.UseDeclaration{
		{Name: "Card", Path: "components/card.gastro"},
	}
	cases := []string{
		`{{ wrap Card (dict "Title" "hi") }}body{{ end }}`,
		`{{ wrap Card }}body{{ end }}`,                          // no dict
		`{{- wrap Card (dict "Title" "hi") -}}body{{- end -}}`,   // trim markers
		`{{wrap Card (dict "Title" "hi")}}body{{end}}`,           // tight whitespace
		`{{ wrap Card (dict "Title" .Title) }}{{ wrap Card (dict "Title" "nested") }}x{{ end }}{{ end }}`, // nested
	}
	for _, body := range cases {
		t.Run(body, func(t *testing.T) {
			tree, err := lsptemplate.ParseTemplateBody(body, uses)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tree == nil {
				t.Fatal("expected non-nil tree")
			}
		})
	}
}

// TestParseTemplateBody_WrapForm_PreservesLiteralWrap guards the
// rewrite's anchor on `{{` — a literal `wrap` inside a quoted string
// argument is not a wrap action and must not be rewritten. Otherwise
// the substitution would corrupt byte positions inside string
// arguments.
func TestParseTemplateBody_WrapForm_PreservesLiteralWrap(t *testing.T) {
	uses := []parser.UseDeclaration{
		{Name: "Card", Path: "components/card.gastro"},
	}
	body := `{{ Card (dict "Title" "wrap me") }}`
	tree, err := lsptemplate.ParseTemplateBody(body, uses)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tree == nil {
		t.Fatal("expected non-nil tree")
	}
	// Round-trip the parsed tree's source through Sprint to confirm
	// the literal string survived. The tree's Root.String() omits some
	// whitespace, so check for the substring instead.
	if !strings.Contains(tree.Root.String(), `"wrap me"`) {
		t.Errorf(`expected "wrap me" string literal to survive rewrite; tree source: %s`, tree.Root.String())
	}
}
