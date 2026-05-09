package template

import (
	"fmt"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"text/template/parse"

	"github.com/andrioid/gastro/internal/codegen"
	"github.com/andrioid/gastro/internal/parser"
	"github.com/andrioid/gastro/pkg/gastro"
)

// CompletionItem represents an LSP completion suggestion.
type CompletionItem struct {
	Label      string
	Detail     string // type info or description
	InsertText string // text to insert (may differ from label); may contain snippet syntax when IsSnippet is true
	FilterText string // text the editor uses for fuzzy matching (optional)
	IsSnippet  bool   // true when InsertText contains snippet tabstop syntax ($0, ${1:...})
}

// Diagnostic represents an LSP diagnostic (error/warning).
// Positions are 0-indexed and relative to the template body.
type Diagnostic struct {
	StartLine int
	StartChar int
	EndLine   int
	EndChar   int
	Message   string
	Severity  int // 1=Error, 2=Warning. Zero is treated as 1 (Error) by the LSP server.
}

// VariableCompletions returns completion items for {{ . }} expressions
// based on exported frontmatter variables.
func VariableCompletions(info *codegen.FrontmatterInfo) []CompletionItem {
	items := make([]CompletionItem, 0, len(info.ExportedVars))
	for _, v := range info.ExportedVars {
		items = append(items, CompletionItem{
			Label:      "." + v.Name,
			Detail:     "frontmatter variable",
			InsertText: "." + v.Name,
			FilterText: "." + v.Name,
		})
	}
	if info.IsComponent {
		items = append(items, CompletionItem{
			Label:      ".Children",
			Detail:     "children content",
			InsertText: ".Children",
			FilterText: ".Children",
		})
	}
	return items
}

// ComponentCompletions returns completion items for component tag names
// based on use declarations.
func ComponentCompletions(uses []parser.UseDeclaration) []CompletionItem {
	items := make([]CompletionItem, 0, len(uses))
	for _, u := range uses {
		items = append(items, CompletionItem{
			Label:      u.Name,
			Detail:     u.Path,
			InsertText: u.Name,
		})
	}
	return items
}

// compileTimeDirective describes a keyword handled by the gastro code
// generator (not the runtime FuncMap). These are stripped or expanded at
// compile time, so they don't appear in gastro.DefaultFuncs() and need to
// be surfaced separately for completion.
type compileTimeDirective struct {
	name    string
	detail  string
	plain   string // insertText when snippet support is disabled
	snippet string // insertText when snippet support is enabled
}

var compileTimeDirectives = []compileTimeDirective{
	{
		name:    "wrap",
		detail:  "compile-time directive — wraps children in a layout component",
		plain:   "wrap ",
		snippet: "wrap ${1:Component} (dict \"${2:key}\" ${3:value})$0",
	},
	{
		name:    "markdown",
		detail:  "compile-time directive — inlines rendered markdown (path must be a string literal)",
		plain:   "markdown \"\"",
		snippet: "markdown \"${1:path.md}\"$0",
	},
	{
		name:    "raw",
		detail:  "compile-time directive — begins a block whose contents bypass template parsing",
		plain:   "raw }}$0{{ endraw",
		snippet: "raw }}$0{{ endraw",
	},
	{
		name:    "endraw",
		detail:  "compile-time directive — closes a {{ raw }} block",
		plain:   "endraw",
		snippet: "endraw",
	},
}

// FuncMapCompletions returns completion items for template functions
// available in {{ }} expressions (gastro functions + Go template builtins
// + gastro compile-time directives).
//
// When snippetSupport is true, compile-time directives are returned with
// snippet placeholders (e.g. wrap/markdown insert a useful argument skeleton).
func FuncMapCompletions(snippetSupport bool) []CompletionItem {
	funcs := gastro.DefaultFuncs()
	items := make([]CompletionItem, 0, len(funcs)+len(goTemplateBuiltins)+len(compileTimeDirectives))
	for name := range funcs {
		items = append(items, CompletionItem{
			Label:      name,
			Detail:     "template function",
			InsertText: name,
		})
	}
	for _, name := range goTemplateBuiltins {
		items = append(items, CompletionItem{
			Label:      name,
			Detail:     "Go template builtin",
			InsertText: name,
		})
	}
	for _, d := range compileTimeDirectives {
		item := CompletionItem{
			Label:  d.name,
			Detail: d.detail,
		}
		if snippetSupport {
			item.InsertText = d.snippet
			item.IsSnippet = true
		} else {
			item.InsertText = d.plain
		}
		items = append(items, item)
	}
	return items
}

