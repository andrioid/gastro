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
	for _, relPath := range allFiles {
		absPath := filepath.Join(projectDir, relPath)
		result, err := compileFile(absPath, relPath, absProjectDir, outputDir)
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

func compileFile(absPath, relPath, absProjectDir, outputDir string) (compileResult, error) {
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

	// Pages have no component metadata
	if !isComponent {
		return compileResult{template: tmplMeta, warnings: info.Warnings, markdownDeps: markdownDeps}, nil
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

	return compileResult{template: tmplMeta, component: compMeta, warnings: info.Warnings, markdownDeps: markdownDeps}, nil
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
	"regexp"
	"strings"

	gastroRuntime "github.com/andrioid/gastro/pkg/gastro"
)

// Suppress unused import warning for bytes (needed when templates have component uses).
var _ bytes.Buffer

// Suppress unused import warnings for error-enhancement dependencies.
var _ = fmt.Errorf
var _ = regexp.Compile
var _ = strings.Contains

// Option configures the generated router.
type Option func(*config)

type config struct {
	funcs template.FuncMap
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

// __gastro_templateRegistry stores parsed templates keyed by function name.
var __gastro_templateRegistry map[string]*template.Template

// __gastro_isDev indicates whether the app is running in dev mode.
var __gastro_isDev bool

// __gastro_userFuncs holds the configured FuncMap for dev-mode re-parsing.
var __gastro_userFuncs template.FuncMap

// __gastro_buildFuncMap constructs the FuncMap for the named template,
// merging user functions with per-template component wiring.
func __gastro_buildFuncMap(name string, userFuncs template.FuncMap) template.FuncMap {
	fm := template.FuncMap{}
	for k, v := range userFuncs {
		fm[k] = v
	}
	switch name {
{{- range .Templates}}{{- if .Uses}}{{$fn := .FuncName}}
	case "{{$fn}}":
{{- range .Uses}}
		fm["{{ .Name }}"] = {{ .FuncName }}
{{- end}}
		fm["__gastro_render_children"] = func(n string, data any) template.HTML {
			var __buf bytes.Buffer
			__gastro_getTemplate("{{$fn}}").ExecuteTemplate(&__buf, n, data)
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
func __gastro_parseTemplate(name string, tfs fs.FS, userFuncs template.FuncMap) (*template.Template, error) {
	content, err := fs.ReadFile(tfs, __gastro_templateFile(name))
	if err != nil {
		return nil, fmt.Errorf("gastro: reading template %s: %w", name, err)
	}
	tmpl, err := template.New(name).Funcs(__gastro_buildFuncMap(name, userFuncs)).Parse(string(content))
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
func __gastro_getTemplate(name string) *template.Template {
	if __gastro_isDev {
		tfs := gastroRuntime.GetTemplateFS(templateFS)
		tmpl, err := __gastro_parseTemplate(name, tfs, __gastro_userFuncs)
		if err != nil {
			log.Fatalf("gastro: %v", err)
		}
		return tmpl
	}
	return __gastro_templateRegistry[name]
}

// Routes returns an http.Handler with all gastro page routes and static assets.
func Routes(opts ...Option) http.Handler {
	cfg := &config{
		funcs: gastroRuntime.DefaultFuncs(),
	}
	for _, opt := range opts {
		opt(cfg)
	}

	__gastro_isDev = gastroRuntime.IsDev()
	__gastro_userFuncs = cfg.funcs

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

	// Parse all templates into the registry.
	tfs := gastroRuntime.GetTemplateFS(templateFS)
	__gastro_templateRegistry = make(map[string]*template.Template)
	for _, name := range []string{
{{- range .Templates}}
		"{{ .FuncName }}",
{{- end}}
	} {
		tmpl, err := __gastro_parseTemplate(name, tfs, cfg.funcs)
		if err != nil {
			log.Fatalf("gastro: %v", err)
		}
		__gastro_templateRegistry[name] = tmpl
	}

	mux := http.NewServeMux()
{{- if .HasStatic}}
	staticFS := gastroRuntime.GetStaticFS(staticAssetFS)
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(staticFS)))
{{- end}}
{{- range .Routes}}
	mux.HandleFunc("{{ .Pattern }}", {{ .FuncName }})
{{- end}}

	if __gastro_isDev {
		gastroRuntime.DevReloader.Start()
		mux.HandleFunc("GET /__gastro/reload", gastroRuntime.DevReloader.HandleSSE)
		return gastroRuntime.DevReloader.Middleware(mux)
	}
	return mux
}
`))

// renderData is the data passed to renderTmpl.
type renderData struct {
	Components []componentMeta
}

var renderTmpl = template.Must(template.New("render").Parse(`// Code generated by gastro. DO NOT EDIT.
package gastro

import (
	"fmt"
	"html/template"
)

// Suppress unused import warnings.
var _ = fmt.Errorf
var _ template.HTML

// Render provides typed methods to render components as HTML strings.
// Useful for SSE handlers that need to send component markup as patches.
var Render = &renderAPI{}

type renderAPI struct{}
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
	result := {{ .FuncName }}(propsMap)
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
