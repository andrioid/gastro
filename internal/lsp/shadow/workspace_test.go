package shadow_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andrioid/gastro/internal/lsp/shadow"
)

func TestWorkspace_CreatesDirectory(t *testing.T) {
	projectDir := createTestProject(t)
	ws, err := shadow.NewWorkspace(projectDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer ws.Close()

	if _, err := os.Stat(ws.Dir()); os.IsNotExist(err) {
		t.Fatal("shadow workspace directory was not created")
	}
}

func TestWorkspace_SymlinksGoMod(t *testing.T) {
	projectDir := createTestProject(t)
	ws, err := shadow.NewWorkspace(projectDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer ws.Close()

	goModPath := filepath.Join(ws.Dir(), "go.mod")
	if _, err := os.Stat(goModPath); os.IsNotExist(err) {
		t.Fatal("go.mod not symlinked into shadow workspace")
	}

	// Should be a symlink, not a copy
	info, err := os.Lstat(goModPath)
	if err != nil {
		t.Fatalf("lstat: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Error("go.mod should be a symlink")
	}
}

func TestWorkspace_SymlinksSourceDirs(t *testing.T) {
	projectDir := createTestProject(t)
	ws, err := shadow.NewWorkspace(projectDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer ws.Close()

	// The db/ directory should be accessible via the shadow workspace
	dbDir := filepath.Join(ws.Dir(), "db")
	if _, err := os.Stat(dbDir); os.IsNotExist(err) {
		t.Fatal("db/ directory not accessible in shadow workspace")
	}
}

func TestWorkspace_WritesVirtualFile(t *testing.T) {
	projectDir := createTestProject(t)
	ws, err := shadow.NewWorkspace(projectDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer ws.Close()

	gastroContent := `---
import "fmt"

Title := "Hello"
---
<h1>{{ .Title }}</h1>`

	vf, err := ws.UpdateFile("pages/index.gastro", gastroContent)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Virtual file should exist on disk
	virtualPath := ws.VirtualFilePath("pages/index.gastro")
	if _, err := os.Stat(virtualPath); os.IsNotExist(err) {
		t.Fatal("virtual .go file was not written to disk")
	}

	// Should have a source map
	if vf.SourceMap == nil {
		t.Fatal("source map should not be nil")
	}
}

func TestWorkspace_VirtualFileIsValidGo(t *testing.T) {
	projectDir := createTestProject(t)
	ws, err := shadow.NewWorkspace(projectDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer ws.Close()

	gastroContent := `---
import "fmt"

Title := fmt.Sprintf("Hello %s", "World")
---
<h1>{{ .Title }}</h1>`

	vf, err := ws.UpdateFile("pages/index.gastro", gastroContent)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The function body should NOT contain uncommented import statements
	// (imports inside a function body are a Go syntax error)
	src := vf.GoSource
	// Find the function body — name is unique per file
	funcIdx := strings.Index(src, "func __gastro_handler_")
	if funcIdx == -1 {
		t.Fatal("expected func __gastro_handler_* in virtual file")
	}
	funcBody := src[funcIdx:]
	for _, line := range strings.Split(funcBody, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "//") {
			continue
		}
		if strings.HasPrefix(trimmed, "import ") || trimmed == "import (" {
			t.Errorf("function body contains uncommented import statement: %q\nfunction body:\n%s", trimmed, funcBody)
		}
	}
}

func TestWorkspace_VirtualFileGroupedImportsValidGo(t *testing.T) {
	projectDir := createTestProject(t)
	ws, err := shadow.NewWorkspace(projectDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer ws.Close()

	gastroContent := `---
import (
	"fmt"
	"strings"
)

Title := fmt.Sprintf("Hello %s", strings.ToUpper("world"))
---
<h1>{{ .Title }}</h1>`

	vf, err := ws.UpdateFile("pages/index.gastro", gastroContent)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	src := vf.GoSource

	funcIdx := strings.Index(src, "func __gastro_handler_")
	if funcIdx == -1 {
		t.Fatal("expected func __gastro_handler_* in virtual file")
	}
	funcBody := src[funcIdx:]
	for _, line := range strings.Split(funcBody, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "//") {
			continue
		}
		if strings.HasPrefix(trimmed, "import ") || trimmed == "import (" {
			t.Errorf("function body contains uncommented import statement: %q\nfunction body:\n%s", trimmed, funcBody)
		}
	}
}

func TestWorkspace_MultipleFilesCoexist(t *testing.T) {
	projectDir := createTestProject(t)
	ws, err := shadow.NewWorkspace(projectDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer ws.Close()

	content1 := "---\nTitle := \"A\"\n---\n<h1>A</h1>"
	content2 := "---\nTitle := \"B\"\n---\n<h1>B</h1>"

	ws.UpdateFile("pages/index.gastro", content1)
	ws.UpdateFile("pages/about.gastro", content2)

	path1 := ws.VirtualFilePath("pages/index.gastro")
	path2 := ws.VirtualFilePath("pages/about.gastro")

	// Files should have different paths
	if path1 == path2 {
		t.Errorf("virtual files should have different paths: %s", path1)
	}

	// Both should exist
	if _, err := os.Stat(path1); os.IsNotExist(err) {
		t.Error("virtual file 1 does not exist")
	}
	if _, err := os.Stat(path2); os.IsNotExist(err) {
		t.Error("virtual file 2 does not exist")
	}
}

func TestWorkspace_CloseRemovesDirectory(t *testing.T) {
	projectDir := createTestProject(t)
	ws, err := shadow.NewWorkspace(projectDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	dir := ws.Dir()
	ws.Close()

	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Error("shadow workspace directory should be removed on Close")
	}
}

func TestWorkspace_SourceMapAccuracy(t *testing.T) {
	projectDir := createTestProject(t)
	ws, err := shadow.NewWorkspace(projectDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer ws.Close()

	// Realistic .gastro file with imports and multiple lines.
	// Line numbers are 1-indexed for clarity:
	//   gastro line 1: ---
	//   gastro line 2: import "fmt"
	//   gastro line 3: (blank)
	//   gastro line 4: ctx := gastro.Context()
	//   gastro line 5: Title := fmt.Sprintf("Hello %s", "World")
	//   gastro line 6: ---
	//   gastro line 7: <h1>{{ .Title }}</h1>
	gastroContent := "---\nimport \"fmt\"\n\nctx := gastro.Context()\nTitle := fmt.Sprintf(\"Hello %s\", \"World\")\n---\n<h1>{{ .Title }}</h1>"

	vf, err := ws.UpdateFile("pages/index.gastro", gastroContent)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The frontmatter starts at gastro line 2 (line after first ---).
	// Each gastro frontmatter line should map to a unique virtual line.
	// Verify by checking the virtual file content at the mapped lines.
	virtualLines := strings.Split(vf.GoSource, "\n")

	// Test each frontmatter line roundtrips correctly
	for gastroLine := 2; gastroLine <= 5; gastroLine++ {
		virtualLine := vf.SourceMap.GastroToVirtual(gastroLine)
		backGastro := vf.SourceMap.VirtualToGastro(virtualLine)
		if backGastro != gastroLine {
			t.Errorf("roundtrip failed: gastro %d -> virtual %d -> gastro %d", gastroLine, virtualLine, backGastro)
		}

		// Virtual line should be in bounds
		if virtualLine < 1 || virtualLine > len(virtualLines) {
			t.Errorf("gastro line %d mapped to out-of-bounds virtual line %d (file has %d lines)", gastroLine, virtualLine, len(virtualLines))
			continue
		}
	}

	// Verify specific content at mapped positions.
	// Gastro line 2 is "import "fmt"" which becomes "// import "fmt"" in the function body.
	vLine2 := vf.SourceMap.GastroToVirtual(2)
	if !strings.Contains(virtualLines[vLine2-1], "// import") {
		t.Errorf("gastro line 2 should map to commented import, got: %q", virtualLines[vLine2-1])
	}

	// Gastro line 4 is "ctx := gastro.Context()"
	vLine4 := vf.SourceMap.GastroToVirtual(4)
	if !strings.Contains(virtualLines[vLine4-1], "gastro.Context()") {
		t.Errorf("gastro line 4 should map to gastro.Context() line, got: %q", virtualLines[vLine4-1])
	}

	// Gastro line 5 is "Title := fmt.Sprintf(...)"
	vLine5 := vf.SourceMap.GastroToVirtual(5)
	if !strings.Contains(virtualLines[vLine5-1], "Title := fmt.Sprintf") {
		t.Errorf("gastro line 5 should map to Title assignment, got: %q", virtualLines[vLine5-1])
	}
}

func TestWorkspace_ExportedVarSuppression(t *testing.T) {
	projectDir := createTestProject(t)
	ws, err := shadow.NewWorkspace(projectDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer ws.Close()

	gastroContent := `---
import "fmt"

title := "private"
Title := fmt.Sprintf("Hello %s", "World")
Count := 42
---
<h1>{{ .Title }}</h1>`

	vf, err := ws.UpdateFile("pages/index.gastro", gastroContent)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	src := vf.GoSource

	// Exported vars should have _ = suppression lines
	if !strings.Contains(src, "_ = Title") {
		t.Error("expected _ = Title suppression line for exported var")
	}
	if !strings.Contains(src, "_ = Count") {
		t.Error("expected _ = Count suppression line for exported var")
	}

	// Private vars should NOT have suppression lines
	if strings.Contains(src, "_ = title") {
		t.Error("private var 'title' should not have a suppression line")
	}

	// The suppression lines should NOT appear for non-variable identifiers
	if strings.Contains(src, "_ = fmt") {
		t.Error("imported package 'fmt' should not have a suppression line")
	}
}

func TestWorkspace_FrontmatterEndLine(t *testing.T) {
	projectDir := createTestProject(t)
	ws, err := shadow.NewWorkspace(projectDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer ws.Close()

	tests := []struct {
		name     string
		content  string
		wantLine int // 1-indexed line of the closing ---
	}{
		{
			name:     "simple",
			content:  "---\nTitle := \"Hi\"\n---\n<h1>hi</h1>",
			wantLine: 3,
		},
		{
			name:     "with imports",
			content:  "---\nimport \"fmt\"\n\nTitle := fmt.Sprintf(\"Hi\")\n---\n<h1>hi</h1>",
			wantLine: 5,
		},
		{
			name:     "with blank lines",
			content:  "---\nTitle := \"Hi\"\n\n\n---\n<h1>hi</h1>",
			wantLine: 5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vf, err := ws.UpdateFile("pages/test.gastro", tt.content)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if vf.FrontmatterEndLine != tt.wantLine {
				t.Errorf("FrontmatterEndLine = %d, want %d", vf.FrontmatterEndLine, tt.wantLine)
			}
		})
	}
}

func TestWorkspace_PropsCallRewritten(t *testing.T) {
	projectDir := createTestProject(t)
	ws, err := shadow.NewWorkspace(projectDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer ws.Close()

	gastroContent := `---
type Props struct {
    Title string
}

Title := gastro.Props().Title
---
<h1>{{ .Title }}</h1>`

	vf, err := ws.UpdateFile("components/card.gastro", gastroContent)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	src := vf.GoSource

	// gastro.Props() should be rewritten — no raw gastro.Props calls in output
	if strings.Contains(src, "gastro.Props") {
		t.Errorf("virtual file should not contain gastro.Props call, got:\n%s", src)
	}

	// The rewrite should produce __props references
	if !strings.Contains(src, "__props") {
		t.Errorf("expected __props in virtual file, got:\n%s", src)
	}
}

// createTestProject creates a minimal Go project for testing.
func createTestProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module testproject\n\ngo 1.22\n"), 0o644)

	os.MkdirAll(filepath.Join(dir, "db"), 0o755)
	os.WriteFile(filepath.Join(dir, "db", "db.go"), []byte("package db\n"), 0o644)

	os.MkdirAll(filepath.Join(dir, "pages"), 0o755)

	return dir
}