// FuncSignatures returns a map of template function names to their Go type
// signatures. Gastro functions use reflection; Go builtins use known signatures.
func FuncSignatures() map[string]string {
	sigs := make(map[string]string)
	for name, fn := range gastro.DefaultFuncs() {
		sigs[name] = reflect.TypeOf(fn).String()
	}
	// Go template builtins — signatures from the Go standard library
	for _, name := range []string{"and", "or"} {
		sigs[name] = "func(arg0 any, args ...any) any"
	}
	sigs["not"] = "func(arg any) bool"
	for _, name := range []string{"eq", "ne", "lt", "le", "gt", "ge"} {
		sigs[name] = "func(arg1, arg2 any) bool"
	}
	sigs["print"] = "func(args ...any) string"
	sigs["printf"] = "func(format string, args ...any) string"
	sigs["println"] = "func(args ...any) string"
	sigs["len"] = "func(item any) int"
	sigs["index"] = "func(item any, indices ...any) any"
	sigs["slice"] = "func(item any, indices ...any) any"
	sigs["call"] = "func(fn any, args ...any) any"
	sigs["html"] = "func(args ...any) string"
	sigs["js"] = "func(args ...any) string"
	sigs["urlquery"] = "func(args ...any) string"
	return sigs
}

// ElementTypeFromContainer extracts the element type from a container type string.
// "[]db.Post" → "db.Post", "[]*db.Post" → "*db.Post",
// "map[string]db.Post" → "db.Post". Returns "" for non-container types.
func ElementTypeFromContainer(typeStr string) string {
	if strings.HasPrefix(typeStr, "[]") {
		return typeStr[2:]
	}
	if strings.HasPrefix(typeStr, "[") {
		idx := strings.Index(typeStr, "]")
		if idx >= 0 && idx+1 < len(typeStr) {
			return typeStr[idx+1:]
		}
	}
	if strings.HasPrefix(typeStr, "map[") {
		idx := strings.Index(typeStr, "]")
		if idx >= 0 && idx+1 < len(typeStr) {
			return typeStr[idx+1:]
		}
	}
	return ""
}

// OffsetToLineChar converts a byte offset within text to 0-indexed line and character.
func OffsetToLineChar(text string, offset int) (int, int) {
	line := 0
	lastNewline := -1
	for i := 0; i < offset && i < len(text); i++ {
		if text[i] == '\n' {
			line++
			lastNewline = i
		}
	}
	return line, offset - lastNewline - 1
}

// Diagnose checks a template body for common errors: unknown variables,
// invalid double-dot syntax, unknown components, and invalid component props.
//
// When the template is syntactically valid, it uses Go's text/template/parse
// to build an AST and walks it with scope awareness (range/with blocks rebind
// the dot context). When parsing fails (common during editing), variable
// checks are skipped to avoid false positives — only double-dot syntax and
// unknown component checks run in that case.
//
// propsMap maps component names to their Props struct fields for prop
// validation. Pass nil to skip component prop checks.
//
// The second return value is a WalkReport carrying telemetry from the AST
// walker — currently a count and sample of range/with scopes silently
// skipped due to missing type info. It is empty when the template doesn't
// parse to an AST (the walker doesn't run in that case).
func Diagnose(templateBody string, info *codegen.FrontmatterInfo, uses []parser.UseDeclaration, typeMap map[string]string, resolver FieldResolver, propsMap map[string][]codegen.StructField) ([]Diagnostic, WalkReport) {
	// Strip raw blocks — their content is literal text, not template logic.
	// This prevents false diagnostics for {{ .Var }} inside raw blocks.
	templateBody = stripRawBlocks(templateBody)

	var diags []Diagnostic
	var report WalkReport

	exportedNames := make(map[string]bool, len(info.ExportedVars))
	for _, v := range info.ExportedVars {
		exportedNames[v.Name] = true
	}
	// Children is a synthetic variable injected by the code generator for
	// all components — not declared in frontmatter but always available.
	if info.IsComponent {
		exportedNames["Children"] = true
	}

	// Double-dot syntax is always invalid — check with regex regardless of
	// whether the AST parses (the parser itself rejects double-dot).
	diags = append(diags, diagnoseDoubleDot(templateBody)...)

	// Attempt AST-based scope-aware variable checking
	tree, err := ParseTemplateBody(templateBody, uses)
	if err == nil && tree != nil {
		walkDiags, wr := WalkDiagnostics(tree, templateBody, exportedNames, typeMap, resolver)
		diags = append(diags, walkDiags...)
		report = wr
	}

	diags = append(diags, diagnoseUnknownComponents(templateBody, uses)...)
	diags = append(diags, DiagnoseComponentProps(templateBody, tree, uses, propsMap)...)

	return diags, report
}

