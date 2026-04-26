package codegen

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"

	gastroParser "github.com/andrioid/gastro/internal/parser"
)

// VarInfo represents a variable declared in the frontmatter.
type VarInfo struct {
	Name string
	Line int // 1-indexed line within the frontmatter
}

// Warning represents a non-fatal issue detected during frontmatter analysis.
// Warnings don't block compilation but indicate likely mistakes.
type Warning struct {
	Line    int // 1-indexed line within the frontmatter
	Message string
}

// FrontmatterInfo contains the analysis results of a .gastro frontmatter block.
type FrontmatterInfo struct {
	ExportedVars  []VarInfo // Uppercase variables — available to the template
	PrivateVars   []VarInfo // Lowercase variables — private to the frontmatter
	IsPage        bool      // true if gastro.Context() is called
	IsComponent   bool      // true if gastro.Props() is called
	PropsTypeName string    // e.g. "Props" — from type Props struct in the frontmatter
	Warnings      []Warning // Non-fatal issues detected during analysis
}

const wrapperSuffix = "\n}"

// AnalyzeFrontmatter parses the frontmatter Go code and extracts variable
// declarations, gastro.Context() calls, and gastro.Props() calls.
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

	// Count type declarations named "Props" for validation
	var propsTypeCount int
	var propsIsStruct bool

	// Track exported vars assigned bare gastro.Props() (no field selector).
	// These are almost always a mistake — the user likely meant gastro.Props().FieldName.
	type barePropsVar struct {
		name string
		line int // 1-indexed within frontmatter
	}
	var barePropsExportedVars []barePropsVar

	ast.Inspect(file, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.AssignStmt:
			if node.Tok == token.DEFINE { // :=
				for i, lhs := range node.Lhs {
					ident, ok := lhs.(*ast.Ident)
					if !ok || ident.Name == "_" {
						continue
					}
					classifyVar(info, ident, fset, prefixLineCount)

					// Check if this exported var is assigned bare gastro.Props()
					if token.IsExported(ident.Name) && i < len(node.Rhs) {
						if call, ok := node.Rhs[i].(*ast.CallExpr); ok && isGastroPropsCall(call) {
							pos := fset.Position(ident.Pos())
							barePropsExportedVars = append(barePropsExportedVars, barePropsVar{
								name: ident.Name,
								line: pos.Line - prefixLineCount,
							})
						}
					}
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
			if node.Tok == token.TYPE {
				for _, spec := range node.Specs {
					ts, ok := spec.(*ast.TypeSpec)
					if !ok {
						continue
					}
					if ts.Name.Name == "Props" {
						propsTypeCount++
						if _, ok := ts.Type.(*ast.StructType); ok {
							propsIsStruct = true
						}
					}
				}
			}

		case *ast.CallExpr:
			detectGastroMarkers(info, node)
		}

		return true
	})

	if err := validateFrontmatter(info, propsTypeCount, propsIsStruct); err != nil {
		return nil, err
	}

	// Populate warnings for bare gastro.Props() on exported vars.
	// These are non-fatal: the component still compiles but renders incorrectly.
	// Line numbers from the AST are relative to the body after type hoisting,
	// so we look up the original line in the raw frontmatter instead.
	for _, bpv := range barePropsExportedVars {
		origLine := findLineInFrontmatter(frontmatter, bpv.name+` := gastro.Props()`)
		info.Warnings = append(info.Warnings, Warning{
			Line:    origLine,
			Message: fmt.Sprintf("%s is assigned the entire Props struct; did you mean gastro.Props().%s?", bpv.name, bpv.name),
		})
	}

	// Detect references to ctx without the gastro.Context() marker.
	// The marker tells the code generator to inject
	// `ctx := gastroRuntime.NewContext(w, r)` at the top of the handler.
	// Without it, ctx is undefined and the user gets a confusing
	// "undefined: ctx" error from the Go compiler. Surface a friendlier
	// hint here.
	if !info.IsPage && !info.IsComponent {
		if line, ok := findUndeclaredCtxReference(file, fset, prefixLineCount); ok {
			info.Warnings = append(info.Warnings, Warning{
				Line:    line,
				Message: "ctx is referenced but gastro.Context() was not called: add `ctx := gastro.Context()` to mark this file as a page",
			})
		}
	}

	return info, nil
}

