package compiler_test

// Integration tests for GET-only pages and WithHandler (plan v0.4 §4.8,
// Phase 7). Compiled and run in a subprocess against the real generated
// router and http.ServeMux.

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andrioid/gastro/internal/compiler"
)

func TestRouting_GetOnlyPagesAndWithHandler(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping routing integration test in -short mode")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not in PATH")
	}

	projectDir, repoRoot := newBuildableProject(t, "routing")
	writeProjectFiles(t, projectDir, map[string]string{
		"pages/index.gastro":      "<main>home</main>\n",
		"pages/x.gastro":          "<main>x page</main>\n",
		"pages/tasks/[id].gastro": "---\nID := r.PathValue(\"id\")\n---\n<main>task {{ .ID }}</main>\n",
		"pages/tasks/new.gastro":  "<main>new task</main>\n",
		"static/app.css":          "body{}\n",
		"go.mod": "module routing_repro\n\ngo 1.27.2\n\n" +
			"require github.com/andrioid/gastro v0.0.0\n\n" +
			"replace github.com/andrioid/gastro => " + repoRoot + "\n",
		"routing_test.go": routingSubprocessTest,
	})

	if _, err := compiler.Compile(projectDir, filepath.Join(projectDir, ".gastro"), compiler.CompileOptions{}); err != nil {
		t.Fatalf("compile: %v", err)
	}

	out, err := runGo(t, projectDir, "test", "-race", "-count=1", "-v", "-run", "Test", ".")
	if err != nil {
		t.Fatalf("go test -race failed: %v\n%s", err, out)
	}
	for _, sub := range []string{
		"TestPagesAreGetOnly", "TestCommandsCoexistWithPages", "TestWithHandlerOverridesPage",
		"TestWithHandlerOverridesStatic", "TestMiddlewareWrapsPagesAndHandlers",
		"TestDepsReachHandler", "TestConflictPanicsNamingBothSources", "TestDuplicateOverridePanics",
	} {
		if !strings.Contains(out, "--- PASS: "+sub) {
			t.Errorf("subprocess did not pass %s; output:\n%s", sub, out)
		}
	}
}

const routingSubprocessTest = `package routing_repro

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gastro "routing_repro/.gastro"
	gastroRuntime "github.com/andrioid/gastro/pkg/gastro"
)

func do(t *testing.T, h http.Handler, method, path string) (*http.Response, string) {
	t.Helper()
	srv := httptest.NewServer(h)
	defer srv.Close()
	req, err := http.NewRequestWithContext(context.Background(), method, srv.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

func text(s string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, s) })
}

func TestPagesAreGetOnly(t *testing.T) {
	h := gastro.New().Handler()
	resp, _ := do(t, h, http.MethodPost, "/x")
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("POST /x = %d, want 405", resp.StatusCode)
	}
	if allow := resp.Header.Get("Allow"); allow != "GET, HEAD" {
		t.Errorf("Allow = %q, want %q", allow, "GET, HEAD")
	}
	resp, body := do(t, h, http.MethodHead, "/x")
	if resp.StatusCode != http.StatusOK || body != "" {
		t.Errorf("HEAD /x = %d %q, want 200 with no body", resp.StatusCode, body)
	}
	if _, body := do(t, h, http.MethodGet, "/x"); !strings.Contains(body, "x page") {
		t.Errorf("GET /x body = %q", body)
	}
}

func TestCommandsCoexistWithPages(t *testing.T) {
	h := gastro.New(
		gastro.WithHandler("POST /tasks/{id}/place", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, "placed "+r.PathValue("id"))
		})),
		gastro.WithHandler("POST /tasks/{id}", text("updated")),
		gastro.WithHandler("/healthz", text("ok")),
	).Handler()
	cases := []struct{ method, path, want string }{
		{http.MethodGet, "/tasks/7", "task 7"},
		{http.MethodGet, "/tasks/new", "new task"},
		{http.MethodPost, "/tasks/7/place", "placed 7"},
		{http.MethodPost, "/tasks/7", "updated"},
		{http.MethodDelete, "/healthz", "ok"},
	}
	for _, c := range cases {
		if _, body := do(t, h, c.method, c.path); !strings.Contains(body, c.want) {
			t.Errorf("%s %s = %q, want %q", c.method, c.path, body, c.want)
		}
	}
}

func TestWithHandlerOverridesPage(t *testing.T) {
	h := gastro.New(gastro.WithHandler("GET /x", text("x override"))).Handler()
	if _, body := do(t, h, http.MethodGet, "/x"); body != "x override" {
		t.Errorf("GET /x = %q, want override", body)
	}
}

func TestWithHandlerOverridesStatic(t *testing.T) {
	h := gastro.New(gastro.WithHandler("GET /static/", text("static override"))).Handler()
	if _, body := do(t, h, http.MethodGet, "/static/app.css"); body != "static override" {
		t.Errorf("GET /static/app.css = %q, want override", body)
	}
}

func TestMiddlewareWrapsPagesAndHandlers(t *testing.T) {
	mw := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Wrapped", "yes")
			next.ServeHTTP(w, r)
		})
	}
	h := gastro.New(
		gastro.WithHandler("POST /tasks/{id}/place", text("placed")),
		gastro.WithHandler("GET /x", text("x override")),
		gastro.WithMiddleware("/", mw),
	).Handler()
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/"},
		{http.MethodGet, "/tasks/1"},
		{http.MethodPost, "/tasks/1/place"},
		{http.MethodGet, "/x"},
	} {
		if resp, _ := do(t, h, c.method, c.path); resp.Header.Get("X-Wrapped") != "yes" {
			t.Errorf("%s %s not wrapped by middleware at /", c.method, c.path)
		}
	}
}

type appDeps struct{ Name string }

func TestDepsReachHandler(t *testing.T) {
	h := gastro.New(
		gastro.WithDeps(appDeps{Name: "deps-ok"}),
		gastro.WithHandler("POST /cmd", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, gastroRuntime.FromContext[appDeps](r.Context()).Name)
		})),
	).Handler()
	if _, body := do(t, h, http.MethodPost, "/cmd"); body != "deps-ok" {
		t.Errorf("POST /cmd = %q, want deps-ok", body)
	}
}

func panicMessage(t *testing.T, fn func()) (msg string) {
	t.Helper()
	defer func() {
		v := recover()
		if v == nil {
			t.Fatal("expected panic")
		}
		s, ok := v.(string)
		if !ok {
			t.Fatalf("panic value %T: %v", v, v)
		}
		msg = s
	}()
	fn()
	return ""
}

func TestConflictPanicsNamingBothSources(t *testing.T) {
	msg := panicMessage(t, func() {
		gastro.New(gastro.WithHandler("/tasks/new", text("h")))
	})
	for _, want := range []string{` + "`" + `WithHandler("/tasks/new")` + "`" + `, "page pages/tasks/[id].gastro"} {
		if !strings.Contains(msg, want) {
			t.Errorf("panic %q missing %q", msg, want)
		}
	}
}

func TestDuplicateOverridePanics(t *testing.T) {
	msg := panicMessage(t, func() {
		gastro.New(gastro.WithHandler("GET /x", text("a")), gastro.WithHandler("GET /x", text("b")))
	})
	for _, want := range []string{"#1", "#2", "page pages/x.gastro"} {
		if !strings.Contains(msg, want) {
			t.Errorf("panic %q missing %q", msg, want)
		}
	}
}
`
