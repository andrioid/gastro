package compiler_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andrioid/gastro/internal/codegen"
	"github.com/andrioid/gastro/internal/compiler"
)

// writeFiles writes relative path → content under dir.
func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		path := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

const (
	cardComponent = "---\n" +
		"type Props struct {\n" +
		"\tTitle string\n" +
		"\tBody  string\n" +
		"}\n" +
		"Title := gastro.Props().Title\n" +
		"Body := gastro.Props().Body\n" +
		"---\n" +
		"<div>{{ .Title }}{{ .Body }}</div>\n"
	layoutComponent = "---\n" +
		"type Props struct { Title string }\n" +
		"Title := gastro.Props().Title\n" +
		"---\n" +
		"<html><title>{{ .Title }}</title><body>{{ .Children }}</body></html>\n"
)

// threeBadKeysProject has three unknown dict keys across two pages. One
// sits inside a wrap block, whose children the transform moves to the end
// of the template.
func threeBadKeysProject() map[string]string {
	return map[string]string{
		"go.mod":                   "module example.com/m\n\ngo 1.27\n",
		"components/card.gastro":   cardComponent,
		"components/layout.gastro": layoutComponent,
		"pages/index.gastro": "---\n" + // 1
			"import (\n" + // 2
			"\tCard \"components/card.gastro\"\n" + // 3
			"\tLayout \"components/layout.gastro\"\n" + // 4
			")\n" + // 5
			"---\n" + // 6
			"{{ wrap Layout (dict \"Title\" \"Home\") }}\n" + // 7
			"  {{ Card (dict \"Tite\" \"a\") }}\n" + // 8
			"{{ end }}\n" + // 9
			"{{ Card (dict \"Title\" \"b\" \"Bdy\" \"c\") }}\n", // 10
		"pages/about.gastro": "---\n" + // 1
			"import Card \"components/card.gastro\"\n" + // 2
			"---\n" + // 3
			"<main>{{ Card (dict \"Heading\" \"x\") }}</main>\n", // 4
	}
}

func TestCompile_ReportsEveryErrorWithPositionAndWritesNothing(t *testing.T) {
	projectDir := t.TempDir()
	writeFiles(t, projectDir, threeBadKeysProject())
	outputDir := filepath.Join(t.TempDir(), ".gastro")

	_, err := compiler.Compile(projectDir, outputDir, compiler.CompileOptions{})

	var diagErr *compiler.DiagnosticsError
	if !errors.As(err, &diagErr) {
		t.Fatalf("expected *compiler.DiagnosticsError, got %T: %v", err, err)
	}
	type at struct {
		file      string
		line, col int
		key       string
	}
	want := []at{
		{"pages/about.gastro", 4, 21, `"Heading"`},
		{"pages/index.gastro", 8, 17, `"Tite"`},
		{"pages/index.gastro", 10, 27, `"Bdy"`},
	}
	if len(diagErr.Diagnostics) != len(want) {
		t.Fatalf("expected %d diagnostics, got %d:\n%v", len(want), len(diagErr.Diagnostics), diagErr)
	}
	for i, w := range want {
		d := diagErr.Diagnostics[i]
		if d.File != w.file || d.Line != w.line || d.Col != w.col {
			t.Errorf("diagnostic %d at %s:%d:%d, want %s:%d:%d", i, d.File, d.Line, d.Col, w.file, w.line, w.col)
		}
		if d.Severity != codegen.SeverityError || !strings.Contains(d.Message, "unknown prop "+w.key) {
			t.Errorf("diagnostic %d = %+v, want an unknown-prop error for %s", i, d, w.key)
		}
	}
	if got := diagErr.Error(); !strings.Contains(got, "pages/index.gastro:8:17: error: unknown prop \"Tite\"") {
		t.Errorf("Error() should print file:line:col: severity: message lines; got:\n%s", got)
	}
	if _, statErr := os.Stat(outputDir); !os.IsNotExist(statErr) {
		t.Errorf("a failed compile must not create or touch the output directory (stat err: %v)", statErr)
	}
}

func TestCompile_FailedRunKeepsPreviousGeneration(t *testing.T) {
	projectDir := t.TempDir()
	files := threeBadKeysProject()
	files["pages/index.gastro"] = "---\nimport Card \"components/card.gastro\"\n---\n{{ Card (dict \"Title\" \"ok\") }}\n"
	delete(files, "pages/about.gastro")
	writeFiles(t, projectDir, files)
	outputDir := t.TempDir()

	if _, err := compiler.Compile(projectDir, outputDir, compiler.CompileOptions{}); err != nil {
		t.Fatalf("first compile: %v", err)
	}
	before := readTree(t, outputDir)

	writeFiles(t, projectDir, map[string]string{
		"pages/index.gastro": "---\nimport Card \"components/card.gastro\"\n---\n{{ Card (dict \"Titel\" \"ok\") }}\n",
		"pages/new.gastro":   "<p>new page</p>\n",
	})
	if _, err := compiler.Compile(projectDir, outputDir, compiler.CompileOptions{}); err == nil {
		t.Fatal("expected the second compile to fail on the bad key")
	}

	after := readTree(t, outputDir)
	if len(after) != len(before) {
		t.Fatalf("output tree changed: %d files before, %d after", len(before), len(after))
	}
	for path, data := range before {
		if string(after[path]) != string(data) {
			t.Errorf("%s changed after a failed compile", path)
		}
	}
}

