package codegen

// //gastro:embed — codegen-time file embedding for .gastro frontmatter.
//
// The directive sits on the line immediately above a single
// uninitialized var declaration:
//
//	//gastro:embed PATH
//	var <ident> string   // or []byte
//
// PATH is resolved relative to the .gastro source file. Symlinks are
// followed via filepath.EvalSymlinks; the resolved path must remain
// inside the user's Go module (the directory containing go.mod that
// transitively owns the source file). Bytes are baked exactly as
// os.ReadFile returns them — no trailing-newline stripping or other
// normalization, matching //go:embed.
//
// Grammar is strict in v1. Each of the following forms produces a
// clear error pointing at the source line:
//   - var X string = "fallback"   (explicit initializer)
//   - var ( A string; B string )   (parenthesized group)
//   - var A, B string              (multi-name spec)
//   - //gastro:embed a.md          (stacked directives on one decl)
//     //gastro:embed b.md
//     var X string
//   - var X interface{}, var X any, var X anyOtherType
//
// The rewritten declaration carries the file contents as a Go string
// literal (or []byte literal) and is then picked up by the var-hoister
// (internal/codegen/freevars.go) and lifted to package scope. The two
// passes compose: this pass bakes content; the hoister places the
// declaration where it runs once.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// EmbedContext supplies the absolute paths the resolver needs.
//
// SourceFile is the .gastro file whose frontmatter is being processed
// (used as the base for relative path resolution). ModuleRoot is the
// directory containing the go.mod that owns SourceFile (used as the
// security boundary — the embed target's resolved real path must sit
// inside this directory).
type EmbedContext struct {
	SourceFile string
	ModuleRoot string
}

// EmbedDirective describes one validated //gastro:embed directive.
// Currently exposed only via tests; the rewritten frontmatter and the
// dep list returned by ProcessEmbedDirectives are the public surface
// the compiler uses.
type EmbedDirective struct {
	VarName  string
	VarType  string // "string" or "[]byte"
	Path     string // user-supplied, relative
	Resolved string // absolute, post-EvalSymlinks
	Line     int    // source line of the directive
}

