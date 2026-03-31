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

	// InsertText includes the dot so textEdit can replace the full ".VarName" range
	for _, c := range completions {
		if c.InsertText != c.Label {
			t.Errorf("InsertText %q should equal Label %q", c.InsertText, c.Label)
		}
		if c.FilterText != c.Label {
			t.Errorf("FilterText %q should equal Label %q", c.FilterText, c.Label)
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

	diags := lsptemplate.Diagnose(templateBody, info, nil, nil, nil)

	found := false
	for _, d := range diags {
		if d.Message == `unknown template variable ".Unknown"` {
			found = true
			// ".Unknown" starts at line 1, char 6 (after "<p>{{ ")
			if d.StartLine != 1 {
				t.Errorf("expected StartLine=1, got %d", d.StartLine)
			}
			if d.StartChar != 6 {
				t.Errorf("expected StartChar=6, got %d", d.StartChar)
			}
			break
		}
	}
	if !found {
		t.Error("expected diagnostic for unknown variable .Unknown")
	}
}

func TestDiagnostics_DoubleDotSyntax(t *testing.T) {
	info := &codegen.FrontmatterInfo{
		ExportedVars: []codegen.VarInfo{
			{Name: "Title"},
		},
	}
	templateBody := `<title>{{ ..Title }}</title>`

	diags := lsptemplate.Diagnose(templateBody, info, nil, nil, nil)

	found := false
	for _, d := range diags {
		if d.Message == `invalid syntax "..Title": use ".Title" instead` {
			found = true
			// "..Title" starts at char 10 (after "<title>{{ ")
			if d.StartLine != 0 {
				t.Errorf("expected StartLine=0, got %d", d.StartLine)
			}
			if d.StartChar != 10 {
				t.Errorf("expected StartChar=10, got %d", d.StartChar)
			}
			break
		}
	}
	if !found {
		t.Errorf("expected diagnostic for double-dot syntax, got: %v", diags)
	}
}

func TestDiagnostics_MultiLinePositions(t *testing.T) {
	info := &codegen.FrontmatterInfo{
		ExportedVars: []codegen.VarInfo{
			{Name: "Title"},
		},
	}
	templateBody := `<h1>{{ .Title }}</h1>
<p>line 2</p>
<p>{{ .Missing }}</p>`

	diags := lsptemplate.Diagnose(templateBody, info, nil, nil, nil)

	found := false
	for _, d := range diags {
		if d.Message == `unknown template variable ".Missing"` {
			found = true
			if d.StartLine != 2 {
				t.Errorf("expected StartLine=2, got %d", d.StartLine)
			}
			break
		}
	}
	if !found {
		t.Error("expected diagnostic for .Missing on line 2")
	}
}

func TestDiagnose_RangeBlockVariables(t *testing.T) {
	info := &codegen.FrontmatterInfo{
		ExportedVars: []codegen.VarInfo{
			{Name: "Title"},
			{Name: "Posts"},
		},
	}
	templateBody := `<h1>{{ .Title }}</h1>
{{ range .Posts }}
<p>{{ .Slug }}</p>
<p>{{ .Author }}</p>
{{ end }}`

	diags := lsptemplate.Diagnose(templateBody, info, nil, nil, nil)

	// .Slug and .Author are fields on range elements — must NOT be flagged
	for _, d := range diags {
		if d.Message == `unknown template variable ".Slug"` {
			t.Error(".Slug inside range block should not be flagged as unknown")
		}
		if d.Message == `unknown template variable ".Author"` {
			t.Error(".Author inside range block should not be flagged as unknown")
		}
	}
}

func TestDiagnose_WithBlockVariables(t *testing.T) {
	info := &codegen.FrontmatterInfo{
		ExportedVars: []codegen.VarInfo{
			{Name: "Author"},
		},
	}
	templateBody := `{{ with .Author }}
<p>{{ .Name }}</p>
{{ end }}`

	diags := lsptemplate.Diagnose(templateBody, info, nil, nil, nil)

	for _, d := range diags {
		if d.Message == `unknown template variable ".Name"` {
			t.Error(".Name inside with block should not be flagged as unknown")
		}
	}
}

func TestDiagnose_IfBlockVariables(t *testing.T) {
	info := &codegen.FrontmatterInfo{
		ExportedVars: []codegen.VarInfo{
			{Name: "ShowTitle"},
		},
	}
	// if does NOT rebind dot — variables inside should still be checked
	templateBody := `{{ if .ShowTitle }}
<h1>{{ .Missing }}</h1>
{{ end }}`

	diags := lsptemplate.Diagnose(templateBody, info, nil, nil, nil)

	found := false
	for _, d := range diags {
		if d.Message == `unknown template variable ".Missing"` {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected diagnostic for .Missing inside if block (if does not rebind dot)")
	}
}

func TestDiagnose_DollarVarInRange(t *testing.T) {
	info := &codegen.FrontmatterInfo{
		ExportedVars: []codegen.VarInfo{
			{Name: "Posts"},
		},
	}
	// $.Title uses root accessor — should be checked against exports
	templateBody := `{{ range .Posts }}
<p>{{ $.Title }}</p>
{{ end }}`

	diags := lsptemplate.Diagnose(templateBody, info, nil, nil, nil)

	found := false
	for _, d := range diags {
		if d.Message == `unknown template variable ".Title"` {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected diagnostic for $.Title (not in exports) inside range block")
	}
}

func TestDiagnose_EmptyBody(t *testing.T) {
	info := &codegen.FrontmatterInfo{
		ExportedVars: []codegen.VarInfo{
			{Name: "Title"},
		},
	}

	diags := lsptemplate.Diagnose("", info, nil, nil, nil)
	if len(diags) != 0 {
		t.Errorf("expected 0 diagnostics for empty body, got %d", len(diags))
	}
}

func TestDiagnose_NoExportsWithVars(t *testing.T) {
	info := &codegen.FrontmatterInfo{}
	templateBody := `<p>{{ .Title }}</p>`

	diags := lsptemplate.Diagnose(templateBody, info, nil, nil, nil)

	found := false
	for _, d := range diags {
		if d.Message == `unknown template variable ".Title"` {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected diagnostic for .Title when no exports exist")
	}
}

func TestOffsetToLineChar(t *testing.T) {
	// offsetToLineChar is not exported, so we test it indirectly through Diagnose
	// by checking position accuracy across various patterns
	info := &codegen.FrontmatterInfo{}

	// Single line: ".Unknown" at offset 4
	diags := lsptemplate.Diagnose(`{{ .Unknown }}`, info, nil, nil, nil)
	if len(diags) > 0 {
		if diags[0].StartLine != 0 {
			t.Errorf("single line: expected StartLine=0, got %d", diags[0].StartLine)
		}
	}

	// First char of second line
	diags = lsptemplate.Diagnose("x\n{{ .Unknown }}", info, nil, nil, nil)
	if len(diags) > 0 {
		if diags[0].StartLine != 1 {
			t.Errorf("second line: expected StartLine=1, got %d", diags[0].StartLine)
		}
		if diags[0].StartChar != 3 {
			t.Errorf("second line: expected StartChar=3, got %d", diags[0].StartChar)
		}
	}
}

func TestFuncSignatures(t *testing.T) {
	sigs := lsptemplate.FuncSignatures()

	// Should include all default functions
	for _, name := range []string{"upper", "lower", "trim", "dict", "timeFormat"} {
		if _, ok := sigs[name]; !ok {
			t.Errorf("expected signature for %q, not found", name)
		}
	}

	// Spot-check a known signature
	if sig := sigs["upper"]; sig != "func(string) string" {
		t.Errorf("upper signature: got %q, want %q", sig, "func(string) string")
	}
}

func TestDiagnostics_UnknownComponent(t *testing.T) {
	info := &codegen.FrontmatterInfo{}
	uses := []parser.UseDeclaration{
		{Name: "Card", Path: "components/card.gastro"},
	}
	templateBody := `<Card Title={.Name} />
<Unknown />`

	diags := lsptemplate.Diagnose(templateBody, info, uses, nil, nil)

	found := false
	for _, d := range diags {
		if d.Message == `unknown component "Unknown": not imported via 'use'` {
			found = true
			if d.StartLine != 1 {
				t.Errorf("expected StartLine=1, got %d", d.StartLine)
			}
			break
		}
	}
	if !found {
		t.Errorf("expected diagnostic for unknown component, got: %v", diags)
	}
}
