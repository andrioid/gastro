package compiler

import (
	"bytes"
	"errors"
	"fmt"
	goformat "go/format"
	"os"
	"path/filepath"
	"slices"
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
	// Version is stamped into every generated file's header. Empty
	// stamps the running binary's version (buildinfo.Version).
	Version string
	// TypecheckProps (--typecheck-props) checks literal dict values
	// against component Props field types at generate time and emits
	// strict render-time type checks (gastroRuntime.CheckPropTypes) into
	// every component, so the generated code differs with the flag.
	TypecheckProps bool
}

// CompileResult contains the output of a successful compilation.
type CompileResult struct {
	// Warnings lists every diagnostic of the run; on success they are
	// all warnings.
	Warnings []Diagnostic
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
//
// Every file is compiled before anything is written, and every diagnostic
// of the run is collected. When any diagnostic is an error (or, under
// Strict, any diagnostic at all) Compile returns a *DiagnosticsError and
// leaves outputDir untouched, so the previous generation keeps working.
func Compile(projectDir, outputDir string, opts CompileOptions) (*CompileResult, error) {
	absProjectDir, err := filepath.Abs(projectDir)
	if err != nil {
		return nil, fmt.Errorf("resolving project dir: %w", err)
	}
	if err := checkGoDirective(absProjectDir); err != nil {
		return nil, err
	}

	header := codegen.GeneratedHeader(opts.Version)

	// Discover all .gastro files
	pageFiles, err := discoverFiles(filepath.Join(projectDir, "pages"), "pages")
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("discovering pages: %w", err)
	}

	componentFiles, err := discoverFiles(filepath.Join(projectDir, "components"), "components")
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("discovering components: %w", err)
	}
	allFiles := append(pageFiles, componentFiles...)

	// Pre-pass: gather every component's Props field list so the per-file
	// pass can validate (dict ...) keys against the destination schema.
	// Cheap (parse + frontmatter analyze + struct hoist; no template work).
	propsByPath, knownComponents, err := gatherComponentSchemas(componentFiles, projectDir)
	if err != nil {
		return nil, fmt.Errorf("gathering component schemas: %w", err)
	}

	// Pre-pass: detect component name collisions. Two component files
	// that produce the same ExportedName (e.g. components/post-card.gastro
	// and components/post/card.gastro both producing "PostCard") would
	// yield duplicate type and method names in render.go and overwrite
	// each other's per-file Go output.
	diags := findComponentNameCollisions(componentFiles)

	// Pre-pass: detect page-id collisions across pages and components.
	// DerivePageID is intentionally lossy (collapses non-ident chars to
	// underscores) so two visually-distinct paths can sanitise to the
	// same id. With mangled hoisted decls, that would yield duplicate
	// package-level symbols.
	diags = append(diags, findPageIDCollisions(pageFiles, componentFiles)...)
	diags = append(diags, findGeneratedIdentifierClashes(componentFiles)...)

	outputs := make([]*fileOutput, 0, len(allFiles))
	for _, relPath := range allFiles {
		absPath := filepath.Join(projectDir, relPath)
		out, fileDiags := compileFile(absPath, relPath, absProjectDir, opts, propsByPath, knownComponents)
		diags = append(diags, fileDiags...)
		if out != nil {
			outputs = append(outputs, out)
		}
	}

	if opts.Strict {
		for i := range diags {
			diags[i].Severity = codegen.SeverityError
		}
	}
	sortDiagnostics(diags)
	if slices.ContainsFunc(diags, Diagnostic.IsError) {
		return nil, &DiagnosticsError{Diagnostics: diags}
	}

	// Every file compiled cleanly; only now touch outputDir.
	for _, sub := range []string{"pages", "components", "templates"} {
		if err := os.MkdirAll(filepath.Join(outputDir, sub), 0o755); err != nil {
			return nil, fmt.Errorf("creating output directory: %w", err)
		}
	}

	var components []componentMeta
	var templates []templateMeta
	var allEmbedDeps []string
	for _, out := range outputs {
		templatePath := filepath.Join(outputDir, "templates", out.template.TemplateFile)
		if err := os.WriteFile(templatePath, []byte(out.templateBody), 0o644); err != nil {
			return nil, fmt.Errorf("writing template for %s: %w", out.relPath, err)
		}
		// All generated .go files go flat in the output directory (same
		// package).
		if err := writeGoFile(filepath.Join(outputDir, outputGoFile(out.relPath)), []byte(out.goCode)); err != nil {
			return nil, fmt.Errorf("writing handler for %s: %w", out.relPath, err)
		}
		templates = append(templates, out.template)
		if out.component != nil {
			components = append(components, *out.component)
		}
		allEmbedDeps = append(allEmbedDeps, out.embedDeps...)
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
	if err := generateEmbedFile(header, hasStatic, outputDir); err != nil {
		return nil, fmt.Errorf("generating embed: %w", err)
	}

	// Generate routes file
	routes := router.BuildRoutes(pageFiles)
	if err := generateRoutesFile(header, routes, templates, components, hasStatic, outputDir); err != nil {
		return nil, fmt.Errorf("generating routes: %w", err)
	}

	// Generate render file for component rendering API
	if len(components) > 0 {
		if err := generateRenderFile(header, components, outputDir); err != nil {
			return nil, fmt.Errorf("generating render: %w", err)
		}
	}

	// Prune stale outputs: any generated .go (per-file handlers, embed.go,
	// routes.go, render.go) or template .html left over from a source file
	// that no longer exists. Without this a deleted page/component leaves
	// an orphaned handler in package gastro that still references removed
	// symbols and breaks `go build` until `.gastro/` is wiped by hand.
	// Static assets are pruned separately by syncStatic.
	keep := map[string]struct{}{
		"embed.go":  {},
		"routes.go": {},
	}
	if len(components) > 0 {
		keep["render.go"] = struct{}{}
	}
	for _, relPath := range allFiles {
		keep[outputGoFile(relPath)] = struct{}{}
		keep[filepath.Join("templates", outputTemplateFile(relPath))] = struct{}{}
	}
	if err := pruneStaleOutputs(outputDir, keep); err != nil {
		return nil, fmt.Errorf("pruning stale outputs: %w", err)
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
		Warnings:       diags,
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
func findPageIDCollisions(pageFiles, componentFiles []string) []Diagnostic {
	var warnings []Diagnostic
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
				warnings = append(warnings, Diagnostic{
					File:     relPath,
					Line:     1,
					Col:      1,
					Severity: codegen.SeverityWarning,
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
func findComponentNameCollisions(componentFiles []string) []Diagnostic {
	if len(componentFiles) < 2 {
		return nil
	}
	seen := make(map[string]string, len(componentFiles)) // ExportedName -> relPath
	var warnings []Diagnostic
	for _, relPath := range componentFiles {
		exported := codegen.ExportedComponentName(codegen.HandlerFuncName(relPath))
		if first, ok := seen[exported]; ok {
			warnings = append(warnings, Diagnostic{
				File:     relPath,
				Line:     1,
				Col:      1,
				Severity: codegen.SeverityWarning,
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

// generatedIdentifiers are the exported package-level names every
// generated package declares in routes.go and render.go.
var generatedIdentifiers = map[string]bool{
	"New": true, "Routes": true, "Router": true, "Option": true,
	"WithDevMode": true, "WithFuncs": true, "WithClassMerger": true, "WithDeps": true,
	"WithHandler": true, "WithMiddleware": true, "WithRequestFuncs": true, "WithErrorHandler": true,
}

// findGeneratedIdentifierClashes reports components whose exported name
// would redeclare a package-level identifier of the generated package:
// each component exports a Component value named after it, next to the
// generated API and every component's XProps type.
func findGeneratedIdentifierClashes(componentFiles []string) []Diagnostic {
	propsTypes := make(map[string]string, len(componentFiles)) // "XProps" -> relPath
	for _, relPath := range componentFiles {
		propsTypes[codegen.ExportedComponentName(codegen.HandlerFuncName(relPath))+"Props"] = relPath
	}
	var diags []Diagnostic
	for _, relPath := range componentFiles {
		exported := codegen.ExportedComponentName(codegen.HandlerFuncName(relPath))
		var clash string
		switch {
		case generatedIdentifiers[exported]:
			clash = "the generated router API"
		case propsTypes[exported] != "":
			clash = fmt.Sprintf("the props type of %q", propsTypes[exported])
		default:
			continue
		}
		diags = append(diags, Diagnostic{
			File:     relPath,
			Line:     1,
			Col:      1,
			Severity: codegen.SeverityError,
			Message: fmt.Sprintf(
				"component exported name %q clashes with %s in the generated package; rename the file",
				exported, clash,
			),
		})
	}
	return diags
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

// fileOutput is everything compileFile produces for one source file. It
// always contains template metadata and optionally component metadata
// (nil for pages).
type fileOutput struct {
	relPath      string
	template     templateMeta
	component    *componentMeta
	templateBody string // transformed template, written to templates/
	goCode       string // handler source, written to the output root
	embedDeps    []string
}

// gatherComponentSchemas reads each component file's frontmatter, extracts
// its hoisted Props type, and returns two maps keyed by relative path
// (the same path that appears in `import X "components/foo.gastro"`):
//
//   - propsByPath: components that declare a Props struct, mapped to
//     their field list. Files without a Props struct are omitted; the
//     validator treats a missing key as "no schema, skip validation".
//   - knownComponents: every discovered component (propful AND propless),
//     mapped to true. The compiler uses this to compute `UseInfo.Propless`
//     and to let the dict-key validator distinguish "propless component
//     called bare — fine" from "propful component called bare — error".
//
// Delegates to codegen.ScanComponents (the canonical project-walker)
// and then filters down to the subset the compiler discovered via
// discoverFiles. The filter is necessary because discoverFiles and
// codegen.ScanComponents have slightly different walk policies (the
// former includes hidden directories, the latter skips them); the
// per-file compile pass uses discoverFiles' list, so the schema map
// keyed by that list keeps everything aligned.
func gatherComponentSchemas(componentFiles []string, projectDir string) (map[string][]codegen.StructField, map[string]bool, error) {
	schemas, err := codegen.ScanComponents(projectDir)
	if err != nil {
		return nil, nil, fmt.Errorf("scanning components: %w", err)
	}
	discovered := make(map[string]bool, len(componentFiles))
	for _, p := range componentFiles {
		discovered[p] = true
	}
	propsByPath := make(map[string][]codegen.StructField, len(schemas))
	knownComponents := make(map[string]bool, len(schemas))
	for _, s := range schemas {
		if !discovered[s.RelPath] {
			continue
		}
		knownComponents[s.RelPath] = true
		if len(s.PropsFields) == 0 {
			continue
		}
		propsByPath[s.RelPath] = s.PropsFields
	}
	// Frontmatter-less files in components/ are valid propless components
	// (the compiler infers IsComponent for them later). ScanComponents only
	// returns files it could parse a frontmatter from, so they're missing
	// from `schemas` — backfill them here so knownComponents is complete.
	for _, p := range componentFiles {
		knownComponents[p] = true
	}
	return propsByPath, knownComponents, nil
}

// compileFile compiles one .gastro file in memory. It returns the file's
// output, or nil when any error-severity diagnostic was found, plus every
// diagnostic for the file. Nothing is written: Compile writes the outputs
// once the whole project compiled cleanly.
func compileFile(absPath, relPath, absProjectDir string, opts CompileOptions, propsByPath map[string][]codegen.StructField, knownComponents map[string]bool) (*fileOutput, []Diagnostic) {
	content, err := os.ReadFile(absPath)
	if err != nil {
		return nil, []Diagnostic{{File: relPath, Line: 1, Col: 1, Severity: codegen.SeverityError, Message: err.Error()}}
	}
	pos := newSourcePositions(relPath, string(content))

	file, err := parser.Parse(relPath, string(content))
	if err != nil {
		// parser.Parse prefixes the file name, which the diagnostic
		// already carries.
		msg := err.Error()
		if inner := errors.Unwrap(err); inner != nil {
			msg = inner.Error()
		}
		return nil, []Diagnostic{pos.at(1, 1, codegen.SeverityError, msg)}
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
	embedCtx := codegen.EmbedContext{SourceFile: absPath, ModuleRoot: moduleRoot}
	rewrittenFM, embedDeps, err := codegen.ProcessEmbedDirectives(file.Frontmatter, embedCtx)
	if err != nil {
		return nil, embedDiagnostics(pos, file, embedCtx, err)
	}
	// The rewrite keeps line numbers, so frontmatter-relative lines
	// reported below still map through file.FrontmatterSourceLine.
	file.Frontmatter = rewrittenFM

	info, err := codegen.AnalyzeFrontmatter(file.Frontmatter)
	if err != nil {
		var hoistErr *codegen.HoistError
		if errors.As(err, &hoistErr) {
			return nil, []Diagnostic{pos.atLine(file.FrontmatterSourceLine(hoistErr.Line), codegen.SeverityError, hoistErr.Message())}
		}
		return nil, []Diagnostic{pos.at(file.FrontmatterLine, 1, codegen.SeverityError, err.Error())}
	}

	var diags []Diagnostic
	for _, w := range info.Warnings {
		diags = append(diags, pos.atLine(file.FrontmatterSourceLine(w.Line), codegen.SeverityWarning, w.Message))
	}

	// Validate the template as written: un-imported components (issue
	// #39) and (dict ...) keys against each component's Props schema.
	// Positions are template-body relative; the body starts at column 1
	// of file.TemplateBodyLine.
	validateOpts := codegen.ValidateDictKeysOptions{TypecheckProps: opts.TypecheckProps}
	for _, d := range codegen.ValidateTemplate(file.TemplateBody, file.Uses, propsByPath, validateOpts) {
		diags = append(diags, pos.at(file.TemplateBodyLine+d.StartLine-1, d.StartCol, d.Severity, d.Message))
	}
	if slices.ContainsFunc(diags, Diagnostic.IsError) {
		return nil, diags
	}

	// Check for children usage before template transformation. Goes
	// through codegen.TemplateRendersChildren so the LSP shadow's
	// componentScan and this compile pass can never disagree on whether
	// a component renders children — see internal/codegen/template.go.
	hasChildren := codegen.TemplateRendersChildren(file.TemplateBody)

	transformedBody, err := codegen.TransformTemplate(file.TemplateBody, file.Uses)
	if err != nil {
		return nil, append(diags, pos.at(file.TemplateBodyLine, 1, codegen.SeverityError, err.Error()))
	}

	// Determine component status: explicit gastro.Props() call, or
	// directory-based inference for frontmatter-less files.
	isComponent := info.IsComponent
	if strings.HasPrefix(relPath, "components/") && !info.IsComponent {
		isComponent = true
	}

	file.TemplateBody = transformedBody
	handlerCode, err := codegen.GenerateHandler(file, info, isComponent, codegen.GenerateOptions{MangleHoisted: true, Version: opts.Version, TypecheckProps: opts.TypecheckProps})
	if err != nil {
		return nil, append(diags, pos.at(file.FrontmatterLine, 1, codegen.SeverityError, err.Error()))
	}

	funcName := codegen.HandlerFuncName(relPath)

	// Build UseInfo for this template's component dependencies. The
	// Propless flag drives FuncMap codegen: propless components get a
	// variadic wrapper so bare calls like `{{ Icon }}` work; propful
	// components keep the direct method assignment so bare-calling them
	// is a parse/validation error rather than a silent zero-prop render.
	var uses []codegen.UseInfo
	for _, u := range file.Uses {
		_, hasProps := propsByPath[u.Path]
		known := knownComponents[u.Path]
		uses = append(uses, codegen.UseInfo{
			Name:     u.Name,
			FuncName: codegen.HandlerFuncName(u.Path),
			// Treat unknown imports (typo, missing file) as propful so we
			// don't generate a variadic wrapper for a method that won't
			// exist. The downstream Go compile will surface the missing
			// method clearly.
			Propless: known && !hasProps,
		})
	}

	// outputTemplateFile / outputGoFile mirror the prune pass's
	// keep-set transform so a live output is never deleted.
	out := &fileOutput{
		relPath:      relPath,
		templateBody: transformedBody,
		goCode:       handlerCode,
		embedDeps:    embedDeps,
		template: templateMeta{
			FuncName:     funcName,
			TemplateFile: outputTemplateFile(relPath),
			Uses:         uses,
		},
	}

	// Pages have no component metadata
	if !isComponent {
		return out, diags
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

	propsFields := codegen.ParseStructFields(hoistedTypes)
	// The attribute-forwarding bag (a field of type gastro.Attrs) is
	// hoisted verbatim from frontmatter, but render.go is itself package
	// gastro and imports the runtime under the gastroRuntime alias.
	// Normalize the type so the inline XProps struct compiles (#41).
	for i := range propsFields {
		if propsFields[i].Type == "gastro.Attrs" {
			propsFields[i].Type = "gastroRuntime.Attrs"
		}
	}

	out.component = &componentMeta{
		ExportedName:  codegen.ExportedComponentName(funcName),
		FuncName:      funcName,
		HasProps:      info.PropsTypeName != "",
		PropsTypeName: propsTypeName,
		PropsFields:   propsFields,
		HasChildren:   hasChildren,
	}
	return out, diags
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

// outputGoFile maps a source .gastro relative path (e.g.
// "pages/blog/[slug].gastro") to the basename of the flat per-file Go
// output written at the .gastro/ root (e.g. "pages_blog_slug.go").
// compileFile writes the handler under this name and Compile's prune
// pass builds its keep-set from it, so both share one transform.
func outputGoFile(relPath string) string {
	name := strings.TrimSuffix(relPath, ".gastro")
	name = strings.ReplaceAll(name, "/", "_")
	name = strings.ReplaceAll(name, "[", "")
	name = strings.ReplaceAll(name, "]", "")
	name = strings.ReplaceAll(name, "-", "_")
	return name + ".go"
}

// OutputGoFile is the exported form of outputGoFile. cmd/gastro uses it to
// reconstruct the generated→source file association when remapping
// `go build` diagnostics back to the .gastro source they came from.
func OutputGoFile(relPath string) string { return outputGoFile(relPath) }

// outputTemplateFile maps a source .gastro relative path to the basename
// of the template HTML written under .gastro/templates/ (e.g.
// "pages_blog_[slug].html"). Unlike outputGoFile it keeps bracket and
// dash characters because the name is matched verbatim against
// templateMeta.TemplateFile at runtime.
func outputTemplateFile(relPath string) string {
	name := strings.TrimSuffix(relPath, ".gastro")
	name = strings.ReplaceAll(name, "/", "_")
	return name + ".html"
}

// pruneStaleOutputs removes generated files under outputDir that the
// current Compile run did not produce. keep holds the output-relative
// names that must survive: root-level .go files by basename, and template
// .html files keyed as filepath.Join("templates", name).
//
// Scope is deliberately narrow — only root-level *.go and templates/*.html,
// the two file classes a deleted .gastro source can orphan. Transient dev
// artifacts (the dev-server binary, .reload IPC file, *.go.tmp from an
// in-flight atomic write) and the static/ copy (managed by syncStatic)
// carry other names and are never touched.
//
// Runs last, after routes.go/render.go are rewritten to drop references to
// the removed file, so there is no window where a live file points at a
// pruned one.
func pruneStaleOutputs(outputDir string, keep map[string]struct{}) error {
	entries, err := os.ReadDir(outputDir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		if _, ok := keep[e.Name()]; ok {
			continue
		}
		if err := os.Remove(filepath.Join(outputDir, e.Name())); err != nil {
			return err
		}
	}

	tmplDir := filepath.Join(outputDir, "templates")
	tmplEntries, err := os.ReadDir(tmplDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, e := range tmplEntries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".html") {
			continue
		}
		if _, ok := keep[filepath.Join("templates", e.Name())]; ok {
			continue
		}
		if err := os.Remove(filepath.Join(tmplDir, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

// embedData is the data passed to embedTmpl.
type embedData struct {
	Header    string
	HasStatic bool
}

var embedTmpl = template.Must(template.New("embed").Parse(`{{ .Header }}
package gastro

import "embed"

//go:embed templates/*
var templateFS embed.FS
{{- if .HasStatic }}

//go:embed static/*
var staticAssetFS embed.FS
{{- end }}
`))

func generateEmbedFile(header string, hasStatic bool, outputDir string) error {
	var buf bytes.Buffer
	if err := embedTmpl.Execute(&buf, embedData{Header: header, HasStatic: hasStatic}); err != nil {
		return err
	}
	return writeGoFile(filepath.Join(outputDir, "embed.go"), buf.Bytes())
}

// routesData is the data passed to routesTmpl.
type routesData struct {
	Header    string
	Routes    []router.Route
	Templates []templateMeta
	// ComponentNames is the sorted list of every component's exported
	// name (e.g. "Layout", "Card"). Emitted into the generated routes.go
	// as a constant slice so __gastro_probeBinders can check for binder
	// names that would shadow a component dispatcher. We use exported
	// names — not Use names — so components that no template currently
	// imports are still protected from future shadow drift.
	ComponentNames []string
	HasStatic      bool
}

var routesTmpl = template.Must(template.New("routes").Parse(`{{ .Header }}
package gastro

import (
	"bytes"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/http/httptest"
	"reflect"
	"regexp"
	"runtime/debug"
	"strings"

	gastroRuntime "github.com/andrioid/gastro/pkg/gastro"
)

// Suppress unused import warning for bytes (needed when templates have component uses).
var _ bytes.Buffer

// Suppress unused import warnings for error-enhancement dependencies.
var _ = fmt.Errorf
var _ = regexp.Compile
var _ = strings.Contains
var _ = reflect.TypeOf
var _ = httptest.NewRequest
var _ = debug.Stack
var _ io.Writer

// Option configures the generated router. Pass options to New().
type Option func(*config)

type config struct {
	funcs        template.FuncMap
	deps         map[reflect.Type]any
	handlers     []handlerEntry
	middleware   []middlewareEntry
	devMode      *bool // nil = use GASTRO_DEV env var; non-nil = override
	errorHandler gastroRuntime.PageErrorHandler
	binders      []func(*http.Request) template.FuncMap
	classMerger  gastroRuntime.ClassMerger
	// userFuncNames records names registered via WithFuncs so the probe
	// can distinguish them from built-in defaults when reporting
	// collisions. Tracked separately because cfg.funcs starts as
	// DefaultFuncs() and WithFuncs merges on top, so post-merge we can't
	// tell which source contributed a given name.
	userFuncNames map[string]bool
}

// middlewareEntry pairs a route-pattern matcher with the middleware to
// install. Stored as a slice so multiple WithMiddleware calls for the
// same pattern compose in registration order — the first registered
// middleware ends up outermost.
type middlewareEntry struct {
	pattern string
	fn      gastroRuntime.MiddlewareFunc
}

// handlerEntry is one WithHandler registration, kept in call order so
// conflict panics name the earlier registration first.
type handlerEntry struct {
	pattern string
	h       http.Handler
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
		if c.userFuncNames == nil {
			c.userFuncNames = map[string]bool{}
		}
		for k, v := range fm {
			c.funcs[k] = v
			c.userFuncNames[k] = true
		}
	}
}

// WithClassMerger plugs a class-merging function (e.g. a Tailwind-aware
// merger like tailwind-merge-go) into the attrs helper and the twMerge
// template func. The dependency lives in your module — gastro core ships
// only DefaultClassMerger (plain concatenation, no conflict resolution).
//
//	router := gastro.New(gastro.WithClassMerger(twmerge.Merge))
func WithClassMerger(m gastroRuntime.ClassMerger) Option {
	return func(c *config) { c.classMerger = m }
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

// WithHandler registers h for an http.ServeMux pattern next to the
// generated page routes. The pattern may carry a method ("POST
// /tasks/{id}/place") or not ("/healthz"). Pages are GET views; commands
// are ordinary handlers registered here, sharing the router's middleware
// and deps (gastro.From[T](r.Context()) works inside h).
//
// A pattern identical to a page's ("GET /board") or to the static-asset
// prefix ("GET /static/") replaces that route's handler. Any other
// pattern adds a route. A registration that http.ServeMux rejects as
// conflicting panics in New() with a message naming both sources.
func WithHandler(pattern string, h http.Handler) Option {
	return func(c *config) {
		c.handlers = append(c.handlers, handlerEntry{pattern: pattern, h: h})
	}
}

// WithMiddleware wraps the handler of every route — generated page
// routes and WithHandler routes alike — whose path matches the supplied
// http.ServeMux pattern. Patterns use Go's stdlib pattern syntax:
// "/counter" for an exact path, "/admin/{path...}" for a subtree
// wildcard, "/blog/{slug}" to match a parametrised route.
//
// Patterns are path-only — there is no method scoping. Middleware that
// must only run for, say, POST should branch on r.Method internally.
// Method-qualified routes ("POST /tasks/{id}/place") are matched by
// their path part.
//
// Validation: at New() time the pattern must match at least one known
// route, probed via gastroRuntime.PatternMatchesAnyRoute. A pattern that
// matches nothing panics with a descriptive error.
//
// Composition: multiple WithMiddleware calls for overlapping patterns
// compose in registration order. The first call ends up outermost
// (runs first on the request, last on the response). When WithHandler
// replaces a page route, the middleware wraps the replacement.
func WithMiddleware(pattern string, fn gastroRuntime.MiddlewareFunc) Option {
	return func(c *config) {
		c.middleware = append(c.middleware, middlewareEntry{pattern: pattern, fn: fn})
	}
}

// WithRequestFuncs registers a function-map binder that runs once per
// request. The returned FuncMap is merged with the static FuncMap built
// from WithFuncs before the page (or a Render of c.With(r)) executes.
//
// Multiple WithRequestFuncs calls compose. Helper names must be unique
// across the union of built-ins, WithFuncs, and all binders — including
// any name that shadows a Gastro built-in. Duplicates panic at New().
//
// Binders are probed at New() with a synthetic *http.Request whose
// context carries no adopter-installed values. Binders must therefore:
//   - return a stable key set independent of request state, and
//   - not panic during top-level execution when context accessors return
//     zero values (e.g. an i18n FromCtx helper must tolerate a missing
//     locale).
//
// Closures and method values inside the returned map are NOT invoked
// during probing; only the map's keys are read.
//
// If a binder panics at request time (not probe time), Gastro recovers
// the panic, logs it with the binder's registration index, and dispatches
// to the configured error handler (default: 500 response). One bad
// binder cannot crash the server.
//
// Zero-cost when unused: if no WithRequestFuncs option is registered,
// page handlers fall through to the same direct-Execute path as before.
func WithRequestFuncs(binder func(*http.Request) template.FuncMap) Option {
	if binder == nil {
		panic("gastro: WithRequestFuncs: nil binder")
	}
	return func(c *config) {
		c.binders = append(c.binders, binder)
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
	binders      []func(*http.Request) template.FuncMap
	// binderKeys is the union of helper names contributed by every
	// registered binder. It's discovered at probe time and used to seed
	// placeholder funcs into parse-time FuncMaps so templates that
	// reference request-aware helpers parse successfully at New(). The
	// real funcs are applied later via Funcs() at request time.
	binderKeys map[string]bool
}

// __gastro_binderPlaceholder is the variadic stub installed for every
// discovered binder helper name at parse time. It is never invoked at
// runtime: the request-aware execution path replaces it via Funcs()
// before Execute. If somehow invoked (e.g. a stale Clone), it returns
// nil and the template renders an empty string for that call.
func __gastro_binderPlaceholder(args ...any) any { return nil }

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

// Render renders component c with typed props and returns its HTML. Each
// file under components/ has a Component value in this package (see
// render.go):
//
//	html, err := router.Render(gastro.Card, gastro.CardProps{Title: "Hello"})
//
// The props struct goes straight to the component, with no map round-trip;
// props of another component's type fail to compile. Bind the render to a
// request with c.With(r) so request-aware helpers (WithRequestFuncs) see
// its state.
func (__r *Router) Render[P any](c gastroRuntime.Component[P], props P) (template.HTML, error) {
	return gastroRuntime.RenderComponent(__r, c, props)
}

// __gastro_componentNames is the sorted list of every component's
// exported name in this project. Generated from the codegen pipeline
// so __gastro_probeBinders can reject any WithRequestFuncs binder whose
// helper name would shadow a component dispatcher (silent shadowing
// would mean a template's bare-component call resolves to a binder
// helper instead, depending on registration order — brittle, hard to
// debug).
var __gastro_componentNames = []string{
{{- range .ComponentNames }}
	"{{ . }}",
{{- end }}
}

// __gastro_componentClosuresForRequest returns request-aware dispatcher
// closures for every child component used by the named template. Each
// closure captures r and injects the __gastro_request sentinel into the
// child's propsMap before calling the bare componentX method. The
// component method (generated by codegen) detects the sentinel and
// routes its own Execute through __gastro_renderComponent — so a page
// invoking a bare child component sees binder helpers all the way down
// the tree.
//
// Returns an empty FuncMap when the template has no Uses; callers merge
// unconditionally. Mirrors the per-template switch shape of
// __gastro_buildFuncMap so adding a component automatically gets
// request-aware nested dispatch with no extra wiring.
func (__r *Router) __gastro_componentClosuresForRequest(name string, r *http.Request) template.FuncMap {
	fm := template.FuncMap{}
	switch name {
{{- range .Templates}}{{- if .Uses}}{{$fn := .FuncName}}
	case "{{$fn}}":
{{- range .Uses}}
{{- if .Propless}}
		// Propless component: variadic wrapper so bare calls work.
		fm["{{ .Name }}"] = func(p ...map[string]any) (template.HTML, error) {
			var props map[string]any
			if len(p) > 0 && p[0] != nil {
				props = p[0]
			} else {
				props = map[string]any{}
			}
			props["__gastro_request"] = r
			return __r.{{ .FuncName }}(props)
		}
{{- else}}
		fm["{{ .Name }}"] = func(p map[string]any) (template.HTML, error) {
			if p == nil {
				p = map[string]any{}
			}
			p["__gastro_request"] = r
			return __r.{{ .FuncName }}(p)
		}
{{- end}}
{{- end}}
{{- end}}{{end}}
	}
	return fm
}

// __gastro_buildFuncMap constructs the FuncMap for the named template,
// merging user functions with per-template component wiring and any
// placeholder stubs for request-aware helpers discovered at probe time.
func (__r *Router) __gastro_buildFuncMap(name string, userFuncs template.FuncMap) template.FuncMap {
	fm := template.FuncMap{}
	for k, v := range userFuncs {
		fm[k] = v
	}
	// Seed placeholder stubs for request-aware helper names so the
	// template parser accepts references like t or csrfField at New()
	// time. The real funcs are supplied per request via
	// __gastro_renderPage's .Funcs() call.
	for k := range __r.binderKeys {
		if _, exists := fm[k]; !exists {
			fm[k] = __gastro_binderPlaceholder
		}
	}
	switch name {
{{- range .Templates}}{{- if .Uses}}{{$fn := .FuncName}}
	case "{{$fn}}":
{{- range .Uses}}
{{- if .Propless}}
		// Propless component: variadic wrapper so bare calls work.
		fm["{{ .Name }}"] = func(p ...map[string]any) (template.HTML, error) {
			var props map[string]any
			if len(p) > 0 && p[0] != nil {
				props = p[0]
			} else {
				props = map[string]any{}
			}
			return __r.{{ .FuncName }}(props)
		}
{{- else}}
		fm["{{ .Name }}"] = __r.{{ .FuncName }}
{{- end}}
{{- end}}
		// Errors propagate so a component failing inside a wrap slot
		// (a dev-mode props violation) fails the enclosing render.
		fm["__gastro_render_children"] = func(n string, data any) (template.HTML, error) {
			var __buf bytes.Buffer
			if err := __r.__gastro_getTemplate("{{$fn}}").ExecuteTemplate(&__buf, n, data); err != nil {
				return "", err
			}
			return template.HTML(__buf.String()), nil
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

// __gastro_renderPage dispatches a page render. It is the single entry
// point from generated page handlers; the same call site is used whether
// or not WithRequestFuncs binders are registered.
//
//   - When no binders are registered, this falls through to the same
//     __gastro_getTemplate(name).Execute(w, data) call as pre-binder
//     code. No Clone, no per-request FuncMap rebuild.
//   - When binders are registered, each binder is invoked with r, its
//     returned funcs are merged onto a Cloned template (prod) or applied
//     to the freshly-parsed dev template (dev), and Execute runs against
//     that per-request template.
//
// Panics inside binders or template execution are recovered, logged with
// the binder index, and returned as an error so the page handler can
// dispatch through __gastro_handleError. Generated handlers call this
// method and route any non-nil error through their installed error
// handler.
func (__r *Router) __gastro_renderPage(name string, w http.ResponseWriter, r *http.Request, data any) (rerr error) {
	if len(__r.binders) == 0 {
		return __r.__gastro_getTemplate(name).Execute(w, data)
	}
	var buf bytes.Buffer
	if err := __r.__gastro_executeForRequest(name, r, &buf, data); err != nil {
		return err
	}
	_, err := w.Write(buf.Bytes())
	return err
}

// __gastro_renderComponent is the request-aware sibling of
// __gastro_renderPage used by components rendered with a request bound
// (router.Render(X.With(r), …) or a nested call from a request-aware
// page). It always uses the per-request FuncMap path
// (writes into the supplied bytes.Buffer so the caller can return
// template.HTML). When no binders are registered the per-request path
// still works — it just produces an empty extras FuncMap and the
// behaviour collapses to a Clone-and-Execute.
func (__r *Router) __gastro_renderComponent(name string, r *http.Request, buf *bytes.Buffer, data any) error {
	return __r.__gastro_executeForRequest(name, r, buf, data)
}

// __gastro_executeForRequest is the shared request-aware execution path.
// It builds the per-request FuncMap (built-in + WithFuncs + binders +
// request-aware component dispatchers), either Clones the cached
// production template or applies funcs to the per-request dev-mode
// parse, and Executes into the supplied writer.
//
// Nested component propagation: when this template invokes a child
// component via a bare component call, the FuncMap entry for that
// component is overridden here with a closure that injects the
// __gastro_request sentinel into the child's propsMap. The child
// component method detects the sentinel and routes its own Execute
// back through this same path — so binder helpers (t, csrfField,
// cspNonce, …) resolve correctly all the way down the component tree.
//
// Wrap-block propagation: a wrap directive desugars to a call invoking
// __gastro_render_children to render slot content. We install a closure
// here AFTER template selection so it captures the per-request tmpl
// (clone in prod, fresh parse in dev). Slot bodies consequently inherit
// the same per-request FuncMap as the enclosing template.
//
// Cost: each nested component render does its own Clone (prod) or fresh
// parse (dev), so a page with N nested components costs O(N) Clones.
// Measured on Apple M3 / Go 1.26, a typical component template clones
// in roughly 1.2 µs and ~3.5 KB per level — well under the noise floor
// of HTTP + middleware + Execute. See BenchmarkNestedClone for the
// per-depth roll-up.
//
// Panics inside binders or Execute are recovered, logged with the
// panicking binder's registration index when known, and returned as an
// error so the caller (page handler or component method) can decide how
// to surface the failure to the client.
func (__r *Router) __gastro_executeForRequest(name string, r *http.Request, w io.Writer, data any) (rerr error) {
	defer func() {
		if rec := recover(); rec != nil {
			log.Printf("gastro: WithRequestFuncs panic on %s %s: %v\n%s", r.Method, r.URL.Path, rec, debug.Stack())
			rerr = fmt.Errorf("gastro: WithRequestFuncs panic on %s %s: %v", r.Method, r.URL.Path, rec)
		}
	}()

	extra := template.FuncMap{}
	for i, b := range __r.binders {
		fm := __r.__gastro_callBinder(i, b, r)
		for k, v := range fm {
			extra[k] = v
		}
	}
	// Component-dispatcher closures: override the parse-time bare
	// __r.componentX entries with closures that thread r through to the
	// child render, so nested bare-component invocations see request-
	// aware helpers.
	for k, v := range __r.__gastro_componentClosuresForRequest(name, r) {
		extra[k] = v
	}

	var tmpl *template.Template
	if __r.isDev {
		effective := template.FuncMap{}
		for k, v := range __r.userFuncs {
			effective[k] = v
		}
		for k, v := range extra {
			effective[k] = v
		}
		tfs := gastroRuntime.GetTemplateFS(templateFS)
		parsed, err := __r.__gastro_parseTemplate(name, tfs, effective)
		if err != nil {
			return err
		}
		tmpl = parsed
	} else {
		cached := __r.registry[name]
		if cached == nil {
			return fmt.Errorf("gastro: no template registered for %q", name)
		}
		cloned, err := cached.Clone()
		if err != nil {
			return fmt.Errorf("gastro: cloning template %q: %w", name, err)
		}
		tmpl = cloned
	}

	// Install the wrap-block child renderer AFTER tmpl is selected so the
	// closure captures the right *Template instance. In dev mode that's
	// the fresh parse; in prod it's the clone. Both inherit the per-
	// request FuncMap below, so wrap-slot bodies see request-aware
	// helpers. (The parse-time wrap closure in __gastro_buildFuncMap
	// targets the registry copy, which doesn't carry per-request funcs;
	// overriding here is what makes nested wrap propagation work.)
	extra["__gastro_render_children"] = func(n string, d any) (template.HTML, error) {
		var buf bytes.Buffer
		if err := tmpl.ExecuteTemplate(&buf, n, d); err != nil {
			return "", err
		}
		return template.HTML(buf.String()), nil
	}
	tmpl.Funcs(extra)
	return tmpl.Execute(w, data)
}

// __gastro_callBinder invokes a binder with recover-and-rethrow semantics
// so a binder panic is attributable to its registration index in logs.
func (__r *Router) __gastro_callBinder(i int, b func(*http.Request) template.FuncMap, r *http.Request) (fm template.FuncMap) {
	defer func() {
		if rec := recover(); rec != nil {
			panic(fmt.Sprintf("WithRequestFuncs[%d]: %v", i, rec))
		}
	}()
	return b(r)
}

// __gastro_probeBinders runs every registered binder once at New() time
// with a synthetic request and validates that the union of built-ins,
// WithFuncs, and binder keys contains no duplicates. Panics on the first
// collision with a message naming both sources. Returns the discovered
// union of binder helper names so the caller can seed placeholder stubs
// into parse-time FuncMaps.
func __gastro_probeBinders(cfg *config) map[string]bool {
	if len(cfg.binders) == 0 {
		return nil
	}
	probe := httptest.NewRequest(http.MethodGet, "/__gastro_probe", nil)

	seen := map[string]string{}
	for name := range gastroRuntime.DefaultFuncs() {
		seen[name] = "built-in"
	}
	// WithFuncs entries override the "built-in" attribution: if an adopter
	// used WithFuncs to override a built-in helper, a binder colliding on
	// that name should be reported against WithFuncs (the actual source
	// the adopter registered) rather than "built-in".
	for name := range cfg.userFuncNames {
		seen[name] = "WithFuncs"
	}
	// Component dispatchers occupy template-function namespace at parse
	// time — a bare-component invocation resolves through fm["Name"] in
	// buildFuncMap. A binder helper sharing a component name would
	// silently shadow the dispatcher in the request-aware path, making
	// template behaviour depend on registration order. Forbid it at New()
	// with a clear collision message naming the component name as the
	// conflicting source.
	for _, name := range __gastro_componentNames {
		if _, exists := seen[name]; !exists {
			seen[name] = "component"
		}
	}
	keys := map[string]bool{}
	for i, b := range cfg.binders {
		fm := b(probe)
		src := fmt.Sprintf("WithRequestFuncs[%d]", i)
		for name := range fm {
			if existing, ok := seen[name]; ok {
				panic(fmt.Sprintf(
					"gastro: helper name %q registered twice\n  - %s\n  - %s",
					name, existing, src,
				))
			}
			seen[name] = src
			keys[name] = true
		}
	}
	return keys
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
//		gastro.WithHandler("POST /tasks/{id}/place", placeTask),
//	)
//	http.ListenAndServe(":8080", router.Handler())
func New(opts ...Option) *Router {
	cfg := &config{
		funcs: gastroRuntime.DefaultFuncs(),
	}
	for _, opt := range opts {
		opt(cfg)
	}

	// Bind the class-merge-dependent template funcs to the configured
	// ClassMerger. html/template funcs can't call sibling funcs, so attrs
	// and twMerge must hold the merger directly. DefaultFuncs() installed
	// plain-concat versions; override them when WithClassMerger was set,
	// unless the user replaced them via WithFuncs.
	if cfg.classMerger != nil {
		if !cfg.userFuncNames["attrs"] {
			cfg.funcs["attrs"] = gastroRuntime.BuildAttrsFunc(cfg.classMerger)
		}
		if !cfg.userFuncNames["twMerge"] {
			merger := cfg.classMerger
			cfg.funcs["twMerge"] = func(classes ...string) string { return merger(classes...) }
		}
	}

	isDev := gastroRuntime.IsDev()
	if cfg.devMode != nil {
		isDev = *cfg.devMode
	}

	// Probe each binder with a synthetic request to discover its key set.
	// This validates name uniqueness against built-ins, WithFuncs, and other
	// binders. Binders must tolerate an empty context here — closures
	// inside the returned FuncMap are NOT invoked during probing; only the
	// map's keys are read.
	binderKeys := __gastro_probeBinders(cfg)

	__r := &Router{
		isDev:        isDev,
		userFuncs:    cfg.funcs,
		deps:         cfg.deps,
		errorHandler: cfg.errorHandler,
		binders:      cfg.binders,
		binderKeys:   binderKeys,
	}

	// Route table: the static-asset prefix and the GET-only page routes,
	// then WithHandler registrations — an identical pattern replaces the
	// generated route, any other pattern adds one.
	generated := []gastroRuntime.Route{
{{- if .HasStatic}}
		{Pattern: "GET /static/", Source: "static assets (static/)", Handler: http.StripPrefix("/static/", http.FileServerFS(gastroRuntime.GetStaticFS(staticAssetFS)))},
{{- end}}
{{- range .Routes}}
		{Pattern: {{ printf "%q" .Pattern }}, Source: {{ printf "%q" (print "page " .File) }}, Handler: http.HandlerFunc(__r.{{ .FuncName }})},
{{- end}}
	}
	handlers := make([]gastroRuntime.Route, 0, len(cfg.handlers))
	for i, he := range cfg.handlers {
		handlers = append(handlers, gastroRuntime.Route{
			Pattern: he.pattern,
			Source:  fmt.Sprintf("WithHandler(%q) #%d", he.pattern, i+1),
			Handler: he.h,
		})
	}
	routes := gastroRuntime.MergeRoutes(generated, handlers)

	// Validate WithMiddleware patterns: each must match at least one
	// route (page or WithHandler) via gastroRuntime.PatternMatchesAnyRoute,
	// so a typo panics at startup instead of silently wrapping nothing.
	knownRoutes := make([]string, 0, len(routes))
	for _, rt := range routes {
		knownRoutes = append(knownRoutes, rt.Pattern)
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

	// Wrap every route with each middleware whose pattern matches it.
	// Middleware composes in registration order: the first WithMiddleware
	// call ends up outermost (runs first on the request). A WithHandler
	// that replaced a page is wrapped like the page would have been.
	for i := range routes {
		for j := len(cfg.middleware) - 1; j >= 0; j-- {
			mw := cfg.middleware[j]
			if gastroRuntime.MiddlewareApplies(mw.pattern, routes[i].Pattern) {
				routes[i].Handler = mw.fn(routes[i].Handler)
			}
		}
	}

	// Conflicting registrations panic here, naming both sources.
	__r.mux = gastroRuntime.NewRouteMux(routes)
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

// Mux returns the underlying *http.ServeMux. Prefer WithHandler, which
// registers next to the page routes and receives WithMiddleware; routes
// added directly on the mux bypass WithMiddleware and the conflict check.
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
	Header     string
	Components []componentMeta
}

var renderTmpl = template.Must(template.New("render").Parse(`{{ .Header }}

// This file declares one typed component handle per file under
// components/, plus its XProps type. Render a component from Go code
// (HTTP handlers, SSE patch streams, tests) through the router:
//
//	router := gastro.New(opts...)
//	html, err := router.Render(gastro.Card, gastro.CardProps{Title: "Hello"})
//
// Bind the render to a request so request-aware helpers
// (WithRequestFuncs) see its state:
//
//	html, err := router.Render(gastro.Card.With(r), gastro.CardProps{Title: "Hello"})
//
// The typed props go straight to the component's render function (the
// same one the template dict path ends in), so frontmatter logic runs
// identically in both paths without a map[string]any round-trip.
package gastro

import (
	"fmt"
	"html/template"
	"net/http"

	gastroRuntime "github.com/andrioid/gastro/pkg/gastro"
)

// Suppress unused import warnings.
var _ = fmt.Errorf
var _ template.HTML
var _ http.Request
var _ gastroRuntime.Attrs

// __gastro_routerFor recovers this package's *Router from the untyped
// router a gastroRuntime.Component render function receives.
func __gastro_routerFor(rt any, component string) (*Router, error) {
	r, ok := rt.(*Router)
	if !ok || r == nil {
		return nil, fmt.Errorf("gastro: component %s must be rendered by the Router of its own generated package", component)
	}
	return r, nil
}
{{ range .Components }}
{{- /*
  XProps type definition. Lives in render.go (not in the per-component
  file) so it can include the synthetic Children field without modifying
  the user's hoisted Props struct.

  Three shapes:
    - HasProps && !HasChildren  -> alias to the user's hoisted struct
    - HasChildren               -> real struct: user fields + Children template.HTML
    - !HasProps && !HasChildren -> empty struct, so every component has a
                                   distinct props type
*/ -}}
{{- if .HasChildren }}
// {{ .ExportedName }}Props is the typed prop struct for {{ .ExportedName }}.
// The Children field carries the rendered HTML children content; it is
// auto-added by codegen because the component template references {{ "{{ .Children }}" }}.
type {{ .ExportedName }}Props struct {
{{- range .PropsFields }}
	{{ .Name }} {{ .Type }}
{{- end }}
	Children template.HTML
}
{{- else if .HasProps }}
// {{ .ExportedName }}Props is the typed prop struct for {{ .ExportedName }}.
type {{ .ExportedName }}Props = {{ .PropsTypeName }}
{{- else }}
// {{ .ExportedName }}Props is the (empty) prop struct for {{ .ExportedName }},
// which declares no Props.
type {{ .ExportedName }}Props struct{}
{{- end }}

// {{ .ExportedName }} is the typed handle for this component. Render it with
// router.Render({{ .ExportedName }}, {{ .ExportedName }}Props{…}).
var {{ .ExportedName }} = gastroRuntime.NewComponent(func(rt any, req *http.Request, props {{ .ExportedName }}Props) (template.HTML, error) {
	r, err := __gastro_routerFor(rt, "{{ .ExportedName }}")
	if err != nil {
		return "", err
	}
{{- if .HasChildren }}
{{- if .HasProps }}
	return r.{{ .FuncName }}__render({{ .PropsTypeName }}{
{{- range .PropsFields }}
		{{ .Name }}: props.{{ .Name }},
{{- end }}
	}, props.Children, req)
{{- else }}
	return r.{{ .FuncName }}__render(props.Children, req)
{{- end }}
{{- else if .HasProps }}
	return r.{{ .FuncName }}__render(props, "", req)
{{- else }}
	return r.{{ .FuncName }}__render("", req)
{{- end }}
})
{{ end }}`))

func generateRenderFile(header string, components []componentMeta, outputDir string) error {
	var buf bytes.Buffer
	if err := renderTmpl.Execute(&buf, renderData{Header: header, Components: components}); err != nil {
		return err
	}
	return writeGoFile(filepath.Join(outputDir, "render.go"), buf.Bytes())
}

func generateRoutesFile(header string, routes []router.Route, templates []templateMeta, components []componentMeta, hasStatic bool, outputDir string) error {
	var buf bytes.Buffer
	componentNames := make([]string, 0, len(components))
	for _, c := range components {
		componentNames = append(componentNames, c.ExportedName)
	}
	// Sort for stable codegen output — routesData.ComponentNames is
	// emitted as a literal slice and any non-determinism would cause
	// gastro check to flag spurious drift on every regen.
	sort.Strings(componentNames)
	err := routesTmpl.Execute(&buf, routesData{
		Header:         header,
		Routes:         routes,
		Templates:      templates,
		ComponentNames: componentNames,
		HasStatic:      hasStatic,
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
