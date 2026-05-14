package codegen

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"sort"
	"strings"
	"text/template"

	gastroParser "github.com/andrioid/gastro/internal/parser"
)

// UseInfo holds the template-friendly data for a component use declaration.
type UseInfo struct {
	Name     string // e.g. "Layout"
	FuncName string // e.g. "componentLayout"
}

// GenerateOptions configures emission. The zero value is suitable for the
// LSP shadow workspace: no name mangling. Production codegen sets
// MangleHoisted=true.
type GenerateOptions struct {
	// MangleHoisted controls whether top-level var/const/type/func
	// declarations are renamed with a per-page prefix
	// (__page_<id>_Name / __component_<id>_Name).
	//
	// Set to true for production codegen where many pages share one
	// Go package and need collision-proof names. Set to false for
	// the LSP shadow workspace, where each .gastro file is already
	// in its own subpackage and mangling would only degrade
	// hover/completion/diagnostic UX without buying any safety.
	MangleHoisted bool
}

// GenerateHandler produces Go source code for a page handler or component
// render function from a parsed .gastro file and its frontmatter analysis.
// isComponent indicates whether this file should generate a component render
// function. This is typically info.IsComponent, but the compiler may override
// it for files in components/ that have no frontmatter.
//
// Marker rewriting (Track B §4.10): the rewriter is invoked for its
// rewritten-source output only here. Warning emission (deprecation,
// unknown gastro runtime symbol) happens in AnalyzeFrontmatter so both
// the codegen pipeline and the LSP receive the same diagnostics by
// reading info.Warnings.
func GenerateHandler(file *gastroParser.File, info *FrontmatterInfo, isComponent bool, opts GenerateOptions) (string, error) {
	funcName := HandlerFuncName(file.Filename)

	// Compute the per-file mangling prefix. Empty when mangling is
	// disabled — every hoisted decl's MangledName equals its Name.
	prefix := ""
	if opts.MangleHoisted {
		kind := "page"
		if isComponent {
			kind = "component"
		}
		prefix = "__" + kind + "_" + DerivePageID(file.Filename) + "_"
	}

	// Build the per-page hoisted-name map and apply it to every
	// HoistedDecl. The analyzer populates info.HoistedDecls with
	// unmangled names; we re-resolve MangledName here so the same
	// info object can be re-used across mangle modes (production and
	// shadow paths).
	//
	// Self-sufficiency: if info.HoistedBody is empty (e.g. tests that
	// construct info manually rather than via AnalyzeFrontmatter), run
	// HoistDecls on the raw frontmatter so the codegen output remains
	// consistent regardless of which entry point the caller used.
	hoistedDecls := make([]HoistedDecl, len(info.HoistedDecls))
	copy(hoistedDecls, info.HoistedDecls)
	bodySource := info.HoistedBody
	if bodySource == "" && len(hoistedDecls) == 0 {
		var localBody string
		var localDecls []HoistedDecl
		var herr error
		localBody, localDecls, _, herr = HoistDecls(file.Frontmatter, HoistOptions{})
		if herr != nil {
			return "", herr
		}
		bodySource = localBody
		hoistedDecls = localDecls
	}
	if bodySource == "" {
		bodySource = file.Frontmatter
	}

	names := make(map[string]string, len(hoistedDecls))
	for i := range hoistedDecls {
		new := applyMangle(hoistedDecls[i].Name, prefix, hoistedDecls[i].Kind)
		hoistedDecls[i].MangledName = new
		names[hoistedDecls[i].Name] = new
	}
	frontmatter, _ := rewriteFrontmatter(bodySource, info, isComponent)
	// The body residue may still contain a top-level `type Props
	// struct{...}` for components when info.HoistedBody was empty
	// (legacy/test pattern). The hoister already moved it into
	// hoistedDecls, but the legacy HoistTypeDeclarations call inside
	// rewriteFrontmatter removes it again from text. Either path
	// produces a body without the type decl, so this is safe.
	_ = frontmatter

	// Apply hoisted-ref rewrites to the body and to each hoisted decl's
	// SourceText so cross-decl references and body-side references
	// resolve to the mangled names.
	frontmatter = RewriteHoistedRefs(frontmatter, names)
	for i := range hoistedDecls {
		hoistedDecls[i].SourceText = RewriteHoistedRefsInDecl(hoistedDecls[i].SourceText, names)
	}

	// Component-only: resolve PropsTypeName to either the mangled
	// __component_<id>_Props or the unmangled "Props". Pages have no
	// PropsTypeName.
	propsTypeName := info.PropsTypeName
	if isComponent && propsTypeName != "" {
		if mn, ok := names[propsTypeName]; ok {
			propsTypeName = mn
		}
	}

	// Build the rendered text block of all hoisted decls (in source
	// order). Each decl is emitted with its MangledName substituted
	// for the original Name on the LHS.
	hoistedBlock := renderHoistedBlock(hoistedDecls)

	// Compute the per-exported-var emit name (used as the value in
	// __data). For hoisted exported vars/consts, use the mangled
	// package-scope name; for := body locals, use the local name.
	var exportedVarEmit []exportedVarEmission
	for _, v := range info.ExportedVars {
		emit := v.Name
		if mn, ok := names[v.Name]; ok {
			emit = mn
		}
		exportedVarEmit = append(exportedVarEmit, exportedVarEmission{
			Name:     v.Name,
			EmitName: emit,
		})
	}

	// Build use info for pages that import components
	var uses []UseInfo
	for _, u := range file.Uses {
		uses = append(uses, UseInfo{
			Name:     u.Name,
			FuncName: HandlerFuncName(u.Path),
		})
	}

	data := generateData{
		PackageName:   "gastro",
		FuncName:      funcName,
		ExportedName:  ExportedComponentName(funcName),
		Imports:       dedupeAutoImports(file.Imports),
		Frontmatter:   frontmatter,
		ExportedVars:  exportedVarEmit,
		TemplateBody:  file.TemplateBody,
		IsPage:        info.IsPage,
		PropsTypeName: propsTypeName,
		IsComponent:   isComponent,
		HoistedDecls:  hoistedBlock,
		Uses:          uses,
	}

	tmpl := handlerTmpl
	if isComponent {
		tmpl = componentTmpl
	}

	var buf strings.Builder
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("generating handler for %s: %w", file.Filename, err)
	}

	return buf.String(), nil
}

