package compiler_test

// Integration tests for WithRequestFuncs: request-aware template helpers.
// See tmp/withrequestfuncs-plan.md §4 Phase 1 for the design.
//
// Behaviours locked in here:
//   - Probe at New() detects helper-name collisions across built-ins,
//     WithFuncs, and binders (panics with both sources named).
//   - Binders run once per request; helpers see request-specific state
//     captured via closures over r / r.Context().
//   - Concurrent requests get isolated FuncMaps (no leakage; -race clean).
//   - A binder that panics at request time is recovered, logged, and
//     dispatched through the error handler (500). One bad binder cannot
//     crash the server.
//   - When no binders are registered, the page handler still emits a
//     normal response (zero-cost-when-unused: same path as before).
//   - Nil binder is rejected at option construction time.
//
// Compiled in a subprocess so the generated code is exercised end-to-end
// against the real stdlib http.ServeMux. Same shape as the existing
// middleware / error-handler integration tests.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andrioid/gastro/internal/compiler"
)

func TestCompile_WithRequestFuncs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping WithRequestFuncs integration test in -short mode")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not in PATH")
	}

	repoRoot := findRepoRoot(t)

	projectDir := t.TempDir()
	pagesDir := filepath.Join(projectDir, "pages")
	if err := os.MkdirAll(pagesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	componentsDir := filepath.Join(projectDir, "components")
	if err := os.MkdirAll(componentsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// A single page that exercises a request-aware helper. The helper
	// returns a value derived from r — specifically the Accept-Language
	// header — so the test can prove the binder sees per-request state.
	mustWriteFile(t, filepath.Join(pagesDir, "index.gastro"),
		"<p>locale={{ locale }}</p>\n")
	// A panicking-helper page exercises the runtime recover path.
	mustWriteFile(t, filepath.Join(pagesDir, "boom.gastro"),
		"<p>{{ boom }}</p>\n")
	// A component used by Render.With(r).Greeting(...) tests. It reads a
	// request-aware helper inside its template body so the test can prove
	// nested binders work via the Render.With path.
	mustWriteFile(t, filepath.Join(componentsDir, "greeting.gastro"),
		"<span>{{ locale }}-greeting</span>\n")
	// A no-Props leaf component that reads a binder helper inside its
	// body. Used by the bare-call nested-propagation test.
	mustWriteFile(t, filepath.Join(componentsDir, "nest.gastro"),
		"<span>nest-locale={{ locale }}</span>\n")
	// A wrap-capable component (Children is auto-provided by codegen for
	// every component; no Props declaration needed). The slot's body is
	// authored by the page, and we assert the slot inherits the page's
	// per-request FuncMap so the binder helper resolves inside it.
	mustWriteFile(t, filepath.Join(componentsDir, "wrapper.gastro"),
		"<div class=\"wrap\">{{ .Children }}</div>\n")
	// Page that invokes a bare child component using a binder helper.
	// Without nested propagation, {{ locale }} inside Nest would render
	// empty (the parse-time placeholder); with propagation it resolves
	// against the page's request.
	mustWriteFile(t, filepath.Join(pagesDir, "nested.gastro"),
		"---\n"+
			"import Nest \"components/nest.gastro\"\n"+
			"---\n"+
			"<main>page-locale={{ locale }} {{ Nest (dict) }}</main>\n")
	// Page that wraps a slot body inside a component. The slot body uses
	// the binder helper, exercising the wrap-block propagation path.
	mustWriteFile(t, filepath.Join(pagesDir, "wrapnest.gastro"),
		"---\n"+
			"import Wrapper \"components/wrapper.gastro\"\n"+
			"---\n"+
			"{{ wrap Wrapper (dict) }}<em>slot-locale={{ locale }}</em>{{ end }}\n")

	gastroOut := filepath.Join(projectDir, ".gastro")
	if _, err := compiler.Compile(projectDir, gastroOut, compiler.CompileOptions{}); err != nil {
		t.Fatalf("compile: %v", err)
	}

	mustWriteFile(t, filepath.Join(projectDir, "go.mod"),
		"module gastro_reqfuncs_repro\n\n"+
			"go 1.26.1\n\n"+
			"require github.com/andrioid/gastro v0.0.0\n\n"+
			"replace github.com/andrioid/gastro => "+repoRoot+"\n")

	mustWriteFile(t, filepath.Join(projectDir, "reqfuncs_test.go"), `package reqfuncs_test

import (
	"html/template"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	gastro "gastro_reqfuncs_repro/.gastro"
)

// makeBinder returns a request-aware FuncMap exposing both helpers
// referenced by the test pages (locale and boom). Both helpers must be
// declared by every binder used in a rendering test because the Router
// parses all templates at New() and rejects unknown helper names.
//
// boomPanic controls whether the boom helper panics at request time
// (for the recover-and-500 test) or returns a benign string.
func makeBinder(boomPanic bool) func(*http.Request) template.FuncMap {
	return func(r *http.Request) template.FuncMap {
		return template.FuncMap{
			"locale": func() string {
				al := r.Header.Get("Accept-Language")
				if al == "" {
					return "en"
				}
				return al
			},
			"boom": func() string {
				if boomPanic && strings.Contains(r.URL.Path, "boom") {
					panic("simulated helper failure")
				}
				return "ok"
			},
		}
	}
}

// TestBinderRunsPerRequest: two requests with different Accept-Language
// headers produce different rendered output. Confirms the binder is
// invoked per request and its closure sees the correct r.
func TestBinderRunsPerRequest(t *testing.T) {
	r := gastro.New(gastro.WithRequestFuncs(makeBinder(false)))
	srv := httptest.NewServer(r.Handler())
	defer srv.Close()

	for _, tc := range []struct{ accept, want string }{
		{"en", "locale=en"},
		{"de", "locale=de"},
		{"", "locale=en"},
	} {
		req, _ := http.NewRequest("GET", srv.URL+"/", nil)
		if tc.accept != "" {
			req.Header.Set("Accept-Language", tc.accept)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if !strings.Contains(string(body), tc.want) {
			t.Errorf("accept=%q: body=%q, want substring %q", tc.accept, body, tc.want)
		}
	}
}

// TestConcurrentRequestsDoNotLeak: many goroutines fire requests with
// distinct headers; every response must echo back its own header. If
// the per-request FuncMap leaked across requests (e.g. shared mutable
// state), we'd see crossover.
//
// Run under -race in the parent test so any data race surfaces.
func TestConcurrentRequestsDoNotLeak(t *testing.T) {
	r := gastro.New(gastro.WithRequestFuncs(makeBinder(false)))
	srv := httptest.NewServer(r.Handler())
	defer srv.Close()

	const N = 50
	var wg sync.WaitGroup
	wg.Add(N)
	errs := make(chan string, N)
	for i := 0; i < N; i++ {
		go func(i int) {
			defer wg.Done()
			locale := "loc-" + strings.Repeat("x", i%5+1)
			req, _ := http.NewRequest("GET", srv.URL+"/", nil)
			req.Header.Set("Accept-Language", locale)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				errs <- err.Error()
				return
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			want := "locale=" + locale
			if !strings.Contains(string(body), want) {
				errs <- "body=" + string(body) + " want=" + want
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
}

// TestHelperPanicRecovered: a helper that panics at request time returns
// a 500 (the default error handler's behaviour) rather than crashing the
// server. The panic is recovered inside __gastro_renderPage and the next
// request to a non-panicking page must still succeed.
func TestHelperPanicRecovered(t *testing.T) {
	r := gastro.New(gastro.WithRequestFuncs(makeBinder(true)))
	srv := httptest.NewServer(r.Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/boom")
	if err != nil {
		t.Fatalf("get /boom: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", resp.StatusCode)
	}

	// Server still alive: a non-panicking page renders.
	resp2, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("get /: %v", err)
	}
	body, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("expected 200 on /, got %d body=%q", resp2.StatusCode, body)
	}
	_ = atomic.Int32{} // keep sync/atomic in imports for other tests
}

// TestCollisionWithBuiltIn: a binder returning a helper named after a
// Gastro built-in (e.g. "upper") panics at New() with both sources named.
func TestCollisionWithBuiltIn(t *testing.T) {
	defer func() {
		v := recover()
		if v == nil {
			t.Fatal("expected panic for built-in collision")
		}
		msg, ok := v.(string)
		if !ok {
			t.Fatalf("panic value is not a string: %T", v)
		}
		if !strings.Contains(msg, "upper") {
			t.Errorf("panic should name colliding helper; got %q", msg)
		}
		if !strings.Contains(msg, "built-in") {
			t.Errorf("panic should name built-in source; got %q", msg)
		}
		if !strings.Contains(msg, "WithRequestFuncs") {
			t.Errorf("panic should name binder source; got %q", msg)
		}
	}()

	gastro.New(gastro.WithRequestFuncs(func(*http.Request) template.FuncMap {
		return template.FuncMap{
			"upper": func() string { return "x" },
		}
	}))
}

// TestCollisionBetweenBinders: two binders returning the same helper
// name panic at New() with both binder indices named.
func TestCollisionBetweenBinders(t *testing.T) {
	defer func() {
		v := recover()
		if v == nil {
			t.Fatal("expected panic for binder-vs-binder collision")
		}
		msg, ok := v.(string)
		if !ok {
			t.Fatalf("panic value is not a string: %T", v)
		}
		if !strings.Contains(msg, "WithRequestFuncs[0]") || !strings.Contains(msg, "WithRequestFuncs[1]") {
			t.Errorf("panic should name both binder indices; got %q", msg)
		}
	}()

	gastro.New(
		gastro.WithRequestFuncs(func(*http.Request) template.FuncMap {
			return template.FuncMap{"t": func() string { return "a" }}
		}),
		gastro.WithRequestFuncs(func(*http.Request) template.FuncMap {
			return template.FuncMap{"t": func() string { return "b" }}
		}),
	)
}

// TestCollisionWithUserFuncs: a binder returning a helper already
// registered via WithFuncs panics at New() with both sources named.
func TestCollisionWithUserFuncs(t *testing.T) {
	defer func() {
		v := recover()
		if v == nil {
			t.Fatal("expected panic for WithFuncs collision")
		}
		msg, ok := v.(string)
		if !ok {
			t.Fatalf("panic value is not a string: %T", v)
		}
		if !strings.Contains(msg, "myhelper") {
			t.Errorf("panic should name colliding helper; got %q", msg)
		}
		if !strings.Contains(msg, "WithFuncs") {
			t.Errorf("panic should name WithFuncs source; got %q", msg)
		}
	}()

	gastro.New(
		gastro.WithFuncs(template.FuncMap{
			"myhelper": func() string { return "user" },
		}),
		gastro.WithRequestFuncs(func(*http.Request) template.FuncMap {
			return template.FuncMap{"myhelper": func() string { return "binder" }}
		}),
	)
}

// TestCollisionWithUserFuncsOverridingBuiltin: if WithFuncs overrides a
// built-in helper (an existing documented WithFuncs feature) and a binder
// then collides on that same name, the panic message should attribute
// the collision to "WithFuncs" (the source the adopter actually
// registered), not to "built-in". Without this, an adopter who
// override-and-collides on e.g. "upper" would see a misleading message
// pointing at a source they never directly touched.
func TestCollisionWithUserFuncsOverridingBuiltin(t *testing.T) {
	defer func() {
		v := recover()
		if v == nil {
			t.Fatal("expected panic for WithFuncs-overrides-builtin + binder collision")
		}
		msg, ok := v.(string)
		if !ok {
			t.Fatalf("panic value is not a string: %T", v)
		}
		if !strings.Contains(msg, "upper") {
			t.Errorf("panic should name colliding helper; got %q", msg)
		}
		if !strings.Contains(msg, "WithFuncs") {
			t.Errorf("panic should attribute to WithFuncs (the actual override source); got %q", msg)
		}
		if strings.Contains(msg, "built-in") {
			t.Errorf("panic should not say built-in when WithFuncs overrode the built-in; got %q", msg)
		}
	}()

	gastro.New(
		gastro.WithFuncs(template.FuncMap{
			"upper": func(s string) string { return s }, // override built-in
		}),
		gastro.WithRequestFuncs(func(*http.Request) template.FuncMap {
			return template.FuncMap{"upper": func() string { return "x" }}
		}),
	)
}

// TestNilBinderRejected: WithRequestFuncs(nil) panics with a descriptive
// message at option construction.
func TestNilBinderRejected(t *testing.T) {
	defer func() {
		v := recover()
		if v == nil {
			t.Fatal("expected panic for nil binder")
		}
		msg, _ := v.(string)
		if !strings.Contains(msg, "nil binder") {
			t.Errorf("panic should mention nil binder; got %v", v)
		}
	}()
	_ = gastro.WithRequestFuncs(nil)
}

// TestRenderWithBindsRequest: Render.With(r).Component(props) routes the
// component render through the request-aware FuncMap path, so binder
// helpers resolve against r. Without With(r), the static path is taken
// and binder funcs are absent — in that case the helper renders empty
// (the placeholder stub returns nil).
func TestRenderWithBindsRequest(t *testing.T) {
	router := gastro.New(gastro.WithRequestFuncs(makeBinder(false)))

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Accept-Language", "fr")

	html, err := router.Render().With(req).Greeting()
	if err != nil {
		t.Fatalf("Render.With.Greeting: %v", err)
	}
	if !strings.Contains(html, "fr-greeting") {
		t.Errorf("expected request-aware locale to flow into component; got %q", html)
	}

	// The package-level Render takes the static path. The placeholder
	// stub returns nil so {{ locale }} renders as the empty string.
	htmlStatic, err := router.Render().Greeting()
	if err != nil {
		t.Fatalf("Render.Greeting: %v", err)
	}
	if strings.Contains(htmlStatic, "fr") {
		t.Errorf("static path should not see request state; got %q", htmlStatic)
	}
}

// TestNestedComponentInheritsBinder: a page invokes a child component
// via a bare {{ Nest (dict) }} call. The child's template body uses a
// binder helper. With nested propagation, the helper resolves against
// the page's request; without it, the child would render the helper as
// empty (the parse-time placeholder).
func TestNestedComponentInheritsBinder(t *testing.T) {
	r := gastro.New(gastro.WithRequestFuncs(makeBinder(false)))
	srv := httptest.NewServer(r.Handler())
	defer srv.Close()

	req, _ := http.NewRequest("GET", srv.URL+"/nested", nil)
	req.Header.Set("Accept-Language", "fr")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get /nested: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	// The page itself sees fr (sanity check for the page-level path)...
	if !strings.Contains(string(body), "page-locale=fr") {
		t.Errorf("page-level helper did not resolve; body=%q", body)
	}
	// ...and the bare-call child component inherits it.
	if !strings.Contains(string(body), "nest-locale=fr") {
		t.Errorf("nested component did not inherit request-aware helper; body=%q", body)
	}
}

// TestWrapSlotInheritsBinder: a page uses a {{ wrap Wrapper (dict) }}
// block whose slot body references a binder helper. The slot must be
// rendered with the page's per-request FuncMap so the helper resolves.
// This exercises the post-tmpl-selection override of
// __gastro_render_children inside __gastro_executeForRequest.
func TestWrapSlotInheritsBinder(t *testing.T) {
	r := gastro.New(gastro.WithRequestFuncs(makeBinder(false)))
	srv := httptest.NewServer(r.Handler())
	defer srv.Close()

	req, _ := http.NewRequest("GET", srv.URL+"/wrapnest", nil)
	req.Header.Set("Accept-Language", "fr")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get /wrapnest: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "slot-locale=fr") {
		t.Errorf("wrap-slot did not inherit request-aware helper; body=%q", body)
	}
}

// TestRenderWithNilRequestIsHarmless: calling With(nil) collapses to the
// static path. Documented as a guard so handler code can pass r without
// branching.
func TestRenderWithNilRequestIsHarmless(t *testing.T) {
	router := gastro.New(gastro.WithRequestFuncs(makeBinder(false)))
	if _, err := router.Render().With(nil).Greeting(); err != nil {
		t.Fatalf("Render.With(nil).Greeting: %v", err)
	}
}

// TestNoBindersIsZeroCostPath: when binders are registered, even pages
// that never invoke a request-aware helper render normally. This pins
// the contract that the request-aware path is non-destructive for
// templates that don't use binder funcs. (A truly binder-less Router
// can't render these test pages because they reference helpers that
// aren't in scope without WithRequestFuncs; that scenario is covered by
// every other Gastro test in the suite.)
func TestNoBindersIsZeroCostPath(t *testing.T) {
	r := gastro.New(gastro.WithRequestFuncs(makeBinder(false)))
	srv := httptest.NewServer(r.Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}
`)

	cmd := exec.Command("go", "test", "-race", "-count=1", "-v", "-run", "Test", "./...")
	cmd.Dir = projectDir
	cmd.Env = append(os.Environ(), "GOFLAGS=")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go test -race failed: %v\n%s", err, out)
	}
	if strings.Contains(string(out), "FAIL") {
		t.Fatalf("subprocess test failed:\n%s", out)
	}
	// Sanity check: the new override-built-in test must actually run in the
	// subprocess (catches accidental omission of the test from the embedded
	// source). Output is captured -v so RUN lines are present.
	if !strings.Contains(string(out), "TestCollisionWithUserFuncsOverridingBuiltin") {
		t.Errorf("subprocess did not run TestCollisionWithUserFuncsOverridingBuiltin; output:\n%s", out)
	}
}
