package format

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andrioid/gastro/internal/parser"
)

// --- Frontmatter tests ---

func TestFormatImportBlock(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		imports []string
		uses    []useDecl
		want    string
	}{
		{
			name: "no imports",
			want: "",
		},
		{
			name:    "single go import",
			imports: []string{"fmt"},
			want:    "import (\n\t\"fmt\"\n)",
		},
		{
			name:    "multiple go imports sorted",
			imports: []string{"os", "fmt", "strings"},
			want:    "import (\n\t\"fmt\"\n\t\"os\"\n\t\"strings\"\n)",
		},
		{
			name: "component imports only",
			uses: []useDecl{
				{Name: "Layout", Path: "components/layout.gastro"},
				{Name: "Card", Path: "components/card.gastro"},
			},
			want: "import (\n\tCard \"components/card.gastro\"\n\tLayout \"components/layout.gastro\"\n)",
		},
		{
			name:    "mixed with blank line separator",
			imports: []string{"fmt", "myapp/db"},
			uses: []useDecl{
				{Name: "Layout", Path: "components/layout.gastro"},
			},
			want: "import (\n\t\"fmt\"\n\t\"myapp/db\"\n\n\tLayout \"components/layout.gastro\"\n)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var uses []parser.UseDeclaration
			for _, u := range tt.uses {
				uses = append(uses, parser.UseDeclaration{Name: u.Name, Path: u.Path})
			}
			got := formatImportBlock(tt.imports, uses)
			if got != tt.want {
				t.Errorf("got:\n%s\n\nwant:\n%s", got, tt.want)
			}
		})
	}
}

func TestFormatGoBody(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		code string
		want string
	}{
		{
			name: "empty",
			code: "",
			want: "",
		},
		{
			name: "simple assignment formatting",
			code: "x:=1\ny :=2",
			want: "x := 1\ny := 2",
		},
		{
			name: "preserves type declarations",
			code: "type Props struct{\nName string\n}\n\nTitle:=gastro.Props().Name",
			want: "type Props struct {\n\tName string\n}\n\nTitle := gastro.Props().Name",
		},
		{
			name: "if/error handling",
			code: "posts,err:=db.List()\nif err!=nil{\nreturn\n}\nPosts:=posts",
			want: "posts, err := db.List()\nif err != nil {\n\treturn\n}\nPosts := posts",
		},
		{
			name: "preserves comments",
			code: "// Fetch data\nx := 1",
			want: "// Fetch data\nx := 1",
		},
		{
			name: "syntax error returns original",
			code: "x := ",
			want: "x :=",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := formatGoBody(tt.code)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got:\n%s\n\nwant:\n%s", got, tt.want)
			}
		})
	}
}

// --- Template tests ---

func TestFormatTemplate_BasicHTML(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "already indented",
			input: "<div>\n\t<p>Hello</p>\n</div>",
			want:  "<div>\n\t<p>Hello</p>\n</div>",
		},
		{
			name:  "needs indentation",
			input: "<div>\n<p>Hello</p>\n</div>",
			want:  "<div>\n\t<p>Hello</p>\n</div>",
		},
		{
			name:  "nested tags",
			input: "<div>\n<section>\n<p>Hello</p>\n</section>\n</div>",
			want:  "<div>\n\t<section>\n\t\t<p>Hello</p>\n\t</section>\n</div>",
		},
		{
			name:  "void elements no indent change",
			input: "<div>\n<img src=\"...\">\n<br>\n<p>Text</p>\n</div>",
			want:  "<div>\n\t<img src=\"...\">\n\t<br>\n\t<p>Text</p>\n</div>",
		},
		{
			name:  "self-closing tags no indent change",
			input: "<div>\n<img src=\"...\" />\n<p>Text</p>\n</div>",
			want:  "<div>\n\t<img src=\"...\" />\n\t<p>Text</p>\n</div>",
		},
		{
			name:  "preserves empty lines",
			input: "<div>\n\n<p>Text</p>\n\n</div>",
			want:  "<div>\n\n\t<p>Text</p>\n\n</div>",
		},
		{
			name:  "doctype not indented",
			input: "<!DOCTYPE html>\n<html>\n<head>\n<title>Hi</title>\n</head>\n</html>",
			want:  "<!DOCTYPE html>\n<html>\n\t<head>\n\t\t<title>Hi</title>\n\t</head>\n</html>",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := formatTemplate(tt.input)
			if got != tt.want {
				t.Errorf("got:\n%s\n\nwant:\n%s", got, tt.want)
			}
		})
	}
}

