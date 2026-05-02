package codegen_test

import (
	"strings"
	"testing"

	"github.com/andrioid/gastro/internal/codegen"
)

func TestAnalyze_ExtractsUppercaseVariables(t *testing.T) {
	frontmatter := `Title := "Hello"
Items := []string{"a", "b"}
count := 3`

	info, err := codegen.AnalyzeFrontmatter(frontmatter)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(info.ExportedVars) != 2 {
		t.Fatalf("expected 2 exported vars, got %d: %v", len(info.ExportedVars), info.ExportedVars)
	}

	assertVarExists(t, info.ExportedVars, "Title")
	assertVarExists(t, info.ExportedVars, "Items")
}

func TestAnalyze_ExcludesLowercaseVariables(t *testing.T) {
	frontmatter := `title := "Hello"
err := doSomething()
count := 3`

	info, err := codegen.AnalyzeFrontmatter(frontmatter)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(info.ExportedVars) != 0 {
		t.Fatalf("expected 0 exported vars, got %d: %v", len(info.ExportedVars), info.ExportedVars)
	}

	if len(info.PrivateVars) != 3 {
		t.Fatalf("expected 3 private vars, got %d: %v", len(info.PrivateVars), info.PrivateVars)
	}
}

func TestAnalyze_SkipsBlankIdentifier(t *testing.T) {
	frontmatter := `_, err := doSomething()
Title := "Hello"`

	info, err := codegen.AnalyzeFrontmatter(frontmatter)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// _ should not appear in either list
	for _, v := range info.ExportedVars {
		if v.Name == "_" {
			t.Error("blank identifier should not be in exported vars")
		}
	}
	for _, v := range info.PrivateVars {
		if v.Name == "_" {
			t.Error("blank identifier should not be in private vars")
		}
	}

	assertVarExists(t, info.ExportedVars, "Title")
	assertVarExists(t, info.PrivateVars, "err")
}

func TestAnalyze_DetectsContextCall(t *testing.T) {
	frontmatter := `ctx := gastro.Context()
Title := "Hello"`

	info, err := codegen.AnalyzeFrontmatter(frontmatter)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !info.IsPage {
		t.Error("expected IsPage to be true when gastro.Context() is called")
	}
}

func TestAnalyze_NoContextCallMeansNotPage(t *testing.T) {
	frontmatter := `Title := "Hello"`

	info, err := codegen.AnalyzeFrontmatter(frontmatter)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if info.IsPage {
		t.Error("expected IsPage to be false when gastro.Context() is not called")
	}
}

func TestAnalyze_DetectsPropsCall(t *testing.T) {
	frontmatter := `type Props struct {
	Title string
	Urgent bool
}

props := gastro.Props()`

	info, err := codegen.AnalyzeFrontmatter(frontmatter)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !info.IsComponent {
		t.Error("expected IsComponent to be true when gastro.Props is called")
	}

	if info.PropsTypeName != "Props" {
		t.Errorf("PropsTypeName: got %q, want %q", info.PropsTypeName, "Props")
	}
}

func TestAnalyze_HandlesVarDeclarations(t *testing.T) {
	// var keyword declarations in addition to short := declarations
	frontmatter := `var Title string = "Hello"
var count int = 5`

	info, err := codegen.AnalyzeFrontmatter(frontmatter)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertVarExists(t, info.ExportedVars, "Title")
	assertVarExists(t, info.PrivateVars, "count")
}

func TestAnalyze_HandlesMultipleAssignment(t *testing.T) {
	frontmatter := `Title, Subtitle := "Hello", "World"
a, b := 1, 2`

	info, err := codegen.AnalyzeFrontmatter(frontmatter)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertVarExists(t, info.ExportedVars, "Title")
	assertVarExists(t, info.ExportedVars, "Subtitle")
	assertVarExists(t, info.PrivateVars, "a")
	assertVarExists(t, info.PrivateVars, "b")
}

func TestAnalyze_EmptyFrontmatter(t *testing.T) {
	info, err := codegen.AnalyzeFrontmatter("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(info.ExportedVars) != 0 {
		t.Errorf("expected 0 exported vars, got %d", len(info.ExportedVars))
	}

	if len(info.PrivateVars) != 0 {
		t.Errorf("expected 0 private vars, got %d", len(info.PrivateVars))
	}
}

