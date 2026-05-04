package shadow

import (
	"fmt"
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
	propsType    string // hoisted type name (e.g. "Props"); already unique-renamed
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

	src, err := codegen.GenerateHandler(parsed, info, isComponent)
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

// symlinkProject mirrors the user's project tree into the shadow
// workspace. go.mod and go.sum are copied (and go.mod is patched to
// rewrite relative `replace` directives to absolute paths) rather than
// symlinked so the shadow's temp directory can resolve modules
// independently of the project's relative layout. Everything else is
// symlinked for cheap reuse.
func (ws *Workspace) symlinkProject() error {
	entries, err := os.ReadDir(ws.projectDir)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		name := entry.Name()
		// Skip hidden directories, .gastro output, and any prior
		// shadow subdirectories.
		if strings.HasPrefix(name, ".") {
			continue
		}
		if strings.HasPrefix(name, "gastro_") {
			continue
		}

		src := filepath.Join(ws.projectDir, name)
		dst := filepath.Join(ws.dir, name)

		if name == "go.mod" {
			if err := copyAndPatchGoMod(src, dst, ws.projectDir); err != nil {
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

// scanComponents walks <projectDir>/components for .gastro files and
// returns metadata used to project per-component methods into the
// Router stub. Failures (unreadable files, parse errors) are silent
// because they're caught with full context by the codegen pipeline;
// the shadow falls back to a no-method *renderAPI stub for any
// component it can't read, matching today's silent cold-start UX.
func scanComponents(projectDir string) []componentScan {
	componentsDir := filepath.Join(projectDir, "components")
	if _, err := os.Stat(componentsDir); err != nil {
		return nil
	}

	var out []componentScan
	_ = filepath.Walk(componentsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".gastro") {
			return nil
		}
		rel, err := filepath.Rel(projectDir, path)
		if err != nil {
			return nil
		}
		// Normalise to forward slashes so the relative path matches
		// what users write in `import X "components/foo.gastro"`.
		rel = filepath.ToSlash(rel)

		content, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		parsed, err := parser.Parse(rel, string(content))
		if err != nil {
			return nil
		}
		fInfo, err := codegen.AnalyzeFrontmatter(parsed.Frontmatter)
		if err != nil {
			return nil
		}

		funcName := codegen.HandlerFuncName(rel)
		exported := codegen.ExportedComponentName(funcName)

		var fields []codegen.StructField
		if fInfo.PropsTypeName != "" {
			_, hoisted := codegen.HoistTypeDeclarations(parsed.Frontmatter)
			fields = codegen.ParseStructFields(hoisted)
		}

		// Codegen renames the user's hoisted Props type to
		// "<funcName><Title>" to avoid collisions across components.
		// We mirror that rename here so the stub's XProps alias
		// targets the right symbol.
		propsType := ""
		if fInfo.PropsTypeName != "" {
			propsType = funcName + strings.ToUpper(fInfo.PropsTypeName[:1]) + fInfo.PropsTypeName[1:]
		}

		out = append(out, componentScan{
			relPath:      rel,
			exportedName: exported,
			hasProps:     fInfo.PropsTypeName != "",
			hasChildren:  strings.Contains(parsed.TemplateBody, "{{ .Children }}"),
			propsFields:  fields,
			propsType:    propsType,
		})
		return nil
	})
	return out
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
	var sb strings.Builder
	sb.WriteString("// Auto-generated by gastro LSP shadow. DO NOT EDIT.\n")
	sb.WriteString("package " + pkgName + "\n\n")
	sb.WriteString("import (\n\t\"html/template\"\n\t\"net/http\"\n)\n\n")

	// Suppress unused-import warnings for Router-stub-only files where
	// no component method takes an http.Request.
	sb.WriteString("var _ http.ResponseWriter\nvar _ template.HTML\n\n")

	sb.WriteString("type Router struct{}\n\n")
	sb.WriteString("func (*Router) __gastro_getTemplate(string) *template.Template { return nil }\n")
	sb.WriteString("func (*Router) __gastro_handleError(http.ResponseWriter, *http.Request, error) {}\n\n")

	sb.WriteString("type renderAPI struct{}\n\n")
	sb.WriteString("var Render = &renderAPI{}\n\n")

	for _, c := range ws.componentMD {
		writeComponentStub(&sb, c)
	}
	return sb.String()
}

// writeComponentStub emits the per-component XProps type (alias or
// synthetic struct) and the corresponding *renderAPI method. The
// shape mirrors compiler/compiler.go's renderTmpl — the same three
// cases:
//
//   - HasProps && !HasChildren: XProps is an alias to the user's
//     hoisted Props struct.
//   - HasChildren:              XProps is a fresh struct combining
//                               user fields + Children template.HTML.
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
