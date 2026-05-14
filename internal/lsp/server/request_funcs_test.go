package server

// Tests for WithRequestFuncs binder discovery: AST-scan of project
// main.go to extract helper names that the LSP feeds into template
// parse stubs and completion items.

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

// writeMainGo creates a project-root with the supplied main.go contents
// and returns the project root path.
func writeMainGo(t *testing.T, contents string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(contents), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}
	return dir
}

// TestScanRequestFuncs_LiteralFuncMap: the canonical adopter pattern —
// an inline function literal returning a literal template.FuncMap{...}.
// All keys should appear in the discovered helper set.
func TestScanRequestFuncs_LiteralFuncMap(t *testing.T) {
	dir := writeMainGo(t, `package main

import (
	"html/template"
	"net/http"

	gastro "example.com/.gastro"
)

func main() {
	gastro.New(
		gastro.WithRequestFuncs(func(r *http.Request) template.FuncMap {
			return template.FuncMap{
				"t":  func(s string) string { return s },
				"tn": func(a, b string, n int) string { return a },
				"tc": func(ctx, s string) string { return s },
			}
		}),
	)
}
`)
	entry := newRequestFuncsCache().Lookup(dir)
	got := entry.Names()
	want := []string{"t", "tc", "tn"} // sorted by Names()
	if !equalStrings(got, want) {
		t.Errorf("Names() = %v, want %v", got, want)
	}
	if len(entry.nonLiteralBinders) != 0 {
		t.Errorf("expected no non-literal binder sites; got %v", entry.nonLiteralBinders)
	}
}

// TestScanRequestFuncs_NamedBinder: WithRequestFuncs(namedFunc) where
// namedFunc is a top-level function in the same file. Discovery follows
// one hop and extracts keys from the named function's return literal.
func TestScanRequestFuncs_NamedBinder(t *testing.T) {
	dir := writeMainGo(t, `package main

import (
	"html/template"
	"net/http"

	gastro "example.com/.gastro"
)

func myBinder(r *http.Request) template.FuncMap {
	return template.FuncMap{
		"csrfToken": func() string { return "" },
		"csrfField": func() string { return "" },
	}
}

func main() {
	gastro.New(gastro.WithRequestFuncs(myBinder))
}
`)
	entry := newRequestFuncsCache().Lookup(dir)
	got := entry.Names()
	want := []string{"csrfField", "csrfToken"}
	if !equalStrings(got, want) {
		t.Errorf("Names() = %v, want %v", got, want)
	}
}

// TestScanRequestFuncs_NonLiteralBinder: a binder whose FuncMap is built
// dynamically (e.g. by ranging over a slice) can't be analyzed
// statically. We record the call site for a future diagnostic, but
// helpers contributed by it don't appear in completion.
func TestScanRequestFuncs_NonLiteralBinder(t *testing.T) {
	dir := writeMainGo(t, `package main

import (
	"html/template"
	"net/http"

	gastro "example.com/.gastro"
)

func main() {
	gastro.New(
		gastro.WithRequestFuncs(func(r *http.Request) template.FuncMap {
			fm := make(template.FuncMap)
			fm["dynamic"] = func() string { return "x" }
			return fm
		}),
	)
}
`)
	entry := newRequestFuncsCache().Lookup(dir)
	if len(entry.Names()) != 0 {
		t.Errorf("expected no statically-discovered names; got %v", entry.Names())
	}
	if len(entry.nonLiteralBinders) != 1 {
		t.Fatalf("expected 1 non-literal binder site; got %d", len(entry.nonLiteralBinders))
	}
	if entry.nonLiteralBinders[0].File == "" || entry.nonLiteralBinders[0].Line == 0 {
		t.Errorf("non-literal site should have file+line: %+v", entry.nonLiteralBinders[0])
	}
}

// TestScanRequestFuncs_MultipleBinders: two binders compose. Names from
// both contribute, each with its own BinderID.
func TestScanRequestFuncs_MultipleBinders(t *testing.T) {
	dir := writeMainGo(t, `package main

import (
	"html/template"
	"net/http"

	gastro "example.com/.gastro"
)

func main() {
	gastro.New(
		gastro.WithRequestFuncs(func(r *http.Request) template.FuncMap {
			return template.FuncMap{"t": func() string { return "" }}
		}),
		gastro.WithRequestFuncs(func(r *http.Request) template.FuncMap {
			return template.FuncMap{"csrfToken": func() string { return "" }}
		}),
	)
}
`)
	entry := newRequestFuncsCache().Lookup(dir)
	got := entry.Names()
	want := []string{"csrfToken", "t"}
	if !equalStrings(got, want) {
		t.Errorf("Names() = %v, want %v", got, want)
	}
	tInfo, ok := entry.HelperAt("t")
	if !ok || tInfo.BinderID != 0 {
		t.Errorf("t should be BinderID 0, got %+v ok=%v", tInfo, ok)
	}
	csrfInfo, ok := entry.HelperAt("csrfToken")
	if !ok || csrfInfo.BinderID != 1 {
		t.Errorf("csrfToken should be BinderID 1, got %+v ok=%v", csrfInfo, ok)
	}
}

// TestScanRequestFuncs_NoMain: missing main.go returns an empty entry.
// This is the common case for projects that don't use WithRequestFuncs;
// the LSP should silently treat them as having no request-aware helpers.
func TestScanRequestFuncs_NoMain(t *testing.T) {
	dir := t.TempDir()
	entry := newRequestFuncsCache().Lookup(dir)
	if len(entry.Names()) != 0 {
		t.Errorf("expected empty names for missing main.go; got %v", entry.Names())
	}
	if len(entry.nonLiteralBinders) != 0 {
		t.Errorf("expected no sites for missing main.go; got %v", entry.nonLiteralBinders)
	}
}

// TestScanRequestFuncs_CacheInvalidation: after a Lookup, modifying
// main.go and calling Lookup again re-scans the file.
func TestScanRequestFuncs_CacheInvalidation(t *testing.T) {
	dir := writeMainGo(t, `package main

import (
	"html/template"
	"net/http"

	gastro "example.com/.gastro"
)

func main() {
	gastro.New(
		gastro.WithRequestFuncs(func(r *http.Request) template.FuncMap {
			return template.FuncMap{"t": func() string { return "" }}
		}),
	)
}
`)
	cache := newRequestFuncsCache()
	if got := cache.Lookup(dir).Names(); !equalStrings(got, []string{"t"}) {
		t.Fatalf("first lookup: got %v", got)
	}

	// Rewrite main.go with a different key. We force the modtime to be
	// distinguishable by sleeping a hair, or by explicitly chtimes-ing.
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(`package main

import (
	"html/template"
	"net/http"

	gastro "example.com/.gastro"
)

func main() {
	gastro.New(
		gastro.WithRequestFuncs(func(r *http.Request) template.FuncMap {
			return template.FuncMap{"renamed": func() string { return "" }}
		}),
	)
}
`), 0o644); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	// Bump modtime explicitly so cache invalidation is deterministic on
	// filesystems with second-level granularity.
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(filepath.Join(dir, "main.go"), future, future); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	if got := cache.Lookup(dir).Names(); !equalStrings(got, []string{"renamed"}) {
		t.Errorf("after rewrite: got %v, want [renamed]", got)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	x := append([]string(nil), a...)
	y := append([]string(nil), b...)
	sort.Strings(x)
	sort.Strings(y)
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}


