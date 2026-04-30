package compiler

import (
	"fmt"
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
	// MarkdownDeps lists absolute paths to every .md file referenced by a
	// {{ markdown "..." }} directive during this compile. Useful for the
	// dev watcher so changes to these files can trigger a regenerate.
	MarkdownDeps []string
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
	if err != nil {
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
	var allMarkdownDeps []string
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
		allMarkdownDeps = append(allMarkdownDeps, result.markdownDeps...)
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

	return &CompileResult{Warnings: allWarnings, MarkdownDeps: dedupeStrings(allMarkdownDeps)}, nil
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
	template     templateMeta
	component    *componentMeta
	warnings     []codegen.Warning
	markdownDeps []string
}

// gatherComponentSchemas reads each component file's frontmatter, extracts
// its hoisted Props type, and returns a map keyed by relative path (the
// same path that appears in `import X "components/foo.gastro"`). Files
// without a Props struct are omitted; readers are expected to treat a
// missing key as "no schema, skip validation".
func gatherComponentSchemas(componentFiles []string, projectDir string) (map[string][]codegen.StructField, error) {
	schemas := make(map[string][]codegen.StructField, len(componentFiles))
	for _, relPath := range componentFiles {
		absPath := filepath.Join(projectDir, relPath)
		content, err := os.ReadFile(absPath)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", relPath, err)
		}
		file, err := parser.Parse(relPath, string(content))
		if err != nil {
			// Parse errors will surface during the main pass with full
			// context; don't abort the schema gather over them.
			continue
		}
		info, err := codegen.AnalyzeFrontmatter(file.Frontmatter)
		if err != nil || info.PropsTypeName == "" {
			continue
		}
		_, hoisted := codegen.HoistTypeDeclarations(file.Frontmatter)
		fields := codegen.ParseStructFields(hoisted)
		if len(fields) > 0 {
			schemas[relPath] = fields
		}
	}
	return schemas, nil
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

	// Analyze frontmatter
	info, err := codegen.AnalyzeFrontmatter(file.Frontmatter)
	if err != nil {
		return compileResult{}, err
	}

	// Check for children usage before template transformation
	hasChildren := strings.Contains(file.TemplateBody, "{{ .Children }}")

	// Expand {{ markdown "path" }} directives before template transformation
	// so the resulting HTML is treated as part of the template body by all
	// downstream passes ({{ raw }}, {{ wrap }}, etc.).
	mdCtx := codegen.MarkdownContext{
		ProjectRoot: absProjectDir,
		SourceDir:   filepath.Dir(absPath),
	}
	bodyWithMarkdown, markdownDeps, err := codegen.ProcessMarkdownDirectives(file.TemplateBody, mdCtx)
	if err != nil {
		return compileResult{}, err
	}

	// Transform template body
	transformedBody, err := codegen.TransformTemplate(bodyWithMarkdown, file.Uses)
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
	handlerCode, err := codegen.GenerateHandler(file, info, isComponent)
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
	if err := os.WriteFile(goFilePath, []byte(handlerCode), 0o644); err != nil {
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

	// Combine frontmatter warnings with dict-key validation warnings. The
	// dict warnings already carry template-body line numbers; rebase them
	// onto the source file's coordinate system by offsetting by the line
	// where the template body starts.
	combinedWarnings := append([]codegen.Warning(nil), info.Warnings...)
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
		return compileResult{template: tmplMeta, warnings: combinedWarnings, markdownDeps: markdownDeps}, nil
	}

	_, hoistedTypes := codegen.HoistTypeDeclarations(file.Frontmatter)

	// Derive the unique props type name (same logic as GenerateHandler)
	propsTypeName := info.PropsTypeName
	if propsTypeName != "" {
		propsTypeName = funcName + strings.ToUpper(propsTypeName[:1]) + propsTypeName[1:]
	}

	compMeta := &componentMeta{
		ExportedName:  codegen.ExportedComponentName(funcName),
		FuncName:      funcName,
		HasProps:      info.PropsTypeName != "",
		PropsTypeName: propsTypeName,
		PropsFields:   codegen.ParseStructFields(hoistedTypes),
		HasChildren:   hasChildren,
	}

	return compileResult{template: tmplMeta, component: compMeta, warnings: combinedWarnings, markdownDeps: markdownDeps}, nil
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
	funcs     template.FuncMap
	deps      map[reflect.Type]any
	overrides map[string]http.Handler
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

// WithOverride replaces the auto-generated handler for a route pattern with a
// user-supplied http.Handler. The pattern must exactly match one of the
// gastro auto-routes (e.g. "GET /", "GET /blog/{slug}"); New() panics if it
// does not, to catch typos early.
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

// Router holds the parsed templates, registered options, and the underlying
// http.ServeMux for a gastro app. Construct with New(); access the handler
// via Handler() or, for direct mux mutation, via Mux().
type Router struct {
	isDev     bool
	userFuncs template.FuncMap
	registry  map[string]*template.Template
	deps      map[reflect.Type]any
	mux       *http.ServeMux
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
//		gastro.WithOverride("GET /", customHomeHandler),
//	)
//	http.ListenAndServe(":8080", router.Handler())
func New(opts ...Option) *Router {
	cfg := &config{
		funcs: gastroRuntime.DefaultFuncs(),
	}
	for _, opt := range opts {
		opt(cfg)
	}

	__r := &Router{
		isDev:     gastroRuntime.IsDev(),
		userFuncs: cfg.funcs,
		deps:      cfg.deps,
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

	// Build the mux. Overrides win over auto-routes by matching pattern.
	mux := http.NewServeMux()
{{- if .HasStatic}}
	if h, ok := cfg.overrides["GET /static/"]; ok {
		mux.Handle("GET /static/", h)
	} else {
		staticFS := gastroRuntime.GetStaticFS(staticAssetFS)
		mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(staticFS)))
	}
{{- end}}
{{- range .Routes}}
	if h, ok := cfg.overrides["{{ .Pattern }}"]; ok {
		mux.Handle("{{ .Pattern }}", h)
	} else {
		mux.HandleFunc("{{ .Pattern }}", __r.{{ .FuncName }})
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
func (r *renderAPI) {{ .ExportedName }}({{ if .HasProps }}props {{ .ExportedName }}Props{{ if .HasChildren }}, {{ end }}{{ end }}{{ if .HasChildren }}children ...template.HTML{{ end }}) (string, error) {
	propsMap := map[string]any{
{{- range .PropsFields }}
		"{{ .Name }}": props.{{ .Name }},
{{- end }}
	}
{{- if .HasChildren }}
	if len(children) > 0 {
		propsMap["__children"] = children[0]
	}
{{- end }}
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
	f, err := os.Create(filepath.Join(outputDir, "render.go"))
	if err != nil {
		return err
	}
	defer f.Close()

	return renderTmpl.Execute(f, renderData{Components: components})
}

func generateRoutesFile(routes []router.Route, templates []templateMeta, hasStatic bool, outputDir string) error {
	f, err := os.Create(filepath.Join(outputDir, "routes.go"))
	if err != nil {
		return err
	}
	defer f.Close()

	return routesTmpl.Execute(f, routesData{
		Routes:    routes,
		Templates: templates,
		HasStatic: hasStatic,
	})
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