func TestCompile_ReportsErrorsFromEveryFile(t *testing.T) {
	projectDir := t.TempDir()
	writeFiles(t, projectDir, map[string]string{
		"go.mod":                 "module example.com/m\n\ngo 1.27\n",
		"components/card.gastro": cardComponent,
		// Unknown component, positioned on line 2.
		"pages/a.gastro": "<main>\n  {{ Widget (dict) }}\n</main>\n",
		// Missing closing frontmatter delimiter.
		"pages/b.gastro": "---\nTitle := \"x\"\n<p>{{ .Title }}</p>\n",
		// Bare call to a component that requires props.
		"pages/c.gastro": "---\nimport Card \"components/card.gastro\"\n---\n<p>{{ Card }}</p>\n",
	})

	_, err := compiler.Compile(projectDir, t.TempDir(), compiler.CompileOptions{})

	var diagErr *compiler.DiagnosticsError
	if !errors.As(err, &diagErr) {
		t.Fatalf("expected *compiler.DiagnosticsError, got %T: %v", err, err)
	}
	got := map[string]compiler.Diagnostic{}
	for _, d := range diagErr.Diagnostics {
		got[d.File] = d
	}
	if d := got["pages/a.gastro"]; d.Line != 2 || d.Col != 6 || !strings.Contains(d.Message, `"Widget"`) {
		t.Errorf("pages/a.gastro: got %v, want 2:6 unknown component Widget", d)
	}
	if d := got["pages/b.gastro"]; !strings.Contains(d.Message, "missing closing ---") {
		t.Errorf("pages/b.gastro: got %v, want the missing delimiter error", d)
	}
	if d := got["pages/c.gastro"]; d.Line != 4 || d.Col != 7 || !strings.Contains(d.Message, "requires props") {
		t.Errorf("pages/c.gastro: got %v, want 4:7 requires props", d)
	}
}

func TestCompile_WarningsCarryPositionsAndDoNotFailNonStrict(t *testing.T) {
	projectDir := t.TempDir()
	writeFiles(t, projectDir, map[string]string{
		"go.mod": "module example.com/m\n\ngo 1.27\n",
		"pages/index.gastro": "---\n" + // 1
			"import \"net/http\"\n" + // 2
			"\n" + // 3
			"if r.URL.Query().Get(\"x\") != \"\" {\n" + // 4
			"\thttp.Error(w, \"bad\", http.StatusBadRequest)\n" + // 5
			"}\n" + // 6
			"Title := \"hi\"\n" + // 7
			"---\n" +
			"<h1>{{ .Title }}</h1>\n",
	})

	result, err := compiler.Compile(projectDir, t.TempDir(), compiler.CompileOptions{})
	if err != nil {
		t.Fatalf("warnings must not fail a non-strict compile: %v", err)
	}
	if len(result.Warnings) != 1 {
		t.Fatalf("expected exactly one missing-return warning, got %d: %v", len(result.Warnings), result.Warnings)
	}
	w := result.Warnings[0]
	if w.File != "pages/index.gastro" || w.Line != 5 || w.Col != 2 || w.Severity != codegen.SeverityWarning {
		t.Errorf("warning = %v, want pages/index.gastro:5:2 warning", w)
	}

	_, err = compiler.Compile(projectDir, t.TempDir(), compiler.CompileOptions{Strict: true})
	var diagErr *compiler.DiagnosticsError
	if !errors.As(err, &diagErr) || len(diagErr.Diagnostics) != 1 || !diagErr.Diagnostics[0].IsError() {
		t.Fatalf("strict compile should fail with the warning promoted to an error, got %v", err)
	}
}

func TestCompile_HeaderMutationIsNotAResponseWrite(t *testing.T) {
	projectDir := t.TempDir()
	writeFiles(t, projectDir, map[string]string{
		"go.mod": "module example.com/m\n\ngo 1.27\n",
		"pages/index.gastro": "---\n" +
			"w.Header().Set(\"Cache-Control\", \"no-store\")\n" +
			"w.Header().Add(\"Vary\", \"Accept\")\n" +
			"Title := \"hi\"\n" +
			"---\n" +
			"<h1>{{ .Title }}</h1>\n",
	})

	// Strict promotes any warning to an error.
	result, err := compiler.Compile(projectDir, t.TempDir(), compiler.CompileOptions{Strict: true})
	if err != nil {
		t.Fatalf("header mutations must not be reported as response writes: %v", err)
	}
	if len(result.Warnings) != 0 {
		t.Errorf("expected no warnings, got %v", result.Warnings)
	}
}