// diagnoseDoubleDot detects invalid double-dot syntax (e.g. {{ ..Title }}).
func diagnoseDoubleDot(templateBody string) []Diagnostic {
	var diags []Diagnostic
	doubleDotRe := regexp.MustCompile(`\.\.([A-Z][a-zA-Z0-9]*)`)
	for _, idx := range doubleDotRe.FindAllStringIndex(templateBody, -1) {
		startLine, startChar := OffsetToLineChar(templateBody, idx[0])
		endLine, endChar := OffsetToLineChar(templateBody, idx[1])
		varName := strings.TrimPrefix(templateBody[idx[0]:idx[1]], "..")
		diags = append(diags, Diagnostic{
			StartLine: startLine,
			StartChar: startChar,
			EndLine:   endLine,
			EndChar:   endChar,
			Message:   fmt.Sprintf("invalid syntax %q: use %q instead", ".."+varName, "."+varName),
		})
	}
	return diags
}

// componentInvocationFallbackRegex matches bare PascalCase calls ({{ Card ...)
// and wrap calls ({{ wrap Layout ...) for use when AST parsing fails.
var componentInvocationFallbackRegex = regexp.MustCompile(`\{\{\s*(?:wrap\s+)?([A-Z][a-zA-Z0-9]*)`)

// diagnoseUnknownComponents detects PascalCase function calls (component
// invocations) and {{ wrap X }} blocks where X is not imported.
//
// Primary: walks the AST from ParseTemplateBody to find IdentifierNodes
// that are PascalCase and not in the known components set.
// Fallback: uses regex when the template fails to parse (common during editing).
func diagnoseUnknownComponents(templateBody string, uses []parser.UseDeclaration) []Diagnostic {
	knownComponents := make(map[string]bool, len(uses))
	for _, u := range uses {
		knownComponents[u.Name] = true
	}

	tree, err := ParseTemplateBody(templateBody, uses)
	if err != nil {
		return diagnoseUnknownComponentsRegex(templateBody, knownComponents)
	}

	return diagnoseUnknownComponentsAST(tree, templateBody, knownComponents)
}

// diagnoseUnknownComponentsAST walks the parse tree to find PascalCase
// identifier nodes used as function calls that aren't known components.
func diagnoseUnknownComponentsAST(tree *parse.Tree, templateBody string, knownComponents map[string]bool) []Diagnostic {
	if tree == nil || tree.Root == nil {
		return nil
	}

	var diags []Diagnostic
	walkNodes(tree.Root.Nodes, func(node parse.Node) {
		cmd, ok := node.(*parse.CommandNode)
		if !ok || len(cmd.Args) == 0 {
			return
		}
		ident, ok := cmd.Args[0].(*parse.IdentifierNode)
		if !ok {
			return
		}
		name := ident.Ident
		if !token.IsExported(name) || knownComponents[name] {
			return
		}
		offset := int(ident.Position())
		startLine, startChar := OffsetToLineChar(templateBody, offset)
		endLine, endChar := OffsetToLineChar(templateBody, offset+len(name))
		diags = append(diags, Diagnostic{
			StartLine: startLine,
			StartChar: startChar,
			EndLine:   endLine,
			EndChar:   endChar,
			Message:   fmt.Sprintf("unknown component %q: not imported", name),
		})
	})
	return diags
}

// diagnoseUnknownComponentsRegex is the fallback for when the AST can't be
// parsed (e.g. during editing). Uses regex to find PascalCase calls.
func diagnoseUnknownComponentsRegex(templateBody string, knownComponents map[string]bool) []Diagnostic {
	var diags []Diagnostic
	for _, idx := range componentInvocationFallbackRegex.FindAllStringSubmatchIndex(templateBody, -1) {
		compName := templateBody[idx[2]:idx[3]]
		if !knownComponents[compName] {
			startLine, startChar := OffsetToLineChar(templateBody, idx[2])
			endLine, endChar := OffsetToLineChar(templateBody, idx[3])
			diags = append(diags, Diagnostic{
				StartLine: startLine,
				StartChar: startChar,
				EndLine:   endLine,
				EndChar:   endChar,
				Message:   fmt.Sprintf("unknown component %q: not imported", compName),
			})
		}
	}
	return diags
}

