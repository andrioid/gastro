package shadow

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/andrioid/gastro/internal/codegen"
	"github.com/andrioid/gastro/internal/lsp/sourcemap"
	"github.com/andrioid/gastro/internal/parser"
)

// propsCallRegex matches legacy "varname := gastro.Props[TypeName]()" syntax.
var propsCallRegex = regexp.MustCompile(`^(\w+)\s*:=\s*gastro\.Props\[(\w+)\]\(\)$`)

// newPropsCallRegex matches "varname := gastro.Props()" syntax (no generics).
var newPropsCallRegex = regexp.MustCompile(`^(\w+)\s*:=\s*gastro\.Props\(\)$`)

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

	// Analyze frontmatter early so we can configure the gastro stubs correctly
	info, _ := codegen.AnalyzeFrontmatter(parsed.Frontmatter)

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
	// multiple .gastro files are open simultaneously
	funcName := uniqueFuncName(gastroFile)
	sb.WriteString(fmt.Sprintf("func %s() {\n", funcName))
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

		src := filepath.Join(ws.projectDir, name)
		dst := filepath.Join(ws.dir, name)

		if err := os.Symlink(src, dst); err != nil {
			return fmt.Errorf("symlinking %s: %w", name, err)
		}
	}

	return nil
}

// commentOutImportsAndUses replaces `import` lines with comments,
// and rewrites `gastro.Props()` / `gastro.Props[T]()` calls for gopls.
// These transformations ensure the virtual file is valid Go while preserving
// line numbers for source map accuracy.
func commentOutImportsAndUses(frontmatter string) string {
	var lines []string
	inGroupedImport := false

	// Track whether we've injected the __props declaration
	propsInjected := false

	// First pass: check if Props struct is defined to get the type name
	propsTypeName := "Props" // default — the struct must be named "Props"
	hasPropsStruct := strings.Contains(frontmatter, "type Props struct")

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

		// Legacy: use declarations — still comment out for backward compat
		if strings.HasPrefix(trimmed, "use ") {
			lines = append(lines, "// "+trimmed)
			continue
		}

		// Legacy: gastro.Props[T]() -> var varname T
		if m := propsCallRegex.FindStringSubmatch(trimmed); m != nil {
			varName := m[1]
			typeName := m[2]
			lines = append(lines, fmt.Sprintf("var %s %s", varName, typeName))
			continue
		}

		// New: varname := gastro.Props() -> var varname Props
		if m := newPropsCallRegex.FindStringSubmatch(trimmed); m != nil {
			varName := m[1]
			lines = append(lines, fmt.Sprintf("var %s %s", varName, propsTypeName))
			continue
		}

		// New: gastro.Props().Field in expressions -> __props.Field
		// We need to inject `var __props Props` before the first usage
		if strings.Contains(trimmed, "gastro.Props()") {
			if !propsInjected && hasPropsStruct {
				lines = append(lines, fmt.Sprintf("var __props %s", propsTypeName))
				propsInjected = true
			}
			rewritten := strings.ReplaceAll(line, "gastro.Props()", "__props")
			lines = append(lines, rewritten)
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
