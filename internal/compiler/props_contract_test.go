package compiler_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andrioid/gastro/internal/compiler"
)

// writePropsProject writes a Card component with the given Props struct
// body and a page whose template body is page.
func writePropsProject(t *testing.T, propsFields, page string) string {
	t.Helper()
	projectDir := t.TempDir()
	for _, d := range []string{"components", "pages"} {
		if err := os.MkdirAll(filepath.Join(projectDir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	mustWriteFile(t, filepath.Join(projectDir, "components", "card.gastro"),
		"---\n"+
			"type Props struct {\n"+propsFields+"}\n\n"+
			"p := gastro.Props()\n"+
			"_ = p\n"+
			"---\n"+
			"<div></div>\n")
	mustWriteFile(t, filepath.Join(projectDir, "pages", "index.gastro"),
		"---\n"+
			`import Card "components/card.gastro"`+"\n"+
			"---\n"+
			page+"\n")
	return projectDir
}

func findDiagnostic(t *testing.T, err error, substr string) compiler.Diagnostic {
	t.Helper()
	var diagErr *compiler.DiagnosticsError
	if !errors.As(err, &diagErr) {
		t.Fatalf("expected *compiler.DiagnosticsError, got %T: %v", err, err)
	}
	for _, d := range diagErr.Diagnostics {
		if strings.Contains(d.Message, substr) {
			return d
		}
	}
	t.Fatalf("no diagnostic containing %q in: %v", substr, diagErr)
	return compiler.Diagnostic{}
}

func TestCompile_OmittedRequiredPropFails(t *testing.T) {
	projectDir := writePropsProject(t,
		"\tTitle string `gastro:\"required\"`\n\tBody string\n",
		`<main>{{ Card (dict "Body" "x") }}</main>`)

	for _, strict := range []bool{false, true} {
		_, err := compiler.Compile(projectDir, t.TempDir(), compiler.CompileOptions{Strict: strict})
		d := findDiagnostic(t, err, `missing required prop "Title" on component Card`)
		if d.File != "pages/index.gastro" || d.Line != 4 || d.Col != 10 {
			t.Errorf("strict=%v: diagnostic at %s:%d:%d, want pages/index.gastro:4:10", strict, d.File, d.Line, d.Col)
		}
	}
}

func TestCompile_OmittedOptionalPropPasses(t *testing.T) {
	projectDir := writePropsProject(t,
		"\tTitle string `gastro:\"required\"`\n\tBody string\n",
		`<main>{{ Card (dict "Title" "x") }}</main>`)

	// Strict: not even a warning for the omitted optional Body.
	if _, err := compiler.Compile(projectDir, t.TempDir(), compiler.CompileOptions{Strict: true}); err != nil {
		t.Fatalf("optional props must never warn: %v", err)
	}
}

func TestCompile_RequiredCheckOnlyEmittedForRequiredProps(t *testing.T) {
	for _, tc := range []struct {
		fields string
		want   bool
	}{
		{"\tTitle string `gastro:\"required\"`\n", true},
		{"\tTitle string\n", false},
	} {
		projectDir := writePropsProject(t, tc.fields, `{{ Card (dict "Title" "x") }}`)
		out := t.TempDir()
		if _, err := compiler.Compile(projectDir, out, compiler.CompileOptions{}); err != nil {
			t.Fatalf("compile: %v", err)
		}
		src := readGeneratedComponent(t, out)
		if got := strings.Contains(src, "gastroRuntime.CheckRequiredProps(__props)"); got != tc.want {
			t.Errorf("fields %q: CheckRequiredProps emitted = %v, want %v", tc.fields, got, tc.want)
		}
	}
}

func TestCompile_TypecheckPropsLiteralMismatch(t *testing.T) {
	projectDir := writePropsProject(t, "\tCount int\n", `<main>{{ Card (dict "Count" "three") }}</main>`)

	// On by default.
	_, err := compiler.Compile(projectDir, t.TempDir(), compiler.CompileOptions{})
	d := findDiagnostic(t, err, `prop "Count" on component Card: cannot use "three"`)
	if d.File != "pages/index.gastro" || d.Line != 4 || d.Col != 29 {
		t.Errorf("diagnostic at %s:%d:%d, want pages/index.gastro:4:29", d.File, d.Line, d.Col)
	}

	if _, err := compiler.Compile(projectDir, t.TempDir(), compiler.CompileOptions{Strict: true, NoTypecheckProps: true}); err != nil {
		t.Fatalf("with NoTypecheckProps the literal passes: %v", err)
	}
}

func TestCompile_TypecheckPropsEmitsRuntimeCheck(t *testing.T) {
	projectDir := writePropsProject(t, "\tCount int\n", `{{ Card (dict "Count" 3) }}`)
	for _, off := range []bool{false, true} {
		out := t.TempDir()
		if _, err := compiler.Compile(projectDir, out, compiler.CompileOptions{NoTypecheckProps: off}); err != nil {
			t.Fatalf("compile: %v", err)
		}
		src := readGeneratedComponent(t, out)
		if got := strings.Contains(src, "gastroRuntime.CheckPropTypes["); got == off {
			t.Errorf("NoTypecheckProps=%v: CheckPropTypes emitted = %v", off, got)
		}
	}
}

func TestCompile_PropsFieldNamedChildrenRejected(t *testing.T) {
	for _, fields := range []string{
		"\tTitle string\n\tChildren string\n",
		"\tTitle,  Children string\n",
		"\tTitle string\n\tChildren\n",
	} {
		projectDir := writePropsProject(t, fields, `{{ Card (dict "Title" "x") }}`)
		_, err := compiler.Compile(projectDir, t.TempDir(), compiler.CompileOptions{})
		d := findDiagnostic(t, err, `Props field "Children" is reserved`)
		if !d.IsError() {
			t.Errorf("fields %q: want an error, got %+v", fields, d)
		}
		if !strings.Contains(d.Message, "{{ wrap }}") || !strings.Contains(d.Message, "rename") {
			t.Errorf("fields %q: message should name the wrap slot and suggest a rename: %q", fields, d.Message)
		}
		wantLine, wantCol := 4, 2
		if strings.HasPrefix(fields, "\tTitle,") {
			wantLine, wantCol = 3, 10
		}
		if d.File != "components/card.gastro" || d.Line != wantLine || d.Col != wantCol {
			t.Errorf("fields %q: diagnostic at %s:%d:%d, want components/card.gastro:%d:%d", fields, d.File, d.Line, d.Col, wantLine, wantCol)
		}
	}
}

func readGeneratedComponent(t *testing.T, outDir string) string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(outDir, "component*card*.go"))
	if err != nil || len(matches) != 1 {
		entries, _ := os.ReadDir(outDir)
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("expected one generated card component file, got %v (dir: %v)", matches, names)
	}
	b, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