// findUndeclaredCtxReference returns the 1-indexed frontmatter line number of
// the first use of `ctx` that is not on the LHS of an assignment, when no
// declaration of `ctx` exists anywhere in the file. Returns (0, false) when
// `ctx` is properly declared or never referenced.
//
// Heuristic, not a full scope analysis. False positives are acceptable
// because the result is a non-blocking warning, not an error.
func findUndeclaredCtxReference(file *ast.File, fset *token.FileSet, prefixLineCount int) (int, bool) {
	// Collect declarations and LHS positions in one pass.
	declared := false
	lhs := map[token.Pos]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.AssignStmt:
			if node.Tok == token.DEFINE {
				for _, l := range node.Lhs {
					if id, ok := l.(*ast.Ident); ok {
						lhs[id.NamePos] = true
						if id.Name == "ctx" {
							declared = true
						}
					}
				}
			}
		case *ast.ValueSpec:
			for _, id := range node.Names {
				if id.Name == "ctx" {
					declared = true
				}
			}
		case *ast.FuncDecl:
			if node.Type.Params != nil {
				for _, p := range node.Type.Params.List {
					for _, id := range p.Names {
						if id.Name == "ctx" {
							declared = true
						}
					}
				}
			}
		}
		return true
	})
	if declared {
		return 0, false
	}

	var foundPos token.Pos
	ast.Inspect(file, func(n ast.Node) bool {
		if foundPos != token.NoPos {
			return false
		}
		id, ok := n.(*ast.Ident)
		if !ok || id.Name != "ctx" {
			return true
		}
		if lhs[id.NamePos] {
			return true
		}
		foundPos = id.NamePos
		return false
	})
	if foundPos == token.NoPos {
		return 0, false
	}

	line := fset.Position(foundPos).Line - prefixLineCount
	if line < 1 {
		return 0, false
	}
	return line, true
}

// validateFrontmatter checks for consistency between gastro markers and type
// declarations. Returns an error for invalid combinations.
func validateFrontmatter(info *FrontmatterInfo, propsTypeCount int, propsIsStruct bool) error {
	if info.IsPage && info.IsComponent {
		return fmt.Errorf("frontmatter cannot use both gastro.Context() and gastro.Props(): choose one")
	}

	if info.IsComponent && propsTypeCount == 0 {
		return fmt.Errorf("component uses gastro.Props() but no 'type Props struct' is defined")
	}

	if propsTypeCount > 1 {
		return fmt.Errorf("multiple 'type Props struct' declarations found: only one is allowed")
	}

	if propsTypeCount == 1 && !propsIsStruct {
		return fmt.Errorf("'type Props' must be a struct type")
	}

	return nil
}

// findLineInFrontmatter returns the 1-indexed line number within the
// frontmatter where substr first appears, or 1 if not found.
func findLineInFrontmatter(frontmatter, substr string) int {
	for i, line := range strings.Split(frontmatter, "\n") {
		if strings.Contains(strings.TrimSpace(line), substr) {
			return i + 1
		}
	}
	return 1
}

// classifyVar adds a variable to ExportedVars or PrivateVars based on whether
// its name starts with an uppercase letter.
func classifyVar(info *FrontmatterInfo, ident *ast.Ident, fset *token.FileSet, prefixLineCount int) {
	pos := fset.Position(ident.Pos())
	vi := VarInfo{
		Name: ident.Name,
		Line: pos.Line - prefixLineCount,
	}

	if token.IsExported(ident.Name) {
		info.ExportedVars = append(info.ExportedVars, vi)
	} else {
		info.PrivateVars = append(info.PrivateVars, vi)
	}
}

// detectGastroMarkers checks if a call expression is gastro.Context() or
// gastro.Props() and updates info accordingly.
func detectGastroMarkers(info *FrontmatterInfo, call *ast.CallExpr) {
	fn, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return
	}
	ident, ok := fn.X.(*ast.Ident)
	if !ok {
		return
	}
	if ident.Name != "gastro" {
		return
	}
	switch fn.Sel.Name {
	case "Context":
		info.IsPage = true
	case "Props":
		info.IsComponent = true
		info.PropsTypeName = "Props"
	}
}

// isGastroPropsCall returns true if the call expression is gastro.Props().
func isGastroPropsCall(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	ident, ok := sel.X.(*ast.Ident)
	return ok && ident.Name == "gastro" && sel.Sel.Name == "Props"
}

// HoistTypeDeclarations extracts `type ... struct { ... }` declarations from
// frontmatter and returns them separately, since they need to be at package
// level for go/parser to accept them.
func HoistTypeDeclarations(frontmatter string) (body string, typeDecls string) {
	lines := strings.Split(frontmatter, "\n")
	var bodyLines []string
	var typeLines []string
	inType := false
	inBacktick := false
	braceDepth := 0

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Track backtick string state to avoid falsely matching
		// "type " lines inside multi-line raw string literals.
		if !inType {
			if inBacktick {
				// Inside a backtick string — don't look for type declarations
				if gastroParser.HasUnclosedBacktick(line) {
					inBacktick = false
				}
				bodyLines = append(bodyLines, line)
				continue
			}
			if gastroParser.HasUnclosedBacktick(line) {
				inBacktick = true
				bodyLines = append(bodyLines, line)
				continue
			}
		}

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
