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

	dir, err := os.MkdirTemp("", "gastro-lsp-shadow-*")
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
	// Go's build system ignores files whose names start with "_" or ".".
	// Using "gastro_" prefix instead of "__gastro_" so gopls can see them.
	return filepath.Join(ws.dir, "gastro_"+name+".go")
}

// UpdateFile regenerates the virtual .go file for a .gastro file and writes
// it to the shadow workspace. Returns the VirtualFile with source map.
func (ws *Workspace) UpdateFile(gastroFile, content string) (*VirtualFile, error) {
	parsed, err := parser.Parse(gastroFile, content)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", gastroFile, err)
	}

	rawFrontmatter := extractRawFrontmatter(content)
	processedFM := commentOutImportsAndUses(rawFrontmatter)

	var sb strings.Builder

	// Each virtual file lives in its own subdirectory and uses package main.
	// This ensures gopls can resolve the package correctly within the module.
	sb.WriteString("package main\n")

	// Imports
	if len(parsed.Imports) > 0 {
		sb.WriteString("\nimport (\n")
		for _, imp := range parsed.Imports {
			sb.WriteString(fmt.Sprintf("\t%q\n", imp))
		}
		sb.WriteString(")\n")
	}

	// Gastro runtime stubs so gopls doesn't error on gastro.Context() etc.
	// Uses only built-in types (no imports) to avoid interfering with gopls
	// diagnostic analysis. The stubs provide just enough type info for gopls
	// to resolve method calls on the gastro variable.
	sb.WriteString(`
type __gastroCtx struct{}
func (__gastroCtx) Request() interface{} { return nil }
func (__gastroCtx) Param(string) string { return "" }
func (__gastroCtx) Query(string) string { return "" }
func (__gastroCtx) Redirect(string, int) {}
func (__gastroCtx) Error(int, string) {}
func (__gastroCtx) Header(string, string) {}

type __gastroLib struct{}
func (__gastroLib) Context() *__gastroCtx { return nil }

var gastro = __gastroLib{}
`)

	// Function wrapper — unique name per file to avoid conflicts when
	// multiple .gastro files are open simultaneously
	funcName := uniqueFuncName(gastroFile)
	sb.WriteString(fmt.Sprintf("func %s() {\n", funcName))
	virtualFMStart := strings.Count(sb.String(), "\n") + 1

	sb.WriteString(processedFM)

	// Suppress "unused variable" diagnostics for template-exported variables.
	// In gastro, uppercase variables are passed to the template as {{ .VarName }}
	// but gopls can't see that usage since the template body is outside Go code.
	info, analyzeErr := codegen.AnalyzeFrontmatter(parsed.Frontmatter)
	if analyzeErr == nil {
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

	// Write to disk
	virtualPath := ws.VirtualFilePath(gastroFile)
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

		src := filepath.Join(ws.projectDir, name)
		dst := filepath.Join(ws.dir, name)

		if err := os.Symlink(src, dst); err != nil {
			return fmt.Errorf("symlinking %s: %w", name, err)
		}
	}

	return nil
}

// commentOutImportsAndUses replaces `use` and `import` lines with comments.
// These are already extracted by the parser and placed as top-level declarations
// in the virtual file. Leaving them in the function body would be a Go syntax
// error (imports inside a function body are invalid).
func commentOutImportsAndUses(frontmatter string) string {
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

		// Single-line import: import "path"
		if strings.HasPrefix(trimmed, "import ") {
			lines = append(lines, "// "+trimmed)
			continue
		}

		// Use declarations: use Name "path"
		if strings.HasPrefix(trimmed, "use ") {
			lines = append(lines, "// "+trimmed)
			continue
		}

		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
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