// exportedVarEmission carries both the user-visible name (used as the
// __data map key, which the template references via {{ .Title }}) and
// the resolved emit name (the Go ident referenced as the map value,
// either the local := name or the mangled package-scope name).
type exportedVarEmission struct {
	Name     string
	EmitName string
}

// renderHoistedBlock concatenates each HoistedDecl.SourceText into the
// text block emitted at package scope between the imports and the
// handler/component func. The LHS name in each SourceText is rewritten
// to MangledName so the emitted decl carries the correct (possibly
// mangled) identifier.
func renderHoistedBlock(decls []HoistedDecl) string {
	if len(decls) == 0 {
		return ""
	}
	var b strings.Builder
	for i, d := range decls {
		if i > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(rewriteDeclLHS(d))
	}
	return b.String()
}

// rewriteDeclLHS replaces the user-written ident on the LHS of a hoisted
// decl's SourceText with the MangledName. SourceText is produced by
// renderValueSpec / renderTypeSpec / parseFuncBlock so the LHS shape is
// known: `var X`, `const X`, `type X`, `func X(`. We rewrite the first
// occurrence of "<keyword> Name" — simple, robust, deterministic.
func rewriteDeclLHS(d HoistedDecl) string {
	if d.Name == d.MangledName {
		return d.SourceText
	}
	switch d.Kind {
	case HoistVar, HoistConst, HoistType:
		keyword := hoistKindLabel(d.Kind)
		old := keyword + " " + d.Name
		new := keyword + " " + d.MangledName
		return strings.Replace(d.SourceText, old, new, 1)
	case HoistFunc:
		old := "func " + d.Name
		new := "func " + d.MangledName
		return strings.Replace(d.SourceText, old, new, 1)
	}
	return d.SourceText
}

