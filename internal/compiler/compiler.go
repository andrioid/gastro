package compiler

import (
	"bytes"
	"fmt"
	goformat "go/format"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/template"

	"github.com/andrioid/gastro/internal/codegen"
	"github.com/andrioid/gastro/internal/parser"
	"github.com/andrioid/gastro/internal/router"
)

// CompileOptions configures compilation behavior.
type CompileOptions struct {
	Strict bool // Treat warnings as errors (production builds)
}

// FileWarning represents a warning from a specific file during compilation.
type FileWarning struct {
	File    string
	Line    int
	Message string
}

// CompileResult contains the output of a successful compilation.
type CompileResult struct {
	Warnings []FileWarning
	// EmbedDeps lists absolute paths to every external file referenced by a
	// //gastro:embed directive in frontmatter during this compile. Useful
	// for the dev watcher so changes to these files can trigger a
	// regenerate.
	EmbedDeps []string
	// ComponentCount is the number of component files compiled.
	ComponentCount int
	// PageCount is the number of page files compiled.
	PageCount int
}

// Compile reads all .gastro files from a project directory, processes them
// through the parser and code generator, and writes the output to outputDir.
func Compile(projectDir, outputDir string, opts CompileOptions) (*CompileResult, error) {
	// Ensure output subdirectories exist
	for _, sub := range []string{"pages", "components", "templates"} {
		if err := os.MkdirAll(filepath.Join(outputDir, sub), 0o755); err != nil {
			return nil, fmt.Errorf("creating output directory: %w", err)
		}
	}

	// Discover all .gastro files
	pageFiles, err := discoverFiles(filepath.Join(projectDir, "pages"), "pages")
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("discovering pages: %w", err)
	}

	componentFiles, err := discoverFiles(filepath.Join(projectDir, "components"), "components")
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("discovering components: %w", err)
	}

	// Process each file, collecting metadata for render.go and routes.go
	allFiles := append(pageFiles, componentFiles...)
	var components []componentMeta
	var templates []templateMeta
	var allWarnings []FileWarning
	var allEmbedDeps []string
	absProjectDir, err := filepath.Abs(projectDir)
	if err != nil {
		return nil, fmt.Errorf("resolving project dir: %w", err)
	}

	// Pre-pass: gather every component's Props field list so the per-file
	// pass can validate (dict ...) keys against the destination schema.
	// Cheap (parse + frontmatter analyze + struct hoist; no template work).
	propsByPath, err := gatherComponentSchemas(componentFiles, projectDir)
	if err != nil {
		return nil, fmt.Errorf("gathering component schemas: %w", err)
	}

	// Pre-pass: detect component name collisions before any per-file work
	// writes Go files to disk. Two component files that produce the same
	// ExportedName (e.g. components/post-card.gastro and
	// components/post/card.gastro both producing "PostCard") would yield
	// duplicate type and method names in render.go and overwrite each
	// other's per-file Go output. Catch it here with a clear message
	// before either failure mode triggers.
	for _, w := range findComponentNameCollisions(componentFiles) {
		allWarnings = append(allWarnings, w)
		if opts.Strict {
			return nil, fmt.Errorf("compiling %s: %s", w.File, w.Message)
		}
	}

	// Pre-pass: detect page-id collisions across pages and components.
	// DerivePageID is intentionally lossy (collapses non-ident chars to
	// underscores) so two visually-distinct paths can sanitise to the
	// same id. With mangled hoisted decls, that would yield duplicate
	// package-level symbols. Catch it here before codegen runs.
	if collisions := findPageIDCollisions(pageFiles, componentFiles); len(collisions) > 0 {
		for _, w := range collisions {
			allWarnings = append(allWarnings, w)
			if opts.Strict {
				return nil, fmt.Errorf("compiling %s: %s", w.File, w.Message)
			}
		}
	}

	for _, relPath := range allFiles {
		absPath := filepath.Join(projectDir, relPath)
		result, err := compileFile(absPath, relPath, absProjectDir, outputDir, propsByPath)
		if err != nil {
			return nil, fmt.Errorf("compiling %s: %w", relPath, err)
		}

		// Collect warnings from frontmatter analysis
		for _, w := range result.warnings {
			if opts.Strict {
				return nil, fmt.Errorf("compiling %s: %s", relPath, w.Message)
			}
			allWarnings = append(allWarnings, FileWarning{
				File:    relPath,
				Line:    w.Line,
				Message: w.Message,
			})
		}

		templates = append(templates, result.template)
		if result.component != nil {
			components = append(components, *result.component)
		}
		allEmbedDeps = append(allEmbedDeps, result.embedDeps...)
	}

	// Detect static asset directory (ignore dotfiles like .gitkeep, .DS_Store)
	hasStatic := false
	if info, err := os.Stat(filepath.Join(projectDir, "static")); err == nil && info.IsDir() {
		entries, err := os.ReadDir(filepath.Join(projectDir, "static"))
		if err == nil {
			for _, entry := range entries {
				if !strings.HasPrefix(entry.Name(), ".") {
					hasStatic = true
					break
				}
			}
		}
	}

	// Copy static/ into .gastro/ so //go:embed can find it.
	// Go's //go:embed does not follow symlinks to directories, so we copy.
	if hasStatic {
		if err := syncStatic(projectDir, outputDir); err != nil {
			return nil, fmt.Errorf("syncing static: %w", err)
		}
	}

	// Generate embed.go with //go:embed directives
	if err := generateEmbedFile(hasStatic, outputDir); err != nil {
		return nil, fmt.Errorf("generating embed: %w", err)
	}

	// Generate routes file
	routes := router.BuildRoutes(pageFiles)
	if err := generateRoutesFile(routes, templates, hasStatic, outputDir); err != nil {
		return nil, fmt.Errorf("generating routes: %w", err)
	}

	// Generate render file for component rendering API
	if len(components) > 0 {
		if err := generateRenderFile(components, outputDir); err != nil {
			return nil, fmt.Errorf("generating render: %w", err)
		}
	}

	// Count pages vs components.
	var pageCount, componentCount int
	for _, r := range allFiles {
		if strings.HasPrefix(r, "components/") {
			componentCount++
		} else if strings.HasPrefix(r, "pages/") {
			pageCount++
		}
	}

	return &CompileResult{
		Warnings:       allWarnings,
		EmbedDeps:      dedupeStrings(allEmbedDeps),
		ComponentCount: componentCount,
		PageCount:      pageCount,
	}, nil
}