// walkNodes recursively visits all nodes in a parse tree, calling fn for each.
func walkNodes(nodes []parse.Node, fn func(parse.Node)) {
	for _, node := range nodes {
		fn(node)
		switch n := node.(type) {
		case *parse.ListNode:
			if n != nil {
				walkNodes(n.Nodes, fn)
			}
		case *parse.IfNode:
			walkNodes(n.List.Nodes, fn)
			if n.ElseList != nil {
				walkNodes(n.ElseList.Nodes, fn)
			}
		case *parse.RangeNode:
			walkNodes(n.List.Nodes, fn)
			if n.ElseList != nil {
				walkNodes(n.ElseList.Nodes, fn)
			}
		case *parse.WithNode:
			walkNodes(n.List.Nodes, fn)
			if n.ElseList != nil {
				walkNodes(n.ElseList.Nodes, fn)
			}
		case *parse.ActionNode:
			if n.Pipe != nil {
				for _, cmd := range n.Pipe.Cmds {
					fn(cmd)
				}
			}
		case *parse.PipeNode:
			for _, cmd := range n.Cmds {
				fn(cmd)
			}
		case *parse.TemplateNode:
			// {{template}} nodes don't have child nodes to walk
		}
	}
}

// ResolveComponentProps reads a component .gastro file and extracts its Props
// struct fields. openDocs maps file URIs to their content (for unsaved changes);
// if the file isn't open, it's read from disk. Returns nil fields (not an error)
// when the component has no Props struct.
func ResolveComponentProps(projectDir, componentPath string, openDocs map[string]string) ([]codegen.StructField, error) {
	absPath := filepath.Join(projectDir, componentPath)

	var content string
	if docContent, ok := openDocs["file://"+absPath]; ok {
		content = docContent
	} else {
		data, err := os.ReadFile(absPath)
		if err != nil {
			return nil, fmt.Errorf("reading component %s: %w", componentPath, err)
		}
		content = string(data)
	}

	parsed, err := parser.Parse(componentPath, content)
	if err != nil {
		return nil, fmt.Errorf("parsing component %s: %w", componentPath, err)
	}

	_, typeDecls := codegen.HoistTypeDeclarations(parsed.Frontmatter)
	if strings.TrimSpace(typeDecls) == "" {
		return nil, nil
	}

	fields := codegen.ParseStructFields(typeDecls)
	if len(fields) == 0 {
		return nil, nil
	}

	return fields, nil
}

// componentCall represents a component invocation found in the template AST.
type componentCall struct {
	name       string          // PascalCase component name
	nameOffset int             // byte offset of the component name in the template body
	nameEnd    int             // byte offset of the end of the component name
	callOffset int             // byte offset of the start of the entire call ({{ or wrap)
	callEnd    int             // byte offset of the end of the entire call
	props      []componentProp // prop keys extracted from (dict ...) args
	hasDict    bool            // whether a dict call was found
}

// componentProp represents a single prop key found in a component's dict args.
type componentProp struct {
	name   string // the prop key name
	offset int    // byte offset of the key name (inside the quotes)
}

// extractComponentCalls walks the AST to find all component invocations.
// Handles both {{ Card (dict ...) }} and {{ wrap Layout (dict ...) }} forms,
// as well as bare calls like {{ Card }}.
func extractComponentCalls(tree *parse.Tree, templateBody string, knownComponents map[string]bool) []componentCall {
	if tree == nil || tree.Root == nil {
		return nil
	}

	var calls []componentCall
	walkNodes(tree.Root.Nodes, func(node parse.Node) {
		cmd, ok := node.(*parse.CommandNode)
		if !ok || len(cmd.Args) == 0 {
			return
		}

		var compName string
		var compIdent *parse.IdentifierNode
		var dictArgIndex int // index in cmd.Args where dict pipe would be

		ident0, ok := cmd.Args[0].(*parse.IdentifierNode)
		if !ok {
			return
		}

		if ident0.Ident == "wrap" && len(cmd.Args) >= 2 {
			// {{ wrap Layout (dict ...) }}
			ident1, ok := cmd.Args[1].(*parse.IdentifierNode)
			if !ok || !token.IsExported(ident1.Ident) {
				return
			}
			compName = ident1.Ident
			compIdent = ident1
			dictArgIndex = 2
		} else if token.IsExported(ident0.Ident) {
			// {{ Card (dict ...) }} or {{ Card }}
			compName = ident0.Ident
			compIdent = ident0
			dictArgIndex = 1
		} else {
			return
		}

		if !knownComponents[compName] {
			return
		}

		call := componentCall{
			name:       compName,
			nameOffset: int(compIdent.Position()),
			nameEnd:    int(compIdent.Position()) + len(compName),
			callOffset: int(cmd.Position()),
			callEnd:    int(cmd.Position()) + len(cmd.String()),
		}

		// Look for (dict ...) in the remaining args
		if dictArgIndex < len(cmd.Args) {
			call.props, call.hasDict = extractDictProps(cmd.Args[dictArgIndex])
		}

		calls = append(calls, call)
	})

	return calls
}