type generateData struct {
	PackageName   string
	FuncName      string
	ExportedName  string // e.g. "PostCard" — exported name for Render API
	Imports       []string
	Frontmatter   string
	ExportedVars  []exportedVarEmission
	TemplateBody  string
	IsPage        bool
	IsComponent   bool
	PropsTypeName string    // e.g. "Props" — from type Props struct in the frontmatter
	HoistedDecls  string    // rendered text block of all hoisted package-scope decls
	Uses          []UseInfo // component imports used by this page
}

// handlerTmpl generates HTTP handlers that stream output directly to the
// response. Pages stream via Execute(w, ...) which is more efficient and
// follows Go's http.Handler conventions. This is intentionally separate
// from componentTmpl because the I/O model is fundamentally different
// (streaming vs buffering).
//
// Track B (page model v2, docs/history/frictions-plan.md §4.2): the handler
// wraps `w` in a *gastro-owned page writer that tracks whether
// frontmatter wrote a body. After frontmatter completes, the template
// render is skipped iff a body has been committed. This lets one
// .gastro file handle both GET (template render) and POST/etc.
// (frontmatter writes a body, e.g. an SSE patch or redirect) without
// inventing new authoring concepts. The frontmatter-injected
// `ctx := gastroRuntime.NewContext(...)` of the previous model is
// gone; users access the request via the ambient `(w, r)`.
var handlerTmpl = template.Must(template.New("handler").Parse(`// Code generated by gastro. DO NOT EDIT.
package {{ .PackageName }}

import (
	"log"
	"net/http"
	"html/template"

	gastroRuntime "github.com/andrioid/gastro/pkg/gastro"
{{- range .Imports }}
	"{{ . }}"
{{- end }}
)

// Suppress unused-import warnings for the conditional template path.
var _ = template.Must
var _ http.ResponseWriter
var _ = log.Println
{{ if .HoistedDecls }}
{{ .HoistedDecls }}
{{ end }}
func (__router *Router) {{ .FuncName }}(w http.ResponseWriter, r *http.Request) {
	w = gastroRuntime.NewPageWriter(w)
	defer gastroRuntime.Recover(w, r)

	{{ .Frontmatter }}

	// Suppress unused-var warnings for exported frontmatter vars and
	// provide hover-type anchors for the LSP shadow's queryVariableTypes
	// scan. The lines compile to no-op blank-assignment reads.
	{{- range .ExportedVars }}
	_ = {{ .EmitName }}
	{{- end }}

	if gastroRuntime.BodyWritten(w) {
		return
	}

	__data := map[string]any{
	{{- range .ExportedVars }}
		"{{ .Name }}": {{ .EmitName }},
	{{- end }}
	}

	if __err := __router.__gastro_renderPage("{{ .FuncName }}", w, r, __data); __err != nil {
		__router.__gastro_handleError(w, r, __err)
	}
}
`))

