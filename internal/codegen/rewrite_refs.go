package codegen

import (
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strings"
)

// RewriteHoistedRefs rewrites every identifier reference in source that
// matches a hoisted-name key to the corresponding mangled name. The
// rewriter operates on the frontmatter handler-body residue (i.e. what
// Phase 3's HoistDecls returns as `body`) plus, optionally, the source
// text of each hoisted decl's initializer / body / type expression.
//
// names maps unmangled ident → mangled emission name. Empty map (or one
// where every value equals its key, as under MangleHoisted=false) is
// supported as a guaranteed-no-op so callers can pass the same map in
// both modes without branching.
//
// The rewrite is purely syntactic. It skips:
//   - field names in selector expressions (X.Title — Title is a field
//     of X, not a ref to the hoisted Title)
//   - struct literal keys (T{Title: "x"} — Title here is a field key)
//   - LHS positions of declarations (var/const/type/func names)
//   - := LHS positions
//   - function parameter and result names
//
// Shadowing of hoisted names by inner scopes (closures, := in nested
// blocks) is not handled here; the analyzer rejects shadowing in
// frontmatter so this case cannot occur in valid input.
//
// If source fails to parse (mid-edit in the LSP, etc.), the rewriter
// returns the input unchanged so the LSP keeps showing the canonical
// parser error rather than a synthetic mangled-form one.
func RewriteHoistedRefs(source string, names map[string]string) string {
	if len(names) == 0 || strings.TrimSpace(source) == "" {
		return source
	}
	// Bail early if no hoisted name actually changes.
	allNoop := true
	for k, v := range names {
		if k != v {
			allNoop = false
			break
		}
	}
	if allNoop {
		return source
	}

	// Wrap as a function body so go/parser accepts arbitrary statement
	// sequences. Top-level decls (var/const/type) are valid as
	// DeclStmts inside a function body, so this wrapper handles every
	// shape the body residue can produce.
	prefix := "package __r\nfunc __h() {\n"
	prefixLen := len(prefix)
	src := prefix + source + "\n}"

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "rewrite.go", src, parser.AllErrors|parser.ParseComments)
	if err != nil {
		return source
	}

	type edit struct {
		start, end int
		text       string
	}
	var edits []edit

	walker := &refRewriter{
		names: names,
		visit: func(id *ast.Ident) {
			mangled := names[id.Name]
			start := fset.Position(id.Pos()).Offset - prefixLen
			end := fset.Position(id.End()).Offset - prefixLen
			if start < 0 || end > len(source) || start >= end {
				return
			}
			edits = append(edits, edit{start: start, end: end, text: mangled})
		},
	}
	ast.Walk(walker, file)

	if len(edits) == 0 {
		return source
	}

	// Apply edits in reverse so byte offsets stay valid.
	sort.Slice(edits, func(i, j int) bool { return edits[i].start > edits[j].start })
	out := source
	for _, e := range edits {
		out = out[:e.start] + e.text + out[e.end:]
	}
	return out
}

// RewriteHoistedRefsInDecl rewrites the source of a single hoisted decl
// (var/const/type/func at top level). Used to fix up references between
// hoisted decls — e.g. `var Y = X + 1` where X is also hoisted.
//
// declSrc is wrapped as a `package __r\n<declSrc>` file rather than as a
// function body since hoisted decls are top-level Go syntactically.
func RewriteHoistedRefsInDecl(declSrc string, names map[string]string) string {
	if len(names) == 0 || strings.TrimSpace(declSrc) == "" {
		return declSrc
	}
	allNoop := true
	for k, v := range names {
		if k != v {
			allNoop = false
			break
		}
	}
	if allNoop {
		return declSrc
	}

	prefix := "package __r\n"
	prefixLen := len(prefix)
	src := prefix + declSrc

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "decl.go", src, parser.AllErrors|parser.ParseComments)
	if err != nil {
		return declSrc
	}

	type edit struct {
		start, end int
		text       string
	}
	var edits []edit

	walker := &refRewriter{
		names:        names,
		skipDeclLHS:  true, // don't rewrite the hoisted decl's own LHS
		visit: func(id *ast.Ident) {
			mangled := names[id.Name]
			start := fset.Position(id.Pos()).Offset - prefixLen
			end := fset.Position(id.End()).Offset - prefixLen
			if start < 0 || end > len(declSrc) || start >= end {
				return
			}
			edits = append(edits, edit{start: start, end: end, text: mangled})
		},
	}
	ast.Walk(walker, file)

	if len(edits) == 0 {
		return declSrc
	}
	sort.Slice(edits, func(i, j int) bool { return edits[i].start > edits[j].start })
	out := declSrc
	for _, e := range edits {
		out = out[:e.start] + e.text + out[e.end:]
	}
	return out
}

// refRewriter is an ast.Visitor that calls visit on every *ast.Ident that
// is a reference position (not a declaration / field / selector RHS) and
// whose name matches a key in names.
type refRewriter struct {
	names       map[string]string
	skipDeclLHS bool                   // skip top-level decl LHS (used by *InDecl)
	visit       func(id *ast.Ident)
}

