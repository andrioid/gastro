package template_test

import (
	"testing"

	"github.com/andrioid/gastro/internal/codegen"
	lsptemplate "github.com/andrioid/gastro/internal/lsp/template"
	"github.com/andrioid/gastro/internal/parser"
)

func TestVariableCompletions(t *testing.T) {
	info := &codegen.FrontmatterInfo{
		ExportedVars: []codegen.VarInfo{
			{Name: "Title"},
			{Name: "Items"},
			{Name: "Year"},
		},
	}

	completions := lsptemplate.VariableCompletions(info)

	if len(completions) != 3 {
		t.Fatalf("expected 3 completions, got %d", len(completions))
	}

	names := make(map[string]bool)
	for _, c := range completions {
		names[c.Label] = true
	}

	for _, want := range []string{".Title", ".Items", ".Year"} {
		if !names[want] {
			t.Errorf("expected completion %q, not found", want)
		}
	}
}

func TestComponentCompletions(t *testing.T) {
	uses := []parser.UseDeclaration{
		{Name: "Card", Path: "components/card.gastro"},
		{Name: "Layout", Path: "components/layout.gastro"},
	}

	completions := lsptemplate.ComponentCompletions(uses)

	if len(completions) != 2 {
		t.Fatalf("expected 2 completions, got %d", len(completions))
	}

	if completions[0].Label != "Card" {
		t.Errorf("completion[0]: got %q, want %q", completions[0].Label, "Card")
	}
	if completions[1].Label != "Layout" {
		t.Errorf("completion[1]: got %q, want %q", completions[1].Label, "Layout")
	}
}

func TestFuncMapCompletions(t *testing.T) {
	completions := lsptemplate.FuncMapCompletions()

	// Should include built-in functions
	names := make(map[string]bool)
	for _, c := range completions {
		names[c.Label] = true
	}

	for _, want := range []string{"upper", "lower", "join", "dict", "safeHTML", "timeFormat"} {
		if !names[want] {
			t.Errorf("expected built-in function %q in completions", want)
		}
	}
}

func TestDiagnostics_UnknownVariable(t *testing.T) {
	info := &codegen.FrontmatterInfo{
		ExportedVars: []codegen.VarInfo{
			{Name: "Title"},
		},
	}
	templateBody := `<h1>{{ .Title }}</h1>
<p>{{ .Unknown }}</p>`

	diags := lsptemplate.Diagnose(templateBody, info, nil)

	found := false
	for _, d := range diags {
		if d.Message == `unknown template variable ".Unknown"` {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected diagnostic for unknown variable .Unknown")
	}
}

func TestDiagnostics_UnknownComponent(t *testing.T) {
	info := &codegen.FrontmatterInfo{}
	uses := []parser.UseDeclaration{
		{Name: "Card", Path: "components/card.gastro"},
	}
	templateBody := `<Card Title={.Name} />
<Unknown />`

	diags := lsptemplate.Diagnose(templateBody, info, uses)

	found := false
	for _, d := range diags {
		if d.Message == `unknown component "Unknown": not imported via 'use'` {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected diagnostic for unknown component, got: %v", diags)
	}
}
