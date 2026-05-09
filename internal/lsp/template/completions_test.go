package template_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"text/template/parse"

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
	completions := lsptemplate.FuncMapCompletions(false)

	// Should include built-in functions, Go template builtins, and
	// compile-time directives.
	names := make(map[string]bool)
	for _, c := range completions {
		names[c.Label] = true
	}

	for _, want := range []string{
		// gastro runtime funcs
		"upper", "lower", "join", "dict", "safeHTML", "timeFormat",
		// Go template builtins
		"and", "or", "eq", "printf", "len",
		// compile-time directives
		"wrap", "markdown", "raw", "endraw",
	} {
		if !names[want] {
			t.Errorf("expected %q in completions", want)
		}
	}
}

func TestFuncMapCompletions_DirectiveDetails(t *testing.T) {
	// Without snippet support: directives insert plain text.
	plain := lsptemplate.FuncMapCompletions(false)
	byName := map[string]lsptemplate.CompletionItem{}
	for _, c := range plain {
		byName[c.Label] = c
	}

	for _, name := range []string{"wrap", "markdown", "raw", "endraw"} {
		c, ok := byName[name]
		if !ok {
			t.Fatalf("expected %q in completions", name)
		}
		if c.IsSnippet {
			t.Errorf("%q: IsSnippet should be false when snippetSupport=false", name)
		}
		if !strings.Contains(c.Detail, "compile-time directive") {
			t.Errorf("%q: expected detail to mention 'compile-time directive', got %q", name, c.Detail)
		}
	}

	// With snippet support: directives use snippet syntax for wrap/markdown.
	snip := lsptemplate.FuncMapCompletions(true)
	for _, c := range snip {
		if c.Label == "wrap" || c.Label == "markdown" {
			if !c.IsSnippet {
				t.Errorf("%q: IsSnippet should be true when snippetSupport=true", c.Label)
			}
			if !strings.Contains(c.InsertText, "$") {
				t.Errorf("%q: expected snippet placeholder in insertText, got %q", c.Label, c.InsertText)
			}
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

	diags := lsptemplate.Diagnose(templateBody, info, nil, nil, nil, nil)

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

	diags := lsptemplate.Diagnose(templateBody, info, nil, nil, nil, nil)

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

	diags := lsptemplate.Diagnose(templateBody, info, nil, nil, nil, nil)

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

	diags := lsptemplate.Diagnose(templateBody, info, nil, nil, nil, nil)

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

	diags := lsptemplate.Diagnose(templateBody, info, nil, nil, nil, nil)

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

	diags := lsptemplate.Diagnose(templateBody, info, nil, nil, nil, nil)

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

	diags := lsptemplate.Diagnose(templateBody, info, nil, nil, nil, nil)

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

	diags := lsptemplate.Diagnose("", info, nil, nil, nil, nil)
	if len(diags) != 0 {
		t.Errorf("expected 0 diagnostics for empty body, got %d", len(diags))
	}
}

func TestDiagnose_NoExportsWithVars(t *testing.T) {
	info := &codegen.FrontmatterInfo{}
	templateBody := `<p>{{ .Title }}</p>`

	diags := lsptemplate.Diagnose(templateBody, info, nil, nil, nil, nil)

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
	diags := lsptemplate.Diagnose(`{{ .Unknown }}`, info, nil, nil, nil, nil)
	if len(diags) > 0 {
		if diags[0].StartLine != 0 {
			t.Errorf("single line: expected StartLine=0, got %d", diags[0].StartLine)
		}
	}

	// First char of second line
	diags = lsptemplate.Diagnose("x\n{{ .Unknown }}", info, nil, nil, nil, nil)
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
	templateBody := `{{ Card (dict "Title" .Name) }}
{{ Unknown (dict) }}`

	diags := lsptemplate.Diagnose(templateBody, info, uses, nil, nil, nil)

	found := false
	for _, d := range diags {
		if d.Message == `unknown component "Unknown": not imported` {
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

func TestResolveComponentProps(t *testing.T) {
	dir := t.TempDir()

	// Write a component with a Props struct
	content := `---
type Props struct {
	Title string
	Count int
}

Title := gastro.Props().Title
---
<div>{{ .Title }}</div>`

	if err := os.MkdirAll(filepath.Join(dir, "components"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "components", "card.gastro"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	fields, err := lsptemplate.ResolveComponentProps(dir, "components/card.gastro", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fields) != 2 {
		t.Fatalf("expected 2 fields, got %d", len(fields))
	}
	if fields[0].Name != "Title" || fields[0].Type != "string" {
		t.Errorf("field 0: got %+v, want Title/string", fields[0])
	}
	if fields[1].Name != "Count" || fields[1].Type != "int" {
		t.Errorf("field 1: got %+v, want Count/int", fields[1])
	}
}

func TestResolveComponentProps_NoProps(t *testing.T) {
	dir := t.TempDir()

	content := `---
Title := "Hello"
---
<div>{{ .Title }}</div>`

	if err := os.WriteFile(filepath.Join(dir, "simple.gastro"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	fields, err := lsptemplate.ResolveComponentProps(dir, "simple.gastro", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fields != nil {
		t.Errorf("expected nil fields for component without Props, got %v", fields)
	}
}

func TestResolveComponentProps_OpenDocument(t *testing.T) {
	dir := t.TempDir()

	// Write a stale version to disk
	staleContent := `---
type Props struct {
	Old string
}
Old := gastro.Props().Old
---
<div></div>`

	if err := os.WriteFile(filepath.Join(dir, "card.gastro"), []byte(staleContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// Simulate an open document with a newer version
	freshContent := `---
type Props struct {
	Title string
	Body  string
}
Title := gastro.Props().Title
---
<div></div>`

	absPath := filepath.Join(dir, "card.gastro")
	openDocs := map[string]string{
		"file://" + absPath: freshContent,
	}

	fields, err := lsptemplate.ResolveComponentProps(dir, "card.gastro", openDocs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fields) != 2 {
		t.Fatalf("expected 2 fields from open doc, got %d", len(fields))
	}
	if fields[0].Name != "Title" {
		t.Errorf("expected Title from open doc, got %s", fields[0].Name)
	}
	if fields[1].Name != "Body" {
		t.Errorf("expected Body from open doc, got %s", fields[1].Name)
	}
}

// parseForTest is a test helper that parses a template body into an AST.
// Returns nil tree on parse failure (which exercises the regex fallback path).
func parseForTest(body string, uses []parser.UseDeclaration) *parse.Tree {
	tree, _ := lsptemplate.ParseTemplateBody(body, uses)
	return tree
}

func TestDiagnoseComponentProps_UnknownProp(t *testing.T) {
	uses := []parser.UseDeclaration{
		{Name: "Card", Path: "components/card.gastro"},
	}
	propsMap := map[string][]codegen.StructField{
		"Card": {
			{Name: "Title", Type: "string"},
			{Name: "Body", Type: "string"},
		},
	}

	templateBody := `{{ Card (dict "Title" .Name "Bogus" .X) }}`
	tree := parseForTest(templateBody, uses)
	diags := lsptemplate.DiagnoseComponentProps(templateBody, tree, uses, propsMap)

	found := false
	for _, d := range diags {
		if strings.Contains(d.Message, `unknown prop "Bogus"`) {
			found = true
			if d.Severity != 1 {
				t.Errorf("expected severity 1 (Error), got %d", d.Severity)
			}
			break
		}
	}
	if !found {
		t.Errorf("expected diagnostic for unknown prop Bogus, got: %v", diags)
	}
}

func TestDiagnoseComponentProps_MissingProp(t *testing.T) {
	uses := []parser.UseDeclaration{
		{Name: "Card", Path: "components/card.gastro"},
	}
	propsMap := map[string][]codegen.StructField{
		"Card": {
			{Name: "Title", Type: "string"},
			{Name: "Body", Type: "string"},
		},
	}

	templateBody := `{{ Card (dict "Title" .Name) }}`
	tree := parseForTest(templateBody, uses)
	diags := lsptemplate.DiagnoseComponentProps(templateBody, tree, uses, propsMap)

	found := false
	for _, d := range diags {
		if strings.Contains(d.Message, `missing prop "Body"`) {
			found = true
			if d.Severity != 2 {
				t.Errorf("expected severity 2 (Warning), got %d", d.Severity)
			}
			break
		}
	}
	if !found {
		t.Errorf("expected warning for missing prop Body, got: %v", diags)
	}
}

func TestDiagnoseComponentProps_NoPropsStruct(t *testing.T) {
	uses := []parser.UseDeclaration{
		{Name: "Simple", Path: "components/simple.gastro"},
	}
	propsMap := map[string][]codegen.StructField{}

	templateBody := `{{ Simple (dict) }}`
	tree := parseForTest(templateBody, uses)
	diags := lsptemplate.DiagnoseComponentProps(templateBody, tree, uses, propsMap)

	if len(diags) != 0 {
		t.Errorf("expected 0 diagnostics for component without Props, got: %v", diags)
	}
}

func TestDiagnoseComponentProps_WithChildren(t *testing.T) {
	uses := []parser.UseDeclaration{
		{Name: "Layout", Path: "components/layout.gastro"},
	}
	propsMap := map[string][]codegen.StructField{
		"Layout": {
			{Name: "Title", Type: "string"},
		},
	}

	templateBody := `{{ wrap Layout (dict "Title" .Title) }}
<p>child content</p>
{{ end }}`

	tree := parseForTest(templateBody, uses)
	diags := lsptemplate.DiagnoseComponentProps(templateBody, tree, uses, propsMap)

	for _, d := range diags {
		if strings.Contains(d.Message, "unknown prop") {
			t.Errorf("unexpected unknown prop diagnostic: %s", d.Message)
		}
	}
}

// TestDiagnoseComponentProps_ChildrenDictKey_GitPM is a regression for the
// drift documented in tmp/lsp-shadow-audit (and the original git-pm bug
// report): when a page passes Children as an explicit dict key on a
// children-rendering layout, the LSP previously flagged it as an
// unknown prop because its DiagnoseComponentProps did not special-case
// the synthetic key the way codegen.ValidateDictKeys did. Phase 2
// unified the two via codegen.SyntheticPropKey.
//
// If this test ever fails, check that codegen.SyntheticPropKey still
// classifies "Children" as SyntheticChildren AND that
// codegen.ValidateDictKeysFromAST routes that classification through
// to a silent skip (not a diagnostic).
func TestDiagnoseComponentProps_ChildrenDictKey_GitPM(t *testing.T) {
	uses := []parser.UseDeclaration{
		{Name: "Layout", Path: "components/layout.gastro"},
	}
	propsMap := map[string][]codegen.StructField{
		"Layout": {
			{Name: "Title", Type: "string"},
		},
	}
	templateBody := `{{ Layout (dict "Title" .Title "Children" .Body) }}`
	tree := parseForTest(templateBody, uses)
	diags := lsptemplate.DiagnoseComponentProps(templateBody, tree, uses, propsMap)
	for _, d := range diags {
		if strings.Contains(d.Message, "unknown prop \"Children\"") {
			t.Errorf("Children must not be flagged as unknown prop; got: %s", d.Message)
		}
	}
}

// TestDiagnoseComponentProps_DeprecatedUnderscoreChildren is a regression
// for the deprecated-key hint. Phase 3 mirrored the codegen-side
// ValidateDictKeys hint into the LSP so users editing legacy templates
// see the migration guidance in their editor without running
// `gastro generate`. The expected severity is Warning (2), not Error.
func TestDiagnoseComponentProps_DeprecatedUnderscoreChildren(t *testing.T) {
	uses := []parser.UseDeclaration{
		{Name: "Layout", Path: "components/layout.gastro"},
	}
	propsMap := map[string][]codegen.StructField{
		"Layout": {
			{Name: "Title", Type: "string"},
		},
	}
	templateBody := `{{ Layout (dict "Title" .Title "__children" .Body) }}`
	tree := parseForTest(templateBody, uses)
	diags := lsptemplate.DiagnoseComponentProps(templateBody, tree, uses, propsMap)

	var found bool
	for _, d := range diags {
		if !strings.Contains(d.Message, "__children") {
			continue
		}
		if !strings.Contains(d.Message, "no longer recognised") {
			t.Errorf("deprecated-key message changed shape; got: %s", d.Message)
		}
		if d.Severity != 2 {
			t.Errorf("deprecated-key diagnostic must be a Warning (severity 2), got %d", d.Severity)
		}
		found = true
	}
	if !found {
		t.Fatalf("expected a __children deprecation hint; got %d diagnostics: %v", len(diags), diags)
	}
}

// TestDiagnoseComponentProps_WrapFormFallback exercises the regex
// fallback that handles `{{ wrap X (...) }}...{{ end }}` form. Go's
// text/template/parse rejects this because `wrap` isn't a built-in
// block keyword, so DiagnoseComponentProps gets a nil tree and the
// fallback path runs. The fallback delegates synthetic-key
// classification to codegen.SyntheticPropKey, so wrap-form and
// bare-call form agree on which keys are valid — this test asserts
// the canonical Children case is silently accepted, the deprecated
// __children produces a warning, and an unknown key produces an error.
func TestDiagnoseComponentProps_WrapFormFallback(t *testing.T) {
	uses := []parser.UseDeclaration{
		{Name: "Layout", Path: "components/layout.gastro"},
	}
	propsMap := map[string][]codegen.StructField{
		"Layout": {{Name: "Title", Type: "string"}},
	}

	cases := []struct {
		name    string
		body    string
		want    string // expected diagnostic substring; empty = none
		wantSev int    // expected severity if non-zero
	}{
		{
			name: "Children dict key on wrap form is silent",
			body: `{{ wrap Layout (dict "Title" .Title "Children" .Body) }}<p>x</p>{{ end }}`,
			want: "",
		},
		{
			name:    "__children on wrap form is deprecated warning",
			body:    `{{ wrap Layout (dict "Title" .Title "__children" .Body) }}<p>x</p>{{ end }}`,
			want:    `dict key "__children" is no longer recognised`,
			wantSev: 2,
		},
		{
			name:    "unknown key on wrap form is error",
			body:    `{{ wrap Layout (dict "Title" .Title "Bogus" "x") }}<p>x</p>{{ end }}`,
			want:    `unknown prop "Bogus"`,
			wantSev: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// ParseTemplateBody returns nil tree for wrap form —
			// confirm so we know the fallback is what's being tested.
			tree, err := lsptemplate.ParseTemplateBody(tc.body, uses)
			if err == nil && tree != nil {
				t.Fatalf("sanity: expected ParseTemplateBody to fail on wrap form, got tree=%v err=%v", tree, err)
			}
			diags := lsptemplate.DiagnoseComponentProps(tc.body, nil, uses, propsMap)
			if tc.want == "" {
				// Silent path — no key-related diagnostic should fire.
				for _, d := range diags {
					if strings.Contains(d.Message, "unknown prop") || strings.Contains(d.Message, "__children") {
						t.Errorf("expected silent acceptance, got diagnostic: %s", d.Message)
					}
				}
				return
			}
			var matched bool
			for _, d := range diags {
				if strings.Contains(d.Message, tc.want) {
					matched = true
					if tc.wantSev != 0 && d.Severity != tc.wantSev {
						t.Errorf("severity for %q: got %d, want %d", d.Message, d.Severity, tc.wantSev)
					}
				}
			}
			if !matched {
				t.Errorf("expected diagnostic containing %q; got %d diagnostics: %v", tc.want, len(diags), diags)
			}
		})
	}
}

func TestDiagnoseComponentProps_BareCall(t *testing.T) {
	uses := []parser.UseDeclaration{
		{Name: "KpiCard", Path: "components/kpi-card.gastro"},
	}
	propsMap := map[string][]codegen.StructField{
		"KpiCard": {
			{Name: "X", Type: "int"},
			{Name: "Value", Type: "string"},
			{Name: "Label", Type: "string"},
		},
	}

	templateBody := `{{ KpiCard }}`
	tree := parseForTest(templateBody, uses)
	diags := lsptemplate.DiagnoseComponentProps(templateBody, tree, uses, propsMap)

	missingProps := make(map[string]bool)
	for _, d := range diags {
		if strings.Contains(d.Message, "missing prop") {
			for _, f := range []string{"X", "Value", "Label"} {
				if strings.Contains(d.Message, fmt.Sprintf("%q", f)) {
					missingProps[f] = true
				}
			}
			if d.Severity != 2 {
				t.Errorf("expected severity 2 (Warning), got %d for: %s", d.Severity, d.Message)
			}
		}
	}

	for _, f := range []string{"X", "Value", "Label"} {
		if !missingProps[f] {
			t.Errorf("expected missing prop warning for %q, got diagnostics: %v", f, diags)
		}
	}
}

func TestDiagnoseComponentProps_NestedParens(t *testing.T) {
	uses := []parser.UseDeclaration{
		{Name: "Card", Path: "components/card.gastro"},
	}
	propsMap := map[string][]codegen.StructField{
		"Card": {
			{Name: "Date", Type: "string"},
			{Name: "Title", Type: "string"},
		},
	}

	templateBody := `{{ Card (dict "Date" (.CreatedAt | timeFormat "Jan") "Title" .Name) }}`
	tree := parseForTest(templateBody, uses)
	diags := lsptemplate.DiagnoseComponentProps(templateBody, tree, uses, propsMap)

	// Should not have any unknown prop errors
	for _, d := range diags {
		if strings.Contains(d.Message, "unknown prop") {
			t.Errorf("unexpected unknown prop diagnostic: %s", d.Message)
		}
	}
	// Should not have missing prop warnings since both are provided
	for _, d := range diags {
		if strings.Contains(d.Message, "missing prop") {
			t.Errorf("unexpected missing prop diagnostic: %s", d.Message)
		}
	}
}

func TestDetectComponentTagContext(t *testing.T) {
	uses := []parser.UseDeclaration{
		{Name: "Card", Path: "components/card.gastro"},
		{Name: "Layout", Path: "components/layout.gastro"},
	}

	tests := []struct {
		name      string
		input     string // | marks cursor position
		wantNil   bool
		wantComp  string
		wantProps []string
	}{
		{
			name:      "inside dict with existing prop",
			input:     `{{ Card (dict "Title" .Name |`,
			wantComp:  "Card",
			wantProps: []string{"Title"},
		},
		{
			name:     "bare component call with cursor after name",
			input:    `{{ Card |`,
			wantComp: "Card",
		},
		{
			name:      "wrap call inside dict",
			input:     `{{ wrap Layout (dict "Title" .T |`,
			wantComp:  "Layout",
			wantProps: []string{"Title"},
		},
		{
			name:    "cursor outside component call",
			input:   `<p>hello|</p>`,
			wantNil: true,
		},
		{
			name:    "cursor after closed component call",
			input:   `{{ Card (dict "Title" .Title) }}|`,
			wantNil: true,
		},
		{
			name:    "unknown component",
			input:   `{{ Unknown (dict |`,
			wantNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			idx := strings.LastIndex(tt.input, "|")
			templateBody := tt.input[:idx] + tt.input[idx+1:]
			cursorOffset := idx

			// Test regex fallback path (tree=nil, simulating parse failure during editing)
			got := lsptemplate.DetectComponentTagContext(templateBody, cursorOffset, uses, nil)

			if tt.wantNil {
				if got != nil {
					t.Errorf("expected nil, got %+v", got)
				}
				return
			}

			if got == nil {
				t.Fatal("expected non-nil ComponentTagContext, got nil")
			}

			if got.ComponentName != tt.wantComp {
				t.Errorf("ComponentName: got %q, want %q", got.ComponentName, tt.wantComp)
			}

			if len(got.ExistingProps) != len(tt.wantProps) {
				t.Errorf("ExistingProps: got %v, want %v", got.ExistingProps, tt.wantProps)
			} else {
				for i, p := range tt.wantProps {
					if got.ExistingProps[i] != p {
						t.Errorf("ExistingProps[%d]: got %q, want %q", i, got.ExistingProps[i], p)
					}
				}
			}
		})
	}
}

func TestDetectPropValueContext(t *testing.T) {
	tests := []struct {
		name      string
		input     string // | marks cursor position
		wantNil   bool
		afterPipe bool
	}{
		{
			name:    "simple variable in dict value",
			input:   `{{ Card (dict "Title" .|`,
			wantNil: false,
		},
		{
			name:      "after pipe in dict value",
			input:     `{{ Card (dict "Date" (.CreatedAt | t|`,
			afterPipe: true,
		},
		{
			name:    "cursor outside any component call",
			input:   `<p>hello|</p>`,
			wantNil: true,
		},
		{
			name:    "cursor after closed component call",
			input:   `{{ Card (dict "Title" .Title) }}|`,
			wantNil: true,
		},
		{
			name:    "empty input",
			input:   `|`,
			wantNil: true,
		},
		{
			name:    "wrap call with cursor in dict",
			input:   `{{ wrap Layout (dict "Title" .|`,
			wantNil: false,
		},
		{
			name:    "bare component call has no value context",
			input:   `{{ Card |`,
			wantNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			idx := strings.LastIndex(tt.input, "|")
			templateBody := tt.input[:idx] + tt.input[idx+1:]
			cursorOffset := idx

			got := lsptemplate.DetectPropValueContext(templateBody, cursorOffset)

			if tt.wantNil {
				if got != nil {
					t.Errorf("expected nil, got %+v", got)
				}
				return
			}

			if got == nil {
				t.Fatal("expected non-nil PropValueContext, got nil")
			}

			if got.AfterPipe != tt.afterPipe {
				t.Errorf("AfterPipe: got %v, want %v", got.AfterPipe, tt.afterPipe)
			}
		})
	}
}

func TestDiagnose_ChildrenInComponent(t *testing.T) {
	info := &codegen.FrontmatterInfo{
		IsComponent: true,
		ExportedVars: []codegen.VarInfo{
			{Name: "Title"},
		},
	}

	templateBody := `<h1>{{ .Title }}</h1>
<main>{{ .Children }}</main>`

	diags := lsptemplate.Diagnose(templateBody, info, nil, nil, nil, nil)

	for _, d := range diags {
		if strings.Contains(d.Message, "Children") {
			t.Errorf("Children should not be flagged in a component, got: %s", d.Message)
		}
	}
}

func TestDiagnose_ChildrenInNonComponent(t *testing.T) {
	info := &codegen.FrontmatterInfo{
		IsComponent: false,
		ExportedVars: []codegen.VarInfo{
			{Name: "Title"},
		},
	}

	templateBody := `<h1>{{ .Title }}</h1>
<main>{{ .Children }}</main>`

	diags := lsptemplate.Diagnose(templateBody, info, nil, nil, nil, nil)

	found := false
	for _, d := range diags {
		if strings.Contains(d.Message, "Children") {
			found = true
			break
		}
	}
	if !found {
		t.Error("Children should be flagged as unknown in non-component pages")
	}
}

func TestVariableCompletions_IncludesChildrenForComponents(t *testing.T) {
	info := &codegen.FrontmatterInfo{
		IsComponent: true,
		ExportedVars: []codegen.VarInfo{
			{Name: "Title"},
		},
	}

	completions := lsptemplate.VariableCompletions(info)

	hasChildren := false
	for _, c := range completions {
		if c.Label == ".Children" {
			hasChildren = true
			if c.Detail != "children content" {
				t.Errorf("Children detail: got %q, want %q", c.Detail, "children content")
			}
		}
	}
	if !hasChildren {
		t.Error("expected .Children in completions for component")
	}
}

func TestVariableCompletions_ExcludesChildrenForPages(t *testing.T) {
	info := &codegen.FrontmatterInfo{
		IsComponent: false,
		ExportedVars: []codegen.VarInfo{
			{Name: "Title"},
		},
	}

	completions := lsptemplate.VariableCompletions(info)

	for _, c := range completions {
		if c.Label == ".Children" {
			t.Error(".Children should not appear in completions for non-component pages")
		}
	}
}

func TestBuildComponentSnippet(t *testing.T) {
	tests := []struct {
		name   string
		comp   string
		fields []codegen.StructField
		want   string
	}{
		{
			name:   "no props",
			comp:   "Card",
			fields: nil,
			want:   "Card (dict $0)",
		},
		{
			name:   "empty props",
			comp:   "Card",
			fields: []codegen.StructField{},
			want:   "Card (dict $0)",
		},
		{
			name: "single string prop",
			comp: "Card",
			fields: []codegen.StructField{
				{Name: "Title", Type: "string"},
			},
			want: `Card (dict "Title" ${1:""})$0`,
		},
		{
			name: "multiple props with types",
			comp: "Card",
			fields: []codegen.StructField{
				{Name: "Title", Type: "string"},
				{Name: "Count", Type: "int"},
				{Name: "Active", Type: "bool"},
			},
			want: `Card (dict "Title" ${1:""} "Count" ${2:0} "Active" ${3:false})$0`,
		},
		{
			name: "unknown type gets value placeholder",
			comp: "Widget",
			fields: []codegen.StructField{
				{Name: "Data", Type: "db.Record"},
			},
			want: `Widget (dict "Data" ${1:value})$0`,
		},
		{
			name: "prop name with dollar sign is escaped",
			comp: "Card",
			fields: []codegen.StructField{
				{Name: "Price$", Type: "string"},
			},
			want: `Card (dict "Price\$" ${1:""})$0`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := lsptemplate.BuildComponentSnippet(tt.comp, tt.fields)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestStripSnippetSyntax(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "tabstop with placeholder",
			input: `Card (dict "Title" ${1:""} "Count" ${2:0})$0`,
			want:  `Card (dict "Title" "" "Count" 0)`,
		},
		{
			name:  "bare tabstop",
			input: `Card (dict $0)`,
			want:  `Card (dict )`,
		},
		{
			name:  "no snippet syntax",
			input: `Card`,
			want:  `Card`,
		},
		{
			name:  "prop completion snippet",
			input: `"Title" ${1:.}`,
			want:  `"Title" .`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := lsptemplate.StripSnippetSyntax(tt.input)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestComponentPropCompletions_WithSnippets(t *testing.T) {
	fields := []codegen.StructField{
		{Name: "Title", Type: "string"},
		{Name: "Count", Type: "int"},
	}

	// With snippets enabled
	items := lsptemplate.ComponentPropCompletions(fields, nil, true)
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	if !items[0].IsSnippet {
		t.Error("expected IsSnippet=true for snippet mode")
	}
	if items[0].InsertText != `"Title" ${1:.}` {
		t.Errorf("snippet insertText: got %q, want %q", items[0].InsertText, `"Title" ${1:.}`)
	}

	// With snippets disabled
	items = lsptemplate.ComponentPropCompletions(fields, nil, false)
	if items[0].IsSnippet {
		t.Error("expected IsSnippet=false for plain text mode")
	}
	if items[0].InsertText != `"Title" .` {
		t.Errorf("plain insertText: got %q, want %q", items[0].InsertText, `"Title" .`)
	}
}

func TestComponentPropCompletions_FiltersExisting(t *testing.T) {
	fields := []codegen.StructField{
		{Name: "Title", Type: "string"},
		{Name: "Body", Type: "string"},
	}

	items := lsptemplate.ComponentPropCompletions(fields, []string{"Title"}, true)
	if len(items) != 1 {
		t.Fatalf("expected 1 item (Title filtered), got %d", len(items))
	}
	if items[0].Label != "Body" {
		t.Errorf("expected Body, got %s", items[0].Label)
	}
}
