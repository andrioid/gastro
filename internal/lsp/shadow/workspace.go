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
// generated from .gastro frontmatter. gopls analyzes these files to provide
// Go intelligence. The workspace symlinks the user's project so that imports
// from user packages resolve correctly.
type Workspace struct {
	dir        string // temp directory path
	projectDir string // the user's project root
	files      map[string]*VirtualFile
}

// NewWorkspace creates a shadow workspace for the given project directory.
// It creates a temp directory and symlinks the user's go.mod, go.sum, and
// source directories into it.
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

	return ws, nil
}

// Dir returns the path to the shadow workspace directory.
func (ws *Workspace) Dir() string {
	return ws.dir
}

// VirtualFilePath returns the path where a virtual .go file for the given
// .gastro file will be written in the shadow workspace.
// VirtualFilePath returns the path where a virtual .go file for the given
// .gastro file will be written. Files are placed at the module root so gopls
// can analyze them correctly. Each file has a unique name to avoid conflicts.
func (ws *Workspace) VirtualFilePath(gastroFile string) string {
	name := strings.TrimSuffix(gastroFile, ".gastro")
	name = strings.ReplaceAll(name, "/", "_")
	name = strings.ReplaceAll(name, "[", "")
	name = strings.ReplaceAll(name, "]", "")
	// Each shadow file lives in its own subdirectory so type declarations
	// (e.g. type Props struct) don't collide across components.
	return filepath.Join(ws.dir, "gastro_"+name, "main.go")
}

// UpdateFile regenerates the virtual .go file for a .gastro file and writes
// it to the shadow workspace. Returns the VirtualFile with source map.
func (ws *Workspace) UpdateFile(gastroFile, content string) (*VirtualFile, error) {
	parsed, err := parser.Parse(gastroFile, content)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", gastroFile, err)
	}

	// No frontmatter means no Go code for gopls to analyze.
	// Generate a minimal virtual file so stale diagnostics are cleared.
	if parsed.FrontmatterLine == 0 {
		return ws.writeEmptyVirtualFile(gastroFile)
	}

	rawFrontmatter := extractRawFrontmatter(content)
	processedFM := commentOutImports(rawFrontmatter)

	// Analyze frontmatter early so we can configure the gastro stubs correctly
	info, _ := codegen.AnalyzeFrontmatter(parsed.Frontmatter)

	// For components, hoist type declarations (e.g. type Props struct) to
	// package level so the Props() stub return type resolves. The types are
	// commented out in the function body to preserve line count.
	var hoistedTypes string
	if info != nil && info.IsComponent {
		processedFM, hoistedTypes = hoistTypes(processedFM)
	}

	var sb strings.Builder

	// Each virtual file lives in its own subdirectory and uses package main.
	// This ensures gopls can resolve the package correctly within the module.
	sb.WriteString("package main\n")

	// Imports. Track B (docs/history/frictions-plan.md §4.2) makes pages
	// reference ambient w and r; net/http is imported unconditionally
	// so frontmatter that calls r.Method or w.WriteHeader type-checks
	// in gopls.
	sb.WriteString("\nimport (\n\t\"net/http\"\n")
	for _, imp := range parsed.Imports {
		sb.WriteString(fmt.Sprintf("\t%q\n", imp))
	}
	sb.WriteString(")\n")

	// Suppress unused-import warnings for projects whose frontmatter doesn't
	// touch net/http (e.g. component frontmatter).
	sb.WriteString("\nvar _ http.ResponseWriter\n")

	// Hoisted type declarations (must precede stubs so *Props resolves)
	if hoistedTypes != "" {
		sb.WriteString("\n")
		sb.WriteString(hoistedTypes)
		sb.WriteString("\n")
	}

	// Build Props() return type stub based on whether the file has a Props struct.
	// For components, Props() returns a pointer to the Props struct so gopls can
	// resolve field accesses on gastro.Props().FieldName.
	propsReturnType := "interface{}"
	if info != nil && info.IsComponent {
		propsReturnType = "*Props"
	}

	sb.WriteString(fmt.Sprintf(`
type __gastroCtx struct{}
func (__gastroCtx) Request() interface{} { return nil }
func (__gastroCtx) Param(string) string { return "" }
func (__gastroCtx) Query(string) string { return "" }
func (__gastroCtx) Redirect(string, int) {}
func (__gastroCtx) Error(int, string) {}
func (__gastroCtx) Header(string, string) {}

type __gastroLib struct{}
func (__gastroLib) Context() *__gastroCtx { return nil }
func (__gastroLib) Props() %s { return nil }

var gastro = __gastroLib{}
`, propsReturnType))

	// Function wrapper — unique name per file to avoid conflicts when
	// multiple .gastro files are open simultaneously. Track B injects
	// the ambient (w, r) here so frontmatter that uses them type-checks.
	funcName := uniqueFuncName(gastroFile)
	sb.WriteString(fmt.Sprintf("func %s(w http.ResponseWriter, r *http.Request) {\n", funcName))
	sb.WriteString("\t_ = w\n\t_ = r\n")
	virtualFMStart := strings.Count(sb.String(), "\n") + 1

	sb.WriteString(processedFM)

	// Suppress "unused variable" diagnostics for template-exported variables.
	// In gastro, uppercase variables are passed to the template as {{ .VarName }}
	// but gopls can't see that usage since the template body is outside Go code.
	if info != nil {
		for _, v := range info.ExportedVars {
			sb.WriteString(fmt.Sprintf("\n_ = %s", v.Name))
		}
	}

	sb.WriteString("\n}\n")

	// gopls only runs full type-checking on packages that are part of the
	// build graph. Without func main(), this package main file is treated as
	// an orphan and gopls skips deep analysis (no diagnostics, limited
	// completions). Adding an empty main makes it a valid executable target.
	sb.WriteString("\nfunc main() {}\n")

	sm := sourcemap.New(parsed.FrontmatterLine, virtualFMStart)

	vf := &VirtualFile{
		GoSource:           sb.String(),
		SourceMap:          sm,
		Filename:           gastroFile,
		FrontmatterEndLine: parsed.TemplateBodyLine - 1,
	}

	// Write to disk (each shadow file lives in its own subdirectory)
	virtualPath := ws.VirtualFilePath(gastroFile)
	if err := os.MkdirAll(filepath.Dir(virtualPath), 0o755); err != nil {
		return nil, fmt.Errorf("creating virtual file dir: %w", err)
	}
	if err := os.WriteFile(virtualPath, []byte(vf.GoSource), 0o644); err != nil {
		return nil, fmt.Errorf("writing virtual file: %w", err)
	}

	ws.files[gastroFile] = vf
	return vf, nil
}

