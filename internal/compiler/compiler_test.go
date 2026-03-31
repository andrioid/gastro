package compiler_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/andrioid/gastro/internal/compiler"
)

func TestCompile_ProducesOutputDirectory(t *testing.T) {
	projectDir := filepath.Join("testdata", "basic")
	outputDir := t.TempDir()

	err := compiler.Compile(projectDir, outputDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should create the output directory
	if _, err := os.Stat(outputDir); os.IsNotExist(err) {
		t.Fatal("output directory was not created")
	}
}

func TestCompile_GeneratesRouteFile(t *testing.T) {
	projectDir := filepath.Join("testdata", "basic")
	outputDir := t.TempDir()

	err := compiler.Compile(projectDir, outputDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	routesFile := filepath.Join(outputDir, "routes.go")
	if _, err := os.Stat(routesFile); os.IsNotExist(err) {
		t.Fatal("routes.go was not generated")
	}

	content, _ := os.ReadFile(routesFile)
	s := string(content)

	// Should contain route registrations
	assertStringContains(t, s, "GET /")
	assertStringContains(t, s, "GET /about")
}

func TestCompile_GeneratesPageHandlers(t *testing.T) {
	projectDir := filepath.Join("testdata", "basic")
	outputDir := t.TempDir()

	err := compiler.Compile(projectDir, outputDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should generate handler files (flat in output dir, named pages_*.go)
	files, _ := filepath.Glob(filepath.Join(outputDir, "pages_*.go"))
	if len(files) == 0 {
		t.Fatal("no page handler files were generated")
	}
}

func TestCompile_GeneratesTemplateFiles(t *testing.T) {
	projectDir := filepath.Join("testdata", "basic")
	outputDir := t.TempDir()

	err := compiler.Compile(projectDir, outputDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should generate template files
	files, _ := filepath.Glob(filepath.Join(outputDir, "templates", "*.html"))
	if len(files) == 0 {
		t.Fatal("no template files were generated")
	}
}

func assertStringContains(t *testing.T, s, substr string) {
	t.Helper()
	if !contains(s, substr) {
		t.Errorf("expected string to contain %q, but it didn't.\nstring:\n%s", substr, s)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsStr(s, substr))
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
