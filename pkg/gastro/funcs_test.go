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