// writeEmptyVirtualFile generates a minimal virtual .go file for .gastro files
// without frontmatter. This ensures stale diagnostics are cleared when a file
// transitions from having frontmatter to not having it.
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

	ws.files[gastroFile] = vf
	return vf, nil
}

// GetFile returns the VirtualFile for a .gastro file, or nil if not tracked.
func (ws *Workspace) GetFile(gastroFile string) *VirtualFile {
	return ws.files[gastroFile]
}

// FindGastroFileForVirtualPath returns the .gastro file path (relative to
// project dir) that corresponds to a virtual .go file path, or "" if not found.
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

// symlinkProject symlinks the user's project contents into the shadow workspace.
func (ws *Workspace) symlinkProject() error {
	entries, err := os.ReadDir(ws.projectDir)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		name := entry.Name()
		// Skip hidden directories, .gastro output, and the shadow dir itself
		if strings.HasPrefix(name, ".") {
			continue
		}
		// Skip directories matching shadow file naming to avoid collisions
		if strings.HasPrefix(name, "gastro_") {
			continue
		}

		src := filepath.Join(ws.projectDir, name)
		dst := filepath.Join(ws.dir, name)

		if err := os.Symlink(src, dst); err != nil {
			return fmt.Errorf("symlinking %s: %w", name, err)
		}
	}

	return nil
}

// commentOutImports replaces `import` lines with comments to preserve line
// numbers for source map accuracy. All other frontmatter code (including
// gastro.Props() calls) passes through unchanged — the shadow file stubs
// provide real method signatures that gopls can analyze directly.
func commentOutImports(frontmatter string) string {
	var lines []string
	inGroupedImport := false

	for _, line := range strings.Split(frontmatter, "\n") {
		trimmed := strings.TrimSpace(line)

		// Handle grouped import blocks: import ( ... )
		if inGroupedImport {
			lines = append(lines, "// "+trimmed)
			if trimmed == ")" {
				inGroupedImport = false
			}
			continue
		}

		if trimmed == "import (" {
			inGroupedImport = true
			lines = append(lines, "// "+trimmed)
			continue
		}

		// Single-line import: import "path" or import Alias "path"
		if strings.HasPrefix(trimmed, "import ") {
			lines = append(lines, "// "+trimmed)
			continue
		}

		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// hoistTypes extracts type declarations from frontmatter for package-level
// emission, and comments them out in the body to preserve line count. This is
// needed because the __gastroLib.Props() stub returns *Props, which must be
// resolvable at package level.
func hoistTypes(frontmatter string) (body string, typeDecls string) {
	lines := strings.Split(frontmatter, "\n")
	var bodyLines []string
	var typeLines []string
	inType := false
	braceDepth := 0

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if !inType && strings.HasPrefix(trimmed, "type ") {
			inType = true
			braceDepth = 0
		}

		if inType {
			typeLines = append(typeLines, line)
			bodyLines = append(bodyLines, "// "+trimmed)
			braceDepth += strings.Count(line, "{") - strings.Count(line, "}")
			if braceDepth <= 0 {
				inType = false
			}
		} else {
			bodyLines = append(bodyLines, line)
		}
	}

	return strings.Join(bodyLines, "\n"), strings.Join(typeLines, "\n")
}

// uniqueFuncName generates a unique Go function name from a .gastro file path.
func uniqueFuncName(gastroFile string) string {
	name := strings.TrimSuffix(gastroFile, ".gastro")
	name = strings.ReplaceAll(name, "/", "_")
	name = strings.ReplaceAll(name, "[", "")
	name = strings.ReplaceAll(name, "]", "")
	name = strings.ReplaceAll(name, "-", "_")
	return "__gastro_handler_" + name
}