// discoverFiles walks a directory and returns relative paths of all .gastro files.
func discoverFiles(dir, prefix string) ([]string, error) {
	var files []string

	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".gastro") {
			return nil
		}

		rel, err := filepath.Rel(filepath.Dir(dir), path)
		if err != nil {
			return err
		}
		files = append(files, rel)
		return nil
	})

	return files, err
}

// findPageIDCollisions detects two .gastro files (same kind: both
// pages or both components) whose paths sanitise to the same id via
// DerivePageID. With MangleHoisted=true, hoisted decls from such files
// would produce duplicate package-level symbols. Reports each later
// occurrence as a warning whose message references the first colliding
// path; the first occurrence is left alone.
func findPageIDCollisions(pageFiles, componentFiles []string) []FileWarning {
	var warnings []FileWarning
	for _, group := range []struct {
		kind  string
		files []string
	}{
		{"page", pageFiles},
		{"component", componentFiles},
	} {
		if len(group.files) < 2 {
			continue
		}
		seen := make(map[string]string, len(group.files)) // id -> first relPath
		for _, relPath := range group.files {
			id := codegen.DerivePageID(relPath)
			if first, exists := seen[id]; exists {
				warnings = append(warnings, FileWarning{
					File: relPath,
					Line: 1,
					Message: fmt.Sprintf(
						"%s path sanitises to id %q which collides with %q; rename one of the files so their paths produce distinct ids",
						group.kind, id, first,
					),
				})
				continue
			}
			seen[id] = relPath
		}
	}
	return warnings
}

// findComponentNameCollisions scans the list of component file paths and
// returns a warning for each path that produces the same ExportedName as
// an earlier path in the list. The first occurrence is left alone; every
// subsequent occurrence with the same name produces a warning.
//
// Derived purely from path strings (no file I/O), so this can run in the
// pre-pass before any per-file work writes Go output. The mapping is the
// same one the codegen uses: HandlerFuncName(path) -> ExportedComponentName.
func findComponentNameCollisions(componentFiles []string) []FileWarning {
	if len(componentFiles) < 2 {
		return nil
	}
	seen := make(map[string]string, len(componentFiles)) // ExportedName -> relPath
	var warnings []FileWarning
	for _, relPath := range componentFiles {
		exported := codegen.ExportedComponentName(codegen.HandlerFuncName(relPath))
		if first, ok := seen[exported]; ok {
			warnings = append(warnings, FileWarning{
				File: relPath,
				Line: 0,
				Message: fmt.Sprintf(
					"component name collision: %q and %q both produce the exported name %q; rename one of the files to avoid duplicate type and function names in generated code (and per-file .go output overwriting itself)",
					first, relPath, exported,
				),
			})
		} else {
			seen[exported] = relPath
		}
	}
	return warnings
}

// templateMeta holds per-template metadata needed by routes.go to wire up
// FuncMaps and initialise the template registry.
type templateMeta struct {
	FuncName     string            // e.g. "pageIndex"
	TemplateFile string            // e.g. "pages_index.html"
	Uses         []codegen.UseInfo // component functions this template calls
}

// componentMeta holds metadata about a component for render.go generation.
type componentMeta struct {
	ExportedName  string // e.g. "PostCard"
	FuncName      string // e.g. "componentPostCard"
	HasProps      bool
	PropsTypeName string // e.g. "componentPostCardProps"
	PropsFields   []codegen.StructField
	HasChildren   bool
}

// compileResult is returned by compileFile. It always contains template
// metadata and optionally component metadata (nil for pages).
type compileResult struct {
	template   templateMeta
	component  *componentMeta
	warnings   []codegen.Warning
	embedDeps  []string
}

// gatherComponentSchemas reads each component file's frontmatter, extracts
// its hoisted Props type, and returns a map keyed by relative path (the
// same path that appears in `import X "components/foo.gastro"`). Files
// without a Props struct are omitted; readers are expected to treat a
// missing key as "no schema, skip validation".
//
// Delegates to codegen.ScanComponents (the canonical project-walker)
// and then filters down to the subset the compiler discovered via
// discoverFiles. The filter is necessary because discoverFiles and
// codegen.ScanComponents have slightly different walk policies (the
// former includes hidden directories, the latter skips them); the
// per-file compile pass uses discoverFiles' list, so the schema map
// keyed by that list keeps everything aligned.
func gatherComponentSchemas(componentFiles []string, projectDir string) (map[string][]codegen.StructField, error) {
	schemas, err := codegen.ScanComponents(projectDir)
	if err != nil {
		return nil, fmt.Errorf("scanning components: %w", err)
	}
	discovered := make(map[string]bool, len(componentFiles))
	for _, p := range componentFiles {
		discovered[p] = true
	}
	out := make(map[string][]codegen.StructField, len(schemas))
	for _, s := range schemas {
		if !discovered[s.RelPath] {
			continue
		}
		if len(s.PropsFields) == 0 {
			continue
		}
		out[s.RelPath] = s.PropsFields
	}
	return out, nil
}

