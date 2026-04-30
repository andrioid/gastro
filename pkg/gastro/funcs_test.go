package gastro_test

import (
	"html/template"
	"testing"
	"time"

	"github.com/andrioid/gastro/pkg/gastro"
)

func TestDefaultFuncs_Upper(t *testing.T) {
	funcs := gastro.DefaultFuncs()
	fn := funcs["upper"].(func(string) string)
	if got := fn("hello"); got != "HELLO" {
		t.Errorf("upper: got %q, want %q", got, "HELLO")
	}
}

func TestDefaultFuncs_Lower(t *testing.T) {
	funcs := gastro.DefaultFuncs()
	fn := funcs["lower"].(func(string) string)
	if got := fn("HELLO"); got != "hello" {
		t.Errorf("lower: got %q, want %q", got, "hello")
	}
}

func TestDefaultFuncs_Join(t *testing.T) {
	funcs := gastro.DefaultFuncs()
	fn := funcs["join"].(func([]string, string) string)
	if got := fn([]string{"a", "b", "c"}, ", "); got != "a, b, c" {
		t.Errorf("join: got %q, want %q", got, "a, b, c")
	}
}

func TestDefaultFuncs_Default(t *testing.T) {
	funcs := gastro.DefaultFuncs()
	fn := funcs["default"].(func(any, any) any)

	if got := fn("fallback", ""); got != "fallback" {
		t.Errorf("default with empty: got %v, want %q", got, "fallback")
	}
	if got := fn("fallback", "value"); got != "value" {
		t.Errorf("default with value: got %v, want %q", got, "value")
	}
}

func TestDefaultFuncs_SafeHTML(t *testing.T) {
	funcs := gastro.DefaultFuncs()
	fn := funcs["safeHTML"].(func(string) template.HTML)
	got := fn("<b>bold</b>")
	if string(got) != "<b>bold</b>" {
		t.Errorf("safeHTML: got %q, want %q", got, "<b>bold</b>")
	}
}

func TestDefaultFuncs_TimeFormat(t *testing.T) {
	funcs := gastro.DefaultFuncs()
	// Signature is (layout, time) so it works with pipes
	fn := funcs["timeFormat"].(func(string, time.Time) string)
	tm := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
	if got := fn("Jan 2, 2006", tm); got != "Mar 15, 2026" {
		t.Errorf("timeFormat: got %q, want %q", got, "Mar 15, 2026")
	}
}

func TestDefaultFuncs_Dict(t *testing.T) {
	funcs := gastro.DefaultFuncs()
	fn := funcs["dict"].(func(...any) map[string]any)
	result := fn("key1", "val1", "key2", 42)

	if result["key1"] != "val1" {
		t.Errorf("dict key1: got %v, want %q", result["key1"], "val1")
	}
	if result["key2"] != 42 {
		t.Errorf("dict key2: got %v, want %d", result["key2"], 42)
	}
}

func TestDefaultFuncs_Has(t *testing.T) {
	funcs := gastro.DefaultFuncs()
	fn := funcs["has"].(func(any, ...any) bool)

	// Variadic form: has needle a b c
	if !fn("open", "open", "in_progress") {
		t.Error("has(open, open, in_progress) should be true")
	}
	if fn("closed", "open", "in_progress") {
		t.Error("has(closed, open, in_progress) should be false")
	}

	// Slice form: has needle slice
	tags := []string{"go", "web", "ssr"}
	if !fn("go", tags) {
		t.Error("has(go, []string{go,web,ssr}) should be true")
	}
	if fn("rust", tags) {
		t.Error("has(rust, []string{go,web,ssr}) should be false")
	}

	// Empty slice
	if fn("go", []string{}) {
		t.Error("has(go, []) should be false")
	}

	// []any (the form templates produce when iterating a frontmatter slice)
	if !fn(42, []any{1, 42, 99}) {
		t.Error("has(42, []any{1,42,99}) should be true")
	}

	// Nil haystack — must not panic
	if fn("x") {
		t.Error("has(x) with no haystack should be false")
	}
}

func TestDefaultFuncs_HasKey(t *testing.T) {
	funcs := gastro.DefaultFuncs()
	fn := funcs["hasKey"].(func(any, any) bool)

	m := map[string]any{"Title": "Hi", "Count": 3}
	if !fn("Title", m) {
		t.Error("hasKey Title should be true")
	}
	if fn("Missing", m) {
		t.Error("hasKey Missing should be false")
	}

	// Other map types with string keys
	typed := map[string]bool{"open": true}
	if !fn("open", typed) {
		t.Error("hasKey open on map[string]bool should be true")
	}

	// Int-keyed map
	nonStr := map[int]string{1: "a"}
	if !fn(1, nonStr) {
		t.Error("hasKey 1 on map[int]string should be true")
	}
	if fn("1", nonStr) {
		t.Error("hasKey with mismatched key type should be false (graceful)")
	}

	// map[any]bool (the type set returns) — the active-set idiom.
	anyKeyed := map[any]bool{"open": true, 42: true}
	if !fn("open", anyKeyed) {
		t.Error("hasKey open on map[any]bool should be true")
	}
	if !fn(42, anyKeyed) {
		t.Error("hasKey 42 on map[any]bool should be true")
	}
	if fn("missing", anyKeyed) {
		t.Error("hasKey missing on map[any]bool should be false")
	}

	// Non-map values
	if fn("x", "not a map") {
		t.Error("hasKey on string should be false (graceful, not panic)")
	}
	if fn("x", nil) {
		t.Error("hasKey on nil should be false")
	}
}

func TestDefaultFuncs_Set(t *testing.T) {
	funcs := gastro.DefaultFuncs()
	setFn := funcs["set"].(func(...any) map[any]bool)
	hasKeyFn := funcs["hasKey"].(func(any, any) bool)

	s := setFn("open", "in_progress")
	if !s["open"] {
		t.Error("set should contain open")
	}
	if !s["in_progress"] {
		t.Error("set should contain in_progress")
	}
	if s["closed"] {
		t.Error("set should not contain closed")
	}

	// Empty
	empty := setFn()
	if len(empty) != 0 {
		t.Errorf("empty set should have no entries, got %d", len(empty))
	}

	// Mixed types are fine as long as they're hashable
	mixed := setFn("a", 1, true)
	if len(mixed) != 3 {
		t.Errorf("mixed set should have 3 entries, got %d", len(mixed))
	}

	// Unhashable values (slice) must not panic; they're skipped.
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("set with unhashable value panicked: %v", r)
		}
	}()
	unhash := setFn("keep", []string{"unhashable"}, "alsoKeep")
	if !unhash["keep"] || !unhash["alsoKeep"] {
		t.Error("set should keep hashable items even when unhashable items are present")
	}

	// set + hasKey is the documented active-set idiom.
	active := setFn("a", "b", "c")
	if !hasKeyFn("a", active) {
		t.Error("hasKey(a, set(a,b,c)) should be true")
	}
	if hasKeyFn("d", active) {
		t.Error("hasKey(d, set(a,b,c)) should be false")
	}
}
