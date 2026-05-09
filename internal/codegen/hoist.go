package codegen

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"sort"
	"strings"
	"unicode"
)

// HoistKind tags the syntactic form of a hoisted top-level frontmatter
// declaration. Used by codegen to route emission and by the LSP shadow's
// future stub builder.
type HoistKind int

const (
	HoistVar   HoistKind = iota + 1 // `var X = …` / `var X T`
	HoistConst                      // `const X = …`
	HoistType                       // `type T struct{}` or `type T = X`
	HoistFunc                       // `func F(...) ...` (not method receivers)
)

// HoistedDecl captures one top-level declaration that was promoted out of
// the per-request handler body to package scope. The two name fields are
// always populated; under MangleHoisted == false they hold the same value
// so callers can treat MangledName as authoritative without branching.
type HoistedDecl struct {
	Kind        HoistKind
	Name        string // user-visible identifier as written in frontmatter
	MangledName string // emitted name (== Name when no mangling applied)
	SourceText  string // verbatim declaration text to emit at package scope
	IsExported  bool   // first rune uppercase → goes in __data
	Line        int    // 1-indexed line within the original frontmatter
}

// HoistError is returned when a top-level decl references per-request scope
// (r, w, gastro.Props(), etc.). Carries enough information to format a
// helpful migration hint pointing the user at `:=`.
type HoistError struct {
	Filename string // optional — empty when called outside a compile loop
	Line     int    // 1-indexed within frontmatter
	DeclName string // name of the offending decl (e.g. "Title")
	DeclKind HoistKind
	Detail   string // offending sub-expression (e.g. "r.URL", "gastro.Props()")
	// MigrationHint is a concrete `Name := …` rewrite suggestion built
	// from the original LHS / kind. Empty for func/type, where the
	// guidance is to move to a sibling .go file or pass via Props.
	MigrationHint string
}

func (e *HoistError) Error() string {
	prefix := ""
	if e.Filename != "" {
		prefix = e.Filename + ":"
	}
	kind := hoistKindLabel(e.DeclKind)
	msg := fmt.Sprintf("%s%d: %s %q cannot be hoisted to package scope because it references per-request state (%s).",
		prefix, e.Line, kind, e.DeclName, e.Detail)
	if e.MigrationHint != "" {
		msg += fmt.Sprintf("\n\nHoisted decls run once at process init; per-request state is only available inside the handler. Use `:=` so it runs each request:\n\n    %s", e.MigrationHint)
	} else {
		msg += "\n\nHoisted decls run once at process init; per-request state is only available inside the handler. Move this declaration to a sibling .go file in the same package, or pass the value through component Props."
	}
	return msg
}

func hoistKindLabel(k HoistKind) string {
	switch k {
	case HoistVar:
		return "var"
	case HoistConst:
		return "const"
	case HoistType:
		return "type"
	case HoistFunc:
		return "func"
	default:
		return "decl"
	}
}

// HoistOptions controls naming and rejection behaviour. All fields default
// to zero/false-friendly values so a {} value of HoistOptions matches
// today's pre-mangling behaviour.
type HoistOptions struct {
	// Prefix is prepended to each hoisted decl's name to form
	// MangledName. Empty means no mangling — Name == MangledName for
	// every decl. Convention: "__page_<id>_" or "__component_<id>_".
	Prefix string

	// Filename is reported in HoistError messages. Optional.
	Filename string

	// LineOffset is added to every reported line number so callers
	// that already know the frontmatter's offset within the original
	// .gastro file can produce final coordinates. Default: 0 (lines
	// stay relative to frontmatter content).
	LineOffset int
}

// HoistDecls walks the frontmatter and extracts every top-level
// var/const/type/func declaration whose initializer (or function body)
// does not reference per-request scope. Returns:
//
//	body  — frontmatter source with hoisted decls removed; remaining
//	        text is whatever should still run inside the handler
//	        (statements, := assignments, control flow).
//	decls — one HoistedDecl per hoisted symbol. var-block decls are
//	        split into individual entries with reconstructed source.
//	warnings — non-fatal issues (currently unused; reserved for
//	           future per-decl warnings).
//	err   — *HoistError if any candidate decl references per-request
//	        scope. The first offending decl is reported; remaining
//	        decls are NOT processed.
//
// Func decls are extracted textually first (since `func F() {}` is not
// valid as a function-body statement), then var/const/type decls are
// extracted via AST analysis of the wrapper-parsed remainder.
func HoistDecls(frontmatter string, opts HoistOptions) (body string, decls []HoistedDecl, warnings []Warning, err error) {
	if strings.TrimSpace(frontmatter) == "" {
		return frontmatter, nil, nil, nil
	}

	// Step 1: pull out top-level `func` declarations (including init).
	// They have to come out first because Go forbids `func F()` inside
	// a function body, which is what the wrapper parse uses.
	bodyAfterFuncs, funcDecls, fnErr := extractFuncDecls(frontmatter, opts)
	if fnErr != nil {
		return "", nil, nil, fnErr
	}

	// Step 2: parse the remainder inside a wrapper to find top-level
	// var/const/type GenDecls.
	bodyAfterAll, otherDecls, otherErr := extractGenDecls(bodyAfterFuncs, frontmatter, funcDecls, opts)
	if otherErr != nil {
		return "", nil, nil, otherErr
	}

	all := append(funcDecls, otherDecls...)
	sort.SliceStable(all, func(i, j int) bool { return all[i].Line < all[j].Line })

	return bodyAfterAll, all, warnings, nil
}