func compileFile(absPath, relPath, absProjectDir, outputDir string, propsByPath map[string][]codegen.StructField) (compileResult, error) {
	content, err := os.ReadFile(absPath)
	if err != nil {
		return compileResult{}, err
	}

	// Parse
	file, err := parser.Parse(relPath, string(content))
	if err != nil {
		return compileResult{}, err
	}

	// Expand //gastro:embed directives in the frontmatter before any
	// downstream pass touches it. The pass replaces each directive's
	// uninitialized var with `var X = "<baked>"`; the var-hoister then
	// lifts that decl to package scope. See internal/codegen/embed.go
	// for the contract.
	moduleRoot := codegen.FindModuleRootForFile(absPath)
	if moduleRoot == "" {
		// Fall back to the project directory. //gastro:embed will still
		// reject anything outside this fallback root.
		moduleRoot = absProjectDir
	}
	rewrittenFM, embedDeps, err := codegen.ProcessEmbedDirectives(file.Frontmatter, codegen.EmbedContext{
		SourceFile: absPath,
		ModuleRoot: moduleRoot,
	})
	if err != nil {
		return compileResult{}, err
	}
	file.Frontmatter = rewrittenFM

	// Analyze frontmatter
	info, err := codegen.AnalyzeFrontmatter(file.Frontmatter)
	if err != nil {
		return compileResult{}, err
	}

	// Check for children usage before template transformation. Goes
	// through codegen.TemplateRendersChildren so the LSP shadow's
	// componentScan and this compile pass can never disagree on whether
	// a component renders children — see internal/codegen/template.go.
	hasChildren := codegen.TemplateRendersChildren(file.TemplateBody)

	// Transform template body
	transformedBody, err := codegen.TransformTemplate(file.TemplateBody, file.Uses)
	if err != nil {
		return compileResult{}, err
	}

	// Validate (dict ...) keys against the destination component's Props
	// schema. Unknown keys produce warnings here; opts.Strict in Compile
	// promotes them to errors. Falls back to silent no-op if the body
	// doesn't parse with the stub FuncMap (rare).
	dictWarnings := codegen.ValidateDictKeys(transformedBody, file.Uses, propsByPath)

	// Write template file
	templateName := strings.TrimSuffix(relPath, ".gastro")
	templateName = strings.ReplaceAll(templateName, "/", "_")
	templatePath := filepath.Join(outputDir, "templates", templateName+".html")
	if err := os.WriteFile(templatePath, []byte(transformedBody), 0o644); err != nil {
		return compileResult{}, err
	}

	// Determine component status: explicit gastro.Props() call, or
	// directory-based inference for frontmatter-less files.
	isComponent := info.IsComponent
	if strings.HasPrefix(relPath, "components/") && !info.IsComponent {
		isComponent = true
	}

	// Generate handler Go code
	file.TemplateBody = transformedBody
	handlerCode, err := codegen.GenerateHandler(file, info, isComponent, codegen.GenerateOptions{MangleHoisted: true})
	if err != nil {
		return compileResult{}, err
	}

	// All generated .go files go flat in the output directory (same package)
	goFileName := strings.TrimSuffix(relPath, ".gastro")
	goFileName = strings.ReplaceAll(goFileName, "/", "_")
	goFileName = strings.ReplaceAll(goFileName, "[", "")
	goFileName = strings.ReplaceAll(goFileName, "]", "")
	goFileName = strings.ReplaceAll(goFileName, "-", "_")
	goFilePath := filepath.Join(outputDir, goFileName+".go")
	if err := writeGoFile(goFilePath, []byte(handlerCode)); err != nil {
		return compileResult{}, err
	}

	funcName := codegen.HandlerFuncName(relPath)

	// Build UseInfo for this template's component dependencies
	var uses []codegen.UseInfo
	for _, u := range file.Uses {
		uses = append(uses, codegen.UseInfo{
			Name:     u.Name,
			FuncName: codegen.HandlerFuncName(u.Path),
		})
	}

	tmplMeta := templateMeta{
		FuncName:     funcName,
		TemplateFile: templateName + ".html",
		Uses:         uses,
	}

	// Track B (docs/history/frictions-plan.md §4.9): for pages, run the
	// shared response-write analyzer over the frontmatter and emit a
	// warning for every write site that isn't followed by `return`.
	// Components don't have `w` / `r` in scope, so the analyzer is
	// gated to pages here to avoid surprising warnings on a component
	// that happens to bind a local `w` for unrelated reasons.
	var respwriteWarnings []codegen.Warning
	if !isComponent {
		respwriteWarnings = codegen.ValidateFrontmatterReturns(file.Frontmatter)
	}

	// Combine frontmatter warnings with dict-key validation warnings. The
	// dict warnings already carry template-body line numbers; rebase them
	// onto the source file's coordinate system by offsetting by the line
	// where the template body starts.
	combinedWarnings := append([]codegen.Warning(nil), info.Warnings...)
	combinedWarnings = append(combinedWarnings, respwriteWarnings...)
	for _, w := range dictWarnings {
		line := w.Line
		if file.TemplateBodyLine > 0 {
			line = file.TemplateBodyLine + line - 1
		}
		combinedWarnings = append(combinedWarnings, codegen.Warning{
			Line:    line,
			Message: w.Message,
		})
	}

	// Pages have no component metadata
	if !isComponent {
		return compileResult{template: tmplMeta, warnings: combinedWarnings, embedDeps: embedDeps}, nil
	}

	_, hoistedTypes := codegen.HoistTypeDeclarations(file.Frontmatter)

	// Derive the unique props type name. Mirrors the mangling scheme
	// applied by GenerateHandler under MangleHoisted=true so the
	// per-router render.go references the same symbol the
	// per-component .gen.go declares at package scope.
	propsTypeName := info.PropsTypeName
	if propsTypeName != "" {
		propsTypeName = "__component_" + codegen.DerivePageID(file.Filename) + "_" + propsTypeName
	}

	compMeta := &componentMeta{
		ExportedName:  codegen.ExportedComponentName(funcName),
		FuncName:      funcName,
		HasProps:      info.PropsTypeName != "",
		PropsTypeName: propsTypeName,
		PropsFields:   codegen.ParseStructFields(hoistedTypes),
		HasChildren:   hasChildren,
	}

	return compileResult{template: tmplMeta, component: compMeta, warnings: combinedWarnings, embedDeps: embedDeps}, nil
}

