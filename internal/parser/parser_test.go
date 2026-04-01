package parser_test

import (
	"testing"

	"github.com/andrioid/gastro/internal/parser"
)

func TestParse_SplitsFrontmatterAndBody(t *testing.T) {
	input := `---
import "fmt"

Title := "Hello"
---
<h1>{{ .Title }}</h1>`

	result, err := parser.Parse("test.gastro", input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Imports are extracted and stripped from Frontmatter
	wantFrontmatter := `Title := "Hello"`
	if result.Frontmatter != wantFrontmatter {
		t.Errorf("frontmatter:\ngot:  %q\nwant: %q", result.Frontmatter, wantFrontmatter)
	}

	wantBody := `<h1>{{ .Title }}</h1>`
	if result.TemplateBody != wantBody {
		t.Errorf("template body:\ngot:  %q\nwant: %q", result.TemplateBody, wantBody)
	}
}

func TestParse_ExtractsComponentImports(t *testing.T) {
	input := `---
import Card "components/card.gastro"
import Layout "components/layout.gastro"

Title := "Hello"
---
<h1>{{ .Title }}</h1>`

	result, err := parser.Parse("test.gastro", input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Uses) != 2 {
		t.Fatalf("expected 2 component imports, got %d", len(result.Uses))
	}

	if result.Uses[0].Name != "Card" || result.Uses[0].Path != "components/card.gastro" {
		t.Errorf("use[0]: got {%q, %q}, want {\"Card\", \"components/card.gastro\"}", result.Uses[0].Name, result.Uses[0].Path)
	}

	if result.Uses[1].Name != "Layout" || result.Uses[1].Path != "components/layout.gastro" {
		t.Errorf("use[1]: got {%q, %q}, want {\"Layout\", \"components/layout.gastro\"}", result.Uses[1].Name, result.Uses[1].Path)
	}
}

func TestParse_ComponentImportsStrippedFromFrontmatter(t *testing.T) {
	input := `---
import Card "components/card.gastro"

Title := "Hello"
---
<Card Title={.Title} />`

	result, err := parser.Parse("test.gastro", input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wantFrontmatter := `Title := "Hello"`
	if result.Frontmatter != wantFrontmatter {
		t.Errorf("frontmatter should not contain component imports:\ngot:  %q\nwant: %q", result.Frontmatter, wantFrontmatter)
	}
}

func TestParse_ExtractsImports(t *testing.T) {
	input := `---
import "fmt"
import "myapp/db"

Title := "Hello"
---
<h1>{{ .Title }}</h1>`

	result, err := parser.Parse("test.gastro", input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Imports) != 2 {
		t.Fatalf("expected 2 imports, got %d", len(result.Imports))
	}

	if result.Imports[0] != "fmt" {
		t.Errorf("import[0]: got %q, want %q", result.Imports[0], "fmt")
	}

	if result.Imports[1] != "myapp/db" {
		t.Errorf("import[1]: got %q, want %q", result.Imports[1], "myapp/db")
	}
}

func TestParse_GroupedImports(t *testing.T) {
	input := `---
import (
	"fmt"
	"myapp/db"
)

Title := "Hello"
---
<h1>{{ .Title }}</h1>`

	result, err := parser.Parse("test.gastro", input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Imports) != 2 {
		t.Fatalf("expected 2 imports, got %d", len(result.Imports))
	}

	if result.Imports[0] != "fmt" {
		t.Errorf("import[0]: got %q, want %q", result.Imports[0], "fmt")
	}

	if result.Imports[1] != "myapp/db" {
		t.Errorf("import[1]: got %q, want %q", result.Imports[1], "myapp/db")
	}
}

func TestParse_ImportsStrippedFromFrontmatter(t *testing.T) {
	input := `---
import "fmt"

Title := "Hello"
---
<h1>{{ .Title }}</h1>`

	result, err := parser.Parse("test.gastro", input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wantFrontmatter := `Title := "Hello"`
	if result.Frontmatter != wantFrontmatter {
		t.Errorf("frontmatter should not contain import declarations:\ngot:  %q\nwant: %q", result.Frontmatter, wantFrontmatter)
	}
}

func TestParse_GroupedImportsStrippedFromFrontmatter(t *testing.T) {
	input := `---
import (
	"fmt"
	"myapp/db"
)

Title := "Hello"
---
<h1>{{ .Title }}</h1>`

	result, err := parser.Parse("test.gastro", input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wantFrontmatter := `Title := "Hello"`
	if result.Frontmatter != wantFrontmatter {
		t.Errorf("frontmatter should not contain import declarations:\ngot:  %q\nwant: %q", result.Frontmatter, wantFrontmatter)
	}
}

func TestParse_MixedImportBlock(t *testing.T) {
	input := `---
import (
	"fmt"
	"myapp/db"

	Layout "components/layout.gastro"
	PostCard "components/post-card.gastro"
)

Title := "Hello"
---
<h1>{{ .Title }}</h1>`

	result, err := parser.Parse("test.gastro", input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Imports) != 2 {
		t.Fatalf("expected 2 Go imports, got %d: %v", len(result.Imports), result.Imports)
	}
	if result.Imports[0] != "fmt" {
		t.Errorf("import[0]: got %q, want %q", result.Imports[0], "fmt")
	}
	if result.Imports[1] != "myapp/db" {
		t.Errorf("import[1]: got %q, want %q", result.Imports[1], "myapp/db")
	}

	if len(result.Uses) != 2 {
		t.Fatalf("expected 2 component imports, got %d: %v", len(result.Uses), result.Uses)
	}
	if result.Uses[0].Name != "Layout" || result.Uses[0].Path != "components/layout.gastro" {
		t.Errorf("use[0]: got {%q, %q}, want {\"Layout\", \"components/layout.gastro\"}", result.Uses[0].Name, result.Uses[0].Path)
	}
	if result.Uses[1].Name != "PostCard" || result.Uses[1].Path != "components/post-card.gastro" {
		t.Errorf("use[1]: got {%q, %q}, want {\"PostCard\", \"components/post-card.gastro\"}", result.Uses[1].Name, result.Uses[1].Path)
	}

	wantFrontmatter := `Title := "Hello"`
	if result.Frontmatter != wantFrontmatter {
		t.Errorf("frontmatter should not contain imports:\ngot:  %q\nwant: %q", result.Frontmatter, wantFrontmatter)
	}
}

func TestParse_ComponentImportRequiresAlias(t *testing.T) {
	input := `---
import "components/layout.gastro"

Title := "Hello"
---
<h1>{{ .Title }}</h1>`

	_, err := parser.Parse("test.gastro", input)
	if err == nil {
		t.Fatal("expected error for .gastro import without alias")
	}
}

func TestParse_ComponentImportRejectsDotImport(t *testing.T) {
	input := `---
import . "components/layout.gastro"

Title := "Hello"
---
<h1>{{ .Title }}</h1>`

	_, err := parser.Parse("test.gastro", input)
	if err == nil {
		t.Fatal("expected error for dot import of .gastro file")
	}
}

func TestParse_ComponentImportRejectsBlankImport(t *testing.T) {
	input := `---
import _ "components/layout.gastro"

Title := "Hello"
---
<h1>{{ .Title }}</h1>`

	_, err := parser.Parse("test.gastro", input)
	if err == nil {
		t.Fatal("expected error for blank import of .gastro file")
	}
}

func TestParse_ComponentImportRequiresUppercase(t *testing.T) {
	input := `---
import layout "components/layout.gastro"

Title := "Hello"
---
<h1>{{ .Title }}</h1>`

	_, err := parser.Parse("test.gastro", input)
	if err == nil {
		t.Fatal("expected error for lowercase component import alias")
	}
}

func TestParse_EmptyFrontmatter(t *testing.T) {
	input := `---
---
<h1>Hello</h1>`

	result, err := parser.Parse("test.gastro", input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Frontmatter != "" {
		t.Errorf("expected empty frontmatter, got: %q", result.Frontmatter)
	}

	if result.TemplateBody != "<h1>Hello</h1>" {
		t.Errorf("template body: got %q, want %q", result.TemplateBody, "<h1>Hello</h1>")
	}
}

func TestParse_MissingDelimitersReturnsError(t *testing.T) {
	input := `<h1>Hello</h1>`

	_, err := parser.Parse("test.gastro", input)
	if err == nil {
		t.Fatal("expected an error for missing --- delimiters, got nil")
	}
}

func TestParse_SingleDelimiterReturnsError(t *testing.T) {
	input := `---
Title := "Hello"
<h1>{{ .Title }}</h1>`

	_, err := parser.Parse("test.gastro", input)
	if err == nil {
		t.Fatal("expected an error for missing closing --- delimiter, got nil")
	}
}

func TestParse_EmptyTemplateBody(t *testing.T) {
	input := `---
Title := "Hello"
---`

	result, err := parser.Parse("test.gastro", input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.TemplateBody != "" {
		t.Errorf("expected empty template body, got: %q", result.TemplateBody)
	}
}

func TestParse_RecordsFilename(t *testing.T) {
	input := `---
---
<h1>Hello</h1>`

	result, err := parser.Parse("pages/index.gastro", input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Filename != "pages/index.gastro" {
		t.Errorf("filename: got %q, want %q", result.Filename, "pages/index.gastro")
	}
}

func TestParse_FrontmatterLineNumbers(t *testing.T) {
	input := `---
import "fmt"

Title := "Hello"
---
<h1>{{ .Title }}</h1>`

	result, err := parser.Parse("test.gastro", input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Frontmatter starts at line 2 (line after the first ---)
	if result.FrontmatterLine != 2 {
		t.Errorf("frontmatter start line: got %d, want %d", result.FrontmatterLine, 2)
	}

	// Template body starts at line 6 (line after the second ---)
	if result.TemplateBodyLine != 6 {
		t.Errorf("template body start line: got %d, want %d", result.TemplateBodyLine, 6)
	}
}

func TestParse_TripleDashInsideStringLiteral(t *testing.T) {
	// --- inside a string literal in the frontmatter should NOT be
	// treated as a delimiter
	input := `---
Separator := "---"
Title := "Hello"
---
<h1>{{ .Title }}</h1>`

	result, err := parser.Parse("test.gastro", input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wantFrontmatter := `Separator := "---"
Title := "Hello"`
	if result.Frontmatter != wantFrontmatter {
		t.Errorf("frontmatter:\ngot:  %q\nwant: %q", result.Frontmatter, wantFrontmatter)
	}

	wantBody := `<h1>{{ .Title }}</h1>`
	if result.TemplateBody != wantBody {
		t.Errorf("template body:\ngot:  %q\nwant: %q", result.TemplateBody, wantBody)
	}
}