// componentTmpl generates render functions that return template.HTML.
// Components buffer into a bytes.Buffer because they must return the complete
// HTML string to the caller (parent template or Render API). This is intentionally
// separate from handlerTmpl because the I/O model is fundamentally different
// (buffering vs streaming).
var componentTmpl = template.Must(template.New("component").Parse(`// Code generated by gastro. DO NOT EDIT.
package {{ .PackageName }}

import (
	"bytes"
	"html/template"
	"log"

	gastroRuntime "github.com/andrioid/gastro/pkg/gastro"
{{- range .Imports }}
	"{{ . }}"
{{- end }}
)

// Suppress unused import warnings
var _ = gastroRuntime.DefaultFuncs
var _ bytes.Buffer
var _ = template.Must
var _ = log.Println

{{- if .HoistedDecls }}

{{ .HoistedDecls }}
{{- end }}

// {{ .FuncName }} is the unexported component method used by templates.
// To render this component from Go (handlers, SSE patches), call
// gastro.Render.{{ .ExportedName }}(...) instead. See render.go.
func (__router *Router) {{ .FuncName }}(propsMap map[string]any) template.HTML {
	// Children is the dict key injected by TransformTemplate for wrap blocks
	// and set by Render.X(XProps{Children: ...}) calls. Pulled out of the
	// map directly so the user's hoisted Props struct stays unmodified.
	// Renamed from "__children" in A5.
	var __children template.HTML
	if __c, __ok := propsMap["Children"]; __ok {
		__children, _ = __c.(template.HTML)
		delete(propsMap, "Children")
	}
	_ = __children

	{{- if .PropsTypeName }}
	__props, __err := gastroRuntime.MapToStruct[{{ .PropsTypeName }}](propsMap)
	if __err != nil {
		log.Printf("gastro: component {{ .FuncName }}: %v", __err)
		return ""
	}
	_ = __props
	{{- end }}

	{{ .Frontmatter }}

	// Suppress unused-var warnings for exported frontmatter vars and
	// provide hover-type anchors for the LSP shadow's queryVariableTypes
	// scan. The lines compile to no-op blank-assignment reads.
	{{- range .ExportedVars }}
	_ = {{ .EmitName }}
	{{- end }}

	__data := map[string]any{
	{{- range .ExportedVars }}
		"{{ .Name }}": {{ .EmitName }},
	{{- end }}
		"Children": __children,
	}

	var __buf bytes.Buffer
	if __err := __router.__gastro_getTemplate("{{ .FuncName }}").Execute(&__buf, __data); __err != nil {
		log.Printf("gastro: component {{ .FuncName }}: template execution failed: %v", __err)
		return ""
	}
	return template.HTML(__buf.String())
}
`))

// rewriteFrontmatter applies all gastro-marker rewrites to the
// frontmatter and returns the rewritten source plus any warnings
// produced (deprecation notices, unknown-symbol diagnostics).
//
// Track B (docs/history/frictions-plan.md §4.10) consolidates marker handling
// into a single AST pass that recognises a finite, allowlisted set of
// gastro.X references:
//
//	| Frontmatter call          | Rewritten to                      |
//	|---------------------------|-----------------------------------|
//	| gastro.Props()            | __props                           |
//	| gastro.Context()          | gastroRuntime.NewContext(w, r)    |
//	|                           |   (deprecated; emits a warning)   |
//	| gastro.From[T](ctx)       | gastroRuntime.FromContext[T](ctx) |
//	| gastro.FromOK[T](ctx)     | gastroRuntime.FromContextOK[T]…   |
//	| gastro.FromContext[T](…)  | gastroRuntime.FromContext[T](…)   |
//	| gastro.FromContextOK[T]…  | gastroRuntime.FromContextOK[T]…   |
//	| gastro.NewSSE(w, r)       | gastroRuntime.NewSSE(w, r)        |
//	| gastro.Render.X(…)        | Render.X(…)                       |
//
// Any other gastro.X reference produces a warning: "unknown gastro
// runtime symbol; did you mean to import a package?". This keeps the
// implicit gastro namespace finite and predictable.
//
// For components (isComponent), type declarations are also hoisted out
// of the body via HoistTypeDeclarations so the componentTmpl can place
// them at package level.
func rewriteFrontmatter(frontmatter string, info *FrontmatterInfo, isComponent bool) (string, []Warning) {
	rewritten, warnings := rewriteGastroMarkers(frontmatter)

	if isComponent {
		body, _ := HoistTypeDeclarations(rewritten)
		return strings.TrimSpace(body), warnings
	}
	return strings.TrimSpace(rewritten), warnings
}

