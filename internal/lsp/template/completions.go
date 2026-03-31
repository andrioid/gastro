package template

import (
	"fmt"
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
// invalid double-dot syntax, and unknown components.
//
// When the template is syntactically valid, it uses Go's text/template/parse
// to build an AST and walks it with scope awareness (range/with blocks rebind
// the dot context). When parsing fails (common during editing), variable
// checks are skipped to avoid false positives — only double-dot syntax and
// unknown component checks run in that case.
func Diagnose(templateBody string, info *codegen.FrontmatterInfo, uses []parser.UseDeclaration, typeMap map[string]string, resolver FieldResolver) []Diagnostic {
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

// diagnoseUnknownComponents detects <PascalCase> tags not imported via 'use'.
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
				Message:   fmt.Sprintf("unknown component %q: not imported via 'use'", compName),
			})
		}
	}
	return diags
}