// applyMangle returns the mangled name for an identifier given the prefix.
// `init` is preserved verbatim so multiple `func init()` declarations per
// package retain their Go semantics.
func applyMangle(name, prefix string, kind HoistKind) string {
	if kind == HoistFunc && name == "init" {
		return "init"
	}
	if prefix == "" {
		return name
	}
	return prefix + name
}

// extractFuncDecls scans the frontmatter for top-level `func` declarations
// using a brace-counting line scanner. Each candidate block is then parsed
// as a complete Go file to validate it and extract the AST for free-var
// analysis. Returns the frontmatter with func blocks removed and the list
// of HoistedDecls.
//
// Limitations:
//   - Only func declarations starting at column 0 are recognised. Indented
//     funcs are treated as statements (and would fail later parsing
//     anyway).
//   - The scanner does not recognise raw-string-literal contents or
//     line-spanning comments containing `{` or `}`. Frontmatter doesn't
//     typically include either at top-level, so this is acceptable.
func extractFuncDecls(frontmatter string, opts HoistOptions) (string, []HoistedDecl, error) {
	lines := strings.Split(frontmatter, "\n")
	var keep []string
	var decls []HoistedDecl

	i := 0
	for i < len(lines) {
		line := lines[i]
		if !isTopLevelFuncStart(line) {
			keep = append(keep, line)
			i++
			continue
		}

		// Scan forward until braces balance. The opening brace can be
		// on the same line as `func` or on a continuation line.
		braceDepth := 0
		braceSeen := false
		end := i
		for end < len(lines) {
			cur := lines[end]
			open := strings.Count(cur, "{")
			close := strings.Count(cur, "}")
			braceDepth += open - close
			if open > 0 {
				braceSeen = true
			}
			if braceSeen && braceDepth <= 0 {
				break
			}
			end++
		}
		if end >= len(lines) {
			// Unbalanced; treat the line as a statement and let the
			// downstream parser report it.
			keep = append(keep, line)
			i++
			continue
		}

		blockText := strings.Join(lines[i:end+1], "\n")
		decl, err := parseFuncBlock(blockText, i+1, opts)
		if err != nil {
			return "", nil, err
		}
		if decl == nil {
			// Couldn't parse — keep as-is so the wrapper parse
			// surfaces a more useful error.
			keep = append(keep, lines[i:end+1]...)
			i = end + 1
			continue
		}
		decls = append(decls, *decl)
		// Replace the func block with blank lines so subsequent line
		// numbers stay stable.
		for range lines[i : end+1] {
			keep = append(keep, "")
		}
		i = end + 1
	}

	return strings.Join(keep, "\n"), decls, nil
}

// isTopLevelFuncStart reports whether a line begins a top-level func decl.
// Recognises `func F(`, `func (recv) M(`, `func init(`. Method receivers
// are not currently hoistable through this path because the analyzer
// treats them as user-defined types' methods which already have a stable
// receiver-typed home in production codegen. We keep the recognition
// permissive in case a future change wants to relax that.
func isTopLevelFuncStart(line string) bool {
	if strings.HasPrefix(line, "func ") || strings.HasPrefix(line, "func(") {
		return true
	}
	return false
}

// parseFuncBlock parses a single textual func block as a Go file and
// builds a HoistedDecl. Returns (nil, nil) if the block doesn't parse
// (caller treats this as "leave it where it was"). Returns (nil,
// HoistError) if the func references per-request scope.
func parseFuncBlock(blockText string, startLine int, opts HoistOptions) (*HoistedDecl, error) {
	src := "package __hoist\n" + blockText
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "block.go", src, parser.AllErrors)
	if err != nil {
		return nil, nil
	}

	for _, d := range file.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok {
			continue
		}
		if fn.Recv != nil {
			// Method on a frontmatter-declared type. Today we don't
			// hoist these — leave for a future iteration if needed.
			return nil, nil
		}
		if hit, ref := ReferencesPerRequestScope(fn); hit {
			return nil, &HoistError{
				Filename: opts.Filename,
				Line:     opts.LineOffset + startLine,
				DeclName: fn.Name.Name,
				DeclKind: HoistFunc,
				Detail:   ref.Detail,
			}
		}
		// Re-print the func with the correct name. SourceText is the
		// original block text minus the synthetic `package __hoist\n`
		// prefix — we keep formatting verbatim.
		return &HoistedDecl{
			Kind:        HoistFunc,
			Name:        fn.Name.Name,
			MangledName: applyMangle(fn.Name.Name, opts.Prefix, HoistFunc),
			SourceText:  strings.TrimSuffix(blockText, "\n"),
			IsExported:  unicode.IsUpper(rune(fn.Name.Name[0])),
			Line:        opts.LineOffset + startLine,
		}, nil
	}
	return nil, nil
}

