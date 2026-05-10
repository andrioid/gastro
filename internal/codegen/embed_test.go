package codegen_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/andrioid/gastro/internal/codegen"
)

// makeModuleScaffold creates a temp Go module rooted at a fresh tmpdir.
// Returns (moduleRoot, sourceFile) where sourceFile sits at
// <root>/pages/page.gastro and acts as the imaginary .gastro source
// path the embed pass resolves relative to.
func makeModuleScaffold(t *testing.T) (root, sourceFile string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	pagesDir := filepath.Join(dir, "pages")
	if err := os.MkdirAll(pagesDir, 0o755); err != nil {
		t.Fatalf("mkdir pages: %v", err)
	}
	src := filepath.Join(pagesDir, "page.gastro")
	if err := os.WriteFile(src, []byte("placeholder"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	return dir, src
}

func writeFile(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func process(t *testing.T, frontmatter, sourceFile, moduleRoot string) (string, []string, error) {
	t.Helper()
	return codegen.ProcessEmbedDirectives(frontmatter, codegen.EmbedContext{
		SourceFile: sourceFile,
		ModuleRoot: moduleRoot,
	})
}

func TestEmbed_StringVar(t *testing.T) {
	root, src := makeModuleScaffold(t)
	writeFile(t, filepath.Join(root, "pages", "intro.md"), []byte("# hello\n"))

	in := `

//gastro:embed intro.md
var Content string
`
	out, deps, err := process(t, in, src, root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, `var Content = "# hello\n"`) {
		t.Fatalf("expected baked literal, got:\n%s", out)
	}
	if strings.Contains(out, "//gastro:embed") {
		t.Errorf("directive comment should be stripped from rewritten output:\n%s", out)
	}
	if len(deps) != 1 {
		t.Fatalf("want 1 dep, got %v", deps)
	}
	wantDep, _ := filepath.EvalSymlinks(filepath.Join(root, "pages", "intro.md"))
	if deps[0] != wantDep {
		t.Errorf("dep mismatch: want %s, got %s", wantDep, deps[0])
	}
}

func TestEmbed_BytesVar(t *testing.T) {
	root, src := makeModuleScaffold(t)
	writeFile(t, filepath.Join(root, "data.bin"), []byte("hello"))

	in := `

//gastro:embed ../data.bin
var Data []byte
`
	out, _, err := process(t, in, src, root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, `var Data = []byte("hello")`) {
		t.Fatalf("expected []byte literal, got:\n%s", out)
	}
}

func TestEmbed_TrailingNewlinePreserved(t *testing.T) {
	root, src := makeModuleScaffold(t)
	writeFile(t, filepath.Join(root, "pages", "x.md"), []byte("body\n"))

	in := `

//gastro:embed x.md
var X string
`
	out, _, err := process(t, in, src, root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, `"body\n"`) {
		t.Fatalf("expected trailing newline preserved (literal `body\\n`), got:\n%s", out)
	}
}

func TestEmbed_TemplateHTMLRejected(t *testing.T) {
	root, src := makeModuleScaffold(t)
	writeFile(t, filepath.Join(root, "x.md"), []byte("hi"))

	// Real frontmatter has imports stripped by the gastro parser before
	// it reaches the embed pass; test the bare type expression as it
	// would actually appear.
	in := `
//gastro:embed x.md
var X template.HTML
`
	_, _, err := process(t, in, src, root)
	if err == nil {
		t.Fatal("expected error for template.HTML var type")
	}
	if !strings.Contains(err.Error(), "string") || !strings.Contains(err.Error(), "[]byte") {
		t.Errorf("error should mention supported types: %v", err)
	}
}

func TestEmbed_OtherTypeRejected(t *testing.T) {
	root, src := makeModuleScaffold(t)
	writeFile(t, filepath.Join(root, "x.md"), []byte("hi"))

	cases := []struct {
		name string
		typ  string
	}{
		{"int", "int"},
		{"any", "any"},
		{"interface{}", "interface{}"},
		{"named", "myType"},
		{"pointer", "*string"},
		{"fixed array", "[5]byte"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := `

//gastro:embed x.md
var X ` + tc.typ + `
`
			_, _, err := process(t, in, src, root)
			if err == nil {
				t.Fatalf("expected error for type %q", tc.typ)
			}
			if !strings.Contains(err.Error(), "string") || !strings.Contains(err.Error(), "[]byte") {
				t.Errorf("error should mention supported types: %v", err)
			}
		})
	}
}

func TestEmbed_InitializerRejected(t *testing.T) {
	root, src := makeModuleScaffold(t)
	writeFile(t, filepath.Join(root, "x.md"), []byte("hi"))

	in := `

//gastro:embed x.md
var X string = "fallback"
`
	_, _, err := process(t, in, src, root)
	if err == nil {
		t.Fatal("expected error for explicit initializer")
	}
	if !strings.Contains(err.Error(), "initializer") {
		t.Errorf("error should mention `initializer`: %v", err)
	}
}

func TestEmbed_ParenthesizedGroupRejected(t *testing.T) {
	root, src := makeModuleScaffold(t)
	writeFile(t, filepath.Join(root, "x.md"), []byte("hi"))

	in := `

//gastro:embed x.md
var (
	A string
	B string
)
`
	_, _, err := process(t, in, src, root)
	if err == nil {
		t.Fatal("expected error for parenthesized group")
	}
	if !strings.Contains(err.Error(), "parenthesized") && !strings.Contains(err.Error(), "group") {
		t.Errorf("error should mention `parenthesized` or `group`: %v", err)
	}
}

func TestEmbed_MultiNameSpecRejected(t *testing.T) {
	root, src := makeModuleScaffold(t)
	writeFile(t, filepath.Join(root, "x.md"), []byte("hi"))

	in := `

//gastro:embed x.md
var A, B string
`
	_, _, err := process(t, in, src, root)
	if err == nil {
		t.Fatal("expected error for multi-name spec")
	}
	if !strings.Contains(err.Error(), "multi-name") && !strings.Contains(err.Error(), "one var per directive") {
		t.Errorf("error should mention `multi-name`: %v", err)
	}
}

func TestEmbed_StackedDirectivesRejected(t *testing.T) {
	root, src := makeModuleScaffold(t)
	writeFile(t, filepath.Join(root, "a.md"), []byte("a"))
	writeFile(t, filepath.Join(root, "b.md"), []byte("b"))

	in := `

//gastro:embed a.md
//gastro:embed b.md
var X string
`
	_, _, err := process(t, in, src, root)
	if err == nil {
		t.Fatal("expected error for stacked directives")
	}
	if !strings.Contains(err.Error(), "multiple") && !strings.Contains(err.Error(), "stacked") {
		t.Errorf("error should mention `multiple` or `stacked`: %v", err)
	}
}

func TestEmbed_PathRelativeToSource(t *testing.T) {
	root, _ := makeModuleScaffold(t)
	// .gastro file is deeper than the default scaffold's pages/page.gastro
	deepDir := filepath.Join(root, "pages", "blog", "2026")
	if err := os.MkdirAll(deepDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	deepSrc := filepath.Join(deepDir, "post.gastro")
	if err := os.WriteFile(deepSrc, []byte("placeholder"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	writeFile(t, filepath.Join(deepDir, "post.md"), []byte("DEEP"))

	in := `

//gastro:embed post.md
var X string
`
	out, _, err := process(t, in, deepSrc, root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, `"DEEP"`) {
		t.Fatalf("expected DEEP content, got:\n%s", out)
	}
}

func TestEmbed_AbsolutePathRejected(t *testing.T) {
	root, src := makeModuleScaffold(t)
	writeFile(t, filepath.Join(root, "x.md"), []byte("hi"))

	abs := filepath.Join(root, "x.md")
	in := `

//gastro:embed ` + abs + `
var X string
`
	_, _, err := process(t, in, src, root)
	if err == nil {
		t.Fatal("expected error for absolute path")
	}
	if !strings.Contains(err.Error(), "absolute") {
		t.Errorf("error should mention `absolute`: %v", err)
	}
}

func TestEmbed_PathOutsideModule_Rejected(t *testing.T) {
	root, src := makeModuleScaffold(t)

	// Try to embed a path that escapes via ..
	in := `

//gastro:embed ../../../../etc/passwd
var X string
`
	_, _, err := process(t, in, src, root)
	if err == nil {
		t.Fatal("expected error for path escaping module")
	}
	if !strings.Contains(err.Error(), "escapes the module root") &&
		!strings.Contains(err.Error(), "outside the module root") {
		t.Errorf("error should mention module-root escape: %v", err)
	}
}

func TestEmbed_MissingFile(t *testing.T) {
	root, src := makeModuleScaffold(t)

	in := `

//gastro:embed missing.md
var X string
`
	_, _, err := process(t, in, src, root)
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	if !strings.Contains(err.Error(), "missing.md") {
		t.Errorf("error should mention the bad path: %v", err)
	}
}

func TestEmbed_SymlinkInsideModule_Followed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks unreliable on Windows")
	}
	root, src := makeModuleScaffold(t)
	writeFile(t, filepath.Join(root, "real", "post.md"), []byte("REAL"))

	linkPath := filepath.Join(root, "pages", "post.md")
	if err := os.Symlink(filepath.Join(root, "real", "post.md"), linkPath); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	in := `

//gastro:embed post.md
var X string
`
	out, deps, err := process(t, in, src, root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, `"REAL"`) {
		t.Fatalf("expected symlink to be followed; got:\n%s", out)
	}
	// Dep should be the post-EvalSymlinks real path.
	wantReal, _ := filepath.EvalSymlinks(filepath.Join(root, "real", "post.md"))
	if len(deps) != 1 || deps[0] != wantReal {
		t.Errorf("dep should track real path; want %s, got %v", wantReal, deps)
	}
}

func TestEmbed_SymlinkEscapingModule_Followed(t *testing.T) {
	// Locked-in behaviour: a user-placed symlink inside the module
	// targeting a file outside the module is FOLLOWED, not rejected.
	// The user opts in by creating the symlink. This makes monorepo
	// layouts work — e.g. examples/gastro/docs -> ../../docs in the
	// gastro repo lets the website embed shared content owned by the
	// parent module. Syntactic `..` escapes in the directive argument
	// are still rejected (see TestEmbed_PathOutsideModule_Rejected).
	if runtime.GOOS == "windows" {
		t.Skip("symlinks unreliable on Windows")
	}
	parent := t.TempDir()
	inside := filepath.Join(parent, "inside")
	outside := filepath.Join(parent, "outside")
	if err := os.MkdirAll(filepath.Join(inside, "pages"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(inside, "go.mod"), []byte("module example.com/m\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	src := filepath.Join(inside, "pages", "page.gastro")
	if err := os.WriteFile(src, []byte("placeholder"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	if err := os.WriteFile(filepath.Join(outside, "shared.md"), []byte("SHARED"), 0o644); err != nil {
		t.Fatalf("write shared: %v", err)
	}
	linkPath := filepath.Join(inside, "pages", "shared.md")
	if err := os.Symlink(filepath.Join(outside, "shared.md"), linkPath); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	in := "\n//gastro:embed shared.md\nvar X string\n"
	out, deps, err := codegen.ProcessEmbedDirectives(in, codegen.EmbedContext{
		SourceFile: src,
		ModuleRoot: inside,
	})
	if err != nil {
		t.Fatalf("user-placed symlink should be followed even when target is outside the module: %v", err)
	}
	if !strings.Contains(out, `"SHARED"`) {
		t.Errorf("expected SHARED contents baked, got:\n%s", out)
	}
	wantReal, _ := filepath.EvalSymlinks(filepath.Join(outside, "shared.md"))
	if len(deps) != 1 || deps[0] != wantReal {
		t.Errorf("dep should track real path; want %s, got %v", wantReal, deps)
	}
}

func TestEmbed_InvalidUTF8_StringVar_Rejected(t *testing.T) {
	root, src := makeModuleScaffold(t)
	// 0xFF is not valid UTF-8.
	writeFile(t, filepath.Join(root, "pages", "bin.dat"), []byte{0xFF, 0xFE, 0xFD})

	in := `

//gastro:embed bin.dat
var X string
`
	_, _, err := process(t, in, src, root)
	if err == nil {
		t.Fatal("expected error for non-UTF-8 string embed")
	}
	if !strings.Contains(err.Error(), "UTF-8") {
		t.Errorf("error should mention UTF-8: %v", err)
	}

	// []byte should accept the same file.
	in2 := `

//gastro:embed bin.dat
var X []byte
`
	out, _, err := process(t, in2, src, root)
	if err != nil {
		t.Fatalf("[]byte should accept binary: %v", err)
	}
	if !strings.Contains(out, `[]byte(`) {
		t.Errorf("expected []byte literal in output: %s", out)
	}
}

func TestEmbed_MultipleInOneFile(t *testing.T) {
	root, src := makeModuleScaffold(t)
	writeFile(t, filepath.Join(root, "a.md"), []byte("A"))
	writeFile(t, filepath.Join(root, "b.md"), []byte("B"))

	in := `

//gastro:embed ../a.md
var A string

//gastro:embed ../b.md
var B string
`
	out, deps, err := process(t, in, src, root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, `var A = "A"`) {
		t.Errorf("expected baked A:\n%s", out)
	}
	if !strings.Contains(out, `var B = "B"`) {
		t.Errorf("expected baked B:\n%s", out)
	}
	if len(deps) != 2 {
		t.Errorf("want 2 deps, got %d: %v", len(deps), deps)
	}
}

func TestEmbed_NoDirectivePresent(t *testing.T) {
	root, src := makeModuleScaffold(t)
	in := `

var X = "untouched"
`
	out, deps, err := process(t, in, src, root)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if out != in {
		t.Errorf("expected unchanged output, got:\n%s", out)
	}
	if deps != nil {
		t.Errorf("expected nil deps, got %v", deps)
	}
}

func TestEmbed_PreservesNonDirectiveDocComments(t *testing.T) {
	root, src := makeModuleScaffold(t)
	writeFile(t, filepath.Join(root, "pages", "x.md"), []byte("hi"))

	in := `

// Content holds the rendered hero copy. Do not delete.
//gastro:embed x.md
var Content string
`
	out, _, err := process(t, in, src, root)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if !strings.Contains(out, "// Content holds the rendered hero copy") {
		t.Errorf("non-directive comment should be preserved:\n%s", out)
	}
	if strings.Contains(out, "//gastro:embed") {
		t.Errorf("directive line should be stripped:\n%s", out)
	}
}

// --- ValidateEmbedDirectives (LSP-side, read-only) ---

func TestValidate_NoDirectives(t *testing.T) {
	root, src := makeModuleScaffold(t)
	dirs, diags := codegen.ValidateEmbedDirectives("var X = 1\n", codegen.EmbedContext{SourceFile: src, ModuleRoot: root})
	if len(dirs) != 0 || len(diags) != 0 {
		t.Errorf("expected no directives or diagnostics, got dirs=%v diags=%v", dirs, diags)
	}
}

func TestValidate_HappyPath(t *testing.T) {
	root, src := makeModuleScaffold(t)
	writeFile(t, filepath.Join(root, "pages", "intro.md"), []byte("# hi"))
	in := "\n//gastro:embed intro.md\nvar Content string\n"
	dirs, diags := codegen.ValidateEmbedDirectives(in, codegen.EmbedContext{SourceFile: src, ModuleRoot: root})
	if len(diags) != 0 {
		t.Errorf("expected no diagnostics, got %v", diags)
	}
	if len(dirs) != 1 {
		t.Fatalf("expected 1 directive, got %v", dirs)
	}
	if dirs[0].VarName != "Content" || dirs[0].VarType != "string" || dirs[0].Path != "intro.md" {
		t.Errorf("directive content unexpected: %+v", dirs[0])
	}
}

func TestValidate_MissingFile_DiagKind(t *testing.T) {
	root, src := makeModuleScaffold(t)
	in := "\n//gastro:embed nope.md\nvar X string\n"
	_, diags := codegen.ValidateEmbedDirectives(in, codegen.EmbedContext{SourceFile: src, ModuleRoot: root})
	if len(diags) != 1 || diags[0].Kind != codegen.EmbedDiagMissingFile {
		t.Fatalf("expected single MissingFile diag, got %v", diags)
	}
}

func TestValidate_OutsideModule_DiagKind(t *testing.T) {
	root, src := makeModuleScaffold(t)
	in := "\n//gastro:embed ../../../../etc/passwd\nvar X string\n"
	_, diags := codegen.ValidateEmbedDirectives(in, codegen.EmbedContext{SourceFile: src, ModuleRoot: root})
	if len(diags) != 1 || diags[0].Kind != codegen.EmbedDiagOutsideModule {
		t.Fatalf("expected single OutsideModule diag, got %v", diags)
	}
}

func TestValidate_BadVarType_DiagKind(t *testing.T) {
	root, src := makeModuleScaffold(t)
	writeFile(t, filepath.Join(root, "pages", "x.md"), []byte("hi"))
	in := "\n//gastro:embed x.md\nvar X int\n"
	_, diags := codegen.ValidateEmbedDirectives(in, codegen.EmbedContext{SourceFile: src, ModuleRoot: root})
	if len(diags) != 1 || diags[0].Kind != codegen.EmbedDiagBadVarType {
		t.Fatalf("expected single BadVarType diag, got %v", diags)
	}
	if diags[0].VarType != "int" {
		t.Errorf("expected VarType=int, got %q", diags[0].VarType)
	}
}

func TestValidate_StackedDirectives_DiagKind(t *testing.T) {
	root, src := makeModuleScaffold(t)
	in := "\n//gastro:embed a.md\n//gastro:embed b.md\nvar X string\n"
	_, diags := codegen.ValidateEmbedDirectives(in, codegen.EmbedContext{SourceFile: src, ModuleRoot: root})
	if len(diags) != 1 || diags[0].Kind != codegen.EmbedDiagBadGrammar {
		t.Fatalf("expected single BadGrammar (stacked) diag, got %v", diags)
	}
}

func TestValidate_OrphanDirective_DiagKind(t *testing.T) {
	root, src := makeModuleScaffold(t)
	writeFile(t, filepath.Join(root, "pages", "x.md"), []byte("hi"))
	// Directive with no var declaration following it at all —
	// nothing for go/parser's CommentMap to bind to.
	in := "\n//gastro:embed x.md\n\nFoo := 42\n_ = Foo\n"
	_, diags := codegen.ValidateEmbedDirectives(in, codegen.EmbedContext{SourceFile: src, ModuleRoot: root})
	if len(diags) != 1 || diags[0].Kind != codegen.EmbedDiagBadGrammar {
		t.Fatalf("expected single BadGrammar (orphan) diag, got %v", diags)
	}
}

func TestValidate_LineNumbersAreFrontmatterRelative(t *testing.T) {
	root, src := makeModuleScaffold(t)
	in := "\n\n\n//gastro:embed nope.md\nvar X string\n"
	_, diags := codegen.ValidateEmbedDirectives(in, codegen.EmbedContext{SourceFile: src, ModuleRoot: root})
	if len(diags) != 1 {
		t.Fatalf("expected 1 diag, got %v", diags)
	}
	// Directive is on line 4 of the frontmatter (after three leading
	// blank lines). DeclLine is 5.
	if diags[0].DirectiveLine != 4 {
		t.Errorf("expected DirectiveLine=4, got %d", diags[0].DirectiveLine)
	}
	if diags[0].DeclLine != 5 {
		t.Errorf("expected DeclLine=5, got %d", diags[0].DeclLine)
	}
}
