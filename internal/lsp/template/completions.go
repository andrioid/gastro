package template

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"

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
	diags = append(diags, DiagnoseComponentProps(templateBody, uses, propsMap)...)

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

// diagnoseUnknownComponents detects <PascalCase> tags that are not imported.
func diagnoseUnknownComponents(templateBody string, uses []parser.UseDeclaration) []Diagnostic {
	knownComponents := make(map[string]bool, len(uses))
	for _, u := range uses {
		knownComponents[u.Name] = true
	}

	var diags []Diagnostic
	compRe := regexp.MustCompile(`<([A-Z][a-zA-Z0-9]*)[\s/>]`)
	for _, idx := range compRe.FindAllStringSubmatchIndex(templateBody, -1) {
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

// componentTagRegex matches self-closing and open component tags with their props.
var componentTagRegex = regexp.MustCompile(`<([A-Z][a-zA-Z0-9]*)((?:\s+\w+=(?:\{[^}]*\}|"[^"]*"))*)\s*/?>`)

// componentPropRegex matches Key={.expr} or Key="literal" patterns inside a tag.
var componentPropRegex = regexp.MustCompile(`(\w+)=(?:\{[^}]*\}|"[^"]*")`)

// DiagnoseComponentProps checks that props passed to component tags match
// the component's Props struct. Reports unknown props as errors and missing
// props as warnings.
func DiagnoseComponentProps(templateBody string, uses []parser.UseDeclaration, propsMap map[string][]codegen.StructField) []Diagnostic {
	if len(propsMap) == 0 {
		return nil
	}

	usePaths := make(map[string]string, len(uses))
	for _, u := range uses {
		usePaths[u.Name] = u.Path
	}

	var diags []Diagnostic

	for _, idx := range componentTagRegex.FindAllStringSubmatchIndex(templateBody, -1) {
		compName := templateBody[idx[2]:idx[3]]
		propsStr := ""
		if idx[4] >= 0 && idx[5] >= 0 {
			propsStr = strings.TrimSpace(templateBody[idx[4]:idx[5]])
		}

		fields, ok := propsMap[compName]
		if !ok {
			continue
		}

		// Build set of known field names
		fieldNames := make(map[string]bool, len(fields))
		for _, f := range fields {
			fieldNames[f.Name] = true
		}

		// Extract provided prop names
		providedProps := make(map[string]bool)
		propMatches := componentPropRegex.FindAllStringSubmatch(propsStr, -1)
		for _, m := range propMatches {
			providedProps[m[1]] = true
		}

		// Check for unknown props
		for _, m := range componentPropRegex.FindAllStringSubmatchIndex(propsStr, -1) {
			propName := propsStr[m[2]:m[3]]
			if !fieldNames[propName] {
				// Calculate position relative to the full template body
				propAbsOffset := idx[4] + m[2]
				// Skip leading whitespace that was trimmed
				if propsStr != templateBody[idx[4]:idx[5]] {
					propAbsOffset = strings.Index(templateBody[idx[4]:idx[5]], propsStr) + idx[4] + m[2]
				}
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
					Message:   fmt.Sprintf("unknown prop %q on component <%s>; available: %s", propName, compName, strings.Join(available, ", ")),
					Severity:  1,
				})
			}
		}

		// Check for missing props (warning)
		tagStartLine, tagStartChar := OffsetToLineChar(templateBody, idx[0])
		tagEndLine, tagEndChar := OffsetToLineChar(templateBody, idx[1])
		for _, f := range fields {
			if !providedProps[f.Name] {
				diags = append(diags, Diagnostic{
					StartLine: tagStartLine,
					StartChar: tagStartChar,
					EndLine:   tagEndLine,
					EndChar:   tagEndChar,
					Message:   fmt.Sprintf("missing prop %q on component <%s>", f.Name, compName),
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

// unclosedTagRegex matches an opening component tag up to where the cursor
// might be positioned (no closing > or />).
var unclosedTagRegex = regexp.MustCompile(`<([A-Z][a-zA-Z0-9]*)((?:\s+\w+=(?:\{[^}]*\}|"[^"]*"))*)\s*$`)

// DetectComponentTagContext determines if the cursor (given as a byte offset
// in the template body) is inside a component tag. Returns nil if the cursor
// is not inside a component tag.
func DetectComponentTagContext(templateBody string, cursorOffset int, uses []parser.UseDeclaration) *ComponentTagContext {
	if cursorOffset < 0 || cursorOffset > len(templateBody) {
		return nil
	}

	// Build set of known component names
	known := make(map[string]bool, len(uses))
	for _, u := range uses {
		known[u.Name] = true
	}

	// Take the text before the cursor and look for an unclosed component tag
	before := templateBody[:cursorOffset]

	// Find the last `<` that starts a PascalCase tag
	lastOpen := strings.LastIndex(before, "<")
	if lastOpen < 0 {
		return nil
	}

	// Check for a closing `>` or `/>` between the tag open and cursor —
	// if found, the tag is already closed and cursor is not inside it
	afterOpen := before[lastOpen:]
	if strings.Contains(afterOpen, "/>") || strings.ContainsRune(afterOpen, '>') {
		return nil
	}

	// Match the tag pattern
	m := unclosedTagRegex.FindStringSubmatch(afterOpen)
	if m == nil {
		return nil
	}

	name := m[1]
	if !known[name] {
		return nil
	}

	// Extract already-specified props from the attributes string
	attrsStr := m[2]
	var existing []string
	propRe := regexp.MustCompile(`(\w+)=`)
	for _, pm := range propRe.FindAllStringSubmatch(attrsStr, -1) {
		existing = append(existing, pm[1])
	}

	return &ComponentTagContext{
		ComponentName: name,
		ExistingProps: existing,
	}
}

// PropValueContext is the result of detecting whether the cursor is inside a
// prop value expression ({...}) within a component tag.
type PropValueContext struct {
	AfterPipe bool // true if cursor is after a | — suggest functions, not variables
}

// DetectPropValueContext determines if the cursor is inside an incomplete
// prop value expression within a component tag. Returns nil if the cursor
// is not in a prop value context.
func DetectPropValueContext(templateBody string, cursorOffset int) *PropValueContext {
	if cursorOffset <= 0 || cursorOffset > len(templateBody) {
		return nil
	}

	text := templateBody[:cursorOffset]

	if !isInsideComponentTag(text) {
		return nil
	}
	if !isInsideUnclosedBrace(text) {
		return nil
	}

	return &PropValueContext{
		AfterPipe: isAfterPipeOutsideQuotes(text),
	}
}

// isInsideComponentTag returns true if the text ends inside an unclosed
// component tag (a <PascalCase tag with no closing > or />).
func isInsideComponentTag(text string) bool {
	lastOpen := strings.LastIndex(text, "<")
	if lastOpen < 0 {
		return false
	}

	afterOpen := text[lastOpen:]

	// If there's a > or /> after the <, the tag is closed
	if strings.Contains(afterOpen, "/>") || strings.ContainsRune(afterOpen, '>') {
		return false
	}

	// Check that the tag starts with a PascalCase name (component tag)
	if len(afterOpen) < 2 {
		return false
	}
	return afterOpen[1] >= 'A' && afterOpen[1] <= 'Z'
}

// isInsideUnclosedBrace returns true if the text has an unclosed { — meaning
// the last { appears after the last }.
func isInsideUnclosedBrace(text string) bool {
	lastOpen := strings.LastIndex(text, "{")
	lastClose := strings.LastIndex(text, "}")
	return lastOpen > lastClose
}

// isAfterPipeOutsideQuotes returns true if the cursor (end of text) is after
// a | character that is not inside a quoted string. Scans from the last
// unclosed { to the end of text.
func isAfterPipeOutsideQuotes(text string) bool {
	braceStart := strings.LastIndex(text, "{")
	if braceStart < 0 {
		return false
	}

	expr := text[braceStart+1:]
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

	// Cursor is after a pipe if the last unquoted | exists and there's
	// only whitespace and identifier chars between it and the cursor
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
			InsertText: f.Name + `={.}`,
		})
	}
	return items
}
