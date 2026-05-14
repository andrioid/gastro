package shadow

import (
	"fmt"
	"go/ast"
	goparser "go/parser"
	"os"
	"path/filepath"
	"strings"

	"github.com/andrioid/gastro/internal/codegen"
	"github.com/andrioid/gastro/internal/lsp/sourcemap"
	"github.com/andrioid/gastro/internal/parser"
)

// Workspace manages a temporary directory containing virtual .go files
// generated from .gastro frontmatter. gopls analyzes these files to
// provide Go intelligence. The workspace symlinks the user's project so
// that imports from user packages resolve correctly.
//
// Implementation note (R6): the virtual .go file is the *real* codegen
// output (the same source `gastro generate` would write to
// .gastro/<file>.go), not a hand-rolled approximation. This means the
// shadow inherits everything codegen knows: marker rewrites
// (gastro.From → gastroRuntime.FromContext, etc.), auto-imports,
// hoisted Props types, the synthetic Children dict-key contract, and
// any future codegen changes — without a parallel mirror table in this
// package. The trade-off is that codegen rewrites are line-stable but
// not column-stable (e.g. `gastro.Props()` becomes `__props`),
// matching today's behaviour for the From-style rewrites.
//
// Each shadow .go file lives in its own subpackage (one .gastro file
// per Go package) so package-level declarations like Router stub and
// per-component XProps types do not collide across files.
type Workspace struct {
	dir         string // temp directory path
	projectDir  string // the user's project root
	files       map[string]*VirtualFile
	componentMD []componentScan // cached component metadata for Render stubs
}

// componentScan captures the minimal information needed to project a
// per-component Render method into the shadow Router stub.
type componentScan struct {
	relPath      string // e.g. "components/card.gastro"
	exportedName string // e.g. "Card"
	hasProps     bool
	hasChildren  bool
	propsFields  []codegen.StructField
	// neededImports maps the package qualifier used in Props field
	// types (e.g. "boardview" in `Backlog []boardview.CardData`) to
	// the full Go import path (e.g.
	// "github.com/example/internal/web/boardview"). Only qualifiers
	// actually referenced by the projected XProps are populated, so
	// the Router stub does not pull in unused imports that would
	// trigger "imported and not used" errors.
	neededImports map[string]string
}

// NewWorkspace creates a shadow workspace for the given project
// directory. Symlinks the user's tree (with go.mod / go.sum copied and
// patched so relative `replace` directives resolve from the temp dir),
// then scans `components/` to seed the Render API stub.
func NewWorkspace(projectDir string) (*Workspace, error) {
	absProject, err := filepath.Abs(projectDir)
	if err != nil {
		return nil, fmt.Errorf("resolving project dir: %w", err)
	}

	dir, err := os.MkdirTemp("", "gastro-shadow-*")
	if err != nil {
		return nil, fmt.Errorf("creating shadow workspace: %w", err)
	}

	ws := &Workspace{
		dir:        dir,
		projectDir: absProject,
		files:      make(map[string]*VirtualFile),
	}

	if err := ws.symlinkProject(); err != nil {
		os.RemoveAll(dir)
		return nil, fmt.Errorf("setting up shadow workspace: %w", err)
	}

	// Best-effort component scan; failures are non-fatal because the
	// shadow can still produce useful diagnostics for everything that
	// doesn't reference Render.
	ws.componentMD = scanComponents(absProject)

	return ws, nil
}

// Dir returns the path to the shadow workspace directory.
func (ws *Workspace) Dir() string {
	return ws.dir
}

// VirtualFilePath returns the path where a virtual .go file for the
// given .gastro file will be written. Each file lives in its own
// subdirectory so type declarations and the Router stub don't collide
// across components.
func (ws *Workspace) VirtualFilePath(gastroFile string) string {
	name := strings.TrimSuffix(gastroFile, ".gastro")
	name = strings.ReplaceAll(name, "/", "_")
	name = strings.ReplaceAll(name, "[", "")
	name = strings.ReplaceAll(name, "]", "")
	name = strings.ReplaceAll(name, "-", "_")
	return filepath.Join(ws.dir, "gastro_"+name, "main.go")
}

