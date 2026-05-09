package codegen_test

import (
	"strings"
	"testing"

	"github.com/andrioid/gastro/internal/codegen"
	"github.com/andrioid/gastro/internal/parser"
)

// TestGenerate_HoistedVar verifies that `var X = expr` at frontmatter
// top level is emitted at package scope (init-once) and the __data
// map references the mangled name as the value.
func TestGenerate_HoistedVar(t *testing.T) {
	frontmatter := `import "regexp"

var SlugRE = regexp.MustCompile(` + "`^[a-z]+$`" + `)
Title := "Hello"`

	parsed, err := parser.Parse("pages/index.gastro", "---\n"+frontmatter+"\n---\n<h1>{{ .Title }}</h1>")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, err := codegen.AnalyzeFrontmatter(parsed.Frontmatter)
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	out, err := codegen.GenerateHandler(parsed, info, false, codegen.GenerateOptions{MangleHoisted: true})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	// SlugRE must be at package scope, not inside the handler body.
	if !strings.Contains(out, "var __page_index_SlugRE = regexp.MustCompile") {
		t.Errorf("expected hoisted SlugRE at package scope, got:\n%s", out)
	}
	// __data emits SlugRE as exported var, value is the mangled name.
	if !strings.Contains(out, `"SlugRE": __page_index_SlugRE`) {
		t.Errorf("expected __data[\"SlugRE\"] = __page_index_SlugRE, got:\n%s", out)
	}
	// And `_ = __page_index_SlugRE` is emitted in the handler body so
	// the LSP shadow's queryVariableTypes scan finds the ident under
	// MangleHoisted=true. (Under MangleHoisted=false the same template
	// produces `_ = SlugRE`, exercised by the shadow tests.)
	if !strings.Contains(out, "_ = __page_index_SlugRE") {
		t.Errorf("expected `_ = __page_index_SlugRE` suppression line, got:\n%s", out)
	}
	// Title := stays in the handler body.
	if !strings.Contains(out, `Title := "Hello"`) {
		t.Errorf("expected Title := to remain in body, got:\n%s", out)
	}
}

// TestGenerate_HoistedConst exercises const decls.
func TestGenerate_HoistedConst(t *testing.T) {
	frontmatter := `const Limit = 10
Items := db.List(Limit)`

	parsed, err := parser.Parse("pages/index.gastro", "---\n"+frontmatter+"\n---\n<p>{{ .Items }}</p>")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, err := codegen.AnalyzeFrontmatter(parsed.Frontmatter)
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	out, err := codegen.GenerateHandler(parsed, info, false, codegen.GenerateOptions{MangleHoisted: true})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	if !strings.Contains(out, "const __page_index_Limit = 10") {
		t.Errorf("expected hoisted const at package scope, got:\n%s", out)
	}
	// Reference to Limit inside the body's := is rewritten to mangled.
	if !strings.Contains(out, "db.List(__page_index_Limit)") {
		t.Errorf("expected body ref to be rewritten, got:\n%s", out)
	}
}

// TestGenerate_HoistedFunc emits func at package scope and rewrites
// in-frontmatter calls.
func TestGenerate_HoistedFunc(t *testing.T) {
	frontmatter := `import "strings"

func slug(s string) string {
	return strings.ToLower(s)
}

Slug := slug("Hello")`

	parsed, err := parser.Parse("pages/index.gastro", "---\n"+frontmatter+"\n---\n<p>{{ .Slug }}</p>")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, err := codegen.AnalyzeFrontmatter(parsed.Frontmatter)
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	out, err := codegen.GenerateHandler(parsed, info, false, codegen.GenerateOptions{MangleHoisted: true})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	if !strings.Contains(out, "func __page_index_slug(s string) string") {
		t.Errorf("expected hoisted func at package scope, got:\n%s", out)
	}
	if !strings.Contains(out, `__page_index_slug("Hello")`) {
		t.Errorf("expected body ref to be rewritten, got:\n%s", out)
	}
}

// TestGenerate_HoistedInit_NotMangled keeps `func init()` verbatim so
// multiple init funcs per package retain their Go semantics.
func TestGenerate_HoistedInit_NotMangled(t *testing.T) {
	frontmatter := `import "log"

func init() {
	log.Println("page loaded")
}

Title := "Hello"`

	parsed, err := parser.Parse("pages/index.gastro", "---\n"+frontmatter+"\n---\n<h1>{{ .Title }}</h1>")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, err := codegen.AnalyzeFrontmatter(parsed.Frontmatter)
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	out, err := codegen.GenerateHandler(parsed, info, false, codegen.GenerateOptions{MangleHoisted: true})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	if !strings.Contains(out, "func init() {") {
		t.Errorf("expected unmangled func init at package scope, got:\n%s", out)
	}
	if strings.Contains(out, "__page_index_init") {
		t.Errorf("init should not be mangled, got:\n%s", out)
	}
}

