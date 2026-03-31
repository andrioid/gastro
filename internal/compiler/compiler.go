package compiler

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/andrioid/gastro/internal/codegen"
	"github.com/andrioid/gastro/internal/parser"
	"github.com/andrioid/gastro/internal/router"
)

// Compile reads all .gastro files from a project directory, processes them
// through the parser and code generator, and writes the output to outputDir.
func Compile(projectDir, outputDir string) error {
	// Ensure output subdirectories exist
	for _, sub := range []string{"pages", "components", "templates"} {
		if err := os.MkdirAll(filepath.Join(outputDir, sub), 0o755); err != nil {
			return fmt.Errorf("creating output directory: %w", err)
		}
	}

	// Discover all .gastro files
	pageFiles, err := discoverFiles(filepath.Join(projectDir, "pages"), "pages")
	if err != nil {
		return fmt.Errorf("discovering pages: %w", err)
	}

	componentFiles, err := discoverFiles(filepath.Join(projectDir, "components"), "components")
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("discovering components: %w", err)
	}

	// Process each file, collecting metadata for render.go and routes.go
	allFiles := append(pageFiles, componentFiles...)
	var components []componentMeta
	var templates []templateMeta
	for _, relPath := range allFiles {
		absPath := filepath.Join(projectDir, relPath)
		result, err := compileFile(absPath, relPath, outputDir)
		if err != nil {
			return fmt.Errorf("compiling %s: %w", relPath, err)
		}
		templates = append(templates, result.template)
		if result.component != nil {
			components = append(components, *result.component)
		}
	}

	// Detect static asset directory
	hasStatic := false
	if info, err := os.Stat(filepath.Join(projectDir, "static")); err == nil && info.IsDir() {
		hasStatic = true
	}

	// Copy static/ into .gastro/ so //go:embed can find it.
	// Go's //go:embed does not follow symlinks to directories, so we copy.
	if hasStatic {
		if err := syncStatic(projectDir, outputDir); err != nil {
			return fmt.Errorf("syncing static: %w", err)
		}
	}

	// Generate embed.go with //go:embed directives
	if err := generateEmbedFile(hasStatic, outputDir); err != nil {
		return fmt.Errorf("generating embed: %w", err)
	}

	// Generate routes file
	routes := router.BuildRoutes(pageFiles)
	if err := generateRoutesFile(routes, templates, hasStatic, outputDir); err != nil {
		return fmt.Errorf("generating routes: %w", err)
	}

	// Generate render file for component rendering API
	if len(components) > 0 {
		if err := generateRenderFile(components, outputDir); err != nil {
			return fmt.Errorf("generating render: %w", err)
		}
	}

	return nil
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
	HasSlot       bool
}

// compileResult is returned by compileFile. It always contains template
// metadata and optionally component metadata (nil for pages).
type compileResult struct {
	template  templateMeta
	component *componentMeta
}

func compileFile(absPath, relPath, outputDir string) (compileResult, error) {
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

	// Check for slot usage before template transformation
	hasSlot := strings.Contains(file.TemplateBody, "<slot") || strings.Contains(file.TemplateBody, "{{ .Children }}")

	// Transform template body
	transformedBody, err := codegen.TransformTemplate(file.TemplateBody, file.Uses)
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

	// Generate handler Go code
	file.TemplateBody = transformedBody
	handlerCode, err := codegen.GenerateHandler(file, info)
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
	if !info.IsComponent {
		return compileResult{template: tmplMeta}, nil
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
		HasSlot:       hasSlot,
	}

	return compileResult{template: tmplMeta, component: compMeta}, nil
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

// copyDir recursively copies src into dst, preserving file modes.
func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		return copyFile(path, target, info.Mode())
	})
}

// copyFile copies a single file preserving its mode bits.
func copyFile(src, dst string, mode os.FileMode) error {
	content, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, content, mode)
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
	"html/template"
	"io/fs"
	"log"
	"net/http"

	gastroRuntime "github.com/andrioid/gastro/pkg/gastro"
)

// Suppress unused import warning for bytes (needed when templates have component uses).
var _ bytes.Buffer

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
		fm["__gastro_{{ .Name }}"] = {{ .FuncName }}
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
func __gastro_parseTemplate(name string, tfs fs.FS, userFuncs template.FuncMap) *template.Template {
	content, err := fs.ReadFile(tfs, __gastro_templateFile(name))
	if err != nil {
		log.Printf("gastro: reading template %s: %v", name, err)
		return template.New(name)
	}
	return template.Must(template.New(name).Funcs(__gastro_buildFuncMap(name, userFuncs)).Parse(string(content)))
}

// __gastro_getTemplate returns the parsed template for the given name.
// In dev mode, templates are re-parsed from disk on every call so that
// template changes are reflected immediately without restarting the server.
func __gastro_getTemplate(name string) *template.Template {
	if __gastro_isDev {
		tfs := gastroRuntime.GetTemplateFS(templateFS)
		return __gastro_parseTemplate(name, tfs, __gastro_userFuncs)
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

	// Parse all templates into the registry.
	tfs := gastroRuntime.GetTemplateFS(templateFS)
	__gastro_templateRegistry = make(map[string]*template.Template)
	for _, name := range []string{
{{- range .Templates}}
		"{{ .FuncName }}",
{{- end}}
	} {
		__gastro_templateRegistry[name] = __gastro_parseTemplate(name, tfs, cfg.funcs)
	}

	mux := http.NewServeMux()
{{- if .HasStatic}}
	staticFS := gastroRuntime.GetStaticFS(staticAssetFS)
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(staticFS)))
{{- end}}
{{- range .Routes}}
	mux.HandleFunc("{{ .Pattern }}", {{ .FuncName }})
{{- end}}
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
func (r *renderAPI) {{ .ExportedName }}({{ if .HasProps }}props {{ .ExportedName }}Props{{ if .HasSlot }}, {{ end }}{{ end }}{{ if .HasSlot }}children ...template.HTML{{ end }}) (string, error) {
	propsMap := map[string]any{
{{- range .PropsFields }}
		"{{ .Name }}": props.{{ .Name }},
{{- end }}
	}
{{- if .HasSlot }}
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
