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

func TestAnalyze_RejectsBarePropsOnExportedVar(t *testing.T) {
	frontmatter := `type Props struct {
	Name string
}

Name := gastro.Props()`

	_, err := codegen.AnalyzeFrontmatter(frontmatter)
	if err == nil {
		t.Fatal("expected error when exported var is assigned bare gastro.Props()")
	}
	if !strings.Contains(err.Error(), "entire Props struct") {
		t.Errorf("expected error about entire Props struct, got: %v", err)
	}
	if !strings.Contains(err.Error(), "gastro.Props().Name") {
		t.Errorf("expected error to suggest gastro.Props().Name, got: %v", err)
	}
}

func TestAnalyze_AllowsBarePropsOnPrivateVar(t *testing.T) {
	// Assigning gastro.Props() to a private var is fine — it's the
	// standard "whole struct" pattern: p := gastro.Props()
	frontmatter := `type Props struct {
	Title string
}

p := gastro.Props()
Title := p.Title`

	_, err := codegen.AnalyzeFrontmatter(frontmatter)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAnalyze_AllowsPropsFieldAccessOnExportedVar(t *testing.T) {
	// The correct pattern: Name := gastro.Props().Name
	frontmatter := `type Props struct {
	Name string
}

Name := gastro.Props().Name`

	_, err := codegen.AnalyzeFrontmatter(frontmatter)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
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
