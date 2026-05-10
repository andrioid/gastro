package template_test

import (
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
