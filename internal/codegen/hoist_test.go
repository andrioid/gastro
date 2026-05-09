package codegen_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/andrioid/gastro/internal/codegen"
)

func TestHoistDecls_Empty(t *testing.T) {
	body, decls, _, err := codegen.HoistDecls("", codegen.HoistOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if body != "" {
		t.Errorf("body = %q, want empty", body)
	}
	if len(decls) != 0 {
		t.Errorf("decls = %v, want empty", decls)
	}
}

func TestHoistDecls_VarSimple(t *testing.T) {
	src := `var Title = "Hello"`
	body, decls, _, err := codegen.HoistDecls(src, codegen.HoistOptions{Prefix: "__page_x_"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.TrimSpace(body) != "" {
		t.Errorf("body should be blank after hoisting, got %q", body)
	}
	if len(decls) != 1 {
		t.Fatalf("expected 1 decl, got %d", len(decls))
	}
	d := decls[0]
	if d.Kind != codegen.HoistVar {
		t.Errorf("Kind = %v, want HoistVar", d.Kind)
	}
	if d.Name != "Title" {
		t.Errorf("Name = %q, want Title", d.Name)
	}
	if d.MangledName != "__page_x_Title" {
		t.Errorf("MangledName = %q, want __page_x_Title", d.MangledName)
	}
	if !d.IsExported {
		t.Errorf("IsExported = false, want true")
	}
	if !strings.Contains(d.SourceText, `"Hello"`) {
		t.Errorf("SourceText missing literal: %q", d.SourceText)
	}
}

func TestHoistDecls_VarUnmangled(t *testing.T) {
	src := `var Title = "Hello"`
	_, decls, _, err := codegen.HoistDecls(src, codegen.HoistOptions{}) // no Prefix
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(decls) != 1 {
		t.Fatalf("expected 1 decl, got %d", len(decls))
	}
	if decls[0].Name != decls[0].MangledName {
		t.Errorf("Name (%q) != MangledName (%q) under empty prefix", decls[0].Name, decls[0].MangledName)
	}
}

func TestHoistDecls_VarBlockSplit(t *testing.T) {
	src := `var (
	X = 1
	Y = 2
)`
	_, decls, _, err := codegen.HoistDecls(src, codegen.HoistOptions{Prefix: "__p_"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(decls) != 2 {
		t.Fatalf("expected 2 decls (X and Y), got %d", len(decls))
	}
	for _, d := range decls {
		// Each split decl should be re-rendered as a single var decl.
		if !strings.HasPrefix(strings.TrimSpace(d.SourceText), "var ") {
			t.Errorf("expected single-var rendering, got %q", d.SourceText)
		}
	}
}

func TestHoistDecls_ConstAndType(t *testing.T) {
	src := `const Limit = 10
type Comment struct {
	Author string
	Text   string
}`
	_, decls, _, err := codegen.HoistDecls(src, codegen.HoistOptions{Prefix: "__page_idx_"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(decls) != 2 {
		t.Fatalf("expected 2 decls, got %d", len(decls))
	}
	var sawConst, sawType bool
	for _, d := range decls {
		switch d.Kind {
		case codegen.HoistConst:
			sawConst = true
			if d.MangledName != "__page_idx_Limit" {
				t.Errorf("Limit MangledName = %q", d.MangledName)
			}
		case codegen.HoistType:
			sawType = true
			if d.MangledName != "__page_idx_Comment" {
				t.Errorf("Comment MangledName = %q", d.MangledName)
			}
		}
	}
	if !sawConst || !sawType {
		t.Errorf("missing kinds: const=%v type=%v", sawConst, sawType)
	}
}

func TestHoistDecls_FuncSimple(t *testing.T) {
	src := `func slug(s string) string {
	return strings.ToLower(s)
}`
	body, decls, _, err := codegen.HoistDecls(src, codegen.HoistOptions{Prefix: "__page_idx_"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.TrimSpace(body) != "" {
		t.Errorf("body should be blank, got %q", body)
	}
	if len(decls) != 1 {
		t.Fatalf("expected 1 decl, got %d", len(decls))
	}
	d := decls[0]
	if d.Kind != codegen.HoistFunc {
		t.Errorf("Kind = %v, want HoistFunc", d.Kind)
	}
	if d.MangledName != "__page_idx_slug" {
		t.Errorf("MangledName = %q", d.MangledName)
	}
	if !strings.Contains(d.SourceText, "strings.ToLower") {
		t.Errorf("SourceText missing body: %q", d.SourceText)
	}
}

func TestHoistDecls_FuncInit_NotMangled(t *testing.T) {
	src := `func init() {
	log.Println("hello")
}`
	_, decls, _, err := codegen.HoistDecls(src, codegen.HoistOptions{Prefix: "__page_idx_"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(decls) != 1 {
		t.Fatalf("expected 1 decl, got %d", len(decls))
	}
	if decls[0].MangledName != "init" {
		t.Errorf("MangledName for init = %q, want \"init\"", decls[0].MangledName)
	}
}

func TestHoistDecls_VarReferencingR_Errors(t *testing.T) {
	src := `var Path = r.URL.Path`
	_, _, _, err := codegen.HoistDecls(src, codegen.HoistOptions{Prefix: "__p_", Filename: "fixture.gastro"})
	if err == nil {
		t.Fatal("expected HoistError, got nil")
	}
	var he *codegen.HoistError
	if !errors.As(err, &he) {
		t.Fatalf("expected *HoistError, got %T: %v", err, err)
	}
	if !strings.Contains(he.Detail, "r.URL") {
		t.Errorf("Detail = %q, want to contain r.URL", he.Detail)
	}
	msg := he.Error()
	if !strings.Contains(msg, "Path := r.URL.Path") {
		t.Errorf("error message missing migration hint:\n%s", msg)
	}
}

func TestHoistDecls_ConstReferencingW_Errors(t *testing.T) {
	src := `const X = w`
	_, _, _, err := codegen.HoistDecls(src, codegen.HoistOptions{})
	if err == nil {
		t.Fatal("expected HoistError, got nil")
	}
}

func TestHoistDecls_FuncBodyReferencingR_Errors(t *testing.T) {
	src := `func bad() string {
	return r.URL.Path
}`
	_, _, _, err := codegen.HoistDecls(src, codegen.HoistOptions{Prefix: "__p_"})
	if err == nil {
		t.Fatal("expected HoistError, got nil")
	}
}

func TestHoistDecls_FuncWithLocalRParam_OK(t *testing.T) {
	// Closure parameter shadows the per-request `r`. Must hoist.
	src := `func run(r io.Reader) error {
	return r.Close()
}`
	_, decls, _, err := codegen.HoistDecls(src, codegen.HoistOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(decls) != 1 {
		t.Fatalf("expected 1 decl, got %d", len(decls))
	}
}

func TestHoistDecls_StatementsLeftInBody(t *testing.T) {
	// := assignments and statements stay in the body.
	src := `var Title = "Hello"
Items := db.List()
log.Println("page rendered")`
	body, decls, _, err := codegen.HoistDecls(src, codegen.HoistOptions{Prefix: "__p_"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(decls) != 1 {
		t.Fatalf("expected 1 hoisted decl, got %d", len(decls))
	}
	if !strings.Contains(body, "Items := db.List()") {
		t.Errorf("body missing := statement:\n%s", body)
	}
	if !strings.Contains(body, `log.Println("page rendered")`) {
		t.Errorf("body missing log statement:\n%s", body)
	}
	if strings.Contains(body, "var Title") {
		t.Errorf("body still contains hoisted var:\n%s", body)
	}
}

func TestHoistDecls_GenericFunc(t *testing.T) {
	src := `func mapSlice[T, U any](in []T, fn func(T) U) []U {
	out := make([]U, len(in))
	for i, v := range in {
		out[i] = fn(v)
	}
	return out
}`
	_, decls, _, err := codegen.HoistDecls(src, codegen.HoistOptions{Prefix: "__p_"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(decls) != 1 {
		t.Fatalf("expected 1 decl, got %d", len(decls))
	}
	if decls[0].MangledName != "__p_mapSlice" {
		t.Errorf("MangledName = %q", decls[0].MangledName)
	}
}

func TestHoistDecls_TypeAlias(t *testing.T) {
	src := `type StringList = []string`
	_, decls, _, err := codegen.HoistDecls(src, codegen.HoistOptions{Prefix: "__page_idx_"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(decls) != 1 {
		t.Fatalf("expected 1 decl, got %d", len(decls))
	}
	d := decls[0]
	if d.Kind != codegen.HoistType {
		t.Errorf("Kind = %v, want HoistType", d.Kind)
	}
	if !strings.Contains(d.SourceText, "= []string") {
		t.Errorf("SourceText missing alias: %q", d.SourceText)
	}
}

func TestHoistDecls_LineNumberPreservation(t *testing.T) {
	// After hoisting, the body should have blank lines where the
	// hoisted decls were so that downstream line-number-based
	// diagnostics stay accurate.
	src := `Items := db.List()
var Title = "Hello"
log.Println("hi")`
	body, _, _, err := codegen.HoistDecls(src, codegen.HoistOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := strings.Split(body, "\n")
	if len(got) < 3 {
		t.Fatalf("expected at least 3 lines in body, got %d:\n%s", len(got), body)
	}
	if !strings.Contains(got[0], "Items := db.List()") {
		t.Errorf("line 0 = %q, want Items :=", got[0])
	}
	if strings.TrimSpace(got[1]) != "" {
		t.Errorf("line 1 should be blank, got %q", got[1])
	}
	if !strings.Contains(got[2], "log.Println") {
		t.Errorf("line 2 = %q, want log.Println", got[2])
	}
}
