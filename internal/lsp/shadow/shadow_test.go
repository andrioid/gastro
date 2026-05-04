package shadow_test

import (
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"github.com/andrioid/gastro/internal/lsp/shadow"
)

// TestGenerateVirtualFile_BasicPage verifies that a page with imports and
// frontmatter produces a parseable Go file containing the user's imports
// and frontmatter code.
func TestGenerateVirtualFile_BasicPage(t *testing.T) {
	gastroContent := `---
import "fmt"

ctx := gastro.Context()
Title := "Hello"
---
<h1>{{ .Title }}</h1>`

	vf, err := shadow.GenerateVirtualFile("pages/index.gastro", gastroContent)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Output must be a parseable Go file.
	if _, err := parser.ParseFile(token.NewFileSet(), vf.Filename, vf.GoSource, 0); err != nil {
		t.Fatalf("virtual file is not valid Go: %v\n--- source ---\n%s", err, vf.GoSource)
	}

	// User's import is present at file-level (codegen hoists it into
	// the generated file's import block).
	if !strings.Contains(vf.GoSource, `"fmt"`) {
		t.Error("virtual file should contain user's import")
	}

	// User's frontmatter assignment is present (after codegen rewrites
	// `gastro.Context()` to `gastroRuntime.NewContext(w, r)`).
	if !strings.Contains(vf.GoSource, `Title := "Hello"`) {
		t.Error("virtual file should contain frontmatter code")
	}

	// gastro.Context() is rewritten by codegen, not preserved literally.
	if strings.Contains(vf.GoSource, "gastro.Context()") {
		t.Error("gastro.Context() should be rewritten, not preserved")
	}

	if vf.SourceMap == nil {
		t.Fatal("source map should not be nil")
	}
}

// TestGenerateVirtualFile_ComponentImports verifies that .gastro files
// importing other components produce a parseable Go file. Component
// imports (e.g. `import Card "components/card.gastro"`) are not real
// Go imports — codegen recognises them as Use declarations and keeps
// them out of the import block.
func TestGenerateVirtualFile_ComponentImports(t *testing.T) {
	gastroContent := `---
import Card "components/card.gastro"
import Layout "components/layout.gastro"

Title := "Hello"
---
<h1>{{ .Title }}</h1>`

	vf, err := shadow.GenerateVirtualFile("pages/index.gastro", gastroContent)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := parser.ParseFile(token.NewFileSet(), vf.Filename, vf.GoSource, 0); err != nil {
		t.Fatalf("virtual file is not valid Go: %v\n--- source ---\n%s", err, vf.GoSource)
	}

	// Component imports must not appear as real Go imports — they
	// reference .gastro paths that aren't valid Go module paths.
	if strings.Contains(vf.GoSource, `"components/card.gastro"`) {
		t.Errorf("component import path should not appear as Go import:\n%s", vf.GoSource)
	}
}

// TestGenerateVirtualFile_EmptyFrontmatterReturnsError verifies that
// an empty frontmatter block (--- ---) is reported as a parse error,
// matching the parser's contract.
func TestGenerateVirtualFile_EmptyFrontmatterReturnsError(t *testing.T) {
	gastroContent := `---
---
<h1>Hello</h1>`

	_, err := shadow.GenerateVirtualFile("pages/index.gastro", gastroContent)
	if err == nil {
		t.Fatal("expected an error for empty frontmatter block, got nil")
	}
}

// TestGenerateVirtualFile_NoFrontmatter verifies that .gastro files
// without any frontmatter delimiters produce a minimal, parseable
// virtual file (cleared diagnostics for the gopls session).
func TestGenerateVirtualFile_NoFrontmatter(t *testing.T) {
	gastroContent := `<h1>Hello</h1>
<p>No frontmatter here</p>`

	vf, err := shadow.GenerateVirtualFile("components/divider.gastro", gastroContent)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := parser.ParseFile(token.NewFileSet(), vf.Filename, vf.GoSource, 0); err != nil {
		t.Fatalf("virtual file is not valid Go: %v", err)
	}

	if vf.SourceMap == nil {
		t.Fatal("source map should not be nil")
	}

	// No frontmatter ⇒ no codegen wrapping; the virtual file is the
	// minimal "package main; func main() {}" shell.
	if !strings.Contains(vf.GoSource, "package main") {
		t.Errorf("expected minimal `package main` shell for no-frontmatter file, got:\n%s", vf.GoSource)
	}
}