// stubFilePath returns the path of the Router-stub companion file for a
// given virtual .go file. Lives in the same subpackage so codegen-emitted
// references to Router, the __gastro_* helpers, Render and per-component
// XProps types all resolve.
func (ws *Workspace) stubFilePath(gastroFile string) string {
	return filepath.Join(filepath.Dir(ws.VirtualFilePath(gastroFile)), "router_stub.go")
}

// shadowPackageName derives the Go package name used for the shadow
// subpackage from the virtual file path. Codegen emits `package gastro`
// for every file; the shadow rewrites this to a per-file package name
// so multiple shadow files can co-exist in the same workspace without
// colliding on top-level declarations.
func shadowPackageName(virtualPath string) string {
	return filepath.Base(filepath.Dir(virtualPath))
}

// UpdateFile regenerates the virtual .go file for a .gastro file and
// writes it to the shadow workspace. The virtual file is produced by
// codegen.GenerateHandler — the same source `gastro generate` emits —
// so the shadow inherits codegen's rule set by construction.
func (ws *Workspace) UpdateFile(gastroFile, content string) (*VirtualFile, error) {
	parsed, err := parser.Parse(gastroFile, content)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", gastroFile, err)
	}

	// No frontmatter means no Go code for gopls to analyze. Generate a
	// minimal virtual file so stale diagnostics are cleared.
	if parsed.FrontmatterLine == 0 {
		return ws.writeEmptyVirtualFile(gastroFile)
	}

	info, err := codegen.AnalyzeFrontmatter(parsed.Frontmatter)
	if err != nil {
		return nil, fmt.Errorf("analyzing frontmatter for %s: %w", gastroFile, err)
	}

	isComponent := info.IsComponent
	if !isComponent && strings.HasPrefix(gastroFile, "components/") {
		isComponent = true
	}

	src, err := codegen.GenerateHandler(parsed, info, isComponent, codegen.GenerateOptions{MangleHoisted: false})
	if err != nil {
		return nil, fmt.Errorf("generating shadow source for %s: %w", gastroFile, err)
	}

	// Compute source map. codegen places frontmatter at a known anchor
	// (FindFrontmatterStart). The parser strips imports and leading
	// whitespace from parsed.Frontmatter, so the gastro line that lands
	// at the anchor position is the first non-blank, non-import line
	// in the original frontmatter region — not parsed.FrontmatterLine,
	// which is the first line after the opening `---`.
	hasProps := info.PropsTypeName != ""
	virtualFmStart := codegen.FindFrontmatterStart(src, isComponent, hasProps)
	gastroFmStart := firstFrontmatterContentLine(content, parsed.FrontmatterLine, parsed.TemplateBodyLine-1, isComponent)

	// Rewrite `package gastro` → per-file package name so this shadow
	// does not collide with the project's real .gastro/ package or
	// with sibling shadow files.
	virtualPath := ws.VirtualFilePath(gastroFile)
	pkgName := shadowPackageName(virtualPath)
	src = strings.Replace(src, "package gastro\n", "package "+pkgName+"\n", 1)

	stub := ws.routerStub(pkgName)

	vf := &VirtualFile{
		GoSource:           src,
		SourceMap:          sourcemap.New(gastroFmStart, virtualFmStart),
		Filename:           gastroFile,
		FrontmatterEndLine: parsed.TemplateBodyLine - 1,
	}

	if err := os.MkdirAll(filepath.Dir(virtualPath), 0o755); err != nil {
		return nil, fmt.Errorf("creating virtual file dir: %w", err)
	}
	if err := os.WriteFile(virtualPath, []byte(vf.GoSource), 0o644); err != nil {
		return nil, fmt.Errorf("writing virtual file: %w", err)
	}
	if err := os.WriteFile(ws.stubFilePath(gastroFile), []byte(stub), 0o644); err != nil {
		return nil, fmt.Errorf("writing router stub: %w", err)
	}

	ws.files[gastroFile] = vf
	return vf, nil
}