// extractDictProps extracts prop keys from a dict call argument.
// The dict arg is typically a PipeNode containing a CommandNode with
// IdentifierNode{"dict"} as its first arg, followed by alternating
// string-key / value pairs.
func extractDictProps(node parse.Node) ([]componentProp, bool) {
	// The (dict ...) expression parses as a PipeNode wrapping a CommandNode
	pipe, ok := node.(*parse.PipeNode)
	if !ok || len(pipe.Cmds) == 0 {
		return nil, false
	}

	dictCmd := pipe.Cmds[0]
	if len(dictCmd.Args) == 0 {
		return nil, false
	}

	dictIdent, ok := dictCmd.Args[0].(*parse.IdentifierNode)
	if !ok || dictIdent.Ident != "dict" {
		return nil, false
	}

	var props []componentProp
	// Dict args are: dict "Key1" value1 "Key2" value2 ...
	// Keys are at odd indices (1, 3, 5, ...)
	for i := 1; i < len(dictCmd.Args); i += 2 {
		strNode, ok := dictCmd.Args[i].(*parse.StringNode)
		if !ok {
			continue
		}
		props = append(props, componentProp{
			name:   strNode.Text,
			offset: int(strNode.Position()) + 1, // +1 to skip opening quote
		})
	}

	return props, true
}

// componentCallFallbackRegex matches {{ X (dict ...) }}, {{ wrap X (dict ...) }},
// and bare calls like {{ X }}. Used when AST parsing fails.
var componentCallFallbackRegex = regexp.MustCompile(`\{\{\s*(?:wrap\s+)?([A-Z][a-zA-Z0-9]*)(?:\s+\(dict\b([^)]*)\))?`)

// dictKeyRegex matches string keys inside a dict call: "KeyName".
// Accepts both PascalCase user-prop names and the legacy underscore-
// prefixed synthetic key ("__children") so the wrap-form regex
// fallback in DiagnoseComponentProps can surface a deprecation hint
// for users still on pre-A5 templates. Keys that don't match either
// shape (e.g. lowercase) won't be flagged by the regex path —
// codegen.SyntheticPropKey decides whether a matched key is a
// recognised synthetic, deprecated, or user prop.
var dictKeyRegex = regexp.MustCompile(`"((?:[A-Z][a-zA-Z0-9]*|__[a-z][a-zA-Z0-9]*))"`)