func TestFormatTemplate_TemplateBlocks(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "if block",
			input: "{{ if .Show }}\n<div>Content</div>\n{{ end }}",
			want:  "{{ if .Show }}\n\t<div>Content</div>\n{{ end }}",
		},
		{
			name:  "range block",
			input: "{{ range .Items }}\n<li>{{ .Name }}</li>\n{{ end }}",
			want:  "{{ range .Items }}\n\t<li>{{ .Name }}</li>\n{{ end }}",
		},
		{
			name:  "with block",
			input: "{{ with .Data }}\n<p>{{ . }}</p>\n{{ end }}",
			want:  "{{ with .Data }}\n\t<p>{{ . }}</p>\n{{ end }}",
		},
		{
			name:  "wrap block",
			input: "{{ wrap Layout (dict \"Title\" .Title) }}\n<h1>Hello</h1>\n{{ end }}",
			want:  "{{ wrap Layout (dict \"Title\" .Title) }}\n\t<h1>Hello</h1>\n{{ end }}",
		},
		{
			name:  "nested blocks",
			input: "{{ range .Items }}\n<div>\n{{ if .Active }}\n<span>Active</span>\n{{ end }}\n</div>\n{{ end }}",
			want:  "{{ range .Items }}\n\t<div>\n\t\t{{ if .Active }}\n\t\t\t<span>Active</span>\n\t\t{{ end }}\n\t</div>\n{{ end }}",
		},
		{
			name:  "else block",
			input: "{{ if .Show }}\n<div>Yes</div>\n{{ else }}\n<div>No</div>\n{{ end }}",
			want:  "{{ if .Show }}\n\t<div>Yes</div>\n{{ else }}\n\t<div>No</div>\n{{ end }}",
		},
		{
			name:  "else if block",
			input: "{{ if .A }}\n<div>A</div>\n{{ else if .B }}\n<div>B</div>\n{{ else }}\n<div>C</div>\n{{ end }}",
			want:  "{{ if .A }}\n\t<div>A</div>\n{{ else if .B }}\n\t<div>B</div>\n{{ else }}\n\t<div>C</div>\n{{ end }}",
		},
		{
			name:  "inline if/end no depth change",
			input: "<div>\n{{ if .Show }}<span>Yes</span>{{ end }}\n</div>",
			want:  "<div>\n\t{{ if .Show }}<span>Yes</span>{{ end }}\n</div>",
		},
		{
			name:  "whitespace trimming syntax",
			input: "{{- if .Show -}}\n<div>Content</div>\n{{- end -}}",
			want:  "{{- if .Show -}}\n\t<div>Content</div>\n{{- end -}}",
		},
		{
			name:  "template call (no block)",
			input: "<div>\n{{ template \"header\" . }}\n<p>Content</p>\n</div>",
			want:  "<div>\n\t{{ template \"header\" . }}\n\t<p>Content</p>\n</div>",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := formatTemplate(tt.input)
			if got != tt.want {
				t.Errorf("got:\n%s\n\nwant:\n%s", got, tt.want)
			}
		})
	}
}

func TestFormatTemplate_VerbatimBlocks(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "pre block preserved",
			input: "<div>\n<pre>\n    Preserve\n        this\n</pre>\n</div>",
			want:  "<div>\n\t<pre>\n    Preserve\n        this\n</pre>\n</div>",
		},
		{
			name:  "script block preserved",
			input: "<div>\n<script>\nif (true) {\n    console.log(\"hi\");\n}\n</script>\n</div>",
			want:  "<div>\n\t<script>\nif (true) {\n    console.log(\"hi\");\n}\n</script>\n</div>",
		},
		{
			name:  "style block preserved",
			input: "<div>\n<style>\n.foo {\n    color: red;\n}\n</style>\n</div>",
			want:  "<div>\n\t<style>\n.foo {\n    color: red;\n}\n</style>\n</div>",
		},
		{
			name:  "raw block preserved",
			input: "<div>\n{{ raw }}\n{{ if .Foo }}\n{{ end }}\n{{ endraw }}\n</div>",
			want:  "<div>\n\t{{ raw }}\n{{ if .Foo }}\n{{ end }}\n{{ endraw }}\n</div>",
		},
		{
			name:  "inline script not verbatim",
			input: "<div>\n<script>alert(1)</script>\n</div>",
			want:  "<div>\n\t<script>alert(1)</script>\n</div>",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := formatTemplate(tt.input)
			if got != tt.want {
				t.Errorf("got:\n%s\n\nwant:\n%s", got, tt.want)
			}
		})
	}
}