func (r *refRewriter) Visit(n ast.Node) ast.Visitor {
	if n == nil {
		return nil
	}

	switch node := n.(type) {
	case *ast.SelectorExpr:
		// Walk the X (receiver) but skip Sel (field/method name).
		ast.Walk(r, node.X)
		return nil

	case *ast.CompositeLit:
		// Walk Type as normal so type idents get rewritten if hoisted
		// (e.g. `Comment{...}` where `type Comment` is hoisted).
		if node.Type != nil {
			ast.Walk(r, node.Type)
		}
		// Element keys are field names, not refs. Walk only the
		// values. KeyValueExpr.Key is the field name; KeyValueExpr.Value
		// is the expression.
		for _, elt := range node.Elts {
			if kv, ok := elt.(*ast.KeyValueExpr); ok {
				// In a struct literal, Key is a field name and is
				// NOT a ref. In a map/slice literal, Key IS a ref.
				// Without type info we can't distinguish definitively,
				// so we use a heuristic: if Key is a bare *ast.Ident,
				// assume it's a struct field key (most common case in
				// frontmatter — `T{Title: "..."}`). This is the same
				// trade-off go/ast users make.
				if _, isIdent := kv.Key.(*ast.Ident); !isIdent {
					ast.Walk(r, kv.Key)
				}
				ast.Walk(r, kv.Value)
			} else {
				ast.Walk(r, elt)
			}
		}
		return nil

	case *ast.AssignStmt:
		if node.Tok == token.DEFINE {
			// := LHS is a new binding, not a ref. Walk RHS only.
			for _, rhs := range node.Rhs {
				ast.Walk(r, rhs)
			}
			return nil
		}
		// = LHS may reference an outer hoisted var via ident; walk
		// normally so e.g. `Title = "new"` gets rewritten when Title
		// is hoisted. (Note: Title at package scope is rewritten to
		// __page_x_Title; the LHS rewrite makes the assignment work.)

	case *ast.GenDecl:
		// Top-level var/const/type decls. When skipDeclLHS is set
		// (RewriteHoistedRefsInDecl path), we skip the LHS names but
		// walk the RHS / type bodies normally.
		for _, spec := range node.Specs {
			switch s := spec.(type) {
			case *ast.ValueSpec:
				if r.skipDeclLHS {
					// Skip Names, walk Type and Values.
					if s.Type != nil {
						ast.Walk(r, s.Type)
					}
					for _, v := range s.Values {
						ast.Walk(r, v)
					}
				} else {
					ast.Walk(r, s)
				}
			case *ast.TypeSpec:
				if r.skipDeclLHS {
					// Skip Name, walk Type.
					ast.Walk(r, s.Type)
				} else {
					ast.Walk(r, s)
				}
			}
		}
		return nil

	case *ast.FuncDecl:
		// Skip Name (the func name itself), walk Recv/Type/Body.
		if node.Recv != nil {
			ast.Walk(r, node.Recv)
		}
		if node.Type != nil {
			// Function type: parameters and results. Param names are
			// declarations and shouldn't be rewritten — but their
			// types may reference hoisted types, so we walk only the
			// type expressions.
			walkFuncType(r, node.Type)
		}
		if node.Body != nil {
			ast.Walk(r, node.Body)
		}
		return nil

	case *ast.FuncLit:
		walkFuncType(r, node.Type)
		if node.Body != nil {
			ast.Walk(r, node.Body)
		}
		return nil

	case *ast.Field:
		// Field names (in struct types, function params/results) are
		// declarations, not refs. Walk only the type.
		if node.Type != nil {
			ast.Walk(r, node.Type)
		}
		if node.Tag != nil {
			ast.Walk(r, node.Tag)
		}
		return nil

	case *ast.Ident:
		if _, ok := r.names[node.Name]; ok {
			r.visit(node)
		}
		return nil

	case *ast.LabeledStmt:
		// Label is a declaration, not a ref. Walk Stmt.
		ast.Walk(r, node.Stmt)
		return nil

	case *ast.BranchStmt:
		// goto / break / continue with label — Label is a ref to a
		// labeled statement, not to a hoisted name. Skip it.
		return nil
	}

	return r
}

// walkFuncType walks the parameter and result type expressions of a
// function type, skipping parameter/result NAMES (which are bindings)
// but visiting the type expressions (which may reference hoisted types).
func walkFuncType(r *refRewriter, ft *ast.FuncType) {
	if ft.TypeParams != nil {
		for _, field := range ft.TypeParams.List {
			if field.Type != nil {
				ast.Walk(r, field.Type)
			}
		}
	}
	if ft.Params != nil {
		for _, field := range ft.Params.List {
			if field.Type != nil {
				ast.Walk(r, field.Type)
			}
		}
	}
	if ft.Results != nil {
		for _, field := range ft.Results.List {
			if field.Type != nil {
				ast.Walk(r, field.Type)
			}
		}
	}
}
