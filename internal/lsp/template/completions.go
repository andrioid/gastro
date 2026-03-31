package template

import (
	"fmt"
	"regexp"

	"github.com/andrioid/gastro/internal/codegen"
	"github.com/andrioid/gastro/internal/parser"
	"github.com/andrioid/gastro/pkg/gastro"
)

// CompletionItem represents an LSP completion suggestion.
type CompletionItem struct {
	Label      string
	Detail     string // type info or description
	InsertText string // text to insert (may differ from label)
}

// Diagnostic represents an LSP diagnostic (error/warning).
type Diagnostic struct {
	Line    int
	Message string
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
// available in {{ }} expressions (built-in + pipe targets).
func FuncMapCompletions() []CompletionItem {
	funcs := gastro.DefaultFuncs()
	items := make([]CompletionItem, 0, len(funcs))
	for name := range funcs {
		items = append(items, CompletionItem{
			Label:      name,
			Detail:     "template function",
			InsertText: name,
		})
	}
	return items
}

// Diagnose checks a template body for common errors: unknown variables
// and unknown components.
func Diagnose(templateBody string, info *codegen.FrontmatterInfo, uses []parser.UseDeclaration) []Diagnostic {
	var diags []Diagnostic

	// Check for unknown template variables
	exportedNames := make(map[string]bool, len(info.ExportedVars))
	for _, v := range info.ExportedVars {
		exportedNames[v.Name] = true
	}

	// Find all {{ .VarName }} references
	varRe := regexp.MustCompile(`\{\{[^}]*\.([A-Z][a-zA-Z0-9]*)[^}]*\}\}`)
	for _, match := range varRe.FindAllStringSubmatch(templateBody, -1) {
		varName := match[1]
		if !exportedNames[varName] {
			diags = append(diags, Diagnostic{
				Message: fmt.Sprintf("unknown template variable %q", "."+varName),
			})
		}
	}

	// Check for unknown components
	knownComponents := make(map[string]bool, len(uses))
	for _, u := range uses {
		knownComponents[u.Name] = true
	}

	// Find all <PascalCase component references
	compRe := regexp.MustCompile(`<([A-Z][a-zA-Z0-9]*)[\s/>]`)
	for _, match := range compRe.FindAllStringSubmatch(templateBody, -1) {
		compName := match[1]
		if !knownComponents[compName] {
			diags = append(diags, Diagnostic{
				Message: fmt.Sprintf("unknown component %q: not imported via 'use'", compName),
			})
		}
	}

	return diags
}
