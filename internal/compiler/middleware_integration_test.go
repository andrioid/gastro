package compiler_test

// Integration test for Wave 4 / C2: WithMiddleware composes route
// middleware. The behaviours we want to lock in:
//
//   - WithMiddleware("/exact", fn) wraps the auto-route at /exact.
//   - WithMiddleware("/admin/{path...}", fn) wraps every /admin/x.
//   - Multiple WithMiddleware calls compose in registration order
//     (first registered = outermost).
//   - WithMiddleware + WithOverride targeting the same route =
//     "middleware wraps override" (Q3, plans/frictions-plan.md §7).
//   - Unknown patterns panic at New() time with a descriptive error.
//
// Compiled in a subprocess so the generated code is exercised end-to-end
// against the real stdlib http.ServeMux pattern matcher.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andrioid/gastro/internal/compiler"
)

func TestCompile_WithMiddlewareComposition(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping middleware integration test in -short mode")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not in PATH")
	}

	repoRoot := findRepoRoot(t)

	projectDir := t.TempDir()
	pagesDir := filepath.Join(projectDir, "pages", "admin")
	if err := os.MkdirAll(pagesDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Three pages: /, /admin/users, /admin/settings. The wildcard
	// pattern /admin/{path...} should match the latter two only.
	mustWriteFile(t, filepath.Join(projectDir, "pages", "index.gastro"),
		"---\nTitle := \"Home\"\n---\n<h1>{{ .Title }}</h1>\n")
	mustWriteFile(t, filepath.Join(pagesDir, "users.gastro"),
		"---\nTitle := \"Users\"\n---\n<h1>{{ .Title }}</h1>\n")
	mustWriteFile(t, filepath.Join(pagesDir, "settings.gastro"),
		"---\nTitle := \"Settings\"\n---\n<h1>{{ .Title }}</h1>\n")

	gastroOut := filepath.Join(projectDir, ".gastro")
	if _, err := compiler.Compile(projectDir, gastroOut, compiler.CompileOptions{}); err != nil {
		t.Fatalf("compile: %v", err)
	}

	mustWriteFile(t, filepath.Join(projectDir, "go.mod"),
		"module gastro_middleware_repro\n\n"+
			"go 1.26.1\n\n"+
			"require github.com/andrioid/gastro v0.0.0\n\n"+
			"replace github.com/andrioid/gastro => "+repoRoot+"\n")

	mustWriteFile(t, filepath.Join(projectDir, "middleware_test.go"), `package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	gastro "gastro_middleware_repro/.gastro"
)

// MiddlewareFunc shape matches gastroRuntime.MiddlewareFunc; using the
// raw function type avoids forcing test code to import the runtime
// package alongside the generated one. Go converts the literal at the
// WithMiddleware call site.
type mwFunc = func(http.Handler) http.Handler

// counter returns a middleware that bumps a shared counter on every
// request. Useful for asserting "this middleware ran for that route".
func counter(c *atomic.Int32) mwFunc {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			c.Add(1)
			next.ServeHTTP(w, r)
		})
	}
}

// header returns a middleware that records its registration order in
// a response header. Lets the test assert composition order.
func header(name, value string) mwFunc {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Add(name, value)
			next.ServeHTTP(w, r)
		})
	}
}

// TestExactPatternWrapsOnlyMatchingRoute: a middleware registered for
// "/admin/users" wraps that route and not any other.
func TestExactPatternWrapsOnlyMatchingRoute(t *testing.T) {
	var c atomic.Int32
	r := gastro.New(gastro.WithMiddleware("/admin/users", counter(&c)))
	srv := httptest.NewServer(r.Handler())
	defer srv.Close()

	for _, path := range []string{"/admin/users", "/admin/settings", "/"} {
		c.Store(0)
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatalf("get %s: %v", path, err)
		}
		resp.Body.Close()

		got := c.Load()
		want := int32(0)
		if path == "/admin/users" {
			want = 1
		}
		if got != want {
			t.Errorf("path=%s: middleware ran %d times, want %d", path, got, want)
		}
	}
}

// TestWildcardPatternWrapsSubtree: /admin/{path...} should wrap every
// route under /admin and nothing else.
func TestWildcardPatternWrapsSubtree(t *testing.T) {
	var c atomic.Int32
	r := gastro.New(gastro.WithMiddleware("/admin/{path...}", counter(&c)))
	srv := httptest.NewServer(r.Handler())
	defer srv.Close()

	for _, tc := range []struct {
		path string
		want int32
	}{
		{"/admin/users", 1},
		{"/admin/settings", 1},
		{"/", 0},
	} {
		c.Store(0)
		resp, err := http.Get(srv.URL + tc.path)
		if err != nil {
			t.Fatalf("get %s: %v", tc.path, err)
		}
		resp.Body.Close()
		if got := c.Load(); got != tc.want {
			t.Errorf("path=%s: middleware ran %d times, want %d", tc.path, got, tc.want)
		}
	}
}

// TestMultipleMiddlewareComposeInRegistrationOrder: two WithMiddleware
// calls for overlapping patterns wrap the handler such that the first
// registered runs outermost (added its header first as the request
// flowed down the chain). The header value list reflects the order
// each middleware ran in.
func TestMultipleMiddlewareComposeInRegistrationOrder(t *testing.T) {
	r := gastro.New(
		gastro.WithMiddleware("/admin/{path...}", header("X-MW", "outer")),
		gastro.WithMiddleware("/admin/users", header("X-MW", "inner")),
	)
	srv := httptest.NewServer(r.Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/admin/users")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	resp.Body.Close()

	values := resp.Header.Values("X-MW")
	if len(values) != 2 {
		t.Fatalf("expected 2 X-MW values from two middlewares, got %v", values)
	}
	// First registered runs first: "outer" header is added before "inner".
	if values[0] != "outer" || values[1] != "inner" {
		t.Errorf("composition order wrong: got %v, want [outer inner]", values)
	}
}

// TestMiddlewareWrapsOverride (Q3): WithMiddleware + WithOverride on
// the same route should result in middleware(override) — the override
// replaces the page handler, then middleware wraps that replacement.
// The override sets a body marker; the middleware sets a header.
// Both must be observable.
func TestMiddlewareWrapsOverride(t *testing.T) {
	override := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("OVERRIDDEN"))
	})
	r := gastro.New(
		gastro.WithOverride("/admin/users", override),
		gastro.WithMiddleware("/admin/users", header("X-Wrapped", "yes")),
	)
	srv := httptest.NewServer(r.Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/admin/users")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()

	if got := resp.Header.Get("X-Wrapped"); got != "yes" {
		t.Errorf("middleware did not wrap override; X-Wrapped header = %q", got)
	}
	body := make([]byte, 64)
	n, _ := resp.Body.Read(body)
	if !strings.Contains(string(body[:n]), "OVERRIDDEN") {
		t.Errorf("override did not run; body = %q", string(body[:n]))
	}
}

// TestUnknownPatternPanicsAtNew: a WithMiddleware pattern that matches
// no auto-route panics at New() with a descriptive message.
func TestUnknownPatternPanicsAtNew(t *testing.T) {
	defer func() {
		v := recover()
		if v == nil {
			t.Fatal("expected panic for unknown middleware pattern")
		}
		msg, ok := v.(string)
		if !ok {
			t.Fatalf("panic value is not a string: %T", v)
		}
		if !strings.Contains(msg, "WithMiddleware") {
			t.Errorf("panic should name the option; got %q", msg)
		}
		if !strings.Contains(msg, "/typo") {
			t.Errorf("panic should name the bad pattern; got %q", msg)
		}
	}()

	gastro.New(gastro.WithMiddleware("/typo/never/matches", header("X-MW", "x")))
}
`)

	cmd := exec.Command("go", "test", "-race", "-count=1", "-run", "Test", "./...")
	cmd.Dir = projectDir
	cmd.Env = append(os.Environ(), "GOFLAGS=") // strip outer -race etc.
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go test -race failed: %v\n%s", err, out)
	}
	if strings.Contains(string(out), "FAIL") {
		t.Fatalf("subprocess test failed:\n%s", out)
	}
}