// writeEmptyVirtualFile generates a minimal virtual .go file for
// .gastro files without frontmatter. This ensures stale diagnostics
// are cleared when a file transitions from having frontmatter to not
// having it. Uses package main with a func main() so gopls treats it
// as a valid build target without needing any project context.
func (ws *Workspace) writeEmptyVirtualFile(gastroFile string) (*VirtualFile, error) {
	src := "package main\n\nfunc main() {}\n"

	vf := &VirtualFile{
		GoSource:           src,
		SourceMap:          sourcemap.New(1, 1),
		Filename:           gastroFile,
		FrontmatterEndLine: 0,
	}

	virtualPath := ws.VirtualFilePath(gastroFile)
	if err := os.MkdirAll(filepath.Dir(virtualPath), 0o755); err != nil {
		return nil, fmt.Errorf("creating virtual file dir: %w", err)
	}
	if err := os.WriteFile(virtualPath, []byte(vf.GoSource), 0o644); err != nil {
		return nil, fmt.Errorf("writing virtual file: %w", err)
	}
	// Remove any stale stub from a prior frontmatter version.
	os.Remove(ws.stubFilePath(gastroFile))

	ws.files[gastroFile] = vf
	return vf, nil
}

// GetFile returns the VirtualFile for a .gastro file, or nil if not tracked.
func (ws *Workspace) GetFile(gastroFile string) *VirtualFile {
	return ws.files[gastroFile]
}

// FindGastroFileForVirtualPath returns the .gastro file path (relative
// to project dir) that corresponds to a virtual .go file path, or ""
// if not found.
func (ws *Workspace) FindGastroFileForVirtualPath(virtualPath string) string {
	for gastroFile := range ws.files {
		if ws.VirtualFilePath(gastroFile) == virtualPath {
			return gastroFile
		}
	}
	return ""
}

// Close removes the shadow workspace directory.
func (ws *Workspace) Close() {
	os.RemoveAll(ws.dir)
}

// symlinkProject mirrors the user's Go module into the shadow
// workspace. The mirror starts at the module root (the nearest
// ancestor of projectDir containing a go.mod), not projectDir itself,
// so nested layouts like git-pm/internal/web/ — where the gastro
// project sits below the module root — still get full module
// visibility. Without this, the codegen output (which imports
// gastroRuntime and references project-internal packages) fails
// type-checking with "go.mod not found".
//
// go.mod and go.sum are copied (and go.mod is patched to rewrite
// relative `replace` directives to absolute paths) rather than
// symlinked so the shadow's temp directory can resolve modules
// independently of the original project's relative layout.
// Everything else is symlinked for cheap reuse.
func (ws *Workspace) symlinkProject() error {
	moduleRoot := findModuleRoot(ws.projectDir)
	if moduleRoot == "" {
		// No go.mod anywhere upstream — symlink projectDir as a
		// best-effort fallback. The shadow won't type-check against
		// the real runtime in this case but the LSP will still parse
		// the file and report syntactic issues.
		moduleRoot = ws.projectDir
	}

	entries, err := os.ReadDir(moduleRoot)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		name := entry.Name()
		// Skip hidden directories (including .gastro output, .git, etc.)
		// and any prior shadow subdirectories.
		if strings.HasPrefix(name, ".") {
			continue
		}
		if strings.HasPrefix(name, "gastro_") {
			continue
		}

		src := filepath.Join(moduleRoot, name)
		dst := filepath.Join(ws.dir, name)

		if name == "go.mod" {
			if err := copyAndPatchGoMod(src, dst, moduleRoot); err != nil {
				return fmt.Errorf("patching go.mod: %w", err)
			}
			continue
		}
		if name == "go.sum" {
			data, err := os.ReadFile(src)
			if err != nil {
				return fmt.Errorf("reading go.sum: %w", err)
			}
			if err := os.WriteFile(dst, data, 0o644); err != nil {
				return fmt.Errorf("copying go.sum: %w", err)
			}
			continue
		}
		if err := os.Symlink(src, dst); err != nil {
			return fmt.Errorf("symlinking %s: %w", name, err)
		}
	}

	return nil
}

// findModuleRoot walks up from start looking for a directory that
// contains a go.mod file. Returns "" if none is found before reaching
// the filesystem root. Used so the shadow workspace can mirror the
// full Go module — not just the gastro project subtree — for nested
// layouts where the gastro project (pages/, components/) sits below
// the module root.
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

// copyAndPatchGoMod copies a go.mod from src to dst, rewriting any
// `replace ... => <relpath>` directives where the right-hand side is
// a relative filesystem path. The path is resolved against the
// original projectDir, then written back as an absolute path so the
// directive remains valid from the shadow's temp location.
//
// Everything else (module declaration, requires, version-pinned
// replaces) passes through unchanged.
func copyAndPatchGoMod(src, dst, projectDir string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	absProject, err := filepath.Abs(projectDir)
	if err != nil {
		return err
	}

	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		out = append(out, patchReplaceLine(line, absProject))
	}
	return os.WriteFile(dst, []byte(strings.Join(out, "\n")), 0o644)
}

