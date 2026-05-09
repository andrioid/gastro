package codegen

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/andrioid/gastro/internal/parser"
)

// ComponentSchema is the canonical, transport-neutral description of a
// .gastro component. It carries everything the compiler, the LSP shadow
// workspace, and the LSP server need to project that component into
// their respective worlds:
//
//   - compiler/compiler.go uses Fields for dict-key validation and HasChildren
//     to decide whether to add a Children field to the generated Render.X props.
//   - lsp/shadow/workspace.go uses the same data to synthesise XProps
//     stubs that gopls type-checks against.
//   - lsp/server/util.go uses ExportedName + RelPath to power auto-import
//     completion.
//
// Single source of truth: ScanComponents is the only function that
// reads component .gastro files; everyone else projects from
// []ComponentSchema. If a fifth consumer ever needs richer data, this
// is the type to extend.
type ComponentSchema struct {
	// RelPath is the component's path relative to the project root,
	// always with forward slashes (matches `import X "components/foo.gastro"`).
	RelPath string

	// FuncName is the generated handler function name
	// (e.g. "componentCard" for components/card.gastro).
	FuncName string

	// ExportedName is the Render-API method name for this component
	// (e.g. "Card", or "PostCard" for components/post-card.gastro).
	ExportedName string

	// HasProps is true iff the component's frontmatter declares a Props
	// struct via gastro.Props().
	HasProps bool

	// PropsFields lists the fields of the hoisted Props struct, in
	// declaration order. Empty when HasProps is false. Used for
	// dict-key validation and XProps stub generation.
	PropsFields []StructField

	// HasChildren is true iff the template body renders {{ .Children }}
	// (with optional whitespace-trim markers; see TemplateRendersChildren).
	HasChildren bool

	// Imports are the raw import paths declared in the frontmatter, in
	// declaration order. Used by the shadow workspace to compute the
	// subset of imports actually referenced by Props field types.
	Imports []string
}

// ScanComponents walks projectRoot/components for .gastro files and
// returns one ComponentSchema per readable component. The walk is
// depth-first, sorted lexically per directory by filepath.Walk, so the
// returned slice is deterministic across runs.
//
// Errors are best-effort: a missing components/ directory returns
// (nil, nil); per-file read or parse failures are silently skipped.
// This matches the silent-fallback contract the compiler and shadow
// workspace already rely on (parse errors surface with full context
// during the per-file codegen pass; the shadow falls back to a
// no-method *renderAPI stub for any component it can't read). A hard
// error is returned only if the underlying filepath.Walk fails for
// reasons other than "directory does not exist".
func ScanComponents(projectRoot string) ([]ComponentSchema, error) {
	componentsDir := filepath.Join(projectRoot, "components")
	if _, err := os.Stat(componentsDir); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var out []ComponentSchema
	err := filepath.Walk(componentsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			// Skip hidden directories (e.g. .gastro/) to match the
			// LSP server's discoverComponentsIn behaviour. The root
			// componentsDir itself may be hidden in pathological
			// setups; never skip it.
			if path != componentsDir && strings.HasPrefix(info.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		// Skip symlinks to files to avoid surprises with cyclic or
		// out-of-tree targets.
		if info.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		if !strings.HasSuffix(path, ".gastro") {
			return nil
		}

		rel, err := filepath.Rel(projectRoot, path)
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
		funcName := HandlerFuncName(rel)
		schema := ComponentSchema{
			RelPath:      rel,
			FuncName:     funcName,
			ExportedName: ExportedComponentName(funcName),
		}

		// Per-file parse failures are non-fatal: still emit a basic
		// schema with the discovered path and exported name so
		// auto-import completion (which only needs Name + RelPath)
		// keeps working. Schema-dependent consumers (the shadow
		// XProps stub, the dict-key validator) check HasProps before
		// using PropsFields so they degrade cleanly to "no schema".
		parsed, perr := parser.Parse(rel, string(content))
		if perr != nil {
			out = append(out, schema)
			return nil
		}
		fInfo, ferr := AnalyzeFrontmatter(parsed.Frontmatter)
		if ferr != nil {
			schema.HasChildren = TemplateRendersChildren(parsed.TemplateBody)
			schema.Imports = append([]string(nil), parsed.Imports...)
			out = append(out, schema)
			return nil
		}

		schema.HasProps = fInfo.PropsTypeName != ""
		schema.HasChildren = TemplateRendersChildren(parsed.TemplateBody)
		schema.Imports = append([]string(nil), parsed.Imports...)
		if schema.HasProps {
			_, hoisted := HoistTypeDeclarations(parsed.Frontmatter)
			schema.PropsFields = ParseStructFields(hoisted)
		}
		out = append(out, schema)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// PropsByPath projects a slice of ComponentSchema into the
// path → fields map used by the compiler's dict-key validator. Returns
// an empty map (not nil) so callers can iterate without nil-guards.
// Components without a Props struct are omitted, matching the
// compiler's existing convention that a missing key means "no schema,
// skip validation".
func PropsByPath(schemas []ComponentSchema) map[string][]StructField {
	out := make(map[string][]StructField, len(schemas))
	for _, s := range schemas {
		if len(s.PropsFields) > 0 {
			out[s.RelPath] = s.PropsFields
		}
	}
	return out
}

// DiscoverProjects walks rootDir and returns absolute paths of every
// directory that looks like a gastro project root — i.e. contains
// either a `pages/` or `components/` subdirectory.
//
// The walk skips:
//
//   - Hidden directories (`.git`, `.gastro`, etc.) so codegen output
//     trees aren't re-discovered as projects of their own.
//   - `node_modules/` for the same reason — cheap heuristic that
//     covers the most common nested-package noise.
//   - `testdata/` so test fixture trees don't surface as production
//     projects in CI runs.
//   - The interior of any directory already classified as a project,
//     so a project containing nested `pages/`-named subdirectories
//     (e.g. example fixtures inside an SDK repo) is discovered once,
//     not multiple times.
//
// rootDir is itself eligible: if it contains pages/ or components/,
// it's reported and the walk doesn't descend further. This makes
// `auditshadow /path/to/project` work as before — a single-project
// invocation is just the degenerate case of discovery.
//
// Returns paths in deterministic (lexical) order. An error from the
// underlying walk is surfaced verbatim; per-directory I/O failures
// (permission denied, etc.) are silently skipped.
func DiscoverProjects(rootDir string) ([]string, error) {
	absRoot, err := filepath.Abs(rootDir)
	if err != nil {
		return nil, err
	}

	var projects []string
	walkErr := filepath.Walk(absRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			// Skip dirs we can't read; don't fail the whole walk.
			return nil
		}
		if !info.IsDir() {
			return nil
		}
		name := info.Name()
		if path != absRoot {
			if strings.HasPrefix(name, ".") || name == "node_modules" || name == "testdata" {
				return filepath.SkipDir
			}
		}
		if isGastroProject(path) {
			projects = append(projects, path)
			// Don't recurse into a discovered project — its
			// pages/ and components/ children are *its* contents,
			// not separate projects.
			return filepath.SkipDir
		}
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	return projects, nil
}

// isGastroProject reports whether dir contains a pages/ or
// components/ subdirectory — the structural marker DiscoverProjects
// uses to identify a project root. Mirrors findProjectRoot in
// internal/lsp/server/util.go (the LSP's own project resolver) so
// auditshadow and the LSP agree on what counts as a project.
func isGastroProject(dir string) bool {
	for _, sub := range []string{"pages", "components"} {
		if info, err := os.Stat(filepath.Join(dir, sub)); err == nil && info.IsDir() {
			return true
		}
	}
	return false
}