// gastroMarkerAllowlist enumerates every gastro.X selector the rewriter
// recognises. Anything outside this set produces an "unknown gastro
// runtime symbol" warning. The list is intentionally finite — the
// alternative is letting frontmatter reach into arbitrary gastro
// runtime symbols, which would tie codegen to every internal of
// pkg/gastro.
//
// Values are the rewritten replacements applied to the *selector*
// (gastro.X). Two entries are special: "Props" and "Context" replace
// the entire enclosing call expression, not just the selector — see
// the call-pass loop below.
var gastroMarkerAllowlist = map[string]string{
	// Selector-only rewrites:
	"From":          "gastroRuntime.FromContext",
	"FromOK":        "gastroRuntime.FromContextOK",
	"FromContext":   "gastroRuntime.FromContext",
	"FromContextOK": "gastroRuntime.FromContextOK",
	"NewSSE":        "gastroRuntime.NewSSE",
	"Render":        "Render",

	// Call-expression rewrites — placeholder values; the call-pass
	// substitutes its own text. Listed here so they are NOT flagged as
	// unknown when the bare selector appears (e.g. someone writes
	// `_ = gastro.Props` without parens, which the call-pass won't catch).
	"Props":   "",
	"Context": "",
}

// markerEdit is one substitution to apply to the frontmatter source.
type markerEdit struct {
	start, end int    // byte offsets in the original frontmatter
	text       string // replacement text
}

// rewriteGastroMarkers walks the frontmatter AST and returns the
// rewritten source plus any warnings. The original line structure is
// preserved — every replacement is a same-line text edit — so
// downstream analysers can continue to use raw frontmatter line numbers.
func rewriteGastroMarkers(frontmatter string) (string, []Warning) {
	if strings.TrimSpace(frontmatter) == "" {
		return frontmatter, nil
	}

	prefix := "package __gastro\nfunc __handler() {\n"
	prefixLen := len(prefix)
	prefixLineCount := strings.Count(prefix, "\n")
	src := prefix + frontmatter + "\n}"

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "frontmatter.go", src, parser.AllErrors)
	if err != nil {
		// Parse error — leave rewriting to the runtime parser, but
		// surface no false-positive marker warnings either.
		return frontmatter, nil
	}

	fmOffset := func(p token.Pos) int { return int(p) - 1 - prefixLen }
	fmLine := func(p token.Pos) int { return fset.Position(p).Line - prefixLineCount }

	var edits []markerEdit
	var warnings []Warning
	handledSelector := make(map[token.Pos]bool)

	// Pass 1: full-call rewrites for gastro.Props() and gastro.Context().
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		ident, ok := sel.X.(*ast.Ident)
		if !ok || ident.Name != "gastro" {
			return true
		}
		switch sel.Sel.Name {
		case "Props":
			s, e := fmOffset(call.Pos()), fmOffset(call.End())
			if s < 0 || e > len(frontmatter) {
				return true
			}
			edits = append(edits, markerEdit{start: s, end: e, text: "__props"})
			handledSelector[sel.Pos()] = true
		case "Context":
			s, e := fmOffset(call.Pos()), fmOffset(call.End())
			if s < 0 || e > len(frontmatter) {
				return true
			}
			edits = append(edits, markerEdit{start: s, end: e, text: "gastroRuntime.NewContext(w, r)"})
			handledSelector[sel.Pos()] = true
			warnings = append(warnings, Warning{
				Line:    fmLine(call.Pos()),
				Message: "gastro.Context() is deprecated; use the ambient `r *http.Request` and `w http.ResponseWriter` directly (see docs/pages.md). Slated for removal two minor releases after this warning lands.",
			})
		}
		return true
	})

	// Pass 2: selector-only rewrites for the remaining allowlist entries
	// and unknown-symbol diagnostics for everything else.
	ast.Inspect(file, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if handledSelector[sel.Pos()] {
			return true
		}
		ident, ok := sel.X.(*ast.Ident)
		if !ok || ident.Name != "gastro" {
			return true
		}

		replacement, allowlisted := gastroMarkerAllowlist[sel.Sel.Name]
		if !allowlisted {
			warnings = append(warnings, Warning{
				Line:    fmLine(sel.Pos()),
				Message: fmt.Sprintf("unknown gastro runtime symbol %q; did you mean to import a package?", sel.Sel.Name),
			})
			return true
		}
		if sel.Sel.Name == "Props" || sel.Sel.Name == "Context" {
			// Bare reference (no parens) to a marker. Pass 1 didn't
			// catch it because there's no enclosing CallExpr. Surface
			// it as a warning rather than a silent rewrite.
			warnings = append(warnings, Warning{
				Line:    fmLine(sel.Pos()),
				Message: fmt.Sprintf("gastro.%s is a marker; call it as gastro.%s()", sel.Sel.Name, sel.Sel.Name),
			})
			return true
		}

		s, e := fmOffset(sel.Pos()), fmOffset(sel.End())
		if s < 0 || e > len(frontmatter) || s >= e {
			return true
		}
		edits = append(edits, markerEdit{start: s, end: e, text: replacement})
		return true
	})

	if len(edits) == 0 {
		return frontmatter, warnings
	}

	sort.Slice(edits, func(i, j int) bool { return edits[i].start > edits[j].start })
	result := frontmatter
	for _, e := range edits {
		result = result[:e.start] + e.text + result[e.end:]
	}
	return result, warnings
}