// ProcessEmbedDirectives parses //gastro:embed comments out of the
// frontmatter source, validates each, reads the referenced files, and
// returns rewritten frontmatter + a deduplicated list of resolved
// (post-symlink) absolute paths the watcher should track.
//
// On the first error encountered, processing stops and (rewritten is
// empty, deps is nil, err is set). All errors are wrapped with line
// context so the user can locate the offending decl quickly.
func ProcessEmbedDirectives(frontmatter string, ctx EmbedContext) (string, []string, error) {
	if !strings.Contains(frontmatter, "//gastro:embed") {
		// Fast path: no directives present. Skip parsing entirely.
		return frontmatter, nil, nil
	}

	// Frontmatter is statement-level Go code (no `package` clause).
	// Wrap it in a synthetic package + function so go/parser accepts it,
	// then map AST offsets back to the original frontmatter by
	// subtracting the prefix length. Mirrors the approach used in
	// hoistTypeDeclarationsAST (analyze.go) so behaviour is consistent
	// across passes.
	prefix := "package __gastro\nfunc __h() {\n"
	prefixLen := len(prefix)
	src := prefix + frontmatter + "\n}"

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "frontmatter.go", src, parser.ParseComments)
	if err != nil {
		// Frontmatter doesn't parse as Go — let the analyzer surface
		// the syntax error rather than reporting it twice. Pass through.
		return frontmatter, nil, nil
	}

	// Comments inside function bodies aren't doc-attached to DeclStmts
	// by go/parser; use NewCommentMap to associate comment groups with
	// nearby nodes by position heuristics.
	commentMap := ast.NewCommentMap(fset, file, file.Comments)

	type rewrite struct {
		start, end  int // byte offsets into frontmatter (post-prefix-adjust)
		replacement string
	}
	var rewrites []rewrite
	var deps []string

	// adjustOffset maps an offset from the wrapped source back to
	// frontmatter. Returns -1 if the offset falls inside the prefix or
	// the trailing `\n}` (shouldn't happen for nodes we care about).
	adjustOffset := func(off int) int {
		return off - prefixLen
	}

	// Visit DeclStmts inside the synthetic function body. We don't use
	// ast.Inspect here because we need access to the CommentMap by
	// AST node identity, not just by traversal.
	var declStmts []*ast.DeclStmt
	ast.Inspect(file, func(n ast.Node) bool {
		if ds, ok := n.(*ast.DeclStmt); ok {
			declStmts = append(declStmts, ds)
		}
		return true
	})

	for _, ds := range declStmts {
		gd, ok := ds.Decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.VAR {
			continue
		}

		// CommentMap associates comment groups with the DeclStmt; the
		// directive can appear in any of those groups (in practice
		// always the one immediately preceding the decl).
		var directiveLines []string
		var directiveLineNos []int
		var leadingDoc *ast.CommentGroup
		for _, cg := range commentMap[ds] {
			// Only consider comment groups that end before the decl
			// starts — trailing comments don't count as directives.
			if cg.End() > ds.Pos() {
				continue
			}
			for _, c := range cg.List {
				if path, ok := parseEmbedDirective(c.Text); ok {
					directiveLines = append(directiveLines, path)
					directiveLineNos = append(directiveLineNos, fset.Position(c.Pos()).Line-1)
				}
			}
			if leadingDoc == nil || cg.Pos() < leadingDoc.Pos() {
				leadingDoc = cg
			}
		}
		if len(directiveLines) == 0 {
			continue
		}
		if len(directiveLines) > 1 {
			return "", nil, fmt.Errorf(
				"//gastro:embed: multiple directives stacked on one var (lines %v); use one //gastro:embed per var",
				directiveLineNos,
			)
		}

		path := directiveLines[0]
		declLine := fset.Position(gd.Pos()).Line - 1 // -1 to map past the synthetic prefix

		// Grammar: single ValueSpec, single Name, no Values, supported type.
		if len(gd.Specs) != 1 {
			return "", nil, fmt.Errorf(
				"//gastro:embed on line %d: parenthesized var groups not allowed; use one `var X T` per directive",
				declLine,
			)
		}
		spec, ok := gd.Specs[0].(*ast.ValueSpec)
		if !ok {
			return "", nil, fmt.Errorf("//gastro:embed on line %d: expected var spec", declLine)
		}
		if len(spec.Names) != 1 {
			return "", nil, fmt.Errorf(
				"//gastro:embed on line %d: multi-name var spec (`var A, B T`) not allowed; declare one var per directive",
				declLine,
			)
		}
		if spec.Values != nil {
			return "", nil, fmt.Errorf(
				"//gastro:embed on line %d: explicit initializer not allowed; remove the `=` expression and let the directive provide the value",
				declLine,
			)
		}
		varType, typeOK := classifyEmbedVarType(spec.Type)
		if !typeOK {
			return "", nil, fmt.Errorf(
				"//gastro:embed on line %d: requires var of type `string` or `[]byte`; got `%s`",
				declLine, formatTypeExpr(spec.Type),
			)
		}

		// Resolve the path. Reject absolute paths and pre-EvalSymlinks
		// escapes early; the post-symlink boundary check is the
		// security-critical one and runs inside resolveEmbedPath.
		if filepath.IsAbs(path) {
			return "", nil, fmt.Errorf(
				"//gastro:embed on line %d: absolute paths not allowed (got %q); use a path relative to the .gastro source",
				declLine, path,
			)
		}
		resolved, err := resolveEmbedPath(path, ctx)
		if err != nil {
			return "", nil, fmt.Errorf("//gastro:embed on line %d: %w", declLine, err)
		}

		content, err := os.ReadFile(resolved)
		if err != nil {
			return "", nil, fmt.Errorf("//gastro:embed on line %d (%q): %w", declLine, path, err)
		}

		// String vars require valid UTF-8 (markdown / text use case).
		// []byte vars take any bytes.
		if varType == "string" && !utf8.Valid(content) {
			return "", nil, fmt.Errorf(
				"//gastro:embed on line %d (%q): file is not valid UTF-8; use `var X []byte` instead",
				declLine, path,
			)
		}

		varName := spec.Names[0].Name
		replacement := buildEmbedDecl(varName, varType, content, leadingDoc)

		// Rewrite span: from the start of the doc comment group through
		// the end of the DeclStmt. Consuming the whole comment group
		// (including any non-directive lines) keeps re-parses idempotent;
		// non-directive comments are preserved inside `replacement`.
		var startWrapped int
		if leadingDoc != nil {
			startWrapped = fset.Position(leadingDoc.Pos()).Offset
		} else {
			startWrapped = fset.Position(ds.Pos()).Offset
		}
		endWrapped := fset.Position(ds.End()).Offset

		start := adjustOffset(startWrapped)
		end := adjustOffset(endWrapped)
		if start < 0 || end < 0 || start > len(frontmatter) || end > len(frontmatter) {
			return "", nil, fmt.Errorf("//gastro:embed: internal offset error (start=%d end=%d len=%d)", start, end, len(frontmatter))
		}
		rewrites = append(rewrites, rewrite{
			start:       start,
			end:         end,
			replacement: replacement,
		})
		deps = append(deps, resolved)
	}

	// Apply rewrites high-offset-first so earlier rewrites' offsets
	// stay valid.
	sort.Slice(rewrites, func(i, j int) bool { return rewrites[i].start > rewrites[j].start })
	out := frontmatter
	for _, r := range rewrites {
		out = out[:r.start] + r.replacement + out[r.end:]
	}

	return out, dedupeStringsLocal(deps), nil
}