// extractGenDecls parses the remaining frontmatter (after func decls have
// been removed) inside a synthetic function wrapper, then walks for
// top-level var/const/type DeclStmts. Each candidate runs through the
// free-var check; var-block decls are split into individual entries.
func extractGenDecls(bodyText, originalFrontmatter string, funcDecls []HoistedDecl, opts HoistOptions) (string, []HoistedDecl, error) {
	prefix := "package __hoist\nfunc __h() {\n"
	prefixLen := len(prefix)
	prefixLineCount := strings.Count(prefix, "\n")
	src := prefix + bodyText + "\n}"

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "frontmatter.go", src, parser.ParseComments)
	if err != nil {
		// Mid-edit / syntax error. Leave everything in place so the
		// downstream parser produces the canonical error message.
		return bodyText, nil, nil
	}

	type span struct{ start, end int }
	var removeSpans []span
	var decls []HoistedDecl

	// Walk only the direct children of the func body. Inner decls
	// (e.g. inside an if-block) are intentionally NOT hoisted — the
	// plan's mental model is "only `var`/`const`/`func`/`type` AT
	// FRONTMATTER TOP LEVEL hoist".
	if len(file.Decls) == 0 {
		return bodyText, nil, nil
	}
	fn, ok := file.Decls[0].(*ast.FuncDecl)
	if !ok || fn.Body == nil {
		return bodyText, nil, nil
	}

	for _, stmt := range fn.Body.List {
		ds, ok := stmt.(*ast.DeclStmt)
		if !ok {
			continue
		}
		gd, ok := ds.Decl.(*ast.GenDecl)
		if !ok {
			continue
		}
		if gd.Tok != token.VAR && gd.Tok != token.CONST && gd.Tok != token.TYPE {
			continue
		}

		// Compute the byte-range of this decl in bodyText so we can
		// remove it later. Spans cover whole lines for stable line
		// numbering.
		dsStart := fset.Position(ds.Pos()).Offset
		dsEnd := fset.Position(ds.End()).Offset
		if dsStart < prefixLen || dsEnd > prefixLen+len(bodyText) {
			continue
		}
		bodyStart := dsStart - prefixLen
		bodyEnd := dsEnd - prefixLen
		lineStart, lineEnd := expandToWholeLines(bodyText, bodyStart, bodyEnd)

		// Use the source line number from the parsed file (1-indexed
		// in fset, 1-indexed in frontmatter).
		declLine := fset.Position(ds.Pos()).Line - prefixLineCount

		// Build per-spec HoistedDecls.
		newDecls, err := buildGenDeclHoists(gd, ds, fset, prefixLineCount, opts, declLine, originalFrontmatter)
		if err != nil {
			return "", nil, err
		}
		if len(newDecls) == 0 {
			continue
		}
		decls = append(decls, newDecls...)
		removeSpans = append(removeSpans, span{lineStart, lineEnd})
	}

	// Remove spans in reverse order so byte offsets stay valid.
	sort.Slice(removeSpans, func(i, j int) bool { return removeSpans[i].start > removeSpans[j].start })
	body := bodyText
	for _, s := range removeSpans {
		// Replace with blank lines to keep line numbering stable.
		removed := body[s.start:s.end]
		blank := strings.Repeat("\n", strings.Count(removed, "\n"))
		body = body[:s.start] + blank + body[s.end:]
	}

	return body, decls, nil
}

