package codegen_test

import (
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

props := gastro.Props[Props]()`

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