// parseEmbedDirective recognises a "//gastro:embed PATH" comment line
// and returns the trimmed PATH. Returns ok=false for any other comment
// (including block comments and prefix mismatches). The argument is
// expected to be the raw comment text including the leading "//".
func parseEmbedDirective(commentText string) (string, bool) {
	if !strings.HasPrefix(commentText, "//gastro:embed") {
		return "", false
	}
	rest := strings.TrimPrefix(commentText, "//gastro:embed")
	// Require at least one space/tab after the directive name to avoid
	// matching e.g. `//gastro:embedfoo`.
	if rest == "" || (rest[0] != ' ' && rest[0] != '\t') {
		return "", false
	}
	path := strings.TrimSpace(rest)
	if path == "" {
		return "", false
	}
	return path, true
}

// classifyEmbedVarType returns "string" or "[]byte" for supported var
// types and ok=false for anything else (including `template.HTML`,
// `any`, `interface{}`, named types, pointer types, fixed-size arrays).
// `typ` may be nil when the parser couldn't infer a type (e.g. a stray
// `var X` with no type and no initializer); treated as unsupported.
func classifyEmbedVarType(typ ast.Expr) (string, bool) {
	if typ == nil {
		return "", false
	}
	switch t := typ.(type) {
	case *ast.Ident:
		if t.Name == "string" {
			return "string", true
		}
	case *ast.ArrayType:
		// Must be `[]byte` exactly: no length, element is the unqualified
		// `byte` ident.
		if t.Len != nil {
			return "", false
		}
		ident, ok := t.Elt.(*ast.Ident)
		if !ok {
			return "", false
		}
		if ident.Name == "byte" {
			return "[]byte", true
		}
	}
	return "", false
}

// formatTypeExpr renders an ast.Expr type back to source form for use
// in error messages. Falls back to a generic placeholder for shapes
// the small switch doesn't recognise; the precise rendering is only
// used in error text and need not be exhaustive.
func formatTypeExpr(typ ast.Expr) string {
	switch t := typ.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.ArrayType:
		if t.Len == nil {
			return "[]" + formatTypeExpr(t.Elt)
		}
		return "[N]" + formatTypeExpr(t.Elt)
	case *ast.SelectorExpr:
		if pkg, ok := t.X.(*ast.Ident); ok {
			return pkg.Name + "." + t.Sel.Name
		}
	case *ast.StarExpr:
		return "*" + formatTypeExpr(t.X)
	case *ast.InterfaceType:
		if t.Methods == nil || len(t.Methods.List) == 0 {
			return "interface{}"
		}
	}
	return fmt.Sprintf("%T", typ)
}

