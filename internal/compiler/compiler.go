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

	// Process each file, collecting component metadata for render.go
	allFiles := append(pageFiles, componentFiles...)
	var components []componentMeta
	for _, relPath := range allFiles {
		absPath := filepath.Join(projectDir, relPath)
		meta, err := compileFile(absPath, relPath, outputDir)
		if err != nil {
			return fmt.Errorf("compiling %s: %w", relPath, err)
		}
		if meta != nil {
			components = append(components, *meta)
		}
	}

	// Detect static asset directory
	hasStatic := false
	if info, err := os.Stat(filepath.Join(projectDir, "static")); err == nil && info.IsDir() {
		hasStatic = true
	}

	// Generate routes file
	routes := router.BuildRoutes(pageFiles)
	if err := generateRoutesFile(routes, hasStatic, outputDir); err != nil {
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

// componentMeta holds metadata about a component for render.go generation.
type componentMeta struct {
	ExportedName  string // e.g. "PostCard"
	FuncName      string // e.g. "componentPostCard"
	HasProps      bool
	PropsTypeName string // e.g. "componentPostCardProps"
	PropsFields   []codegen.StructField
	HasSlot       bool
}

func compileFile(absPath, relPath, outputDir string) (*componentMeta, error) {
	content, err := os.ReadFile(absPath)
	if err != nil {
		return nil, err
	}

	// Parse
	file, err := parser.Parse(relPath, string(content))
	if err != nil {
		return nil, err
	}

	// Analyze frontmatter
	info, err := codegen.AnalyzeFrontmatter(file.Frontmatter)
	if err != nil {
		return nil, err
	}

	// Check for slot usage before template transformation
	hasSlot := strings.Contains(file.TemplateBody, "<slot") || strings.Contains(file.TemplateBody, "{{ .Children }}")

	// Transform template body
	transformedBody, err := codegen.TransformTemplate(file.TemplateBody, file.Uses)
	if err != nil {
		return nil, err
	}

	// Write template file
	templateName := strings.TrimSuffix(relPath, ".gastro")
	templateName = strings.ReplaceAll(templateName, "/", "_")
	templatePath := filepath.Join(outputDir, "templates", templateName+".html")
	if err := os.WriteFile(templatePath, []byte(transformedBody), 0o644); err != nil {
		return nil, err
	}

	// Generate handler Go code
	file.TemplateBody = transformedBody
	handlerCode, err := codegen.GenerateHandler(file, info)
	if err != nil {
		return nil, err
	}

	// All generated .go files go flat in the output directory (same package)
	goFileName := strings.TrimSuffix(relPath, ".gastro")
	goFileName = strings.ReplaceAll(goFileName, "/", "_")
	goFileName = strings.ReplaceAll(goFileName, "[", "")
	goFileName = strings.ReplaceAll(goFileName, "]", "")
	goFileName = strings.ReplaceAll(goFileName, "-", "_")
	goFilePath := filepath.Join(outputDir, goFileName+".go")
	if err := os.WriteFile(goFilePath, []byte(handlerCode), 0o644); err != nil {
		return nil, err
	}

	// Collect component metadata for render.go generation
	if !info.IsComponent {
		return nil, nil
	}

	funcName := codegen.HandlerFuncName(relPath)
	_, hoistedTypes := codegen.HoistTypeDeclarations(file.Frontmatter)

	// Derive the unique props type name (same logic as GenerateHandler)
	propsTypeName := info.PropsTypeName
	if propsTypeName != "" {
		propsTypeName = funcName + strings.ToUpper(propsTypeName[:1]) + propsTypeName[1:]
	}

	meta := &componentMeta{
		ExportedName:  codegen.ExportedComponentName(funcName),
		FuncName:      funcName,
		HasProps:      info.PropsTypeName != "",
		PropsTypeName: propsTypeName,
		PropsFields:   codegen.ParseStructFields(hoistedTypes),
		HasSlot:       hasSlot,
	}

	return meta, nil
}

// routesData is the data passed to routesTmpl.
type routesData struct {
	Routes    []router.Route
	HasStatic bool
}

var routesTmpl = template.Must(template.New("routes").Parse(`// Code generated by gastro. DO NOT EDIT.
package gastro

import (
	"html/template"
	"net/http"

	gastroRuntime "github.com/andrioid/gastro/pkg/gastro"
)

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

// Routes returns an http.Handler with all gastro page routes and static assets.
func Routes(opts ...Option) http.Handler {
	cfg := &config{
		funcs: gastroRuntime.DefaultFuncs(),
	}
	for _, opt := range opts {
		opt(cfg)
	}

	_ = cfg // will be used for template funcs once templates support it

	mux := http.NewServeMux()
{{- if .HasStatic }}
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))
{{- end }}
{{- range .Routes }}
	mux.HandleFunc("{{ .Pattern }}", {{ .FuncName }})
{{- end }}
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

func generateRoutesFile(routes []router.Route, hasStatic bool, outputDir string) error {
	f, err := os.Create(filepath.Join(outputDir, "routes.go"))
	if err != nil {
		return err
	}
	defer f.Close()

	return routesTmpl.Execute(f, routesData{
		Routes:    routes,
		HasStatic: hasStatic,
	})
}