// TestGenerate_HoistedFuncCapturingR_Errors rejects hoists that
// reference per-request scope.
func TestGenerate_HoistedFuncCapturingR_Errors(t *testing.T) {
	frontmatter := `var H = func() string { return r.URL.Path }`

	parsed, err := parser.Parse("pages/index.gastro", "---\n"+frontmatter+"\n---\n<h1>X</h1>")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, err = codegen.AnalyzeFrontmatter(parsed.Frontmatter)
	if err == nil {
		t.Fatal("expected HoistError, got nil")
	}
	if !strings.Contains(err.Error(), "r.URL") {
		t.Errorf("error should mention r.URL, got: %v", err)
	}
}

// TestGenerate_PerRequestUntouched verifies := decls and statements
// stay in the handler body.
func TestGenerate_PerRequestUntouched(t *testing.T) {
	frontmatter := `Items := db.List()
log.Println("rendered")`

	parsed, err := parser.Parse("pages/index.gastro", "---\n"+frontmatter+"\n---\n<p>{{ .Items }}</p>")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, err := codegen.AnalyzeFrontmatter(parsed.Frontmatter)
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	out, err := codegen.GenerateHandler(parsed, info, false, codegen.GenerateOptions{MangleHoisted: true})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	if !strings.Contains(out, "Items := db.List()") {
		t.Errorf("expected body to retain := decl, got:\n%s", out)
	}
	if !strings.Contains(out, `log.Println("rendered")`) {
		t.Errorf("expected body to retain log.Println, got:\n%s", out)
	}
}

// TestGenerate_MangleHoistedFalse_NoRename verifies that under the
// LSP shadow's MangleHoisted=false mode, hoisted decls keep their
// user-written names and the body is byte-identical (modulo template
// boilerplate).
func TestGenerate_MangleHoistedFalse_NoRename(t *testing.T) {
	frontmatter := `import "regexp"

var SlugRE = regexp.MustCompile(` + "`^[a-z]+$`" + `)
Title := "Hello"`

	parsed, err := parser.Parse("pages/index.gastro", "---\n"+frontmatter+"\n---\n<h1>{{ .Title }}</h1>")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, err := codegen.AnalyzeFrontmatter(parsed.Frontmatter)
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	out, err := codegen.GenerateHandler(parsed, info, false, codegen.GenerateOptions{MangleHoisted: false})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	// SlugRE stays unmangled at package scope.
	if !strings.Contains(out, "var SlugRE = regexp.MustCompile") {
		t.Errorf("expected unmangled var SlugRE, got:\n%s", out)
	}
	if strings.Contains(out, "__page_") {
		t.Errorf("expected no mangling under MangleHoisted=false, got:\n%s", out)
	}
	// __data uses the unmangled name as the value.
	if !strings.Contains(out, `"SlugRE": SlugRE`) {
		t.Errorf("expected __data[\"SlugRE\"] = SlugRE, got:\n%s", out)
	}
}

// TestGenerate_MangleHoistedFalse_PropsTypeName verifies that
// component Props stays unmangled in shadow mode.
func TestGenerate_MangleHoistedFalse_PropsTypeName(t *testing.T) {
	frontmatter := `type Props struct {
    Title string
}

Title := gastro.Props().Title`

	parsed, err := parser.Parse("components/card.gastro", "---\n"+frontmatter+"\n---\n<h1>{{ .Title }}</h1>")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, err := codegen.AnalyzeFrontmatter(parsed.Frontmatter)
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	out, err := codegen.GenerateHandler(parsed, info, true, codegen.GenerateOptions{MangleHoisted: false})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	if !strings.Contains(out, "type Props struct") {
		t.Errorf("expected unmangled type Props in shadow mode, got:\n%s", out)
	}
	if !strings.Contains(out, "MapToStruct[Props](propsMap)") {
		t.Errorf("expected MapToStruct[Props] (not mangled), got:\n%s", out)
	}
	if strings.Contains(out, "__component_") {
		t.Errorf("expected no mangling under MangleHoisted=false, got:\n%s", out)
	}
}

// TestGenerate_HoistedType_Page demonstrates that pages can hoist
// arbitrary helper types (not Props, which is component-only). The
// type emits at package scope with the __page_<id>_ prefix.
func TestGenerate_HoistedType_Page(t *testing.T) {
	frontmatter := `type Comment struct {
    Author string
    Text   string
}

Comments := []Comment{}`

	parsed, err := parser.Parse("pages/index.gastro", "---\n"+frontmatter+"\n---\n<p>{{ .Comments }}</p>")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, err := codegen.AnalyzeFrontmatter(parsed.Frontmatter)
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	out, err := codegen.GenerateHandler(parsed, info, false, codegen.GenerateOptions{MangleHoisted: true})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	if !strings.Contains(out, "type __page_index_Comment struct") {
		t.Errorf("expected hoisted type with __page_ prefix, got:\n%s", out)
	}
	// Body references to the type get rewritten too.
	if !strings.Contains(out, "[]__page_index_Comment{}") {
		t.Errorf("expected body type ref rewritten, got:\n%s", out)
	}
}
