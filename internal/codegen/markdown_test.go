package codegen_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andrioid/gastro/internal/codegen"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestProcessMarkdownDirectives_NoDirectives(t *testing.T) {
	body := `<h1>Hello</h1><p>No markdown here.</p>`
	got, deps, err := codegen.ProcessMarkdownDirectives(body, codegen.MarkdownContext{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != body {
		t.Errorf("body changed unexpectedly:\ngot:  %q\nwant: %q", got, body)
	}
	if len(deps) != 0 {
		t.Errorf("expected no deps, got %v", deps)
	}
}

func TestProcessMarkdownDirectives_RootRelative(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "content", "hello.md"), "# Hello\n\nworld.\n")

	body := `before {{ markdown "content/hello.md" }} after`
	got, deps, err := codegen.ProcessMarkdownDirectives(body, codegen.MarkdownContext{
		ProjectRoot: root,
		SourceDir:   filepath.Join(root, "pages"),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(got, "<h1") || !strings.Contains(got, "Hello") {
		t.Errorf("expected rendered HTML with <h1>Hello</h1>, got: %q", got)
	}
	if len(deps) != 1 {
		t.Fatalf("expected 1 dep, got %v", deps)
	}
	want := filepath.Join(root, "content", "hello.md")
	if deps[0] != want {
		t.Errorf("dep mismatch:\ngot:  %q\nwant: %q", deps[0], want)
	}
}

func TestProcessMarkdownDirectives_FileRelative(t *testing.T) {
	root := t.TempDir()
	pagesDir := filepath.Join(root, "pages", "docs")
	writeFile(t, filepath.Join(pagesDir, "intro.md"), "# Intro")

	body := `{{ markdown "./intro.md" }}`
	got, deps, err := codegen.ProcessMarkdownDirectives(body, codegen.MarkdownContext{
		ProjectRoot: root,
		SourceDir:   pagesDir,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(got, "Intro") {
		t.Errorf("expected 'Intro' in output, got: %q", got)
	}
	if len(deps) != 1 || deps[0] != filepath.Join(pagesDir, "intro.md") {
		t.Errorf("unexpected deps: %v", deps)
	}
}

func TestProcessMarkdownDirectives_ParentRelative(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "pages", "shared.md"), "# Shared")

	body := `{{ markdown "../shared.md" }}`
	got, _, err := codegen.ProcessMarkdownDirectives(body, codegen.MarkdownContext{
		ProjectRoot: root,
		SourceDir:   filepath.Join(root, "pages", "docs"),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(got, "Shared") {
		t.Errorf("expected 'Shared' in output, got: %q", got)
	}
}

func TestProcessMarkdownDirectives_EscapesTemplateDelimiters(t *testing.T) {
	root := t.TempDir()
	// Code fence containing literal {{ .Name }} which must not be re-parsed
	// as a template expression by html/template.
	md := "# Title\n\n```go\n{{ .Name }}\n```\n"
	writeFile(t, filepath.Join(root, "sample.md"), md)

	body := `{{ markdown "sample.md" }}`
	got, _, err := codegen.ProcessMarkdownDirectives(body, codegen.MarkdownContext{
		ProjectRoot: root,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The raw {{ should be escaped so html/template prints it literally.
	if strings.Contains(got, "{{ .Name }}") {
		t.Errorf("expected {{ .Name }} to be escaped, but it appears verbatim in output: %q", got)
	}
	if !strings.Contains(got, `{{ "{{" }}`) {
		t.Errorf("expected escaped {{ sequence in output, got: %q", got)
	}
}

func TestProcessMarkdownDirectives_AbsolutePathRejected(t *testing.T) {
	body := `{{ markdown "/etc/passwd" }}`
	_, _, err := codegen.ProcessMarkdownDirectives(body, codegen.MarkdownContext{
		ProjectRoot: t.TempDir(),
	})
	if err == nil {
		t.Fatal("expected error for absolute path, got nil")
	}
	if !strings.Contains(err.Error(), "absolute") {
		t.Errorf("error message should mention 'absolute', got: %v", err)
	}
}

func TestProcessMarkdownDirectives_AllowsEscapingProjectRoot(t *testing.T) {
	// Paths starting with ./ or ../ are resolved against SourceDir and may
	// reach files outside the project root (e.g. a shared project-wide docs
	// directory). This is a build-time directive running with the developer's
	// own permissions, so escaping the Gastro project root is allowed.
	top := t.TempDir()
	writeFile(t, filepath.Join(top, "docs", "shared.md"), "# Shared")

	body := `{{ markdown "../../../docs/shared.md" }}`
	got, _, err := codegen.ProcessMarkdownDirectives(body, codegen.MarkdownContext{
		ProjectRoot: filepath.Join(top, "site"),
		SourceDir:   filepath.Join(top, "site", "pages", "docs"),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(got, "Shared") {
		t.Errorf("expected 'Shared' in output, got: %q", got)
	}
}

func TestProcessMarkdownDirectives_MissingFile(t *testing.T) {
	root := t.TempDir()
	body := `{{ markdown "nope.md" }}`
	_, _, err := codegen.ProcessMarkdownDirectives(body, codegen.MarkdownContext{
		ProjectRoot: root,
	})
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

func TestProcessMarkdownDirectives_NonMdExtensionRejected(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "foo.txt"), "hi")
	body := `{{ markdown "foo.txt" }}`
	_, _, err := codegen.ProcessMarkdownDirectives(body, codegen.MarkdownContext{
		ProjectRoot: root,
	})
	if err == nil {
		t.Fatal("expected error for non-.md extension, got nil")
	}
}

func TestProcessMarkdownDirectives_MultipleDirectivesDedupedDeps(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a.md"), "A")
	writeFile(t, filepath.Join(root, "b.md"), "B")

	body := `{{ markdown "a.md" }} {{ markdown "b.md" }} {{ markdown "a.md" }}`
	got, deps, err := codegen.ProcessMarkdownDirectives(body, codegen.MarkdownContext{
		ProjectRoot: root,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(got, "A") || !strings.Contains(got, "B") {
		t.Errorf("expected both A and B in output, got: %q", got)
	}
	if len(deps) != 2 {
		t.Errorf("expected 2 unique deps, got %v", deps)
	}
}

func TestProcessMarkdownDirectives_SyntaxHighlightingClasses(t *testing.T) {
	root := t.TempDir()
	md := "```go\nfunc main() {}\n```\n"
	writeFile(t, filepath.Join(root, "code.md"), md)

	body := `{{ markdown "code.md" }}`
	got, _, err := codegen.ProcessMarkdownDirectives(body, codegen.MarkdownContext{
		ProjectRoot: root,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Chroma with classed output emits <pre class="chroma"> or similar.
	if !strings.Contains(got, "chroma") {
		t.Errorf("expected chroma-highlighted output to contain 'chroma' class, got: %q", got)
	}
}