// patchReplaceLine rewrites a single go.mod line: if it's a `replace`
// directive whose RHS is a relative path, the path is resolved
// against absProject and substituted in place. Other lines pass
// through unchanged.
func patchReplaceLine(line, absProject string) string {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "replace ") || !strings.Contains(trimmed, "=>") {
		return line
	}
	parts := strings.SplitN(line, "=>", 2)
	rhs := strings.TrimSpace(parts[1])
	rhsParts := strings.Fields(rhs)
	if len(rhsParts) == 0 {
		return line
	}
	path := rhsParts[0]
	if !(strings.HasPrefix(path, "./") || strings.HasPrefix(path, "../") || path == "." || path == "..") {
		return line
	}
	abs, err := filepath.Abs(filepath.Join(absProject, path))
	if err != nil {
		return line
	}
	rhsParts[0] = abs
	return parts[0] + "=> " + strings.Join(rhsParts, " ")
}

// firstFrontmatterContentLine returns the 1-indexed gastro line of
// the first line in the frontmatter region that lands at
// FindFrontmatterStart's position in the codegen output, after the
// parser strips imports and (for components) codegen hoists type
// declarations out of the body.
//
// The walk skips:
//   - blank lines (parser/codegen TrimSpace strips leading blanks)
//   - import declarations, single or grouped (parser hoists them
//     into file.Imports)
//   - if component, type declarations (codegen.HoistTypeDeclarations
//     pulls them to package scope)
//
// Returns fmStart if the entire region is skippable; the source map
// will still produce a valid (if not ideal) mapping for the rare
// "frontmatter is types only" case.
func firstFrontmatterContentLine(content string, fmStart, fmEnd int, isComponent bool) int {
	lines := strings.Split(content, "\n")
	inGroupedImport := false
	inType := false
	typeDepth := 0
	for i := fmStart - 1; i < fmEnd && i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		if inGroupedImport {
			if trimmed == ")" {
				inGroupedImport = false
			}
			continue
		}
		if inType {
			typeDepth += strings.Count(line, "{") - strings.Count(line, "}")
			if typeDepth <= 0 {
				inType = false
			}
			continue
		}
		if trimmed == "" {
			continue
		}
		if trimmed == "import (" {
			inGroupedImport = true
			continue
		}
		if strings.HasPrefix(trimmed, "import ") {
			continue
		}
		if isComponent && strings.HasPrefix(trimmed, "type ") {
			inType = true
			typeDepth = strings.Count(line, "{") - strings.Count(line, "}")
			if typeDepth <= 0 {
				// Single-line type declaration (e.g. type X = Y), no
				// brace block to scan; skip just this line.
				inType = false
			}
			continue
		}
		return i + 1
	}
	return fmStart
}

// scanComponents projects codegen.ScanComponents output into the
// shadow-internal componentScan shape used by the Router stub. The
// project-root walk, frontmatter parsing, and Props extraction all
// happen in codegen — see internal/codegen/scan.go — so the shadow
// can never disagree with the compiler about which components exist
// or what fields they have.
//
// Shadow-specific addition: neededImports computes the subset of each
// component's frontmatter imports that the Props field types
// reference, so the synthesised XProps stub doesn't pull in unused
// imports (which would produce "imported and not used" errors when
// gopls type-checks the stub).
func scanComponents(projectDir string) []componentScan {
	schemas, err := codegen.ScanComponents(projectDir)
	if err != nil || len(schemas) == 0 {
		return nil
	}
	out := make([]componentScan, 0, len(schemas))
	for _, s := range schemas {
		out = append(out, componentScan{
			relPath:       s.RelPath,
			exportedName:  s.ExportedName,
			hasProps:      s.HasProps,
			hasChildren:   s.HasChildren,
			propsFields:   s.PropsFields,
			neededImports: neededImportsForFields(s.PropsFields, s.Imports),
		})
	}
	return out
}