func TestFormatTemplate_Comments(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "single-line HTML comment indented normally",
			input: "<div>\n<!-- comment -->\n<p>Text</p>\n</div>",
			want:  "<div>\n\t<!-- comment -->\n\t<p>Text</p>\n</div>",
		},
		{
			name:  "multi-line HTML comment preserved",
			input: "<div>\n<!--\n  Multi-line\n  comment\n-->\n<p>Text</p>\n</div>",
			want:  "<div>\n\t<!--\n  Multi-line\n  comment\n-->\n\t<p>Text</p>\n</div>",
		},
		{
			name:  "single-line template comment",
			input: "<div>\n{{/* comment */}}\n<p>Text</p>\n</div>",
			want:  "<div>\n\t{{/* comment */}}\n\t<p>Text</p>\n</div>",
		},
		{
			name:  "multi-line template comment preserved",
			input: "<div>\n{{/*\n  Multi-line\n  comment\n*/}}\n<p>Text</p>\n</div>",
			want:  "<div>\n\t{{/*\n  Multi-line\n  comment\n*/}}\n\t<p>Text</p>\n</div>",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := formatTemplate(tt.input)
			if got != tt.want {
				t.Errorf("got:\n%s\n\nwant:\n%s", got, tt.want)
			}
		})
	}
}

func TestFormatTemplate_MultiLineAction(t *testing.T) {
	t.Parallel()
	input := "{{ SomeComponent (dict\n    \"Title\" .Title\n    \"Author\" .Author\n) }}"
	want := "{{ SomeComponent (dict\n    \"Title\" .Title\n    \"Author\" .Author\n) }}"

	got := formatTemplate(input)
	if got != want {
		t.Errorf("got:\n%s\n\nwant:\n%s", got, want)
	}
}

func TestFormatTemplate_InlineDirective(t *testing.T) {
	t.Parallel()
	// Template directives inside HTML attributes should not affect indentation
	input := "<div class=\"{{ if .Active }}active{{ end }}\">\n<p>Text</p>\n</div>"
	want := "<div class=\"{{ if .Active }}active{{ end }}\">\n\t<p>Text</p>\n</div>"

	got := formatTemplate(input)
	if got != want {
		t.Errorf("got:\n%s\n\nwant:\n%s", got, want)
	}
}

// --- Full file tests ---

