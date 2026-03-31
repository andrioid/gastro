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

func TestGenerateVirtualFile_UseLinesBecomeComments(t *testing.T) {
	gastroContent := `---
use Card "components/card.gastro"
use Layout "components/layout.gastro"

Title := "Hello"
---
<h1>{{ .Title }}</h1>`

	vf, err := shadow.GenerateVirtualFile("pages/index.gastro", gastroContent)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// use lines should be commented out, not present as code
	if strings.Contains(vf.GoSource, "\nuse ") {
		t.Error("use declarations should be commented out in virtual file")
	}

	// Should be converted to comments to preserve line numbers
	if !strings.Contains(vf.GoSource, "// use Card") {
		t.Error("use declarations should appear as comments")
	}
}

func TestGenerateVirtualFile_EmptyFrontmatter(t *testing.T) {
	gastroContent := `---
---
<h1>Hello</h1>`

	vf, err := shadow.GenerateVirtualFile("pages/index.gastro", gastroContent)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(vf.GoSource, "package") {
		t.Error("even empty frontmatter should produce a valid Go file")
	}
}