// neededImportsForFields walks each Props field type and returns the
// subset of the component's frontmatter imports whose package name
// appears as a qualifier in any field type. This drives the
// "import only what's actually used" property of the Router stub.
func neededImportsForFields(fields []codegen.StructField, imports []string) map[string]string {
	if len(fields) == 0 || len(imports) == 0 {
		return nil
	}
	// Map package name → import path. The package name is the last
	// segment of the path by Go convention; this is wrong for
	// renamed packages (e.g. "v2" suffix) but those are rare in
	// .gastro frontmatter and the consequence of a miss is a stub
	// compile error in cmd/auditshadow, not a user-visible
	// regression — gopls will still parse the page and surface the
	// real frontmatter diagnostics.
	byName := make(map[string]string, len(imports))
	for _, p := range imports {
		byName[importPackageName(p)] = p
	}

	used := map[string]bool{}
	for _, f := range fields {
		for _, q := range typeQualifiers(f.Type) {
			used[q] = true
		}
	}

	out := make(map[string]string)
	for name := range used {
		if path, ok := byName[name]; ok {
			out[name] = path
		}
	}
	return out
}

// importPackageName guesses the package name of an import path by
// taking the last path segment. Correct for almost every Go module;
// renamed packages (e.g. via `package` declaration that differs from
// the path) need a real source scan but those are rare enough in
// frontmatter that the heuristic is acceptable for the LSP.
func importPackageName(path string) string {
	if i := strings.LastIndex(path, "/"); i >= 0 {
		return path[i+1:]
	}
	return path
}

// typeQualifiers returns the package-qualifier identifiers used in a
// Go type expression, e.g. "boardview" in "[]boardview.CardData" or
// "pkg" and "other" in "map[pkg.Key]other.Value". Falls back to a
// regex-like scan if go/parser rejects the type (which happens for
// some valid frontmatter shapes the parser doesn't expect at top
// level).
func typeQualifiers(typeStr string) []string {
	expr, err := goparser.ParseExpr(typeStr)
	if err != nil {
		return typeQualifiersFallback(typeStr)
	}
	var quals []string
	seen := map[string]bool{}
	ast.Inspect(expr, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		id, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		if !seen[id.Name] {
			seen[id.Name] = true
			quals = append(quals, id.Name)
		}
		return false
	})
	return quals
}

// typeQualifiersFallback handles type strings that go/parser can't
// digest. Naive: every "ident." prefix that isn't preceded by a "."
// itself counts as a qualifier. Good enough for the rare cases the
// AST path misses.
func typeQualifiersFallback(typeStr string) []string {
	var out []string
	seen := map[string]bool{}
	for i := 0; i < len(typeStr); i++ {
		c := typeStr[i]
		if !(isIdentStart(c)) {
			continue
		}
		j := i
		for j < len(typeStr) && isIdentPart(typeStr[j]) {
			j++
		}
		if j < len(typeStr) && typeStr[j] == '.' && (i == 0 || typeStr[i-1] != '.') {
			ident := typeStr[i:j]
			if !seen[ident] {
				seen[ident] = true
				out = append(out, ident)
			}
		}
		i = j
	}
	return out
}

func isIdentStart(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_'
}

func isIdentPart(c byte) bool {
	return isIdentStart(c) || (c >= '0' && c <= '9')
}

