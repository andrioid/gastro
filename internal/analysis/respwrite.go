// Package analysis hosts purely-syntactic AST passes shared between the
// codegen pipeline and the LSP. The package contains no runtime logic and
// must not import internal/codegen, internal/compiler, or any LSP package
// — those packages depend on it, not the other way around.
package analysis

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
)

// MissingReturn describes a frontmatter site that writes to the HTTP
// response (or otherwise commits the response) without being followed by
// `return`. Track B (docs/history/frictions-plan.md §4.9) treats this as
// suspicious because frontmatter execution will continue and any uppercase
// variables computed after the write become dead code from the template's
// point of view.
//
// Reported through the existing Warning channel: non-blocking in dev,
// promoted to an error under --strict in `gastro generate`,
// `gastro build`, and `gastro check`. The default policy matches the
// dict-key validation precedent (DECISIONS.md, 2026-04-30).
type MissingReturn struct {
	Line    int    // 1-indexed line within the frontmatter
	Snippet string // the call expression source text (e.g. "http.Error(w, …, 500)")
}

// FindMissingReturns scans frontmatter Go source for response-write call
// sites that are not followed by `return` (or are not the last statement
// of their enclosing block). Returns one MissingReturn per offence.
//
// The detection is strictly syntactic and operates on the AST of the
// frontmatter wrapped in a synthetic function. No type information is
// required: a call is a *write site* iff it satisfies the rules below.
//
// Write site rules (§4.9):
//
//  1. Any argument is the literal identifier `w`. Covers
//     http.Error(w, …), datastar.NewSSE(w, r), http.NotFound(w, r),
//     fmt.Fprintln(w, …), etc.
//  2. Any argument is the literal identifier `r` AND the call is one of
//     the conservative redirect helpers (currently only http.Redirect).
//     The list is intentionally narrow; broaden only when a real stdlib
//     pattern emerges.
//  3. The call is a method on `w` (selector expression with `w` as the
//     base): w.Write(…), w.WriteHeader(…), w.Header().Set(…).
//
// A write site needs no `return` if any of:
//
//   - A `return` statement appears later in the same block. (This
//     covers the headline pattern of multiple writes followed by a
//     single trailing `return`, e.g. NewSSE → PatchElements → return.)
//   - It is the last statement of the synthetic function body, in
//     which case the codegen-wrapped function returns naturally.
//
// The plan also mentions "last statement of its enclosing block"; the
// stricter form above was chosen because the literal reading would
// silently allow `if cond { http.Error(w, …, 400) }` followed by more
// frontmatter, defeating the analyser's stated purpose. In practice
// the only loss is the rare case of a single-line `if`-block whose write
// is intentional and followed by nothing else — trivially fixable by
// adding `return`.
//
// Otherwise FindMissingReturns reports it.
//
// Known false-positive class (§4.9): a helper whose first parameter is
// http.ResponseWriter but does not actually write (e.g.
// `extractRequestID(w, r) string`). Rare in practice; mitigation is to
// remove the unused parameter.
//
// Returns nil if the frontmatter does not parse — surfacing parse errors
// is the responsibility of the existing codegen analyser, not this lint.
func FindMissingReturns(frontmatter string) []MissingReturn {
	if strings.TrimSpace(frontmatter) == "" {
		return nil
	}

	const prefix = "package __gastro\nfunc __handler(w any, r any) {\n"
	prefixLineCount := strings.Count(prefix, "\n")
	src := prefix + frontmatter + "\n}"

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "frontmatter.go", src, parser.AllErrors)
	if err != nil {
		return nil
	}

	var findings []MissingReturn

	// Locate the synthetic function body so we can give its trailing
	// statement the "function returns naturally" exemption.
	var fnBody *ast.BlockStmt
	ast.Inspect(file, func(n ast.Node) bool {
		fd, ok := n.(*ast.FuncDecl)
		if !ok || fd.Name == nil || fd.Name.Name != "__handler" {
			return true
		}
		fnBody = fd.Body
		return false
	})

	ast.Inspect(file, func(n ast.Node) bool {
		block, ok := n.(*ast.BlockStmt)
		if !ok || block == nil {
			return true
		}
		isFnBody := block == fnBody

		// Index of the first ReturnStmt in this block (-1 if absent).
		firstReturn := -1
		for i, stmt := range block.List {
			if _, isReturn := stmt.(*ast.ReturnStmt); isReturn {
				firstReturn = i
				break
			}
		}

		for i, stmt := range block.List {
			call, ok := exprStmtCall(stmt)
			if !ok || !isWriteSite(call) {
				continue
			}
			if firstReturn >= 0 && firstReturn > i {
				continue // a return in the same block follows the write
			}
			if isFnBody && i == len(block.List)-1 {
				continue // last statement of the synthetic function body
			}

			pos := fset.Position(call.Pos())
			line := pos.Line - prefixLineCount
			if line < 1 {
				continue
			}
			findings = append(findings, MissingReturn{
				Line:    line,
				Snippet: callSnippet(frontmatter, call, fset, prefixLineCount),
			})
		}
		return true
	})

	return findings
}

