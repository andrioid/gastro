package codegen

import (
	"go/ast"
)

// PerRequestRef describes a single reference to per-request scope inside a
// hoisted decl. Returned by ReferencesPerRequestScope so callers can build a
// precise migration hint pointing at the offending sub-expression.
type PerRequestRef struct {
	// Node is the AST node that triggered the rejection.
	Node ast.Node
	// Detail is a short human-readable label (e.g. "r.URL.Path",
	// "gastro.Props()", "gastro.From[T](r.Context())"). Used in error
	// messages.
	Detail string
}

// ReferencesPerRequestScope reports whether the given AST node references any
// of the per-request idents/calls that only exist inside a gastro page
// handler. Used by the analyzer to reject `var`/`const`/`func`/`type`
// declarations that look hoistable but actually depend on request-scoped
// state.
//
// The walker tracks bound names introduced by enclosing *ast.FuncLit /
// *ast.FuncDecl parameter lists. A bare `r` or `w` only counts as
// per-request if it is *not* shadowed by an enclosing binder. This means
// patterns like `var H = http.HandlerFunc(func(w http.ResponseWriter, r
// *http.Request) { ... })` are correctly identified as hoistable — the
// inner `w`/`r` are bound by the closure, not the page handler.
//
// Returns (false, nil) if the node is safe to hoist; otherwise (true, ref)
// pointing at the first offending sub-expression encountered.
//
// The check is purely syntactic — false positives are possible (a variable
// named `r` not referring to the request) and accepted as a small cost for
// keeping the implementation simple. The migration path (`:=` instead of
// `var`) is always available.
func ReferencesPerRequestScope(node ast.Node) (bool, *PerRequestRef) {
	w := &freevarWalker{}
	// Seed with one empty scope so unparented walks (e.g. an isolated
	// expression) still treat top-level idents as free.
	w.scopes = append(w.scopes, map[string]bool{})
	ast.Walk(w, node)
	if w.found != nil {
		return true, w.found
	}
	return false, nil
}

// freevarWalker implements ast.Visitor with a stack of bound-name scopes.
// On each function literal or function declaration entry, it pushes a new
// scope populated with the function's parameter names; on exit, it pops.
type freevarWalker struct {
	scopes []map[string]bool
	found  *PerRequestRef
}

func (w *freevarWalker) bound(name string) bool {
	for i := len(w.scopes) - 1; i >= 0; i-- {
		if w.scopes[i][name] {
			return true
		}
	}
	return false
}

func (w *freevarWalker) Visit(n ast.Node) ast.Visitor {
	if w.found != nil {
		return nil
	}
	if n == nil {
		return nil
	}

	switch node := n.(type) {
	case *ast.FuncLit:
		w.pushFuncScope(node.Type)
		defer w.popScope()
		// Walk the body manually so we can pop the scope on exit.
		ast.Walk(w, node.Body)
		return nil

	case *ast.FuncDecl:
		w.pushFuncScope(node.Type)
		defer w.popScope()
		// Recv idents are also bound (component method receivers, etc).
		if node.Recv != nil {
			for _, field := range node.Recv.List {
				for _, name := range field.Names {
					w.scopes[len(w.scopes)-1][name.Name] = true
				}
			}
		}
		if node.Body != nil {
			ast.Walk(w, node.Body)
		}
		return nil

	case *ast.AssignStmt:
		// :=, =, +=, etc. New idents on the LHS of := are bound in the
		// surrounding scope. We don't introduce a new scope, but we do
		// register the LHS so a subsequent reference inside the same
		// hoisted decl's RHS doesn't flag itself.
		if node.Tok.String() == ":=" {
			for _, lhs := range node.Lhs {
				if id, ok := lhs.(*ast.Ident); ok && id.Name != "_" {
					w.scopes[len(w.scopes)-1][id.Name] = true
				}
			}
		}
		// Continue walking RHS / LHS expressions.

	case *ast.RangeStmt:
		if node.Tok.String() == ":=" {
			if id, ok := node.Key.(*ast.Ident); ok && id.Name != "_" {
				w.scopes[len(w.scopes)-1][id.Name] = true
			}
			if id, ok := node.Value.(*ast.Ident); ok && id.Name != "_" {
				w.scopes[len(w.scopes)-1][id.Name] = true
			}
		}

	case *ast.CallExpr:
		if ref := w.checkGastroCall(node); ref != nil {
			w.found = ref
			return nil
		}

	case *ast.SelectorExpr:
		// Catches `r.URL`, `w.Header`, etc.
		if id, ok := node.X.(*ast.Ident); ok {
			if (id.Name == "r" || id.Name == "w") && !w.bound(id.Name) {
				w.found = &PerRequestRef{Node: node, Detail: id.Name + "." + node.Sel.Name}
				return nil
			}
		}

	case *ast.Ident:
		// Bare `r` or `w` reference, not part of a selector expression.
		// Selector cases above already handled `r.X` / `w.X`.
		if (node.Name == "r" || node.Name == "w") && !w.bound(node.Name) {
			w.found = &PerRequestRef{Node: node, Detail: node.Name}
			return nil
		}
	}

	return w
}

// pushFuncScope creates a new scope populated with the parameter and result
// names from the given function type. Result names are included so things
// like `func() (r io.Reader) { _ = r }` don't false-positive.
func (w *freevarWalker) pushFuncScope(ft *ast.FuncType) {
	scope := map[string]bool{}
	if ft != nil {
		if ft.Params != nil {
			for _, field := range ft.Params.List {
				for _, name := range field.Names {
					if name.Name != "_" {
						scope[name.Name] = true
					}
				}
			}
		}
		if ft.Results != nil {
			for _, field := range ft.Results.List {
				for _, name := range field.Names {
					if name.Name != "_" {
						scope[name.Name] = true
					}
				}
			}
		}
	}
	w.scopes = append(w.scopes, scope)
}

func (w *freevarWalker) popScope() {
	w.scopes = w.scopes[:len(w.scopes)-1]
}

// checkGastroCall reports whether a call expression is one of the gastro
// runtime helpers that only make sense inside a per-request handler.
// Recognised forms:
//
//   - gastro.Props()
//   - gastro.Context()
//   - gastro.Children()
//   - gastro.From[T](...)         (via *ast.IndexExpr / *ast.IndexListExpr)
//   - gastro.FromOK[T](...)
//   - gastro.FromContext[T](...)
//   - gastro.FromContextOK[T](...)
//
// Returns nil if the call is unrelated.
func (w *freevarWalker) checkGastroCall(call *ast.CallExpr) *PerRequestRef {
	// Unwrap generic instantiation: gastro.From[T] is parsed as IndexExpr
	// or IndexListExpr around the SelectorExpr.
	fn := call.Fun
	switch f := fn.(type) {
	case *ast.IndexExpr:
		fn = f.X
	case *ast.IndexListExpr:
		fn = f.X
	}

	sel, ok := fn.(*ast.SelectorExpr)
	if !ok {
		return nil
	}
	id, ok := sel.X.(*ast.Ident)
	if !ok || id.Name != "gastro" {
		return nil
	}
	if w.bound("gastro") {
		return nil
	}

	switch sel.Sel.Name {
	case "Props", "Context", "Children",
		"From", "FromOK", "FromContext", "FromContextOK":
		return &PerRequestRef{Node: call, Detail: "gastro." + sel.Sel.Name + "()"}
	}
	return nil
}
