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

	// testdata/basic/static/ exists — routes.go should use embedded FS serving
	assertStringContains(t, s, `GET /static/`)
	assertStringContains(t, s, `http.FileServerFS`)
	assertStringContains(t, s, `gastroRuntime.GetStaticFS`)
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

func TestCompile_GeneratesRenderFile(t *testing.T) {
	projectDir := filepath.Join("testdata", "basic")
	outputDir := t.TempDir()

	err := compiler.Compile(projectDir, outputDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(outputDir, "render.go"))
	if err != nil {
		t.Fatalf("render.go was not generated: %v", err)
	}
	s := string(content)

	// Should have the renderAPI struct and Render var
	assertStringContains(t, s, "var Render = &renderAPI{}")
	assertStringContains(t, s, "type renderAPI struct{}")

	// Should have a Render method for the Layout component (which has Props)
	assertStringContains(t, s, "func (r *renderAPI) Layout(props LayoutProps")

	// Layout uses {{ .Children }}, so it should have children parameter
	assertStringContains(t, s, "children ...template.HTML")
}

func TestCompile_RenderFileWithComposition(t *testing.T) {
	projectDir := filepath.Join("testdata", "composition")
	outputDir := t.TempDir()

	err := compiler.Compile(projectDir, outputDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(outputDir, "render.go"))
	if err != nil {
		t.Fatalf("render.go was not generated: %v", err)
	}
	s := string(content)

	// Should have Render methods for both Badge and Card
	assertStringContains(t, s, "func (r *renderAPI) Badge(props BadgeProps)")
	assertStringContains(t, s, "func (r *renderAPI) Card(props CardProps)")

	// Card calls componentCard internally
	assertStringContains(t, s, "componentCard(propsMap)")

	// Badge's props should be mapped
	assertStringContains(t, s, `"Label": props.Label`)
}

func TestCompile_ComponentComposition(t *testing.T) {
	projectDir := filepath.Join("testdata", "composition")
	outputDir := t.TempDir()

	err := compiler.Compile(projectDir, outputDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Component and page files should use the registry-based lookup.
	// FuncMap wiring is now centralised in routes.go, not per-file.
	for _, file := range []string{"components_card.go", "components_badge.go", "pages_index.go"} {
		content, err := os.ReadFile(filepath.Join(outputDir, file))
		if err != nil {
			t.Fatalf("reading %s: %v", file, err)
		}
		s := string(content)
		if contains(s, "func init()") {
			t.Errorf("%s should not have init() -- template wiring is in routes.go", file)
		}
	}

	// Card should use registry lookup for its own template.
	cardContent, _ := os.ReadFile(filepath.Join(outputDir, "components_card.go"))
	assertStringContains(t, string(cardContent), `__gastro_getTemplate("componentCard")`)

	// Page should use registry lookup for its own template.
	pageContent, _ := os.ReadFile(filepath.Join(outputDir, "pages_index.go"))
	assertStringContains(t, string(pageContent), `__gastro_getTemplate("pageIndex")`)
}

func TestCompile_RoutesContainsTemplateFuncMapWiring(t *testing.T) {
	projectDir := filepath.Join("testdata", "composition")
	outputDir := t.TempDir()

	err := compiler.Compile(projectDir, outputDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content, _ := os.ReadFile(filepath.Join(outputDir, "routes.go"))
	s := string(content)

	// Card template uses Badge -- routes.go should wire it up
	assertStringContains(t, s, `fm["Badge"] = componentBadge`)
	// Page template uses Card -- routes.go should wire it up
	assertStringContains(t, s, `fm["Card"] = componentCard`)
	// Both templates with uses should get render_children wiring
	assertStringContains(t, s, `__gastro_render_children`)
	assertStringContains(t, s, `ExecuteTemplate`)

	// Routes should parse templates from FS and populate the registry
	assertStringContains(t, s, `__gastro_templateRegistry`)
	assertStringContains(t, s, `__gastro_parseTemplate`)
	assertStringContains(t, s, `gastroRuntime.GetTemplateFS`)
}

func TestCompile_GeneratesEmbedFile(t *testing.T) {
	projectDir := filepath.Join("testdata", "basic")
	outputDir := t.TempDir()

	err := compiler.Compile(projectDir, outputDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(outputDir, "embed.go"))
	if err != nil {
		t.Fatalf("embed.go was not generated: %v", err)
	}
	s := string(content)

	assertStringContains(t, s, `//go:embed templates/*`)
	assertStringContains(t, s, `var templateFS embed.FS`)
}

func TestCompile_EmbedFileIncludesStaticWhenPresent(t *testing.T) {
	projectDir := filepath.Join("testdata", "basic")
	outputDir := t.TempDir()

	err := compiler.Compile(projectDir, outputDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content, _ := os.ReadFile(filepath.Join(outputDir, "embed.go"))
	s := string(content)

	// testdata/basic/static/ exists
	assertStringContains(t, s, `//go:embed static/*`)
	assertStringContains(t, s, `var staticAssetFS embed.FS`)
}

func TestCompile_EmbedFileOmitsStaticWhenAbsent(t *testing.T) {
	projectDir := t.TempDir()
	pagesDir := filepath.Join(projectDir, "pages")
	os.MkdirAll(pagesDir, 0o755)
	os.WriteFile(filepath.Join(pagesDir, "index.gastro"), []byte("---\nTitle := \"Hi\"\n---\n<h1>{{ .Title }}</h1>"), 0o644)

	outputDir := t.TempDir()

	err := compiler.Compile(projectDir, outputDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content, _ := os.ReadFile(filepath.Join(outputDir, "embed.go"))
	s := string(content)

	// Should have templates but NOT static
	assertStringContains(t, s, `//go:embed templates/*`)
	if contains(s, "staticAssetFS") {
		t.Error("embed.go should not contain staticAssetFS when no static directory exists")
	}
}

func TestCompile_CopiesStaticDir(t *testing.T) {
	projectDir := filepath.Join("testdata", "basic")
	outputDir := t.TempDir()

	err := compiler.Compile(projectDir, outputDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// .gastro/static/ should be a real directory (not a symlink) so //go:embed works
	staticDir := filepath.Join(outputDir, "static")
	info, err := os.Lstat(staticDir)
	if err != nil {
		t.Fatalf("static dir was not created: %v", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Error("static should be a real directory, not a symlink (Go embed doesn't follow directory symlinks)")
	}
	if !info.IsDir() {
		t.Error("expected static to be a directory")
	}

	// Files from static/ should be present in the copy
	entries, err := os.ReadDir(staticDir)
	if err != nil {
		t.Fatalf("reading static dir: %v", err)
	}
	if len(entries) == 0 {
		t.Error("static dir copy should contain files from the source static/ directory")
	}
}

func TestCompile_NoStaticDirWhenAbsent(t *testing.T) {
	projectDir := t.TempDir()
	pagesDir := filepath.Join(projectDir, "pages")
	os.MkdirAll(pagesDir, 0o755)
	os.WriteFile(filepath.Join(pagesDir, "index.gastro"), []byte("---\nTitle := \"Hi\"\n---\n<h1>{{ .Title }}</h1>"), 0o644)

	outputDir := t.TempDir()

	err := compiler.Compile(projectDir, outputDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	staticDir := filepath.Join(outputDir, "static")
	if _, err := os.Lstat(staticDir); err == nil {
		t.Error("static dir should not exist when project has no static/ directory")
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
