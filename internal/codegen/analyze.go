package codegen

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"unicode"
)

// VarInfo represents a variable declared in the frontmatter.
type VarInfo struct {
	Name string
	Line int // 1-indexed line within the frontmatter
}

// FrontmatterInfo contains the analysis results of a .gastro frontmatter block.
type FrontmatterInfo struct {
	ExportedVars  []VarInfo // Uppercase variables — available to the template
	PrivateVars   []VarInfo // Lowercase variables — private to the frontmatter
	IsPage        bool      // true if gastro.Context() is called
	IsComponent   bool      // true if gastro.Props[T]() is called
	PropsTypeName string    // the T in gastro.Props[T](), e.g. "Props"
}

const wrapperSuffix = "\n}"

// AnalyzeFrontmatter parses the frontmatter Go code and extracts variable
// declarations, gastro.Context() calls, and gastro.Props[T]() calls.
func AnalyzeFrontmatter(frontmatter string) (*FrontmatterInfo, error) {
	if strings.TrimSpace(frontmatter) == "" {
		return &FrontmatterInfo{}, nil
	}

	// Wrap frontmatter in a valid Go file so go/parser can handle it.
	// Type declarations (like `type Props struct{...}`) need to be at
	// package level, so we hoist them out of the function body.
	bodyLines, typeDecls := HoistTypeDeclarations(frontmatter)
	// Type declarations go after "package" but before the function wrapper.
	// We need to count how many lines precede the frontmatter body code
	// so we can map AST positions back to frontmatter line numbers.
	prefix := "package __gastro\n" + typeDecls + "func __handler() {\n"
	prefixLineCount := strings.Count(prefix, "\n")
	src := prefix + bodyLines + wrapperSuffix

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "frontmatter.go", src, parser.AllErrors)
	if err != nil {
		return nil, fmt.Errorf("parsing frontmatter: %w", err)
	}

	info := &FrontmatterInfo{}

	ast.Inspect(file, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.AssignStmt:
			if node.Tok == token.DEFINE { // :=
				for _, lhs := range node.Lhs {
					ident, ok := lhs.(*ast.Ident)
					if !ok || ident.Name == "_" {
						continue
					}
					classifyVar(info, ident, fset, prefixLineCount)
				}
			}

		case *ast.GenDecl:
			if node.Tok == token.VAR {
				for _, spec := range node.Specs {
					vs, ok := spec.(*ast.ValueSpec)
					if !ok {
						continue
					}
					for _, ident := range vs.Names {
						if ident.Name == "_" {
							continue
						}
						classifyVar(info, ident, fset, prefixLineCount)
					}
				}
			}

		case *ast.CallExpr:
			detectGastroMarkers(info, node)
		}

		return true
	})

	return info, nil
}

// classifyVar adds a variable to ExportedVars or PrivateVars based on whether
// its name starts with an uppercase letter.
func classifyVar(info *FrontmatterInfo, ident *ast.Ident, fset *token.FileSet, prefixLineCount int) {
	pos := fset.Position(ident.Pos())
	vi := VarInfo{
		Name: ident.Name,
		Line: pos.Line - prefixLineCount,
	}

	if isExported(ident.Name) {
		info.ExportedVars = append(info.ExportedVars, vi)
	} else {
		info.PrivateVars = append(info.PrivateVars, vi)
	}
}

func isExported(name string) bool {
	if name == "" {
		return false
	}
	return unicode.IsUpper(rune(name[0]))
}

// detectGastroMarkers checks if a call expression is gastro.Context() or
// gastro.Props[T]() and updates info accordingly.
func detectGastroMarkers(info *FrontmatterInfo, call *ast.CallExpr) {
	switch fn := call.Fun.(type) {
	case *ast.SelectorExpr:
		// gastro.Context()
		ident, ok := fn.X.(*ast.Ident)
		if ok && ident.Name == "gastro" && fn.Sel.Name == "Context" {
			info.IsPage = true
		}

	case *ast.IndexExpr:
		// gastro.Props[T]() — go/ast represents generic calls as IndexExpr
		sel, ok := fn.X.(*ast.SelectorExpr)
		if !ok {
			return
		}
		ident, ok := sel.X.(*ast.Ident)
		if !ok {
			return
		}
		if ident.Name == "gastro" && sel.Sel.Name == "Props" {
			info.IsComponent = true
			if typeIdent, ok := fn.Index.(*ast.Ident); ok {
				info.PropsTypeName = typeIdent.Name
			}
		}
	}
}

// HoistTypeDeclarations extracts `type ... struct { ... }` declarations from
// frontmatter and returns them separately, since they need to be at package
// level for go/parser to accept them.
func HoistTypeDeclarations(frontmatter string) (body string, typeDecls string) {
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