// DiagnoseComponentProps checks that props passed to component calls match
// the component's Props struct. Reports unknown props as errors, deprecated
// keys ("__children") and missing props as warnings.
//
// This is a thin LSP-shaped wrapper over codegen.ValidateDictKeysFromAST,
// which is the single source of truth for dict-key validation: see
// internal/codegen/validate.go and internal/codegen/synthetic.go. Both the
// `gastro generate` command and this LSP entry point produce identical
// verdicts on which keys are valid — the codegen-side caller just opts
// out of missing-prop warnings via ValidateDictKeysOptions.EmitMissingProps.
//
// When the tree is nil (the template fails to parse with text/template/parse,
// which is the case for `{{ wrap X }}...{{ end }}` blocks because the Go
// parser doesn't recognise `wrap` as a built-in block keyword), a slim
// regex fallback handles the call-site form so users editing wrap blocks
// still get prop validation. The fallback delegates synthetic-key
// classification to codegen.SyntheticPropKey, so the unknown-vs-deprecated
// vs Children decision can never drift between the two paths.
func DiagnoseComponentProps(templateBody string, tree *parse.Tree, uses []parser.UseDeclaration, propsMap map[string][]codegen.StructField) []Diagnostic {
	if len(propsMap) == 0 || len(uses) == 0 {
		return nil
	}

	// codegen's validator is keyed by component path so two pages
	// importing the same component under different aliases share a
	// schema. The LSP's propsMap is keyed by alias for legacy
	// reasons; rebuild as path-keyed via the use list.
	propsByPath := make(map[string][]codegen.StructField, len(uses))
	for _, u := range uses {
		if fields, ok := propsMap[u.Name]; ok {
			propsByPath[u.Path] = fields
		}
	}
	if len(propsByPath) == 0 {
		return nil
	}

	if tree == nil {
		return diagnoseComponentPropsRegexFallback(templateBody, uses, propsMap)
	}

	raw := codegen.ValidateDictKeysFromAST(
		templateBody,
		tree,
		uses,
		propsByPath,
		codegen.ValidateDictKeysOptions{EmitMissingProps: true},
	)
	if len(raw) == 0 {
		return nil
	}

	diags := make([]Diagnostic, 0, len(raw))
	for _, d := range raw {
		diags = append(diags, Diagnostic{
			// codegen returns 1-indexed (line, col); LSP wants 0-indexed.
			StartLine: d.StartLine - 1,
			StartChar: d.StartCol - 1,
			EndLine:   d.EndLine - 1,
			EndChar:   d.EndCol - 1,
			Message:   d.Message,
			Severity:  int(d.Severity),
		})
	}
	return diags
}

// diagnoseComponentPropsRegexFallback handles dict-key validation when
// text/template/parse rejects the body. The only practical case today
// is `{{ wrap X (dict ...) }}...{{ end }}` form (Go's parser rejects it
// because `wrap` isn't a built-in block keyword). The regex matches the
// call site — not the body content or the closing `{{ end }}` — so it
// can validate keys without needing balanced wrap/end matching.
//
// Synthetic-key classification (Children, __children) is delegated to
// codegen.SyntheticPropKey so the wrap-form path and the bare-call path
// agree on which keys are exempt from the unknown-prop check. The
// regex path emits errors for unknown keys and warnings for the
// deprecated `__children` sentinel; missing-prop warnings are not
// emitted in this path (they require precise schema-vs-provided-keys
// math that's harder to do reliably with regex matches — the AST path
// covers them in every editing state where it succeeds, which is
// every state except wrap-form).
func diagnoseComponentPropsRegexFallback(templateBody string, uses []parser.UseDeclaration, propsMap map[string][]codegen.StructField) []Diagnostic {
	knownComponents := make(map[string][]codegen.StructField, len(uses))
	for _, u := range uses {
		if f, ok := propsMap[u.Name]; ok {
			knownComponents[u.Name] = f
		}
	}

	var diags []Diagnostic
	for _, m := range componentCallFallbackRegex.FindAllStringSubmatchIndex(templateBody, -1) {
		compName := templateBody[m[2]:m[3]]
		fields, ok := knownComponents[compName]
		if !ok {
			continue
		}
		dictArgs := ""
		dictArgsStart := -1
		if m[4] >= 0 && m[5] >= 0 {
			dictArgs = templateBody[m[4]:m[5]]
			dictArgsStart = m[4]
		}
		validNames := make(map[string]bool, len(fields))
		for _, f := range fields {
			validNames[f.Name] = true
		}

		for _, km := range dictKeyRegex.FindAllStringSubmatchIndex(dictArgs, -1) {
			name := dictArgs[km[2]:km[3]]
			absOff := dictArgsStart + km[2]

			switch kind, _ := codegen.SyntheticPropKey(name); kind {
			case codegen.SyntheticChildren:
				continue
			case codegen.SyntheticDeprecatedChildren:
				startLine, startChar := OffsetToLineChar(templateBody, absOff)
				endLine, endChar := OffsetToLineChar(templateBody, absOff+len(name))
				diags = append(diags, Diagnostic{
					StartLine: startLine, StartChar: startChar,
					EndLine: endLine, EndChar: endChar,
					Message:  fmt.Sprintf(`dict key %q is no longer recognised on component %s; use %q instead`, name, compName, "Children"),
					Severity: 2,
				})
				continue
			}
			if validNames[name] {
				continue
			}
			startLine, startChar := OffsetToLineChar(templateBody, absOff)
			endLine, endChar := OffsetToLineChar(templateBody, absOff+len(name))
			available := make([]string, 0, len(fields))
			for _, f := range fields {
				available = append(available, f.Name)
			}
			diags = append(diags, Diagnostic{
				StartLine: startLine, StartChar: startChar,
				EndLine: endLine, EndChar: endChar,
				Message:  fmt.Sprintf("unknown prop %q on component %s; available: %s", name, compName, strings.Join(available, ", ")),
				Severity: 1,
			})
		}
	}
	return diags
}

