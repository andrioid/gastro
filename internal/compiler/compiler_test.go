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

func TestCompile_GeneratesStaticHandler(t *testing.T) {
	projectDir := filepath.Join("testdata", "basic")
	outputDir := t.TempDir()

	err := compiler.Compile(projectDir, outputDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content, _ := os.ReadFile(filepath.Join(outputDir, "routes.go"))
	s := string(content)

	// testdata/basic/static/ exists, so the static handler should be generated
	assertStringContains(t, s, `GET /static/`)
	assertStringContains(t, s, `http.FileServer`)
}

func TestCompile_OmitsStaticHandlerWhenNoDir(t *testing.T) {
	// Create a minimal project without a static/ directory
	projectDir := t.TempDir()
	pagesDir := filepath.Join(projectDir, "pages")
	os.MkdirAll(pagesDir, 0o755)
	os.WriteFile(filepath.Join(pagesDir, "index.gastro"), []byte("---\nTitle := \"Hi\"\n---\n<h1>{{ .Title }}</h1>"), 0o644)

	outputDir := t.TempDir()

	err := compiler.Compile(projectDir, outputDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content, _ := os.ReadFile(filepath.Join(outputDir, "routes.go"))
	s := string(content)

	// No static/ dir, so no static handler should be generated
	if contains(s, "/static/") {
		t.Error("routes.go should not contain /static/ when no static directory exists")
	}
}

func TestCompile_ComponentComposition(t *testing.T) {
	projectDir := filepath.Join("testdata", "composition")
	outputDir := t.TempDir()

	err := compiler.Compile(projectDir, outputDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The card component uses the badge component -- its generated code
	// should contain init() with FuncMap wiring for Badge.
	content, err := os.ReadFile(filepath.Join(outputDir, "components_card.go"))
	if err != nil {
		t.Fatalf("reading components_card.go: %v", err)
	}
	s := string(content)

	assertStringContains(t, s, `func init()`)
	assertStringContains(t, s, `__fm["__gastro_Badge"] = componentBadge`)
	assertStringContains(t, s, `__gastro_render_children`)

	// The badge component has no uses -- should NOT have init()
	badgeContent, err := os.ReadFile(filepath.Join(outputDir, "components_badge.go"))
	if err != nil {
		t.Fatalf("reading components_badge.go: %v", err)
	}
	badgeStr := string(badgeContent)

	if contains(badgeStr, "func init()") {
		t.Error("badge component should not have init() -- it has no uses")
	}

	// The page uses the card component -- should have init() with Card wiring
	pageContent, err := os.ReadFile(filepath.Join(outputDir, "pages_index.go"))
	if err != nil {
		t.Fatalf("reading pages_index.go: %v", err)
	}
	pageStr := string(pageContent)

	assertStringContains(t, pageStr, `func init()`)
	assertStringContains(t, pageStr, `__fm["__gastro_Card"] = componentCard`)
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