func TestAnalyze_DetectsNewPropsCall(t *testing.T) {
	frontmatter := `type Props struct {
	Title string
}

Title := gastro.Props().Title`

	info, err := codegen.AnalyzeFrontmatter(frontmatter)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !info.IsComponent {
		t.Error("expected IsComponent to be true when gastro.Props() is called")
	}

	if info.PropsTypeName != "Props" {
		t.Errorf("PropsTypeName: got %q, want %q", info.PropsTypeName, "Props")
	}
}

func TestAnalyze_DetectsNewPropsCallWholeStruct(t *testing.T) {
	frontmatter := `type Props struct {
	Title string
}

p := gastro.Props()
Title := p.Title`

	info, err := codegen.AnalyzeFrontmatter(frontmatter)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !info.IsComponent {
		t.Error("expected IsComponent to be true when gastro.Props() is called")
	}
}

func TestAnalyze_RejectsContextAndPropsTogether(t *testing.T) {
	frontmatter := `type Props struct {
	Title string
}
ctx := gastro.Context()
props := gastro.Props()`

	_, err := codegen.AnalyzeFrontmatter(frontmatter)
	if err == nil {
		t.Fatal("expected error when both gastro.Context() and gastro.Props() are used")
	}
}

func TestAnalyze_RejectsMissingPropsStruct(t *testing.T) {
	frontmatter := `props := gastro.Props()`

	_, err := codegen.AnalyzeFrontmatter(frontmatter)
	if err == nil {
		t.Fatal("expected error when gastro.Props() is used without type Props struct")
	}
}

func TestAnalyze_RejectsMultiplePropsStructs(t *testing.T) {
	frontmatter := `type Props struct {
	Title string
}

type Props struct {
	Name string
}`

	_, err := codegen.AnalyzeFrontmatter(frontmatter)
	if err == nil {
		t.Fatal("expected error when multiple type Props struct declarations exist")
	}
}

func TestAnalyze_PropsInCommentNotDetected(t *testing.T) {
	// gastro.Props inside a comment should NOT trigger IsComponent
	frontmatter := `// TODO: gastro.Props() should be called here
Title := "Hello"`

	info, err := codegen.AnalyzeFrontmatter(frontmatter)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if info.IsComponent {
		t.Error("expected IsComponent to be false when gastro.Props is only in a comment")
	}
}

func TestAnalyze_PropsInStringNotDetected(t *testing.T) {
	// gastro.Props inside a string literal should NOT trigger IsComponent
	frontmatter := `msg := "Call gastro.Props() to get props"
Title := "Hello"`

	info, err := codegen.AnalyzeFrontmatter(frontmatter)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if info.IsComponent {
		t.Error("expected IsComponent to be false when gastro.Props is only in a string")
	}
}

func TestAnalyze_ContextInCommentNotDetected(t *testing.T) {
	frontmatter := `// gastro.Context() is important
Title := "Hello"`

	info, err := codegen.AnalyzeFrontmatter(frontmatter)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if info.IsPage {
		t.Error("expected IsPage to be false when gastro.Context() is only in a comment")
	}
}