// syncStatic copies the project's static/ directory into outputDir/static/.
// A fresh copy is made on every generate so that deleted files don't linger.
// Go's //go:embed does not follow symlinks to directories, so copying is required.
func syncStatic(projectDir, outputDir string) error {
	src := filepath.Join(projectDir, "static")
	dst := filepath.Join(outputDir, "static")

	// Clean slate — remove old copy so deleted source files don't linger
	if err := os.RemoveAll(dst); err != nil {
		return fmt.Errorf("removing old static copy: %w", err)
	}

	return copyDir(src, dst)
}

// copyDir recursively copies src into dst using os.CopyFS.
func copyDir(src, dst string) error {
	return os.CopyFS(dst, os.DirFS(src))
}

// embedData is the data passed to embedTmpl.
type embedData struct {
	HasStatic bool
}

var embedTmpl = template.Must(template.New("embed").Parse(`// Code generated by gastro. DO NOT EDIT.
package gastro

import "embed"

//go:embed templates/*
var templateFS embed.FS
{{- if .HasStatic }}

//go:embed static/*
var staticAssetFS embed.FS
{{- end }}
`))

func generateEmbedFile(hasStatic bool, outputDir string) error {
	f, err := os.Create(filepath.Join(outputDir, "embed.go"))
	if err != nil {
		return err
	}
	defer f.Close()

	return embedTmpl.Execute(f, embedData{HasStatic: hasStatic})
}

// routesData is the data passed to routesTmpl.
type routesData struct {
	Routes    []router.Route
	Templates []templateMeta
	HasStatic bool
}