// buildGenDeclHoists converts one GenDecl into one or more HoistedDecls,
// running the free-var check per spec. var/const blocks like `var (X = 1;
// Y = 2)` are split into individual entries with reconstructed source so
// each emission stays simple and mangling targets each ident
// independently.
func buildGenDeclHoists(
	gd *ast.GenDecl,
	ds *ast.DeclStmt,
	fset *token.FileSet,
	prefixLineCount int,
	opts HoistOptions,
	declLine int,
	originalFrontmatter string,
) ([]HoistedDecl, error) {
	var out []HoistedDecl
	kind := genDeclKind(gd.Tok)

	for _, spec := range gd.Specs {
		switch s := spec.(type) {
		case *ast.ValueSpec:
			// var/const. Each name gets its own HoistedDecl. Each
			// initializer is checked for per-request refs.
			for i, name := range s.Names {
				if name.Name == "_" {
					continue
				}
				var rhs ast.Expr
				if i < len(s.Values) {
					rhs = s.Values[i]
				}
				if rhs != nil {
					if hit, ref := ReferencesPerRequestScope(rhs); hit {
						return nil, &HoistError{
							Filename:      opts.Filename,
							Line:          opts.LineOffset + fset.Position(name.Pos()).Line - prefixLineCount,
							DeclName:      name.Name,
							DeclKind:      kind,
							Detail:        ref.Detail,
							MigrationHint: buildAssignHint(name.Name, rhs, fset),
						}
					}
				}
				if s.Type != nil {
					if hit, ref := ReferencesPerRequestScope(s.Type); hit {
						return nil, &HoistError{
							Filename: opts.Filename,
							Line:     opts.LineOffset + fset.Position(name.Pos()).Line - prefixLineCount,
							DeclName: name.Name,
							DeclKind: kind,
							Detail:   ref.Detail,
						}
					}
				}
				src := renderValueSpec(gd.Tok, name, s.Type, rhs, fset)
				out = append(out, HoistedDecl{
					Kind:        kind,
					Name:        name.Name,
					MangledName: applyMangle(name.Name, opts.Prefix, kind),
					SourceText:  src,
					IsExported:  isExportedName(name.Name),
					Line:        opts.LineOffset + fset.Position(name.Pos()).Line - prefixLineCount,
				})
			}
		case *ast.TypeSpec:
			// type T = X (alias) and type T struct{...} both hoist.
			if hit, ref := ReferencesPerRequestScope(s.Type); hit {
				return nil, &HoistError{
					Filename: opts.Filename,
					Line:     opts.LineOffset + fset.Position(s.Pos()).Line - prefixLineCount,
					DeclName: s.Name.Name,
					DeclKind: HoistType,
					Detail:   ref.Detail,
				}
			}
			src := renderTypeSpec(s, fset)
			out = append(out, HoistedDecl{
				Kind:        HoistType,
				Name:        s.Name.Name,
				MangledName: applyMangle(s.Name.Name, opts.Prefix, HoistType),
				SourceText:  src,
				IsExported:  isExportedName(s.Name.Name),
				Line:        opts.LineOffset + fset.Position(s.Pos()).Line - prefixLineCount,
			})
		}
	}
	return out, nil
}

func genDeclKind(tok token.Token) HoistKind {
	switch tok {
	case token.VAR:
		return HoistVar
	case token.CONST:
		return HoistConst
	case token.TYPE:
		return HoistType
	}
	return 0
}

// renderValueSpec produces a single-line `var X = E` or `var X T = E` text
// for emission. Used to split var-block decls and to ensure consistent
// formatting regardless of whether the original was a block or single
// decl.
func renderValueSpec(tok token.Token, name *ast.Ident, typ ast.Expr, rhs ast.Expr, fset *token.FileSet) string {
	var b strings.Builder
	b.WriteString(tok.String())
	b.WriteString(" ")
	b.WriteString(name.Name)
	if typ != nil {
		b.WriteString(" ")
		b.WriteString(printNode(typ, fset))
	}
	if rhs != nil {
		b.WriteString(" = ")
		b.WriteString(printNode(rhs, fset))
	}
	return b.String()
}

// renderTypeSpec produces a `type T <...>` text for emission.
func renderTypeSpec(s *ast.TypeSpec, fset *token.FileSet) string {
	var b strings.Builder
	b.WriteString("type ")
	b.WriteString(s.Name.Name)
	if s.TypeParams != nil {
		b.WriteString(printNode(s.TypeParams, fset))
	}
	if s.Assign.IsValid() {
		b.WriteString(" = ")
	} else {
		b.WriteString(" ")
	}
	b.WriteString(printNode(s.Type, fset))
	return b.String()
}

// printNode formats an AST node back to source text using go/printer.
func printNode(n ast.Node, fset *token.FileSet) string {
	var buf strings.Builder
	if err := printer.Fprint(&buf, fset, n); err != nil {
		return fmt.Sprintf("/* gastro: print error: %v */", err)
	}
	return buf.String()
}

// buildAssignHint produces a `Name := <rhs>` migration suggestion for
// HoistError messages.
func buildAssignHint(name string, rhs ast.Expr, fset *token.FileSet) string {
	return fmt.Sprintf("%s := %s", name, printNode(rhs, fset))
}

func isExportedName(name string) bool {
	if name == "" {
		return false
	}
	r := []rune(name)[0]
	return unicode.IsUpper(r)
}
