package format

import (
	"fmt"
	"go/ast"
	goformat "go/format"
	goparser "go/parser"
	"go/token"
	"sort"
	"strings"

	"github.com/andrioid/gastro/internal/parser"
)

// formatFrontmatter formats the Go frontmatter of a .gastro file.
// It receives the already-stripped frontmatter (no imports), the extracted
// Go import paths, and the extracted component import declarations.
func formatFrontmatter(frontmatter string, goImports []string, uses []parser.UseDeclaration) (string, error) {
	importBlock := formatImportBlock(goImports, uses)

	codeBody, err := formatGoBody(frontmatter)
	if err != nil {
		return "", err
	}

	var parts []string
	if importBlock != "" {
		parts = append(parts, importBlock)
	}
	if codeBody != "" {
		parts = append(parts, codeBody)
	}

	result := strings.Join(parts, "\n\n")
	result = collapseBlankLines(result)
	result = strings.TrimSpace(result)
	return result, nil
}

// formatImportBlock formats Go and component imports into a canonical block.
// Two groups separated by a blank line: Go imports, then component imports.
// Each group is sorted alphabetically.
func formatImportBlock(goImports []string, uses []parser.UseDeclaration) string {
	if len(goImports) == 0 && len(uses) == 0 {
		return ""
	}

	sort.Strings(goImports)

	sort.Slice(uses, func(i, j int) bool {
		return uses[i].Name < uses[j].Name
	})

	var buf strings.Builder
	buf.WriteString("import (\n")

	for _, imp := range goImports {
		fmt.Fprintf(&buf, "\t%q\n", imp)
	}

	if len(goImports) > 0 && len(uses) > 0 {
		buf.WriteString("\n")
	}

	for _, use := range uses {
		fmt.Fprintf(&buf, "\t%s %q\n", use.Name, use.Path)
	}

	buf.WriteString(")")
	return buf.String()
}

// formatGoBody formats the Go code body (frontmatter with imports already
// stripped) using go/format. Type declarations are hoisted to package level
// for go/parser compatibility.
func formatGoBody(code string) (string, error) {
	if strings.TrimSpace(code) == "" {
		return "", nil
	}

	bodyLines, typeDecls := hoistTypeDeclarations(code)

	if strings.TrimSpace(bodyLines) == "" && strings.TrimSpace(typeDecls) == "" {
		return "", nil
	}

	// Build a valid Go file for go/format
	var src strings.Builder
	src.WriteString("package __gastro\n\n")

	hasTypeDecls := strings.TrimSpace(typeDecls) != ""
	if hasTypeDecls {
		src.WriteString(typeDecls)
		src.WriteString("\n\n")
	}

	hasBody := strings.TrimSpace(bodyLines) != ""
	if hasBody {
		src.WriteString("func __handler() {\n")
		src.WriteString(bodyLines)
		src.WriteString("\n}")
	}

	formatted, err := goformat.Source([]byte(src.String()))
	if err != nil {
		// go/format failed — return original code unchanged
		return strings.TrimSpace(code), nil
	}

	return unwrapFormattedGo(string(formatted), hasTypeDecls, hasBody)
}

// unwrapFormattedGo uses go/ast to precisely extract the formatted type
// declarations and function body from the synthetic Go file.
func unwrapFormattedGo(formatted string, hasTypeDecls, hasBody bool) (string, error) {
	fset := token.NewFileSet()
	f, err := goparser.ParseFile(fset, "", formatted, goparser.ParseComments)
	if err != nil {
		return "", fmt.Errorf("re-parse formatted code: %w", err)
	}

	var typeLines string
	var bodyLines string

	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.GenDecl:
			if d.Tok == token.TYPE && hasTypeDecls {
				start := fset.Position(d.Pos()).Offset
				end := fset.Position(d.End()).Offset
				typeLines += strings.TrimSpace(formatted[start:end]) + "\n"
			}

		case *ast.FuncDecl:
			if d.Name.Name == "__handler" && hasBody {
				// Extract content between { and }
				lbrace := fset.Position(d.Body.Lbrace).Offset
				rbrace := fset.Position(d.Body.Rbrace).Offset

				inner := formatted[lbrace+1 : rbrace]
				bodyLines = dedentOneLevel(inner)
			}
		}
	}

	typeLines = strings.TrimSpace(typeLines)
	bodyLines = strings.TrimSpace(bodyLines)

	var parts []string
	if typeLines != "" {
		parts = append(parts, typeLines)
	}
	if bodyLines != "" {
		parts = append(parts, bodyLines)
	}

	return strings.Join(parts, "\n\n"), nil
}

// dedentOneLevel removes one leading tab from each line.
func dedentOneLevel(s string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, "\t") {
			lines[i] = line[1:]
		}
	}
	return strings.Join(lines, "\n")
}

// hoistTypeDeclarations extracts `type ... struct { ... }` declarations from
// frontmatter and returns them separately, since they need to be at package
// level for go/parser to accept them.
//
// Duplicated from internal/codegen/analyze.go to avoid cross-package
// dependencies. If that logic changes, this copy must be updated too.
func hoistTypeDeclarations(frontmatter string) (body string, typeDecls string) {
	lines := strings.Split(frontmatter, "\n")
	var bodyLines []string
	var typeLines []string
	inType := false
	braceDepth := 0

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if !inType && strings.HasPrefix(trimmed, "type ") {
			inType = true
			braceDepth = 0
		}

		if inType {
			typeLines = append(typeLines, line)
			braceDepth += strings.Count(line, "{") - strings.Count(line, "}")
			if braceDepth <= 0 {
				inType = false
			}
		} else {
			bodyLines = append(bodyLines, line)
		}
	}

	return strings.Join(bodyLines, "\n"), strings.Join(typeLines, "\n") + "\n"
}