var routesTmpl = template.Must(template.New("routes").Parse(`// Code generated by gastro. DO NOT EDIT.
package gastro

import (
	"bytes"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"reflect"
	"regexp"
	"strings"
	"sync/atomic"

	gastroRuntime "github.com/andrioid/gastro/pkg/gastro"
)

// Suppress unused import warning for bytes (needed when templates have component uses).
var _ bytes.Buffer

// Suppress unused import warnings for error-enhancement dependencies.
var _ = fmt.Errorf
var _ = regexp.Compile
var _ = strings.Contains
var _ = reflect.TypeOf

// Option configures the generated router. Pass options to New().
type Option func(*config)

type config struct {
	funcs        template.FuncMap
	deps         map[reflect.Type]any
	overrides    map[string]http.Handler
	middleware   []middlewareEntry
	devMode      *bool // nil = use GASTRO_DEV env var; non-nil = override
	errorHandler gastroRuntime.PageErrorHandler
}

// middlewareEntry pairs a route-pattern matcher with the middleware to
// install. Stored as a slice so multiple WithMiddleware calls for the
// same pattern compose in registration order — the first registered
// middleware ends up outermost.
type middlewareEntry struct {
	pattern string
	fn      gastroRuntime.MiddlewareFunc
}

// WithDevMode overrides the GASTRO_DEV environment variable for this Router.
// When set to true, templates are re-parsed from disk on every request
// and the dev-reload middleware is attached — regardless of GASTRO_DEV.
// When set to false, production mode is forced even when GASTRO_DEV=1.
// When not called, the default behaviour (checking GASTRO_DEV) applies.
//
// Calling WithDevMode multiple times keeps the last value (no panic);
// the option is intended to be set once at New() time.
func WithDevMode(dev bool) Option {
	return func(c *config) { c.devMode = &dev }
}

// WithFuncs registers additional template helper functions.
// User-provided functions override built-in defaults with the same name.
func WithFuncs(fm template.FuncMap) Option {
	return func(c *config) {
		for k, v := range fm {
			c.funcs[k] = v
		}
	}
}

// WithDeps registers a typed dependency that page handlers can retrieve via
// gastro.From[T](ctx) or gastro.FromContext[T](r.Context()).
//
// The dependency is keyed by its Go type, so each type can have at most one
// instance per router. Multiple WithDeps options with different types compose:
//
//	router := gastro.New(
//		gastro.WithDeps(BoardDeps{State: stateFn, Store: store}),
//		gastro.WithDeps(AuthDeps{Verifier: v}),
//	)
//
// Calling WithDeps twice with the same type panics at New() time.
func WithDeps[T any](deps T) Option {
	return func(c *config) {
		if c.deps == nil {
			c.deps = make(map[reflect.Type]any)
		}
		k := reflect.TypeOf(deps)
		if _, dup := c.deps[k]; dup {
			panic(fmt.Sprintf("gastro: WithDeps: duplicate registration for type %s", k))
		}
		c.deps[k] = deps
	}
}

// WithOverride replaces the auto-generated handler for a route pattern with
// a user-supplied http.Handler. The pattern must exactly match one of the
// gastro auto-routes (e.g. "/", "/blog/{slug}", or the static-asset prefix);
// New() panics if it does not, to catch typos early.
//
// Track B (docs/history/frictions-plan.md §4.2): page patterns are now
// method-less. Where pre-Track-B you'd write WithOverride for an explicit
// HTTP method, you now write the path alone and the override receives every
// method for that path. The static-asset prefix is the lone exception —
// it keeps its method prefix because static files are read-only.
//
// Use this when a page needs typed dependencies that frontmatter cannot
// express, or when a handler needs full control over the response (streaming,
// custom status codes, content negotiation).
func WithOverride(pattern string, h http.Handler) Option {
	return func(c *config) {
		if c.overrides == nil {
			c.overrides = make(map[string]http.Handler)
		}
		c.overrides[pattern] = h
	}
}

// WithMiddleware wraps the handler for every auto-route whose pattern
// matches the supplied http.ServeMux pattern. Patterns use Go's stdlib
// pattern syntax: "/counter" for an exact path, "/admin/{path...}" for
// a subtree wildcard, "/blog/{slug}" to match a parametrised route.
//
// Patterns are path-only — there is no method scoping. Middleware that
// must only run for, say, POST should branch on r.Method internally.
// This mirrors WithOverride's path-only contract (Track B,
// docs/history/frictions-plan.md §4.2) and avoids the asymmetry where
// override and middleware would accept different pattern shapes.
//
// Validation: at New() time the pattern must match at least one known
// auto-route, probed via gastroRuntime.PatternMatchesAnyRoute. A
// pattern that matches nothing panics with a descriptive error — same
// typo-safety posture as WithOverride.
//
// Composition: multiple WithMiddleware calls for overlapping patterns
// compose in registration order. The first call ends up outermost
// (runs first on the request, last on the response). When both
// WithOverride and WithMiddleware target the same route, the override
// replaces the page handler and the middleware then wraps the override
// — "middleware wraps override".
//
// Wave 4 / C2 (docs/history/frictions-plan.md §3 Wave 4).
func WithMiddleware(pattern string, fn gastroRuntime.MiddlewareFunc) Option {
	return func(c *config) {
		c.middleware = append(c.middleware, middlewareEntry{pattern: pattern, fn: fn})
	}
}

// WithErrorHandler installs a custom handler for page render errors.
//
// The handler is invoked when a generated page handler's template Execute
// returns an error — typically a missing field, a panic during template
// rendering recovered by html/template, or an io.Writer error from the
// underlying connection. It is *not* invoked for parse errors (caught at
// New() in production, at request time in dev) or for panics in user
// frontmatter (handled by gastro.Recover).
//
// When unset, gastroRuntime.DefaultErrorHandler is used: log the error and
// write a 500 if the response has not yet committed headers or a body.
// Custom handlers can render a templated error page, attach request IDs,
// emit structured logs, or report to an error tracker. See
// docs/error-handling.md for the full failure-mode catalogue and recipes.
//
// Calling WithErrorHandler multiple times keeps the last value (no panic);
// the option is intended to be set once at New() time.
//
// Wave 4 / C4 (docs/history/frictions-plan.md §3 Wave 4).
func WithErrorHandler(fn gastroRuntime.PageErrorHandler) Option {
	return func(c *config) { c.errorHandler = fn }
}

// Router holds the parsed templates, registered options, and the underlying
// http.ServeMux for a gastro app. Construct with New(); access the handler
// via Handler() or, for direct mux mutation, via Mux().
type Router struct {
	isDev        bool
	userFuncs    template.FuncMap
	registry     map[string]*template.Template
	deps         map[reflect.Type]any
	mux          *http.ServeMux
	errorHandler gastroRuntime.PageErrorHandler
}

// __gastro_handleError dispatches a page render error to the user-supplied
// WithErrorHandler when one is installed; otherwise to the runtime default.
// Centralising the dispatch in one method keeps the codegen-emitted handler
// body a single line and lets us evolve the error-handler contract
// (e.g. add structured context) without touching every generated handler.
func (__r *Router) __gastro_handleError(w http.ResponseWriter, r *http.Request, err error) {
	if __r.errorHandler != nil {
		__r.errorHandler(w, r, err)
		return
	}
	gastroRuntime.DefaultErrorHandler(w, r, err)
}

// __gastro_active is the most-recently-constructed Router, used by the
// package-level Render variable so existing single-router callers keep
// working without code changes. Stored atomically so concurrent New()
// calls (parallel tests, multi-tenant servers) and concurrent Render
// access don't race.
//
// For multi-router scenarios, prefer holding onto the *Router returned by
// New() and calling router.Render().X(...) directly — that path never
// touches the global.
var __gastro_active atomic.Pointer[Router]

// __gastro_buildFuncMap constructs the FuncMap for the named template,
// merging user functions with per-template component wiring.
func (__r *Router) __gastro_buildFuncMap(name string, userFuncs template.FuncMap) template.FuncMap {
	fm := template.FuncMap{}
	for k, v := range userFuncs {
		fm[k] = v
	}
	switch name {
{{- range .Templates}}{{- if .Uses}}{{$fn := .FuncName}}
	case "{{$fn}}":
{{- range .Uses}}
		fm["{{ .Name }}"] = __r.{{ .FuncName }}
{{- end}}
		fm["__gastro_render_children"] = func(n string, data any) template.HTML {
			var __buf bytes.Buffer
			__r.__gastro_getTemplate("{{$fn}}").ExecuteTemplate(&__buf, n, data)
			return template.HTML(__buf.String())
		}
{{- end}}{{end}}
	}
	return fm
}

// __gastro_templateFile maps a template function name to its filename
// within the templates/ directory.
func __gastro_templateFile(name string) string {
	switch name {
{{- range .Templates}}
	case "{{ .FuncName }}":
		return "{{ .TemplateFile }}"
{{- end}}
	}
	return name + ".html"
}

// __gastro_parseTemplate reads a template from tfs and parses it with the
// FuncMap appropriate for its component dependencies.
func (__r *Router) __gastro_parseTemplate(name string, tfs fs.FS, userFuncs template.FuncMap) (*template.Template, error) {
	content, err := fs.ReadFile(tfs, __gastro_templateFile(name))
	if err != nil {
		return nil, fmt.Errorf("gastro: reading template %s: %w", name, err)
	}
	tmpl, err := template.New(name).Funcs(__r.__gastro_buildFuncMap(name, userFuncs)).Parse(string(content))
	if err != nil {
		return nil, __gastro_enhanceTemplateError(err)
	}
	return tmpl, nil
}

// __gastro_pascalCaseFuncRegex matches Go template errors for undefined PascalCase functions,
// which are likely missing component imports.
var __gastro_pascalCaseFuncRegex = regexp.MustCompile("function \"([A-Z][a-zA-Z0-9]*)\" not defined")

// __gastro_enhanceTemplateError rewrites Go template parse errors to provide
// component-specific hints. A generic "function X not defined" for a PascalCase
// name becomes "unknown component X (did you forget to import it?)".
func __gastro_enhanceTemplateError(err error) error {
	msg := err.Error()
	if matches := __gastro_pascalCaseFuncRegex.FindStringSubmatch(msg); len(matches) > 1 {
		return fmt.Errorf("unknown component %q (did you forget to import it?)", matches[1])
	}
	return err
}

// __gastro_getTemplate returns the parsed template for the given name.
// In dev mode, templates are re-parsed from disk on every call so that
// template changes are reflected immediately without restarting the server.
func (__r *Router) __gastro_getTemplate(name string) *template.Template {
	if __r.isDev {
		tfs := gastroRuntime.GetTemplateFS(templateFS)
		tmpl, err := __r.__gastro_parseTemplate(name, tfs, __r.userFuncs)
		if err != nil {
			log.Fatalf("gastro: %v", err)
		}
		return tmpl
	}
	return __r.registry[name]
}

// New constructs a gastro Router from the supplied options.
//
// Mount the router on your HTTP server via Handler() (returns an http.Handler
// that includes the dev-mode reload middleware in development) or Mux() (the
// underlying *http.ServeMux for direct manipulation).
//
// Example:
//
//	router := gastro.New(
//		gastro.WithDeps(BoardDeps{...}),
//		gastro.WithOverride("/", customHomeHandler),
//	)
//	http.ListenAndServe(":8080", router.Handler())
func New(opts ...Option) *Router {
	cfg := &config{
		funcs: gastroRuntime.DefaultFuncs(),
	}
	for _, opt := range opts {
		opt(cfg)
	}

	isDev := gastroRuntime.IsDev()
	if cfg.devMode != nil {
		isDev = *cfg.devMode
	}

	__r := &Router{
		isDev:        isDev,
		userFuncs:    cfg.funcs,
		deps:         cfg.deps,
		errorHandler: cfg.errorHandler,
	}

	// Validate WithOverride patterns: each must match a known auto-route
	// (or the static prefix). Catches typos at startup rather than letting
	// the override silently register a brand-new route.
	knownPatterns := map[string]bool{
{{- if .HasStatic}}
		"GET /static/": true,
{{- end}}
{{- range .Routes}}
		"{{ .Pattern }}": true,
{{- end}}
	}
	for pat := range cfg.overrides {
		if !knownPatterns[pat] {
			known := make([]string, 0, len(knownPatterns))
			for p := range knownPatterns {
				known = append(known, p)
			}
			panic(fmt.Sprintf(
				"gastro: WithOverride: pattern %q does not match any auto-route. known: %v",
				pat, known,
			))
		}
	}

	// Validate WithMiddleware patterns: each must match at least one
	// known auto-route via gastroRuntime.PatternMatchesAnyRoute. Same
	// typo-safety posture as WithOverride; the matcher diverges only
	// because middleware patterns can carry {slug...} wildcards.
	knownRoutes := make([]string, 0, len(knownPatterns))
	for p := range knownPatterns {
		knownRoutes = append(knownRoutes, p)
	}
	for _, mw := range cfg.middleware {
		if err := gastroRuntime.ValidateMiddlewarePattern(mw.pattern, knownRoutes); err != nil {
			panic(err.Error())
		}
	}

	// Warn if user-provided functions shadow component names.
	__gastro_componentNames := make(map[string]bool)
{{- range .Templates}}{{- range .Uses}}
	__gastro_componentNames["{{ .Name }}"] = true
{{- end}}{{- end}}
	for name := range cfg.funcs {
		if __gastro_componentNames[name] {
			log.Printf("gastro: warning: user function %q shadows component %q", name, name)
		}
	}

	// Parse all templates into the router-local registry.
	tfs := gastroRuntime.GetTemplateFS(templateFS)
	__r.registry = make(map[string]*template.Template)
	for _, name := range []string{
{{- range .Templates}}
		"{{ .FuncName }}",
{{- end}}
	} {
		tmpl, err := __r.__gastro_parseTemplate(name, tfs, cfg.funcs)
		if err != nil {
			log.Fatalf("gastro: %v", err)
		}
		__r.registry[name] = tmpl
	}

	// applyMiddleware wraps h with every middleware whose pattern matches
	// route. Middleware composes in registration order: the first
	// WithMiddleware call ends up outermost (runs first on the request).
	// This is the "middleware wraps override" branch of
	// docs/history/frictions-plan.md Q3 — by the time we get here, h is
	// already either the auto-route handler or the override.
	applyMiddleware := func(route string, h http.Handler) http.Handler {
		for i := len(cfg.middleware) - 1; i >= 0; i-- {
			mw := cfg.middleware[i]
			if gastroRuntime.MiddlewareApplies(mw.pattern, route) {
				h = mw.fn(h)
			}
		}
		return h
	}

	// Build the mux. Overrides win over auto-routes by matching pattern,
	// then any matching middleware wraps the resulting handler.
	mux := http.NewServeMux()
{{- if .HasStatic}}
	{
		var h http.Handler
		if over, ok := cfg.overrides["GET /static/"]; ok {
			h = over
		} else {
			staticFS := gastroRuntime.GetStaticFS(staticAssetFS)
			h = http.StripPrefix("/static/", http.FileServerFS(staticFS))
		}
		mux.Handle("GET /static/", applyMiddleware("GET /static/", h))
	}
{{- end}}
{{- range .Routes}}
	{
		var h http.Handler
		if over, ok := cfg.overrides["{{ .Pattern }}"]; ok {
			h = over
		} else {
			h = http.HandlerFunc(__r.{{ .FuncName }})
		}
		mux.Handle("{{ .Pattern }}", applyMiddleware("{{ .Pattern }}", h))
	}
{{- end}}

	__r.mux = mux
	__gastro_active.Store(__r)
	return __r
}

// Handler returns the http.Handler that should be mounted on your HTTP
// server. It wraps the underlying mux with deps-attachment middleware and,
// in dev mode, the reload middleware.
func (__r *Router) Handler() http.Handler {
	var h http.Handler = __r.mux
	if len(__r.deps) > 0 {
		h = __r.attachDepsMiddleware(h)
	}
	if __r.isDev {
		gastroRuntime.DevReloader.Start()
		wrapped := http.NewServeMux()
		wrapped.Handle("/", h)
		wrapped.HandleFunc("GET /__gastro/reload", gastroRuntime.DevReloader.HandleSSE)
		return gastroRuntime.DevReloader.Middleware(wrapped)
	}
	return h
}

// Mux returns the underlying *http.ServeMux. Use this for fine-grained
// route registration that WithOverride cannot express. Mutations bypass
// the deps-attachment middleware, so handlers added this way will not see
// values registered with WithDeps.
func (__r *Router) Mux() *http.ServeMux { return __r.mux }

func (__r *Router) attachDepsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := gastroRuntime.AttachDeps(r.Context(), __r.deps)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// Routes returns an http.Handler with all gastro page routes and static assets.
//
// Deprecated: prefer New(opts...).Handler(), which exposes the underlying
// *Router with typed deps, route overrides, and direct mux access. Routes
// remains as a thin shim for backward compatibility.
func Routes(opts ...Option) http.Handler {
	return New(opts...).Handler()
}
`))