func TestFormatFile_Complete(t *testing.T) {
	t.Parallel()
	input := "---\nimport (\n\"fmt\"\n\"myapp/db\"\n\nLayout \"components/layout.gastro\"\n)\n\nctx:=gastro.Context()\nposts,err:=db.List()\nif err!=nil{\nctx.Error(500,\"fail\")\nreturn\n}\n\nPosts:=posts\n---\n{{ wrap Layout (dict \"Title\" \"Home\") }}\n<h1>Welcome</h1>\n<section>\n{{ range .Posts }}\n<p>{{ .Title }}</p>\n{{ end }}\n</section>\n{{ end }}\n"

	formatted, changed, err := FormatFile("test.gastro", input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !changed {
		t.Fatal("expected file to change")
	}

	// Verify structure
	if !strings.HasPrefix(formatted, "---\n") {
		t.Error("should start with ---")
	}
	if !strings.Contains(formatted, "\n---\n") {
		t.Error("should contain closing ---")
	}
	if !strings.HasSuffix(formatted, "\n") {
		t.Error("should end with newline")
	}

	// Verify imports are sorted
	if !strings.Contains(formatted, "\t\"fmt\"\n\t\"myapp/db\"") {
		t.Error("Go imports should be sorted")
	}
	if !strings.Contains(formatted, "\n\n\tLayout \"components/layout.gastro\"") {
		t.Error("component imports should be separated by blank line")
	}

	// Verify Go code is formatted
	if !strings.Contains(formatted, "ctx := gastro.Context()") {
		t.Error("Go code should be gofmt'd")
	}
}

func TestFormatFile_TemplateOnly(t *testing.T) {
	t.Parallel()

	t.Run("needs formatting", func(t *testing.T) {
		t.Parallel()
		input := "<div>\n<p>Hello</p>\n</div>\n"

		formatted, changed, err := FormatFile("test.gastro", input)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !changed {
			t.Fatal("expected file to change")
		}
		if strings.Contains(formatted, "---") {
			t.Error("template-only file should not have ---")
		}
		if !strings.Contains(formatted, "\t<p>Hello</p>") {
			t.Error("template should be indented")
		}
	})

	t.Run("already formatted", func(t *testing.T) {
		t.Parallel()
		input := "<div>\n\t<p>Hello</p>\n</div>\n"

		_, changed, err := FormatFile("test.gastro", input)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if changed {
			t.Fatal("expected no change for already-formatted file")
		}
	})
}

func TestFormatFile_PreservesWindowsLineEndings(t *testing.T) {
	t.Parallel()
	input := "---\r\nTitle := \"Hello\"\r\n---\r\n<h1>{{ .Title }}</h1>\r\n"

	formatted, _, err := FormatFile("test.gastro", input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(formatted, "\r\n") {
		t.Error("should preserve \\r\\n line endings")
	}
	if strings.Contains(formatted, "\n\n") {
		// All \n should be part of \r\n
		for i := 0; i < len(formatted)-1; i++ {
			if formatted[i] == '\n' && (i == 0 || formatted[i-1] != '\r') {
				t.Error("found bare \\n in windows-style file")
				break
			}
		}
	}
}

func TestFormatFile_Idempotent(t *testing.T) {
	t.Parallel()
	inputs := []struct {
		name    string
		content string
	}{
		{
			name:    "page with imports",
			content: "---\nimport (\n\t\"fmt\"\n\n\tLayout \"components/layout.gastro\"\n)\n\nctx := gastro.Context()\nTitle := fmt.Sprintf(\"Hello\")\n---\n{{ wrap Layout (dict \"Title\" .Title) }}\n\t<h1>{{ .Title }}</h1>\n{{ end }}\n",
		},
		{
			name:    "simple component",
			content: "---\ntype Props struct {\n\tCount int\n}\n\nCount := gastro.Props().Count\n---\n<div id=\"count\">{{ .Count }}</div>\n",
		},
		{
			name:    "template only",
			content: "<div>\n\t<p>Hello</p>\n</div>\n",
		},
	}

	for _, tt := range inputs {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			first, _, err := FormatFile("test.gastro", tt.content)
			if err != nil {
				t.Fatalf("first format: %v", err)
			}

			second, changed, err := FormatFile("test.gastro", first)
			if err != nil {
				t.Fatalf("second format: %v", err)
			}

			if changed {
				t.Errorf("formatting should be idempotent\nfirst:\n%s\nsecond:\n%s", first, second)
			}
		})
	}
}

// --- Example file smoke tests ---

func TestFormatFile_ExampleFiles(t *testing.T) {
	t.Parallel()

	// Find all .gastro files in examples/
	root := filepath.Join("..", "..", "examples")
	if _, err := os.Stat(root); os.IsNotExist(err) {
		t.Skip("examples directory not found")
	}

	var files []string
	filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && strings.HasSuffix(path, ".gastro") {
			files = append(files, path)
		}
		return nil
	})

	if len(files) == 0 {
		t.Skip("no .gastro files found in examples/")
	}

	for _, file := range files {
		file := file
		t.Run(filepath.Base(file), func(t *testing.T) {
			t.Parallel()
			content, err := os.ReadFile(file)
			if err != nil {
				t.Fatalf("read file: %v", err)
			}

			// Format once
			formatted, _, err := FormatFile(file, string(content))
			if err != nil {
				t.Fatalf("format: %v", err)
			}

			// Format twice — must be idempotent
			second, changed, err := FormatFile(file, formatted)
			if err != nil {
				t.Fatalf("second format: %v", err)
			}
			if changed {
				t.Errorf("formatting is not idempotent for %s\nfirst:\n%s\nsecond:\n%s", file, formatted, second)
			}
		})
	}
}

// --- Helper types for test readability ---

type useDecl struct {
	Name string
	Path string
}