// rawBlockRegex matches {{ raw }}...{{ endraw }} blocks. Used to strip
// raw blocks from the template body before running diagnostics.
var rawBlockRegex = regexp.MustCompile(`(?s)\{\{\s*raw\s*\}\}.*?\{\{\s*endraw\s*\}\}`)

// stripRawBlocks replaces {{ raw }}...{{ endraw }} blocks with spaces,
// preserving newlines for correct line-number mapping in diagnostics.
// This prevents false "unknown variable" errors for {{ .Var }} inside
// raw blocks, which are meant to be literal output.
func stripRawBlocks(body string) string {
	return rawBlockRegex.ReplaceAllStringFunc(body, func(match string) string {
		var result strings.Builder
		result.Grow(len(match))
		for _, ch := range match {
			if ch == '\n' {
				result.WriteRune('\n')
			} else {
				result.WriteRune(' ')
			}
		}
		return result.String()
	})
}

// ComponentTagContext is the result of detecting whether the cursor is inside
// a component tag in the template body.
type ComponentTagContext struct {
	ComponentName string   // the PascalCase component name
	ExistingProps []string // prop names already specified on the tag
}

// unclosedComponentCallRegex matches {{ X (dict ..., {{ wrap X (dict ...,
// or bare {{ X / {{ wrap X without a closing }} — indicating the cursor
// is inside the component call. Used as fallback when AST parsing fails.
// The trailing \s* allows matching when the cursor is after whitespace
// following the component name (e.g. "{{ Card |" where | is cursor).
var unclosedComponentCallRegex = regexp.MustCompile(`\{\{\s*(?:wrap\s+)?([A-Z][a-zA-Z0-9]*)(?:\s+\(dict\b([^)]*?))?\s*$`)

// DetectComponentTagContext determines if the cursor (given as a byte offset
// in the template body) is inside a component call. Uses AST when available,
// falls back to regex when the template fails to parse (during editing).
// Returns nil if the cursor is not inside a component call.
func DetectComponentTagContext(templateBody string, cursorOffset int, uses []parser.UseDeclaration, tree *parse.Tree) *ComponentTagContext {
	if cursorOffset < 0 || cursorOffset > len(templateBody) {
		return nil
	}

	known := make(map[string]bool, len(uses))
	for _, u := range uses {
		known[u.Name] = true
	}

	// AST-based detection when the template parses successfully
	if tree != nil {
		return detectComponentTagAST(tree, templateBody, cursorOffset, known)
	}

	// Regex fallback for when the template is syntactically incomplete
	return detectComponentTagRegex(templateBody, cursorOffset, known)
}

// detectComponentTagAST walks the AST to find if the cursor is inside a
// component call, and extracts existing prop keys from the dict args.
func detectComponentTagAST(tree *parse.Tree, templateBody string, cursorOffset int, known map[string]bool) *ComponentTagContext {
	calls := extractComponentCalls(tree, templateBody, known)
	for _, call := range calls {
		if cursorOffset < call.callOffset || cursorOffset > call.callEnd {
			continue
		}
		var existing []string
		for _, p := range call.props {
			existing = append(existing, p.name)
		}
		return &ComponentTagContext{
			ComponentName: call.name,
			ExistingProps: existing,
		}
	}
	return nil
}

// detectComponentTagRegex is the fallback for when the template can't be parsed.
func detectComponentTagRegex(templateBody string, cursorOffset int, known map[string]bool) *ComponentTagContext {
	before := templateBody[:cursorOffset]

	m := unclosedComponentCallRegex.FindStringSubmatch(before)
	if m == nil {
		return nil
	}

	name := m[1]
	if !known[name] {
		return nil
	}

	dictArgs := m[2] // may be empty for bare calls
	var existing []string
	for _, pm := range dictKeyRegex.FindAllStringSubmatch(dictArgs, -1) {
		existing = append(existing, pm[1])
	}

	return &ComponentTagContext{
		ComponentName: name,
		ExistingProps: existing,
	}
}