// renderData is the data passed to renderTmpl.
type renderData struct {
	Components []componentMeta
}

var renderTmpl = template.Must(template.New("render").Parse(`// Code generated by gastro. DO NOT EDIT.

// This file exposes the Render API: typed entry points for rendering
// components from Go code (HTTP handlers, SSE patch streams, tests).
//
// Two public surfaces are generated by gastro:
//
//   - Routes()  in routes.go — mounts the file-based page routes as an
//                              http.Handler. Use this from main().
//   - Render    in this file — typed component rendering for code that
//                              produces HTML outside the page-routing
//                              flow (e.g. SSE handlers patching a card).
//
// Two ways to call it:
//
//	// 1. Package-level Render — simplest, uses the most-recently-
//	//    constructed Router. Fine for single-router apps.
//	html, err := gastro.Render.Card(gastro.CardProps{Title: "Hello"})
//
//	// 2. Router.Render() — prefer this in tests and multi-router setups
//	//    (parallel tests, multi-tenant servers). Never touches the global.
//	router := gastro.New(opts...)
//	html, err := router.Render().Card(gastro.CardProps{Title: "Hello"})
//
// Each method takes a typed Props value (and optional children for
// components that accept them) and returns an HTML string plus an error.
// Internally it calls the unexported componentX function shared with the
// template renderer, so frontmatter logic runs identically in both paths.
package gastro

import (
	"fmt"
	"html/template"
)

// Suppress unused import warnings.
var _ = fmt.Errorf
var _ template.HTML

// Render is the package-level entry point for rendering components from
// Go code. It dispatches to the most-recently-constructed Router (set by
// gastro.New()).
//
// Each method on Render corresponds to a component file in components/ and
// takes that component's typed Props (plus an optional children argument for
// components with slots). It returns the rendered HTML and any execution
// error.
//
// Use Render from HTTP handlers, SSE patch streams, tests — anywhere outside
// the page-routing flow exposed by Routes().
//
// For parallel tests or multi-tenant servers where multiple Routers coexist,
// prefer router.Render().X(...) over the package-level Render. The router-
// scoped path never reads the global, so it's race-free regardless of how
// many Routers are in flight.
//
// See the package-level doc for an example.
var Render = &renderAPI{}

// Render returns a typed component-rendering API bound to this Router.
// Unlike the package-level gastro.Render, methods on the returned value
// dispatch directly to this Router's template registry and never read
// the global __gastro_active pointer — making it safe to use from
// parallel tests and multi-router setups.
func (r *Router) Render() *renderAPI { return &renderAPI{router: r} }

// renderAPI dispatches component-rendering calls to a specific Router.
// A nil router means "use the most-recently-constructed Router" (the
// package-level Render value); a non-nil router pins dispatch to that
// instance.
type renderAPI struct{ router *Router }

// resolve returns the Router this renderAPI should dispatch to. If the
// renderAPI is bound to a specific Router (via Router.Render()), that's
// returned; otherwise the most-recently-constructed Router is loaded
// atomically. Returns nil if no Router has been constructed yet.
func (r *renderAPI) resolve() *Router {
	if r.router != nil {
		return r.router
	}
	return __gastro_active.Load()
}
{{ range .Components }}
{{- /*
  XProps type definition. Lives in render.go (not in the per-component
  file) so it can include the synthetic Children field without modifying
  the user's hoisted Props struct.

  Three shapes:
    - HasProps && !HasChildren  -> alias to the user's hoisted struct
    - HasChildren               -> real struct: user fields + Children template.HTML
    - !HasProps && !HasChildren -> no XProps type generated (Render.X())
*/ -}}
{{- if .HasChildren }}
// {{ .ExportedName }}Props is the typed prop struct for Render.{{ .ExportedName }}.
// The Children field carries the rendered HTML children content; it is
// auto-added by codegen because the component template references {{ "{{ .Children }}" }}.
type {{ .ExportedName }}Props struct {
{{- range .PropsFields }}
	{{ .Name }} {{ .Type }}
{{- end }}
	Children template.HTML
}
{{- else if .HasProps }}
// {{ .ExportedName }}Props is the typed prop struct for Render.{{ .ExportedName }}.
type {{ .ExportedName }}Props = {{ .PropsTypeName }}
{{- end }}

func (r *renderAPI) {{ .ExportedName }}({{ if or .HasProps .HasChildren }}props {{ .ExportedName }}Props{{ end }}) (string, error) {
	propsMap := map[string]any{
{{- range .PropsFields }}
		"{{ .Name }}": props.{{ .Name }},
{{- end }}
{{- if .HasChildren }}
		"Children": props.Children,
{{- end }}
	}
	rt := r.resolve()
	if rt == nil {
		return "", fmt.Errorf("gastro: Render.{{ .ExportedName }}: no router constructed yet (call gastro.New() first)")
	}
	result := rt.{{ .FuncName }}(propsMap)
	if result == "" {
		return "", fmt.Errorf("gastro: render {{ .ExportedName }} failed")
	}
	return string(result), nil
}
{{ end }}`))

