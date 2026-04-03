package shadow_test

import (
	"strings"
	"testing"

	"github.com/andrioid/gastro/internal/lsp/shadow"
)

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

	// Should have package declaration
	if !strings.Contains(vf.GoSource, "package") {
		t.Error("virtual file should have package declaration")
	}

	// Should have the import
	if !strings.Contains(vf.GoSource, `"fmt"`) {
		t.Error("virtual file should contain the import")
	}

	// Should have frontmatter code
	if !strings.Contains(vf.GoSource, `Title := "Hello"`) {
		t.Error("virtual file should contain frontmatter code")
	}

	// Source map should exist
	if vf.SourceMap == nil {
		t.Fatal("source map should not be nil")
	}
}

func TestGenerateVirtualFile_ImportLinesBecomeComments(t *testing.T) {
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

	// Component import lines should be commented out, not present as raw import code
	if strings.Contains(vf.GoSource, "\nimport Card") {
		t.Error("component imports should be commented out in virtual file")
	}

	// Should be converted to comments to preserve line numbers
	if !strings.Contains(vf.GoSource, "// import Card") {
		t.Errorf("import declarations should appear as comments, got:\n%s", vf.GoSource)
	}
}

func TestGenerateVirtualFile_EmptyFrontmatterReturnsError(t *testing.T) {
	gastroContent := `---
---
<h1>Hello</h1>`

	_, err := shadow.GenerateVirtualFile("pages/index.gastro", gastroContent)
	if err == nil {
		t.Fatal("expected an error for empty frontmatter block, got nil")
	}
}

func TestGenerateVirtualFile_NoFrontmatter(t *testing.T) {
	gastroContent := `<h1>Hello</h1>
<p>No frontmatter here</p>`

	vf, err := shadow.GenerateVirtualFile("components/divider.gastro", gastroContent)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(vf.GoSource, "package") {
		t.Error("no-frontmatter file should produce a valid Go file")
	}

	if vf.SourceMap == nil {
		t.Fatal("source map should not be nil")
	}

	// Should not contain function wrapper or frontmatter code
	if strings.Contains(vf.GoSource, "__handler") {
		t.Error("no-frontmatter file should not have __handler function wrapper")
	}
}