// autoImported lists the import paths that the generated handler /
// component templates always emit. User imports are deduped against
// this set so that frontmatter declaring e.g. "net/http" (encouraged by
// Track B for http.Error / http.Redirect) does not produce a duplicate
// import in the generated output.
var autoImported = map[string]bool{
	"log":           true,
	"net/http":      true,
	"html/template": true,
	"bytes":         true, // imported by componentTmpl
}

// dedupeAutoImports filters out import paths that the codegen already
// includes by default. Order is preserved.
func dedupeAutoImports(imports []string) []string {
	if len(imports) == 0 {
		return imports
	}
	out := make([]string, 0, len(imports))
	for _, p := range imports {
		if autoImported[p] {
			continue
		}
		out = append(out, p)
	}
	return out
}

// ExportedComponentName derives an exported name from a component function name.
// For example: "componentPostCard" -> "PostCard"
func ExportedComponentName(funcName string) string {
	return strings.TrimPrefix(funcName, "component")
}

// StructField represents a field in a parsed Props struct.
type StructField struct {
	Name string
	Type string
}

// ParseStructFields extracts field names and types from a hoisted type
// declaration string like "type FooProps struct {\n    Title string\n    Count int\n}".
// Returns fields from the first struct type encountered.
//
// Uses go/parser so inline comments containing `{`, `}`, or backticks, tag
// literals, and multiple-name fields (`A, B string`) are all handled
// correctly. Falls back to a line scanner when the input is not parseable.
func ParseStructFields(hoistedTypes string) []StructField {
	if strings.TrimSpace(hoistedTypes) == "" {
		return nil
	}
	if fields, ok := parseStructFieldsAST(hoistedTypes); ok {
		return fields
	}
	return parseStructFieldsLegacy(hoistedTypes)
}

func parseStructFieldsAST(hoistedTypes string) ([]StructField, bool) {
	src := "package __gastro\n" + hoistedTypes
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "types.go", src, parser.ParseComments)
	if err != nil {
		return nil, false
	}

	var fields []StructField
	done := false
	ast.Inspect(file, func(n ast.Node) bool {
		if done {
			return false
		}
		st, ok := n.(*ast.StructType)
		if !ok {
			return true
		}
		if st.Fields == nil {
			done = true
			return false
		}
		for _, field := range st.Fields.List {
			if len(field.Names) == 0 {
				// Embedded field — skip; not currently supported by the
				// runtime MapToStruct path either.
				continue
			}
			typeStr := exprString(fset, field.Type)
			for _, name := range field.Names {
				fields = append(fields, StructField{
					Name: name.Name,
					Type: typeStr,
				})
			}
		}
		done = true
		return false
	})

	return fields, true
}

// exprString renders an AST expression back to its source-text form using
// go/printer. Used to capture field types verbatim, including composite
// types like `[]string`, `map[string]int`, and qualified identifiers.
func exprString(fset *token.FileSet, expr ast.Expr) string {
	var buf bytes.Buffer
	if err := printer.Fprint(&buf, fset, expr); err != nil {
		return ""
	}
	return buf.String()
}