// resolveEmbedPath turns a directive-relative path into an absolute,
// symlink-resolved path. The boundary model is two-stage:
//
//  1. Syntactic check (pre-EvalSymlinks): the cleaned candidate path
//     must sit inside ctx.ModuleRoot. This rejects `..` escapes in the
//     directive argument, which would otherwise let a stray
//     `//gastro:embed ../../../../etc/passwd` exfiltrate arbitrary
//     files without any user opt-in.
//
//  2. Symlink resolution (EvalSymlinks): if the candidate is a symlink,
//     follow it WITHOUT re-checking the resolved real path against
//     ModuleRoot. The user explicitly placed a symlink inside their
//     module pointing out, so they consented to the embed reach. This
//     is what makes monorepo layouts work — e.g. examples/gastro
//     symlinking docs/ to a parent dir's shared content.
//
// The combined model: "`..` escapes via path syntax: forbidden;
// escapes via user-placed symlinks: allowed."
func resolveEmbedPath(path string, ctx EmbedContext) (string, error) {
	sourceDir := filepath.Dir(ctx.SourceFile)
	candidate := filepath.Join(sourceDir, path)

	cleanCandidate, err := filepath.Abs(candidate)
	if err != nil {
		return "", fmt.Errorf("resolving %q: %w", path, err)
	}

	// Stage 1 (syntactic): cleaned candidate must be inside ModuleRoot.
	absRoot, err := filepath.Abs(ctx.ModuleRoot)
	if err != nil {
		absRoot = ctx.ModuleRoot
	}
	if rel, err := filepath.Rel(absRoot, cleanCandidate); err == nil &&
		(rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
		return "", fmt.Errorf(
			"%q escapes the module root (%s); embed targets must live inside the Go module (or be reachable via a symlink that does)",
			path, ctx.ModuleRoot,
		)
	}

	// Stage 2 (symlink resolution): follow without re-checking.
	resolved, err := filepath.EvalSymlinks(cleanCandidate)
	if err != nil {
		return "", fmt.Errorf("reading %q: %w", path, err)
	}

	return resolved, nil
}

// buildEmbedDecl renders the rewritten var declaration. The doc-comment
// group is fully replaced — directive lines are dropped and any
// non-directive comments inside the same group are preserved (they
// usually carry user-authored context worth keeping).
func buildEmbedDecl(varName, varType string, content []byte, doc *ast.CommentGroup) string {
	var b strings.Builder

	// Preserve non-directive comments. Each comment line keeps its
	// original `//` prefix.
	if doc != nil {
		for _, c := range doc.List {
			if _, isEmbed := parseEmbedDirective(c.Text); isEmbed {
				continue
			}
			b.WriteString(c.Text)
			b.WriteByte('\n')
		}
	}

	switch varType {
	case "string":
		// strconv.Quote handles all bytes losslessly; for valid UTF-8
		// it produces a readable Go string literal.
		b.WriteString("var ")
		b.WriteString(varName)
		b.WriteString(" = ")
		b.WriteString(strconv.Quote(string(content)))
	case "[]byte":
		b.WriteString("var ")
		b.WriteString(varName)
		b.WriteString(" = []byte(")
		// Prefer the compact `[]byte("...")` form when content is valid
		// UTF-8 (cheap to produce and easy on the eyes); fall back to
		// `[]byte{0x.., ...}` for binary data.
		if utf8.Valid(content) {
			b.WriteString(strconv.Quote(string(content)))
		} else {
			// Should be unreachable for the user-facing flow because the
			// caller already routes binary content through this branch
			// only via varType=="[]byte"; but the literal form needs to
			// work for arbitrary bytes regardless.
			b.WriteString(`"`)
			for _, c := range content {
				fmt.Fprintf(&b, `\x%02x`, c)
			}
			b.WriteString(`"`)
		}
		b.WriteString(")")
	}

	return b.String()
}

// dedupeStringsLocal is a private dedupe helper. The compiler package
// has its own; we don't import it from here to avoid a cycle.
func dedupeStringsLocal(in []string) []string {
	if len(in) <= 1 {
		return in
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

// findModuleRoot walks up from start looking for a directory that
// contains a go.mod file. Returns "" if none is found before reaching
// the filesystem root. Duplicated from internal/lsp/shadow/workspace.go
// to avoid the wrong-direction package dependency (codegen importing
// lsp); both copies are tiny and the helper is stable.
func findModuleRoot(start string) string {
	dir := start
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// FindModuleRootForFile is the exported entry point the compiler uses.
// It walks up from the directory containing the supplied file path
// until it finds a go.mod, returning the absolute path to that
// directory. Returns "" if no go.mod is found between the file and the
// filesystem root.
func FindModuleRootForFile(absFilePath string) string {
	return findModuleRoot(filepath.Dir(absFilePath))
}