// exprStmtCall returns (call, true) when stmt is an ExprStmt wrapping a
// CallExpr. The Track B analyser only flags top-level calls — a write
// buried inside an assignment (e.g. `n, err := w.Write(buf)`) is treated
// as fine because the developer is already capturing the result and the
// missing-return rule no longer applies in the same straight-line way.
func exprStmtCall(stmt ast.Stmt) (*ast.CallExpr, bool) {
	es, ok := stmt.(*ast.ExprStmt)
	if !ok {
		return nil, false
	}
	call, ok := es.X.(*ast.CallExpr)
	if !ok {
		return nil, false
	}
	return call, true
}

// isWriteSite implements the §4.9 rules.
func isWriteSite(call *ast.CallExpr) bool {
	// Rule 3: method call on `w`.
	if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
		if base := identName(sel.X); base == "w" {
			return true
		}
		// Sub-rule: w.Header().Set(…) — the inner call's selector base is
		// also `w`, which matches a deeper SelectorExpr chain. Walk to
		// find any `w.Header()` link.
		if hasWHeaderChain(sel) {
			return true
		}
	}

	for _, arg := range call.Args {
		switch identName(arg) {
		case "w":
			// Rule 1.
			return true
		case "r":
			// Rule 2 — conservative redirect-helper allowlist.
			if isRedirectHelper(call.Fun) {
				return true
			}
		}
	}
	return false
}

// hasWHeaderChain returns true when the selector ultimately roots in a
// `w.Header()` call. Examples that match: w.Header().Set(…),
// w.Header().Add(…), w.Header().Del(…). Examples that don't: r.Header.Get(…),
// req.Header.Set(…).
func hasWHeaderChain(sel *ast.SelectorExpr) bool {
	cur := ast.Expr(sel)
	for {
		switch v := cur.(type) {
		case *ast.SelectorExpr:
			cur = v.X
		case *ast.CallExpr:
			innerSel, ok := v.Fun.(*ast.SelectorExpr)
			if !ok {
				return false
			}
			if identName(innerSel.X) == "w" && innerSel.Sel.Name == "Header" {
				return true
			}
			cur = innerSel.X
		default:
			return false
		}
	}
}

// isRedirectHelper recognises stdlib patterns whose first argument is the
// writer and whose second is the request, but whose write-side effect is
// triggered by the request value rather than direct writes to w. Today
// only http.Redirect qualifies. Broaden only with explicit precedent.
func isRedirectHelper(fun ast.Expr) bool {
	sel, ok := fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg := identName(sel.X)
	return pkg == "http" && sel.Sel.Name == "Redirect"
}

// identName returns the source name of a bare identifier, or "" for any
// non-Ident expression.
func identName(e ast.Expr) string {
	id, ok := e.(*ast.Ident)
	if !ok {
		return ""
	}
	return id.Name
}

// callSnippet returns up to one line of the original source for a call,
// for use in diagnostic messages. The call's source range is recovered
// via offsets and trimmed at the first newline so multi-line dict
// literals don't bloat the warning output.
func callSnippet(frontmatter string, call *ast.CallExpr, fset *token.FileSet, prefixLineCount int) string {
	startPos := fset.Position(call.Pos())
	endPos := fset.Position(call.End())
	// Convert from synthetic-source positions back to frontmatter offsets.
	prefixLen := len("package __gastro\nfunc __handler(w any, r any) {\n")
	start := startPos.Offset - prefixLen
	end := endPos.Offset - prefixLen
	if start < 0 || end > len(frontmatter) || start >= end {
		return ""
	}
	snippet := frontmatter[start:end]
	if i := strings.IndexByte(snippet, '\n'); i >= 0 {
		snippet = snippet[:i] + " …"
	}
	if len(snippet) > 80 {
		snippet = snippet[:80] + "…"
	}
	_ = prefixLineCount
	return snippet
}
