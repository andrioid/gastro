package codegen

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
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

	// Track B (page model v2): surface marker-rewrite warnings and
	// missing-return findings here so both the codegen pipeline and the
	// LSP pick them up by reading info.Warnings. The rewriter is run
	// for its diagnostic side effects only — the rewritten source is
	// produced separately by GenerateHandler.
	_, markerWarnings := rewriteGastroMarkers(frontmatter)
	info.Warnings = append(info.Warnings, markerWarnings...)

	// Page-only: response-write → missing-return checks (§4.9). The
	// signal is whether the file is a page; we don't have that here, so
	// the analyzer is run unconditionally and components naturally
	// produce no findings (they have no ambient w / r). Components that
	// happen to bind a local w may get a documented false positive; the
	// alternative is plumbing isPage all the way down, which the
	// minimal-LSP posture (§4.7) doesn't justify.
	respwrite := ValidateFrontmatterReturns(frontmatter)
	info.Warnings = append(info.Warnings, respwrite...)

	return info, nil
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

// HoistTypeDeclarations extracts top-level `type ...` declarations from
// frontmatter and returns them separately. Type declarations need to be at
// package level for go/parser to accept them when the rest of the
// frontmatter is wrapped in a function body.
//
// Uses go/parser to find type declarations precisely so inline comments
// containing `{`, `}`, or backticks (e.g. `Title string // task `code`
// here`) are handled correctly. Falls back to a line-based scanner when the
// frontmatter has syntax errors (e.g. user is mid-edit) so the LSP and
// formatter stay responsive.
func HoistTypeDeclarations(frontmatter string) (body string, typeDecls string) {
	if body, typeDecls, ok := hoistTypeDeclarationsAST(frontmatter); ok {
		return body, typeDecls
	}
	return hoistTypeDeclarationsLegacy(frontmatter)
}

// hoistTypeDeclarationsAST is the precise AST-based implementation used when
// the frontmatter parses cleanly. Returns ok=false if parsing fails so the
// caller can fall back to the legacy line scanner.
func hoistTypeDeclarationsAST(frontmatter string) (body, typeDecls string, ok bool) {
	if strings.TrimSpace(frontmatter) == "" {
		return "", "\n", true
	}

	// Type declarations are valid as DeclStmt in function bodies, so we can
	// parse the entire frontmatter inside a synthetic function without
	// hoisting first. The parser then locates each type decl precisely
	// regardless of comment contents.
	prefix := "package __gastro\nfunc __h() {\n"
	prefixLen := len(prefix)
	src := prefix + frontmatter + "\n}"

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "frontmatter.go", src, parser.ParseComments)
	if err != nil {
		return "", "", false
	}

	// Collect byte spans of type DeclStmts in the original frontmatter.
	type span struct{ start, end int }
	var spans []span
	var typeBuf strings.Builder

	ast.Inspect(file, func(n ast.Node) bool {
		ds, ok := n.(*ast.DeclStmt)
		if !ok {
			return true
		}
		gd, ok := ds.Decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.TYPE {
			return true
		}

		dsStart := fset.Position(ds.Pos()).Offset
		dsEnd := fset.Position(ds.End()).Offset
		if dsStart < prefixLen || dsEnd > prefixLen+len(frontmatter) {
			return true // out-of-bounds; should not happen
		}
		fmStart := dsStart - prefixLen
		fmEnd := dsEnd - prefixLen

		// Expand to whole lines so the body retains its original line
		// structure after removal. This keeps line numbers reported by
		// downstream analysis stable.
		lineStart, lineEnd := expandToWholeLines(frontmatter, fmStart, fmEnd)

		spans = append(spans, span{start: lineStart, end: lineEnd})

		if typeBuf.Len() > 0 {
			typeBuf.WriteString("\n")
		}
		typeBuf.WriteString(frontmatter[fmStart:fmEnd])
		return false
	})

	if len(spans) == 0 {
		return frontmatter, "\n", true
	}

	// Sort by start descending so byte offsets stay valid as we remove.
	sort.Slice(spans, func(i, j int) bool { return spans[i].start > spans[j].start })
	body = frontmatter
	for _, s := range spans {
		body = body[:s.start] + body[s.end:]
	}

	return body, typeBuf.String() + "\n", true
}

// expandToWholeLines extends [start, end) outward to cover whole lines:
// start moves back to the byte after the previous newline (or 0), and end
// moves forward to include the trailing newline (or len(s)).
func expandToWholeLines(s string, start, end int) (int, int) {
	lineStart := start
	for lineStart > 0 && s[lineStart-1] != '\n' {
		lineStart--
	}
	lineEnd := end
	for lineEnd < len(s) && s[lineEnd] != '\n' {
		lineEnd++
	}
	if lineEnd < len(s) && s[lineEnd] == '\n' {
		lineEnd++ // include the trailing newline
	}
	return lineStart, lineEnd
}

// hoistTypeDeclarationsLegacy is the original line-based scanner. Kept as a
// fallback for frontmatter that doesn't parse cleanly (mid-edit in the LSP,
// formatter input with errors). Has known edge cases around inline comments
// containing `{`, `}`, or backticks, but that's acceptable for unparseable
// input — the AST path covers correct code.
func hoistTypeDeclarationsLegacy(frontmatter string) (body string, typeDecls string) {
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