func TestHoistTypeDeclarations_BacktickStringWithType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		frontmatter  string
		wantInBody   []string // substrings that must be in body
		wantInTypes  []string // substrings that must be in typeDecls
		wantNotTypes []string // substrings that must NOT be in typeDecls
	}{
		{
			name:         "normal type declaration",
			frontmatter:  "type Props struct {\n\tTitle string\n}\n\nTitle := \"hello\"",
			wantInBody:   []string{"Title"},
			wantInTypes:  []string{"type Props struct", "Title string"},
			wantNotTypes: nil,
		},
		{
			name:         "type inside backtick string not hoisted",
			frontmatter:  "example := `\ntype Foo struct {\n\tBar string\n}\n`\n\nTitle := \"hello\"",
			wantInBody:   []string{"example", "type Foo struct", "Title"},
			wantInTypes:  nil,
			wantNotTypes: []string{"type Foo"},
		},
		{
			name:         "fake type in backtick then real type after",
			frontmatter:  "example := `\ntype BadStruct struct {\n\tBad string\n}\n`\n\ntype Props struct {\n\tTitle string\n}\n\nTitle := \"hello\"",
			wantInBody:   []string{"example", "type BadStruct", "Title"},
			wantInTypes:  []string{"type Props struct"},
			wantNotTypes: []string{"BadStruct"},
		},
		{
			name:         "backtick string with braces after type",
			frontmatter:  "type Props struct {\n\tTitle string\n}\n\nexample := `\n{\n  \"key\": \"value\"\n}\n`\nTitle := \"hello\"",
			wantInBody:   []string{"example", "Title"},
			wantInTypes:  []string{"type Props struct"},
			wantNotTypes: nil,
		},
		{
			// Regression: inline field comments containing `{` previously
			// threw off the line-based brace counter, causing the closing
			// `}` to be miscounted and the rest of the frontmatter to be
			// hoisted into package scope. AST-based hoisting is immune.
			name:         "inline comment with unbalanced open brace",
			frontmatter:  "type Props struct {\n\tTitle string // contains { unbalanced\n}\n\nTitle := \"hi\"",
			wantInBody:   []string{"Title := \"hi\""},
			wantInTypes:  []string{"type Props struct", "Title string", "unbalanced"},
			wantNotTypes: []string{"Title := \"hi\""},
		},
		{
			name:         "inline comment with unbalanced close brace",
			frontmatter:  "type Props struct {\n\tTitle string // contains } unbalanced\n}\n\nTitle := \"hi\"",
			wantInBody:   []string{"Title := \"hi\""},
			wantInTypes:  []string{"type Props struct", "Title string", "unbalanced"},
			wantNotTypes: []string{"Title := \"hi\""},
		},
		{
			// Regression: a comment with a single backtick previously made
			// the parser think the next line was inside a raw string,
			// breaking type-decl detection.
			name:         "inline comment with backtick",
			frontmatter:  "type Props struct {\n\tTitle string // see `code` here\n}\n\nTitle := \"hi\"",
			wantInBody:   []string{"Title := \"hi\""},
			wantInTypes:  []string{"type Props struct", "Title string", "`code`"},
			wantNotTypes: []string{"Title := \"hi\""},
		},
		{
			name:         "struct tag plus inline comment",
			frontmatter:  "type Props struct {\n\tTitle string `json:\"t,omitempty\"` // tagged field\n}\n\nTitle := \"hi\"",
			wantInBody:   []string{"Title := \"hi\""},
			wantInTypes:  []string{"type Props struct", "json:", "tagged field"},
			wantNotTypes: []string{"Title := \"hi\""},
		},
		{
			name:         "multiple type declarations",
			frontmatter:  "type Item struct {\n\tID string\n}\n\ntype Props struct {\n\tItems []Item\n}\n\nTitle := \"hi\"",
			wantInBody:   []string{"Title := \"hi\""},
			wantInTypes:  []string{"type Item struct", "type Props struct"},
			wantNotTypes: []string{"Title := \"hi\""},
		},
		{
			name:         "empty frontmatter",
			frontmatter:  "",
			wantInBody:   nil,
			wantInTypes:  nil,
			wantNotTypes: nil,
		},
		{
			name:         "only types",
			frontmatter:  "type Props struct {\n\tTitle string\n}",
			wantInBody:   nil,
			wantInTypes:  []string{"type Props struct", "Title string"},
			wantNotTypes: nil,
		},
		{
			name:         "type alias",
			frontmatter:  "type StringAlias = string\n\nTitle := \"hi\"",
			wantInBody:   []string{"Title := \"hi\""},
			wantInTypes:  []string{"type StringAlias = string"},
			wantNotTypes: []string{"Title := \"hi\""},
		},
		{
			// Unparseable frontmatter (mid-edit) should still produce
			// reasonable output via the legacy fallback.
			name:         "unparseable falls back gracefully",
			frontmatter:  "type Props struct {\n\tTitle string\n\nleftover := \"oops\"",
			wantInBody:   nil,
			wantInTypes:  []string{"type Props"},
			wantNotTypes: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			body, typeDecls := codegen.HoistTypeDeclarations(tt.frontmatter)

			for _, want := range tt.wantInBody {
				if !strings.Contains(body, want) {
					t.Errorf("body should contain %q, got:\n%s", want, body)
				}
			}
			for _, want := range tt.wantInTypes {
				if !strings.Contains(typeDecls, want) {
					t.Errorf("typeDecls should contain %q, got:\n%s", want, typeDecls)
				}
			}
			for _, notWant := range tt.wantNotTypes {
				if strings.Contains(typeDecls, notWant) {
					t.Errorf("typeDecls should NOT contain %q, got:\n%s", notWant, typeDecls)
				}
			}
		})
	}
}