func generateRenderFile(components []componentMeta, outputDir string) error {
	var buf bytes.Buffer
	if err := renderTmpl.Execute(&buf, renderData{Components: components}); err != nil {
		return err
	}
	return writeGoFile(filepath.Join(outputDir, "render.go"), buf.Bytes())
}

func generateRoutesFile(routes []router.Route, templates []templateMeta, hasStatic bool, outputDir string) error {
	var buf bytes.Buffer
	err := routesTmpl.Execute(&buf, routesData{
		Routes:    routes,
		Templates: templates,
		HasStatic: hasStatic,
	})
	if err != nil {
		return err
	}
	return writeGoFile(filepath.Join(outputDir, "routes.go"), buf.Bytes())
}

// writeGoFile runs go/format.Source over src to produce canonical,
// gofmt-stable Go, then writes the result to path. Generated files that
// pass through this helper are byte-identical to what `gofmt -w` would
// produce — important because gastro check uses byte-level comparison
// to detect drift, and any tool that reformats the workspace (IDE save
// hooks, `go fmt ./...`) would otherwise create permanent drift loops
// against the codegen output.
//
// If go/format.Source rejects src, the original (unformatted) bytes are
// included in the returned error so the failure can be diagnosed without
// re-running the compile. The on-disk file is not touched in that case.
//
// Two correctness properties beyond formatting:
//
//   - Skip-if-equal: if the on-disk bytes already match the formatted
//     output, the file is left untouched. This preserves the mod-time so
//     external watchers (and the in-tree dev watcher) don't see spurious
//     change events on no-op regenerations, and avoids spurious git diffs
//     for downstream teams that opt into committing the .gastro/ tree.
//
//   - Atomic write: when the bytes do change, write to a sibling ".tmp"
//     file first and rename into place. A rename is atomic on POSIX
//     filesystems, so a concurrent reader (e.g. `go build` running in
//     parallel with a regen, or a watcher killed mid-write) either sees
//     the old complete contents or the new complete contents — never a
//     half-written file. If formatting succeeds but the write fails, no
//     stray ".tmp" is left behind on the success path; on the failure
//     path we make a best-effort cleanup.
func writeGoFile(path string, src []byte) error {
	formatted, err := goformat.Source(src)
	if err != nil {
		return fmt.Errorf("gofmt %s: %w\n--- generated source ---\n%s", path, err, src)
	}

	// Skip-if-equal: avoid bumping mod-time when nothing changed.
	// os.ReadFile errors (file missing, permission denied) fall through
	// to the write path, which will surface the real error if any.
	if existing, rerr := os.ReadFile(path); rerr == nil && bytes.Equal(existing, formatted) {
		return nil
	}

	// Atomic write: temp + rename. The temp file lives alongside the
	// destination so the rename stays on the same filesystem (cross-fs
	// renames fail with EXDEV on Linux).
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, formatted, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		// Best-effort cleanup so we don't leave .tmp turds behind.
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// dedupeStrings returns a sorted deduplicated copy of in.
func dedupeStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
