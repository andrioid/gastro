package template

import (
	"fmt"
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
	InsertText string // text to insert (may differ from label)
	FilterText string // text the editor uses for fuzzy matching (optional)
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

// FuncMapCompletions returns completion items for template functions
// available in {{ }} expressions (gastro functions + Go template builtins).
func FuncMapCompletions() []CompletionItem {
	funcs := gastro.DefaultFuncs()
	items := make([]CompletionItem, 0, len(funcs)+len(goTemplateBuiltins))
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
func Diagnose(templateBody string, info *codegen.FrontmatterInfo, uses []parser.UseDeclaration, typeMap map[string]string, resolver FieldResolver, propsMap map[string][]codegen.StructField) []Diagnostic {
	var diags []Diagnostic

	exportedNames := make(map[string]bool, len(info.ExportedVars))
	for _, v := range info.ExportedVars {
		exportedNames[v.Name] = true
	}

	// Double-dot syntax is always invalid — check with regex regardless of
	// whether the AST parses (the parser itself rejects double-dot).
	diags = append(diags, diagnoseDoubleDot(templateBody)...)

	// Attempt AST-based scope-aware variable checking
	tree, err := ParseTemplateBody(templateBody, uses)
	if err == nil && tree != nil {
		diags = append(diags, WalkDiagnostics(tree, templateBody, exportedNames, typeMap, resolver)...)
	}

	diags = append(diags, diagnoseUnknownComponents(templateBody, uses)...)
	diags = append(diags, DiagnoseComponentProps(templateBody, tree, uses, propsMap)...)

	return diags
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
		if !isPascalCase(name) || knownComponents[name] {
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

// isPascalCase returns true if the string starts with an uppercase letter.
func isPascalCase(s string) bool {
	return len(s) > 0 && s[0] >= 'A' && s[0] <= 'Z'
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
			if !ok || !isPascalCase(ident1.Ident) {
				return
			}
			compName = ident1.Ident
			compIdent = ident1
			dictArgIndex = 2
		} else if isPascalCase(ident0.Ident) {
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

// dictKeyRegex matches string keys inside a dict call: "KeyName"
var dictKeyRegex = regexp.MustCompile(`"([A-Z][a-zA-Z0-9]*)"`)

// DiagnoseComponentProps checks that props passed to component calls match
// the component's Props struct. Reports unknown props as errors and missing
// props as warnings.
//
// Primary: walks the AST to find component calls and extract dict keys.
// Fallback: uses regex when the template fails to parse (common during editing).
func DiagnoseComponentProps(templateBody string, tree *parse.Tree, uses []parser.UseDeclaration, propsMap map[string][]codegen.StructField) []Diagnostic {
	if len(propsMap) == 0 {
		return nil
	}

	if tree != nil {
		return diagnoseComponentPropsAST(templateBody, tree, uses, propsMap)
	}
	return diagnoseComponentPropsRegex(templateBody, uses, propsMap)
}

func diagnoseComponentPropsAST(templateBody string, tree *parse.Tree, uses []parser.UseDeclaration, propsMap map[string][]codegen.StructField) []Diagnostic {
	knownComponents := make(map[string]bool, len(uses))
	for _, u := range uses {
		knownComponents[u.Name] = true
	}

	calls := extractComponentCalls(tree, templateBody, knownComponents)
	var diags []Diagnostic

	for _, call := range calls {
		fields, ok := propsMap[call.name]
		if !ok {
			continue
		}

		fieldNames := make(map[string]bool, len(fields))
		for _, f := range fields {
			fieldNames[f.Name] = true
		}

		providedProps := make(map[string]bool)
		for _, p := range call.props {
			providedProps[p.name] = true
		}

		// Check for unknown props
		for _, p := range call.props {
			if !fieldNames[p.name] {
				startLine, startChar := OffsetToLineChar(templateBody, p.offset)
				endLine, endChar := OffsetToLineChar(templateBody, p.offset+len(p.name))

				available := make([]string, 0, len(fields))
				for _, f := range fields {
					available = append(available, f.Name)
				}
				diags = append(diags, Diagnostic{
					StartLine: startLine,
					StartChar: startChar,
					EndLine:   endLine,
					EndChar:   endChar,
					Message:   fmt.Sprintf("unknown prop %q on component %s; available: %s", p.name, call.name, strings.Join(available, ", ")),
					Severity:  1,
				})
			}
		}

		// Check for missing props (warning)
		tagStartLine, tagStartChar := OffsetToLineChar(templateBody, call.nameOffset)
		tagEndLine, tagEndChar := OffsetToLineChar(templateBody, call.nameEnd)
		for _, f := range fields {
			if !providedProps[f.Name] {
				diags = append(diags, Diagnostic{
					StartLine: tagStartLine,
					StartChar: tagStartChar,
					EndLine:   tagEndLine,
					EndChar:   tagEndChar,
					Message:   fmt.Sprintf("missing prop %q on component %s", f.Name, call.name),
					Severity:  2,
				})
			}
		}
	}

	return diags
}

// diagnoseComponentPropsRegex is the fallback when the AST can't be parsed.
func diagnoseComponentPropsRegex(templateBody string, uses []parser.UseDeclaration, propsMap map[string][]codegen.StructField) []Diagnostic {
	var diags []Diagnostic

	for _, idx := range componentCallFallbackRegex.FindAllStringSubmatchIndex(templateBody, -1) {
		compName := templateBody[idx[2]:idx[3]]
		dictArgs := ""
		if idx[4] >= 0 && idx[5] >= 0 {
			dictArgs = templateBody[idx[4]:idx[5]]
		}

		fields, ok := propsMap[compName]
		if !ok {
			continue
		}

		fieldNames := make(map[string]bool, len(fields))
		for _, f := range fields {
			fieldNames[f.Name] = true
		}

		providedProps := make(map[string]bool)
		for _, m := range dictKeyRegex.FindAllStringSubmatch(dictArgs, -1) {
			providedProps[m[1]] = true
		}

		// Check for unknown props
		for _, m := range dictKeyRegex.FindAllStringSubmatchIndex(dictArgs, -1) {
			propName := dictArgs[m[2]:m[3]]
			if !fieldNames[propName] {
				propAbsOffset := idx[4] + m[2]
				startLine, startChar := OffsetToLineChar(templateBody, propAbsOffset)
				endLine, endChar := OffsetToLineChar(templateBody, propAbsOffset+len(propName))

				available := make([]string, 0, len(fields))
				for _, f := range fields {
					available = append(available, f.Name)
				}
				diags = append(diags, Diagnostic{
					StartLine: startLine,
					StartChar: startChar,
					EndLine:   endLine,
					EndChar:   endChar,
					Message:   fmt.Sprintf("unknown prop %q on component %s; available: %s", propName, compName, strings.Join(available, ", ")),
					Severity:  1,
				})
			}
		}

		// Check for missing props (warning)
		tagStartLine, tagStartChar := OffsetToLineChar(templateBody, idx[2])
		tagEndLine, tagEndChar := OffsetToLineChar(templateBody, idx[3])
		for _, f := range fields {
			if !providedProps[f.Name] {
				diags = append(diags, Diagnostic{
					StartLine: tagStartLine,
					StartChar: tagStartChar,
					EndLine:   tagEndLine,
					EndChar:   tagEndChar,
					Message:   fmt.Sprintf("missing prop %q on component %s", f.Name, compName),
					Severity:  2,
				})
			}
		}
	}

	return diags
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
func ComponentPropCompletions(fields []codegen.StructField, existingProps []string) []CompletionItem {
	existing := make(map[string]bool, len(existingProps))
	for _, p := range existingProps {
		existing[p] = true
	}

	var items []CompletionItem
	for _, f := range fields {
		if existing[f.Name] {
			continue
		}
		items = append(items, CompletionItem{
			Label:      f.Name,
			Detail:     f.Type,
			InsertText: `"` + f.Name + `" .`,
		})
	}
	return items
}