// PropValueContext is the result of detecting whether the cursor is inside a
// prop value expression within a component call's (dict ...) block.
type PropValueContext struct {
	AfterPipe bool // true if cursor is after a | — suggest functions, not variables
}

// DetectPropValueContext determines if the cursor is inside a prop value
// expression within a component call. With the new syntax, props are inside
// (dict "Key" .value ...) — the cursor is in a value position if it's
// inside a component call's dict and not immediately after a string key.
// Returns nil if the cursor is not in a prop value context.
func DetectPropValueContext(templateBody string, cursorOffset int) *PropValueContext {
	if cursorOffset <= 0 || cursorOffset > len(templateBody) {
		return nil
	}

	text := templateBody[:cursorOffset]

	// Check if we're inside a component call with a dict expression.
	// DetectPropValueContext only applies when there's an active dict;
	// bare component calls ({{ Card }}) don't have a value context.
	if !unclosedComponentCallRegex.MatchString(text) {
		return nil
	}

	// Verify there's actually a (dict in the match (capture group 2 is non-empty)
	m := unclosedComponentCallRegex.FindStringSubmatch(text)
	if m == nil || m[2] == "" {
		return nil
	}

	return &PropValueContext{
		AfterPipe: isAfterPipeInDict(text),
	}
}

// isAfterPipeInDict checks if the cursor is after a | inside a dict expression.
func isAfterPipeInDict(text string) bool {
	dictStart := strings.LastIndex(text, "(dict")
	if dictStart < 0 {
		return false
	}

	expr := text[dictStart:]
	inQuote := false
	lastPipe := -1

	for i, ch := range expr {
		switch ch {
		case '"':
			inQuote = !inQuote
		case '|':
			if !inQuote {
				lastPipe = i
			}
		}
	}

	return lastPipe >= 0
}

// ComponentPropCompletions returns completion items for a component's props.
// It filters out props that are already specified on the tag.
// When snippets is true, the insertText uses snippet syntax with a tabstop
// at the value position so the user can tab to fill in the value.
func ComponentPropCompletions(fields []codegen.StructField, existingProps []string, snippets bool) []CompletionItem {
	existing := make(map[string]bool, len(existingProps))
	for _, p := range existingProps {
		existing[p] = true
	}

	var items []CompletionItem
	for _, f := range fields {
		if existing[f.Name] {
			continue
		}
		insertText := `"` + f.Name + `" .`
		isSnippet := false
		if snippets {
			insertText = `"` + escapeSnippetText(f.Name) + `" ${1:.}`
			isSnippet = true
		}
		items = append(items, CompletionItem{
			Label:      f.Name,
			Detail:     f.Type,
			InsertText: insertText,
			IsSnippet:  isSnippet,
		})
	}
	return items
}

// BuildComponentSnippet builds a snippet insertText for a component call with
// a full dict skeleton containing tabstops for each prop value.
// Example: `Card (dict "Title" ${1:""} "Count" ${2:0})$0`
func BuildComponentSnippet(name string, fields []codegen.StructField) string {
	if len(fields) == 0 {
		return name + " (dict $0)"
	}
	var b strings.Builder
	b.WriteString(name)
	b.WriteString(" (dict")
	for i, f := range fields {
		b.WriteString(fmt.Sprintf(` "%s" ${%d:%s}`, escapeSnippetText(f.Name), i+1, snippetPlaceholderForType(f.Type)))
	}
	b.WriteString(")$0")
	return b.String()
}

// snippetPlaceholderForType returns a sensible default placeholder based on the Go type.
func snippetPlaceholderForType(typ string) string {
	switch {
	case strings.Contains(typ, "string"):
		return `""`
	case strings.Contains(typ, "int"), strings.Contains(typ, "float"):
		return "0"
	case strings.Contains(typ, "bool"):
		return "false"
	default:
		return "value"
	}
}

// escapeSnippetText escapes characters that have special meaning in LSP snippet syntax.
func escapeSnippetText(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `$`, `\$`)
	s = strings.ReplaceAll(s, `}`, `\}`)
	return s
}

// StripSnippetSyntax removes snippet tabstop syntax from text, returning
// plain text suitable for editors that don't support snippets.
func StripSnippetSyntax(s string) string {
	// Replace ${N:placeholder} with placeholder
	re := regexp.MustCompile(`\$\{[0-9]+:([^}]*)\}`)
	s = re.ReplaceAllString(s, "$1")
	// Remove bare $0 final tabstops
	s = strings.ReplaceAll(s, "$0", "")
	return s
}