func TestAnalyze_WarnsBarePropsOnExportedVar(t *testing.T) {
	frontmatter := `type Props struct {
	Name string
}

Name := gastro.Props()`

	info, err := codegen.AnalyzeFrontmatter(frontmatter)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(info.Warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d", len(info.Warnings))
	}
	if !strings.Contains(info.Warnings[0].Message, "entire Props struct") {
		t.Errorf("expected warning about entire Props struct, got: %s", info.Warnings[0].Message)
	}
	if !strings.Contains(info.Warnings[0].Message, "gastro.Props().Name") {
		t.Errorf("expected warning to suggest gastro.Props().Name, got: %s", info.Warnings[0].Message)
	}
	// Line should point to the assignment, not the type declaration
	if info.Warnings[0].Line != 5 {
		t.Errorf("expected warning on frontmatter line 5, got %d", info.Warnings[0].Line)
	}
}

func TestAnalyze_WarnsMultipleBarePropsExportedVars(t *testing.T) {
	frontmatter := `type Props struct {
	Name  string
	Title string
}

Name := gastro.Props()
Title := gastro.Props()`

	info, err := codegen.AnalyzeFrontmatter(frontmatter)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(info.Warnings) != 2 {
		t.Fatalf("expected 2 warnings, got %d", len(info.Warnings))
	}
	if !strings.Contains(info.Warnings[0].Message, "gastro.Props().Name") {
		t.Errorf("warning 0: expected Name suggestion, got: %s", info.Warnings[0].Message)
	}
	if !strings.Contains(info.Warnings[1].Message, "gastro.Props().Title") {
		t.Errorf("warning 1: expected Title suggestion, got: %s", info.Warnings[1].Message)
	}
}

func TestAnalyze_AllowsBarePropsOnPrivateVar(t *testing.T) {
	frontmatter := `type Props struct {
	Title string
}

p := gastro.Props()
Title := p.Title`

	info, err := codegen.AnalyzeFrontmatter(frontmatter)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(info.Warnings) != 0 {
		t.Errorf("expected no warnings for private var, got %d: %v", len(info.Warnings), info.Warnings)
	}
}

func TestAnalyze_AllowsPropsFieldAccessOnExportedVar(t *testing.T) {
	frontmatter := `type Props struct {
	Name string
}

Name := gastro.Props().Name`

	info, err := codegen.AnalyzeFrontmatter(frontmatter)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(info.Warnings) != 0 {
		t.Errorf("expected no warnings for field access, got %d: %v", len(info.Warnings), info.Warnings)
	}
}

func assertVarExists(t *testing.T, vars []codegen.VarInfo, name string) {
	t.Helper()
	for _, v := range vars {
		if v.Name == name {
			return
		}
	}
	names := make([]string, len(vars))
	for i, v := range vars {
		names[i] = v.Name
	}
	t.Errorf("expected variable %q in list, got: %v", name, names)
}

// Track B (plans/frictions-plan.md §4.2): the previous
// "ctx is referenced but gastro.Context() was not called" warning is
// gone. Pages no longer have an injected ctx — they use the ambient
// (w, r) directly. A user who writes `ctx.Param(…)` without declaring
// ctx now gets the standard Go undefined-identifier error, which is no
// longer confusing because the page model never auto-injects.
//
// The corresponding tests for that warning are removed. The marker
// rewriter still emits a deprecation warning when `gastro.Context()`
// is called — covered by the marker tests in generate_test.go.