// routerStub builds the synthetic stub file that lives alongside each
// shadow virtual file. It defines:
//
//   - Router type and the two __gastro_* helper methods that codegen
//     output calls (__gastro_getTemplate, __gastro_handleError).
//   - renderAPI type and the package-level Render var.
//   - For each scanned component: a method on *renderAPI matching the
//     real Render API surface, plus an XProps alias / synthetic struct
//     so calls like `Render.Card(CardProps{...})` type-check with
//     field-by-field accuracy.
//
// Cold start (no .gastro/ directory present, no components found):
// the stub still defines a bare *renderAPI, so `Render.X` errors with
// "no method X" until `gastro generate` runs and the next workspace
// invocation picks up the components. This matches the silent
// fallback the audit document accepted (Q1).
//
// Implementation: the stub is generated as a string per shadow
// subpackage (rather than once per workspace) because each shadow
// file lives in its own Go package. The cost is small — there are
// typically O(10) components — but if it ever becomes a hot path the
// stub can be cached on the Workspace.
func (ws *Workspace) routerStub(pkgName string) string {
	// Aggregate all component-needed imports (deduped by path).
	extraImports := map[string]bool{}
	for _, c := range ws.componentMD {
		for _, p := range c.neededImports {
			extraImports[p] = true
		}
	}

	var sb strings.Builder
	sb.WriteString("// Auto-generated by gastro LSP shadow. DO NOT EDIT.\n")
	sb.WriteString("package " + pkgName + "\n\n")

	sb.WriteString("import (\n")
	sb.WriteString("\t\"bytes\"\n")
	sb.WriteString("\t\"html/template\"\n")
	sb.WriteString("\t\"net/http\"\n")
	// Sorted iteration so the stub is deterministic across runs —
	// helps debugging by keeping diffs minimal when components
	// change.
	sortedExtras := sortedKeys(extraImports)
	for _, p := range sortedExtras {
		fmt.Fprintf(&sb, "\t%q\n", p)
	}
	sb.WriteString(")\n\n")

	// Suppress unused-import warnings for the always-on imports.
	// Component-driven imports are guaranteed to be referenced by at
	// least one XProps field type — that's why neededImportsForFields
	// only includes packages whose name appears in a field qualifier
	// — so they don't need suppression lines.
	sb.WriteString("var _ http.ResponseWriter\nvar _ template.HTML\nvar _ bytes.Buffer\n\n")

	sb.WriteString("type Router struct{}\n\n")
	sb.WriteString("func (*Router) __gastro_getTemplate(string) *template.Template { return nil }\n")
	sb.WriteString("func (*Router) __gastro_handleError(http.ResponseWriter, *http.Request, error) {}\n")
	// __gastro_renderPage / __gastro_renderComponent are the request-aware
	// dispatch entry points generated page handlers and component methods
	// call. The shadow stubs them so frontmatter hover/typecheck still
	// works regardless of whether the project registers WithRequestFuncs.
	sb.WriteString("func (*Router) __gastro_renderPage(string, http.ResponseWriter, *http.Request, any) error { return nil }\n")
	sb.WriteString("func (*Router) __gastro_renderComponent(string, *http.Request, *bytes.Buffer, any) error { return nil }\n\n")

	sb.WriteString("type renderAPI struct{}\n\n")
	sb.WriteString("var Render = &renderAPI{}\n\n")

	for _, c := range ws.componentMD {
		writeComponentStub(&sb, c)
	}
	return sb.String()
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	// Use sort.Strings via the strings package (avoid pulling sort)
	// — small list, insertion sort is fine.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1] > out[j]; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}

// writeComponentStub emits the per-component XProps type (alias or
// synthetic struct) and the corresponding *renderAPI method. The
// shape mirrors compiler/compiler.go's renderTmpl — the same three
// cases:
//
//   - HasProps && !HasChildren: XProps is an alias to the user's
//     hoisted Props struct.
//   - HasChildren:              XProps is a fresh struct combining
//     user fields + Children template.HTML.
//   - !HasProps && !HasChildren: no XProps type; method takes no args.
func writeComponentStub(sb *strings.Builder, c componentScan) {
	switch {
	case c.hasChildren:
		fmt.Fprintf(sb, "type %sProps struct {\n", c.exportedName)
		for _, f := range c.propsFields {
			fmt.Fprintf(sb, "\t%s %s\n", f.Name, f.Type)
		}
		sb.WriteString("\tChildren template.HTML\n")
		sb.WriteString("}\n\n")
		fmt.Fprintf(sb, "func (*renderAPI) %s(%sProps) (string, error) { return \"\", nil }\n\n", c.exportedName, c.exportedName)
	case c.hasProps:
		// Project the user's hoisted struct as a fresh declaration
		// rather than aliasing the unique-renamed codegen type — the
		// codegen-renamed type lives in the project's .gastro/
		// package, which the shadow does not import.
		fmt.Fprintf(sb, "type %sProps struct {\n", c.exportedName)
		for _, f := range c.propsFields {
			fmt.Fprintf(sb, "\t%s %s\n", f.Name, f.Type)
		}
		sb.WriteString("}\n\n")
		fmt.Fprintf(sb, "func (*renderAPI) %s(%sProps) (string, error) { return \"\", nil }\n\n", c.exportedName, c.exportedName)
	default:
		fmt.Fprintf(sb, "func (*renderAPI) %s() (string, error) { return \"\", nil }\n\n", c.exportedName)
	}
}
