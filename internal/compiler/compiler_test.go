package compiler_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"testing"

	"github.com/andrioid/gastro/internal/compiler"
)

func TestCompile_ProducesOutputDirectory(t *testing.T) {
	projectDir := filepath.Join("testdata", "basic")
	outputDir := t.TempDir()

	_, err := compiler.Compile(projectDir, outputDir, compiler.CompileOptions{})
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

	_, err := compiler.Compile(projectDir, outputDir, compiler.CompileOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	routesFile := filepath.Join(outputDir, "routes.go")
	if _, err := os.Stat(routesFile); os.IsNotExist(err) {
		t.Fatal("routes.go was not generated")
	}

	content, _ := os.ReadFile(routesFile)
	s := string(content)

	// Track B: page patterns are method-less (frontmatter branches on r.Method).
	assertStringContains(t, s, `mux.HandleFunc("/{$}"`)
	assertStringContains(t, s, `mux.HandleFunc("/about"`)
}

func TestCompile_GeneratesPageHandlers(t *testing.T) {
	projectDir := filepath.Join("testdata", "basic")
	outputDir := t.TempDir()

	_, err := compiler.Compile(projectDir, outputDir, compiler.CompileOptions{})
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

	_, err := compiler.Compile(projectDir, outputDir, compiler.CompileOptions{})
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

	_, err := compiler.Compile(projectDir, outputDir, compiler.CompileOptions{})
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

	_, err := compiler.Compile(projectDir, outputDir, compiler.CompileOptions{})
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

	_, err := compiler.Compile(projectDir, outputDir, compiler.CompileOptions{})
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
	assertStringContains(t, s, "type renderAPI struct{ router *Router }")

	// Should have a Render method for the Layout component (which has Props)
	assertStringContains(t, s, "func (r *renderAPI) Layout(props LayoutProps")

	// Layout uses {{ .Children }}, so it should have children parameter
	assertStringContains(t, s, "children ...template.HTML")

	// Per-Router Render() should be exposed for race-free multi-router use.
	assertStringContains(t, s, "func (r *Router) Render() *renderAPI")
	assertStringContains(t, s, "return &renderAPI{router: r}")

	// Component methods should resolve through r.resolve(), not the global directly.
	assertStringContains(t, s, "rt := r.resolve()")
}

// TestCompile_RoutesUsesAtomicActiveRouter is a regression test for the
// audit's P0 #2: the package-level __gastro_active was a plain *Router,
// causing data races between concurrent New() calls and concurrent Render
// reads. It must be an atomic.Pointer[Router] now.
func TestCompile_RoutesUsesAtomicActiveRouter(t *testing.T) {
	projectDir := filepath.Join("testdata", "basic")
	outputDir := t.TempDir()

	_, err := compiler.Compile(projectDir, outputDir, compiler.CompileOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	routes, err := os.ReadFile(filepath.Join(outputDir, "routes.go"))
	if err != nil {
		t.Fatalf("reading routes.go: %v", err)
	}
	rs := string(routes)

	// Atomic declaration and store; no plain-pointer assignment.
	assertStringContains(t, rs, `"sync/atomic"`)
	assertStringContains(t, rs, "var __gastro_active atomic.Pointer[Router]")
	assertStringContains(t, rs, "__gastro_active.Store(__r)")
	if contains(rs, "__gastro_active = __r") {
		t.Error("routes.go must not contain plain-pointer assignment to __gastro_active (race-prone)")
	}

	// render.go must load the active router atomically when no Router is bound.
	render, err := os.ReadFile(filepath.Join(outputDir, "render.go"))
	if err != nil {
		t.Fatalf("reading render.go: %v", err)
	}
	assertStringContains(t, string(render), "__gastro_active.Load()")
	if contains(string(render), "if __gastro_active == nil") {
		t.Error("render.go must not compare __gastro_active to nil directly (race-prone); use Load()")
	}
}

func TestCompile_RenderFileWithComposition(t *testing.T) {
	projectDir := filepath.Join("testdata", "composition")
	outputDir := t.TempDir()

	_, err := compiler.Compile(projectDir, outputDir, compiler.CompileOptions{})
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

	_, err := compiler.Compile(projectDir, outputDir, compiler.CompileOptions{})
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

	_, err := compiler.Compile(projectDir, outputDir, compiler.CompileOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content, _ := os.ReadFile(filepath.Join(outputDir, "routes.go"))
	s := string(content)

	// Card template uses Badge -- routes.go should wire it up as a Router method value
	assertStringContains(t, s, `fm["Badge"] = __r.componentBadge`)
	// Page template uses Card -- routes.go should wire it up as a Router method value
	assertStringContains(t, s, `fm["Card"] = __r.componentCard`)
	// Both templates with uses should get render_children wiring
	assertStringContains(t, s, `__gastro_render_children`)
	assertStringContains(t, s, `ExecuteTemplate`)

	// Routes should parse templates from FS and populate the per-router registry
	assertStringContains(t, s, `__r.registry`)
	assertStringContains(t, s, `__gastro_parseTemplate`)
	assertStringContains(t, s, `gastroRuntime.GetTemplateFS`)
}

func TestCompile_GeneratesEmbedFile(t *testing.T) {
	projectDir := filepath.Join("testdata", "basic")
	outputDir := t.TempDir()

	_, err := compiler.Compile(projectDir, outputDir, compiler.CompileOptions{})
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

	_, err := compiler.Compile(projectDir, outputDir, compiler.CompileOptions{})
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

	_, err := compiler.Compile(projectDir, outputDir, compiler.CompileOptions{})
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

func TestCompile_EmbedFileOmitsStaticWhenEmpty(t *testing.T) {
	projectDir := t.TempDir()
	pagesDir := filepath.Join(projectDir, "pages")
	staticDir := filepath.Join(projectDir, "static")
	os.MkdirAll(pagesDir, 0o755)
	os.MkdirAll(staticDir, 0o755)
	os.WriteFile(filepath.Join(pagesDir, "index.gastro"), []byte("---\nTitle := \"Hi\"\n---\n<h1>{{ .Title }}</h1>"), 0o644)

	outputDir := t.TempDir()

	_, err := compiler.Compile(projectDir, outputDir, compiler.CompileOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content, _ := os.ReadFile(filepath.Join(outputDir, "embed.go"))
	s := string(content)

	if contains(s, "staticAssetFS") {
		t.Error("embed.go should not contain staticAssetFS when static directory is empty")
	}
}

func TestCompile_EmbedFileOmitsStaticWhenOnlyDotfiles(t *testing.T) {
	projectDir := t.TempDir()
	pagesDir := filepath.Join(projectDir, "pages")
	staticDir := filepath.Join(projectDir, "static")
	os.MkdirAll(pagesDir, 0o755)
	os.MkdirAll(staticDir, 0o755)
	os.WriteFile(filepath.Join(staticDir, ".gitkeep"), []byte(""), 0o644)
	os.WriteFile(filepath.Join(pagesDir, "index.gastro"), []byte("---\nTitle := \"Hi\"\n---\n<h1>{{ .Title }}</h1>"), 0o644)

	outputDir := t.TempDir()

	_, err := compiler.Compile(projectDir, outputDir, compiler.CompileOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content, _ := os.ReadFile(filepath.Join(outputDir, "embed.go"))
	s := string(content)

	if contains(s, "staticAssetFS") {
		t.Error("embed.go should not contain staticAssetFS when static directory has only dotfiles")
	}
}

func TestCompile_CopiesStaticDir(t *testing.T) {
	projectDir := filepath.Join("testdata", "basic")
	outputDir := t.TempDir()

	_, err := compiler.Compile(projectDir, outputDir, compiler.CompileOptions{})
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

	_, err := compiler.Compile(projectDir, outputDir, compiler.CompileOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	staticDir := filepath.Join(outputDir, "static")
	if _, err := os.Lstat(staticDir); err == nil {
		t.Error("static dir should not exist when project has no static/ directory")
	}
}

func TestCompile_ComponentWithoutFrontmatter(t *testing.T) {
	projectDir := filepath.Join("testdata", "basic")
	outputDir := t.TempDir()

	_, err := compiler.Compile(projectDir, outputDir, compiler.CompileOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should generate the component Go file
	goContent, err := os.ReadFile(filepath.Join(outputDir, "components_divider.go"))
	if err != nil {
		t.Fatalf("components_divider.go was not generated: %v", err)
	}
	s := string(goContent)

	// Should use the component template (returns template.HTML, not an HTTP handler).
	// Components are now methods on *Router so they can read the per-router
	// template registry.
	assertStringContains(t, s, "func (__router *Router) componentDivider(propsMap map[string]any) template.HTML")

	// Should NOT have MapToStruct (no Props struct)
	if contains(s, "MapToStruct") {
		t.Error("component without props should not call MapToStruct")
	}

	// Should NOT have http.ResponseWriter (it's a component, not a page)
	if contains(s, "http.ResponseWriter") {
		t.Error("component without frontmatter should not be an HTTP handler")
	}

	// Should generate the template file
	if _, err := os.Stat(filepath.Join(outputDir, "templates", "components_divider.html")); os.IsNotExist(err) {
		t.Fatal("template file was not generated for frontmatter-less component")
	}

	// Should appear in render.go
	renderContent, err := os.ReadFile(filepath.Join(outputDir, "render.go"))
	if err != nil {
		t.Fatalf("render.go was not generated: %v", err)
	}
	rs := string(renderContent)

	// Prop-less component should have a no-argument Render method
	assertStringContains(t, rs, "func (r *renderAPI) Divider()")
	assertStringContains(t, rs, "componentDivider(propsMap)")
}

func TestCompile_PageWithoutFrontmatter(t *testing.T) {
	projectDir := t.TempDir()
	pagesDir := filepath.Join(projectDir, "pages")
	os.MkdirAll(pagesDir, 0o755)
	os.WriteFile(filepath.Join(pagesDir, "index.gastro"), []byte("<h1>Hello World</h1>"), 0o644)

	outputDir := t.TempDir()

	_, err := compiler.Compile(projectDir, outputDir, compiler.CompileOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should generate the page handler file
	goContent, err := os.ReadFile(filepath.Join(outputDir, "pages_index.go"))
	if err != nil {
		t.Fatalf("pages_index.go was not generated: %v", err)
	}
	s := string(goContent)

	// Should be an HTTP handler (page), not a component.
	// Pages are now methods on *Router so they can read the per-router state.
	assertStringContains(t, s, "func (__router *Router) pageIndex(w http.ResponseWriter, r *http.Request)")

	// Should generate the template file
	if _, err := os.Stat(filepath.Join(outputDir, "templates", "pages_index.html")); os.IsNotExist(err) {
		t.Fatal("template file was not generated for frontmatter-less page")
	}
}

// TestCompile_ComponentWithInlineFieldComments is a regression test for the
// audit's P0 #1: inline comments on Props struct fields containing `{`, `}`,
// or backticks previously broke the codegen's line-based brace counter,
// producing invalid Go. The AST-based hoister handles them correctly.
func TestCompile_ComponentWithInlineFieldComments(t *testing.T) {
	projectDir := t.TempDir()
	compDir := filepath.Join(projectDir, "components")
	pagesDir := filepath.Join(projectDir, "pages")
	os.MkdirAll(compDir, 0o755)
	os.MkdirAll(pagesDir, 0o755)

	// Field comments containing every previously-fatal character class:
	// unbalanced `{`, unbalanced `}`, single backtick, full struct tag.
	cardSrc := "---\n" +
		"type Props struct {\n" +
		"\tID    string `json:\"id,omitempty\"` // ulid format\n" +
		"\tTitle string                       // task title with `code` markers\n" +
		"\tNote  string                       // contains { unbalanced open\n" +
		"\tEnd   string                       // contains } unbalanced close\n" +
		"}\n\n" +
		"p := gastro.Props()\n" +
		"ID := p.ID\n" +
		"Title := p.Title\n" +
		"Note := p.Note\n" +
		"End := p.End\n" +
		"---\n" +
		`<div id="{{ .ID }}"><h3>{{ .Title }}</h3><p>{{ .Note }}{{ .End }}</p></div>` + "\n"
	os.WriteFile(filepath.Join(compDir, "card.gastro"), []byte(cardSrc), 0o644)

	os.WriteFile(filepath.Join(pagesDir, "index.gastro"), []byte("---\n"+
		"import Card \"components/card.gastro\"\n"+
		"ctx := gastro.Context()\n_ = ctx\n---\n"+
		`<html><body>{{ Card (dict "ID" "01K" "Title" "Hi" "Note" "n" "End" "e") }}</body></html>`), 0o644)

	outputDir := t.TempDir()
	_, err := compiler.Compile(projectDir, outputDir, compiler.CompileOptions{})
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	// Generated component must define the struct with all four fields and
	// preserve the assignment statements that follow it.
	goSrc, err := os.ReadFile(filepath.Join(outputDir, "components_card.go"))
	if err != nil {
		t.Fatalf("reading generated component: %v", err)
	}
	s := string(goSrc)

	for _, want := range []string{
		"type componentCardProps struct",
		"ID    string",
		"Title string",
		"Note  string",
		"End   string",
		"// ulid format",
		"// task title with `code` markers",
		"// contains { unbalanced open",
		"// contains } unbalanced close",
		"p := __props",
		"ID := p.ID",
		"Note := p.Note",
		"End := p.End",
	} {
		assertStringContains(t, s, want)
	}

	// The struct itself must not appear inside the function body.
	funcStart := indexOf(s, "func (__router *Router) componentCard(")
	if funcStart < 0 {
		t.Fatalf("componentCard func not found in:\n%s", s)
	}
	if contains(s[funcStart:], "type componentCardProps struct") {
		t.Error("struct must be hoisted above the function body, not duplicated inside")
	}

	// Sanity-check: the generated package must compile under go/parser.
	// Catches the original failure mode where stripped-but-not-hoisted
	// fields would leave dangling identifiers in the function body.
	if _, err := goParse(s); err != nil {
		t.Fatalf("generated component does not parse as Go:\n%v\n\n%s", err, s)
	}
}

func goParse(src string) (any, error) {
	return parser.ParseFile(token.NewFileSet(), "generated.go", src, parser.AllErrors)
}

// TestCompile_DictKeyTypoSurfacesWarning is the audit P0 #3 reproducer
// turned into a regression test. A page imports a Card component whose
// Props has fields {Title, Body}, but invokes it with `dict "Tite" ...`.
// Before this validator the compile silently produced HTML with an empty
// Card (the typo'd key was dropped by MapToStruct at render time). Now
// the compile yields a FileWarning that names the typo'd key, the
// component, and the valid prop names.
func TestCompile_DictKeyTypoSurfacesWarning(t *testing.T) {
	projectDir := t.TempDir()
	compDir := filepath.Join(projectDir, "components")
	pagesDir := filepath.Join(projectDir, "pages")
	os.MkdirAll(compDir, 0o755)
	os.MkdirAll(pagesDir, 0o755)

	os.WriteFile(filepath.Join(compDir, "card.gastro"), []byte(
		"---\n"+
			"type Props struct {\n"+
			"\tTitle string\n"+
			"\tBody  string\n"+
			"}\n\n"+
			"p := gastro.Props()\n"+
			"Title := p.Title\n"+
			"Body := p.Body\n"+
			"---\n"+
			`<div><h3>{{ .Title }}</h3><p>{{ .Body }}</p></div>`+"\n"), 0o644)

	// Track B (page model v2): pages no longer call gastro.Context();
	// the ambient (w, r) is enough. Frontmatter can be empty.
	os.WriteFile(filepath.Join(pagesDir, "index.gastro"), []byte(
		"---\n"+
			`import Card "components/card.gastro"`+"\n"+
			"---\n"+
			`<html><body>{{ Card (dict "Tite" "Hi" "Body" "Hello") }}</body></html>`+"\n"), 0o644)

	outputDir := t.TempDir()
	result, err := compiler.Compile(projectDir, outputDir, compiler.CompileOptions{})
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	// Find the dict-typo warning amongst all warnings.
	var found *compiler.FileWarning
	for i := range result.Warnings {
		w := result.Warnings[i]
		if contains(w.Message, `unknown prop "Tite"`) {
			found = &result.Warnings[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("expected an `unknown prop` warning for the typo `Tite`; got: %v", result.Warnings)
	}

	// Warning must reference the page that contains the call site, not
	// the component being called.
	if found.File != "pages/index.gastro" {
		t.Errorf("warning attributed to wrong file: %q", found.File)
	}

	// Strict mode promotes the warning to an error. The strict compile
	// surfaces the *first* warning as an error; the dict-key warning
	// might be queued behind unrelated warnings, so check that any
	// strict error fires AND that the dict typo is among the warnings
	// from the non-strict run above.
	_, err = compiler.Compile(projectDir, t.TempDir(), compiler.CompileOptions{Strict: true})
	if err == nil {
		t.Error("expected strict compile to fail, but it succeeded")
	}
}

// TestCompile_DictKeyValidationDoesNotFalsePositive guards against the
// dict-key validator flagging legitimate calls. Covers the cases that
// the validator must explicitly skip: __children injected by wrap, dynamic
// dict keys, bare-call form, and components that have no Props schema.
func TestCompile_DictKeyValidationDoesNotFalsePositive(t *testing.T) {
	projectDir := t.TempDir()
	compDir := filepath.Join(projectDir, "components")
	pagesDir := filepath.Join(projectDir, "pages")
	os.MkdirAll(compDir, 0o755)
	os.MkdirAll(pagesDir, 0o755)

	// Component with a Props struct (Card) and one without (Divider).
	os.WriteFile(filepath.Join(compDir, "card.gastro"), []byte(
		"---\n"+
			"type Props struct {\n\tTitle string\n}\n\n"+
			"p := gastro.Props()\nTitle := p.Title\n---\n"+
			`<div>{{ .Title }}</div>`+"\n"), 0o644)
	os.WriteFile(filepath.Join(compDir, "divider.gastro"), []byte(`<hr/>`), 0o644)

	// Layout component that takes children — wrap injects __children at
	// compile time, validator must not flag it.
	os.WriteFile(filepath.Join(compDir, "layout.gastro"), []byte(
		"---\n"+
			"type Props struct {\n\tTitle string\n}\n\n"+
			"p := gastro.Props()\nTitle := p.Title\n---\n"+
			`<main>{{ .Children }}</main>`+"\n"), 0o644)

	// Page exercises every "don't false-positive" branch:
	//  - Wrap form (validator sees the post-transform body with __children).
	//  - Bare call without dict.
	//  - Dynamic dict key (.K resolves at render time).
	//  - Component with no Props (Divider) called with a dict.
	os.WriteFile(filepath.Join(pagesDir, "index.gastro"), []byte(
		"---\n"+
			"import (\n"+
			"\tCard \"components/card.gastro\"\n"+
			"\tDivider \"components/divider.gastro\"\n"+
			"\tLayout \"components/layout.gastro\"\n"+
			")\n"+
			"ctx := gastro.Context()\nK := \"Title\"\n_ = ctx\n_ = K\n---\n"+
			"{{ wrap Layout (dict \"Title\" \"Hi\") }}\n"+
			"  {{ Divider }}\n"+
			"  {{ Card (dict .K \"Some\") }}\n"+
			"{{ end }}\n"), 0o644)

	outputDir := t.TempDir()
	result, err := compiler.Compile(projectDir, outputDir, compiler.CompileOptions{})
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	for _, w := range result.Warnings {
		if contains(w.Message, "unknown prop") {
			t.Errorf("unexpected dict-key warning: %s:%d %s", w.File, w.Line, w.Message)
		}
	}
}

// TestCompile_PagesOptional verifies that a project with only a components/
// directory (no pages/) compiles without error. This is the "headless" use-case
// where the host project mounts gastro solely for its component rendering and
// embedded static assets, not for file-based page routing.
func TestCompile_PagesOptional(t *testing.T) {
	projectDir := t.TempDir()

	// Only components/ — no pages/ directory at all.
	if err := os.MkdirAll(filepath.Join(projectDir, "components"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(projectDir, "components", "badge.gastro"),
		[]byte("---\ntype Props struct { Label string }\np := gastro.Props()\nLabel := p.Label\n---\n<span>{{.Label}}</span>\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}

	outputDir := t.TempDir()
	result, err := compiler.Compile(projectDir, outputDir, compiler.CompileOptions{})
	if err != nil {
		t.Fatalf("expected no error for components-only project, got: %v", err)
	}
	if result.PageCount != 0 {
		t.Errorf("expected 0 pages, got %d", result.PageCount)
	}
	if result.ComponentCount != 1 {
		t.Errorf("expected 1 component, got %d", result.ComponentCount)
	}
}

// TestCompile_CountsComponentsAndPages verifies the CompileResult carries
// accurate component and page counts.
func TestCompile_CountsComponentsAndPages(t *testing.T) {
	projectDir := filepath.Join("testdata", "basic")
	outputDir := t.TempDir()

	result, err := compiler.Compile(projectDir, outputDir, compiler.CompileOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// testdata/basic has 2 pages and 2 components.
	if result.PageCount != 2 {
		t.Errorf("expected 2 pages, got %d", result.PageCount)
	}
	if result.ComponentCount != 2 {
		t.Errorf("expected 2 components, got %d", result.ComponentCount)
	}
}

func indexOf(s, substr string) int {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

// TestCompile_ComponentNameCollisionWarning verifies that two component
// files producing the same ExportedName emit a warning (and a strict-mode
// error). This is Wave 1 / A7 from plans/frictions-plan.md.
func TestCompile_ComponentNameCollisionWarning(t *testing.T) {
	projectDir := filepath.Join("testdata", "collision")
	outputDir := t.TempDir()

	// Non-strict mode: should succeed with a warning.
	result, err := compiler.Compile(projectDir, outputDir, compiler.CompileOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	found := false
	for _, w := range result.Warnings {
		if contains(w.Message, "component name collision") && contains(w.Message, "PostCard") {
			found = true
			if w.File == "" {
				t.Error("collision warning should include the file path")
			}
		}
	}
	if !found {
		t.Errorf("expected a component name collision warning for PostCard; got warnings: %+v", result.Warnings)
	}
}

// TestCompile_ComponentNameCollisionStrictError verifies that strict mode
// promotes a component name collision warning to an error AND that the
// error fires before any per-file Go output is written (which would
// otherwise overwrite itself due to the same goFileName collision).
func TestCompile_ComponentNameCollisionStrictError(t *testing.T) {
	projectDir := filepath.Join("testdata", "collision")
	outputDir := t.TempDir()

	_, err := compiler.Compile(projectDir, outputDir, compiler.CompileOptions{Strict: true})
	if err == nil {
		t.Fatal("expected strict mode to error on component name collision")
	}
	if !contains(err.Error(), "component name collision") {
		t.Errorf("error should mention 'component name collision'; got: %v", err)
	}

	// The collision check runs in the pre-pass, before any component
	// Go file is written. Verify no clobbered components_post_card.go
	// landed in the output dir.
	if _, statErr := os.Stat(filepath.Join(outputDir, "components_post_card.go")); statErr == nil {
		t.Error("strict-mode collision should fail before per-file Go output is written; found components_post_card.go on disk")
	}
}

// TestCompile_NoCollisionWhenNamesDiffer verifies that distinct component
// names produce no collision warnings.
func TestCompile_NoCollisionWhenNamesDiffer(t *testing.T) {
	projectDir := t.TempDir()
	compDir := filepath.Join(projectDir, "components")
	if err := os.MkdirAll(compDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Two components with different exported names: Card and Header.
	os.WriteFile(filepath.Join(compDir, "card.gastro"),
		[]byte("---\ntype Props struct { Title string }\np := gastro.Props()\nTitle := p.Title\n---\n<div>{{ .Title }}</div>\n"), 0o644)
	os.WriteFile(filepath.Join(compDir, "header.gastro"),
		[]byte("---\ntype Props struct { Title string }\np := gastro.Props()\nTitle := p.Title\n---\n<header>{{ .Title }}</header>\n"), 0o644)

	outputDir := t.TempDir()
	result, err := compiler.Compile(projectDir, outputDir, compiler.CompileOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, w := range result.Warnings {
		if contains(w.Message, "collision") {
			t.Errorf("unexpected collision warning: %s", w.Message)
		}
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