// parseStructFieldsLegacy is the original line-based scanner, kept as a
// fallback for unparseable input (e.g. mid-edit in the LSP).
func parseStructFieldsLegacy(hoistedTypes string) []StructField {
	lines := strings.Split(hoistedTypes, "\n")
	var fields []StructField
	inStruct := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.Contains(trimmed, "struct {") {
			inStruct = true
			continue
		}
		if inStruct && trimmed == "}" {
			break
		}
		if !inStruct {
			continue
		}

		// Parse "FieldName FieldType" lines
		parts := strings.Fields(trimmed)
		if len(parts) >= 2 {
			fields = append(fields, StructField{
				Name: parts[0],
				Type: parts[1],
			})
		}
	}

	return fields
}

// FindFrontmatterStart returns the 1-indexed line number in the
// generated handler/component source where the user's frontmatter
// content begins. Used by the LSP shadow workspace to translate
// .gastro line numbers into virtual .go line numbers for diagnostics
// and hover.
//
// The codegen templates always insert exactly one blank line between
// the last fixed boilerplate line (the "anchor") and the
// {{ .Frontmatter }} substitution, so frontmatter starts on
// (anchorLine + 2). Anchors are stable across handlerTmpl /
// componentTmpl revisions and are asserted by codegen tests so a
// template change that breaks this contract fails loudly here rather
// than silently shifting LSP diagnostics by a few lines.
//
//	page (handlerTmpl):              "defer gastroRuntime.Recover(w, r)"
//	component, no Props struct:      "_ = __children"
//	component, with Props struct:    "_ = __props"
//
// Returns 0 if the anchor is not found, which indicates the input is
// not a codegen handler/component output (callers should treat this
// as "no frontmatter present").
func FindFrontmatterStart(generated string, isComponent bool, hasProps bool) int {
	var anchor string
	switch {
	case !isComponent:
		anchor = "defer gastroRuntime.Recover(w, r)"
	case hasProps:
		anchor = "_ = __props"
	default:
		anchor = "_ = __children"
	}

	for i, line := range strings.Split(generated, "\n") {
		if strings.TrimSpace(line) == anchor {
			return i + 3 // 0-indexed anchor + blank line + 1 to convert to 1-indexed
		}
	}
	return 0
}

// CountLeadingBlankLines returns the number of leading blank
// (whitespace-only) lines in s. Used by the LSP shadow to align the
// source map with codegen's strings.TrimSpace step on frontmatter:
// codegen strips leading whitespace so the user's first non-blank
// frontmatter line — not their first frontmatter line — is what lands
// at FindFrontmatterStart's position.
func CountLeadingBlankLines(s string) int {
	n := 0
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) != "" {
			break
		}
		n++
	}
	return n
}

// HandlerFuncName derives a Go function name from a .gastro file path.
// For pages: "pages/index.gastro" -> "pageIndex"
// For components: "components/card.gastro" -> "componentCard"
func HandlerFuncName(filename string) string {
	name := filename
	name = strings.TrimSuffix(name, ".gastro")
	name = strings.ReplaceAll(name, "[", "")
	name = strings.ReplaceAll(name, "]", "")

	// Determine prefix based on directory
	var prefix string
	if strings.HasPrefix(name, "pages/") {
		prefix = "page"
		name = strings.TrimPrefix(name, "pages/")
	} else if strings.HasPrefix(name, "components/") {
		prefix = "component"
		name = strings.TrimPrefix(name, "components/")
	} else {
		prefix = "gastro"
	}

	// Split on / and - to create camelCase segments
	parts := strings.FieldsFunc(name, func(r rune) bool {
		return r == '/' || r == '-'
	})

	var result strings.Builder
	result.WriteString(prefix)

	for _, part := range parts {
		if part == "" {
			continue
		}
		result.WriteString(strings.ToUpper(part[:1]) + part[1:])
	}

	return result.String()
}
