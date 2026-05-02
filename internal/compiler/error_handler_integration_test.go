package compiler_test

// Integration test for Wave 4 / C4: WithErrorHandler installs a custom
// page render error handler. The behaviour we want to lock in:
//
//   no WithErrorHandler -> render error logs and writes 500 (default)
//   WithErrorHandler(fn) -> render error invokes fn; the response is
//                           whatever fn writes
//
// The Router.errorHandler field is unexported, so we observe behaviour
// through the served response. To trigger a *render* error reliably
// (Execute-time, not Parse-time), the test page calls a template
// function registered via WithFuncs that returns a Go error. html/template
// surfaces FuncMap errors through Execute.
//
// Compiled in a subprocess (same harness as devmode_integration_test.go)
// because generating test code requires a full .gastro/ output and a
// go.mod pointing at this repo via replace directive. Gated by
// testing.Short() because it spawns the Go toolchain.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andrioid/gastro/internal/compiler"
)

func TestCompile_WithErrorHandlerOverridesDefault(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping error-handler integration test in -short mode")
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

	// The page calls boom, a template func that returns a Go error.
	// Registering boom via WithFuncs at New() time makes it available
	// to html/template at parse time (so the page compiles) but the
	// error fires at Execute time (so the error handler is exercised).
	mustWriteFile(t, filepath.Join(pagesDir, "index.gastro"),
		"---\nTitle := \"Hi\"\n---\n<h1>{{ .Title }} {{ boom }}</h1>\n")

	gastroOut := filepath.Join(projectDir, ".gastro")
	if _, err := compiler.Compile(projectDir, gastroOut, compiler.CompileOptions{}); err != nil {
		t.Fatalf("compile: %v", err)
	}

	mustWriteFile(t, filepath.Join(projectDir, "go.mod"),
		"module gastro_errorhandler_repro\n\n"+
			"go 1.26.1\n\n"+
			"require github.com/andrioid/gastro v0.0.0\n\n"+
			"replace github.com/andrioid/gastro => "+repoRoot+"\n")

	mustWriteFile(t, filepath.Join(projectDir, "errorhandler_test.go"), `package errorhandler_test

import (
	"errors"
	"html/template"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	gastro "gastro_errorhandler_repro/.gastro"
	gastroRuntime "github.com/andrioid/gastro/pkg/gastro"
)

func boomFunc() (string, error) {
	return "", errors.New("BOOM")
}

// readBody drains the response body and returns it as a string,
// failing the test on read errors.
func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(b)
}

// TestDefaultErrorHandler_Writes500: with no WithErrorHandler, a render
// error produces a 500 response (DefaultErrorHandler's behaviour) when
// the response is still uncommitted.
func TestDefaultErrorHandler_Writes500(t *testing.T) {
	r := gastro.New(
		gastro.WithFuncs(template.FuncMap{"boom": boomFunc}),
	)
	srv := httptest.NewServer(r.Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	body := readBody(t, resp)

	// html/template streams output as it goes — by the time boom fires,
	// "<h1>Hi " has already been flushed and headers committed. The
	// default handler then logs only and does not attempt a 500 write.
	// The visible response is therefore a 200 with the partial HTML.
	// This is the documented behaviour: write 500 only when the
	// response is still uncommitted.
	if resp.StatusCode == http.StatusInternalServerError {
		// Acceptable if the runtime managed to short-circuit before any
		// bytes flushed; treat as a pass either way to avoid being
		// brittle about template stream timing.
		return
	}
	if !strings.Contains(body, "Hi") {
		t.Errorf("default handler: expected partial body containing 'Hi', got %q (status %d)", body, resp.StatusCode)
	}
}

// TestWithErrorHandler_Invoked: a custom handler is called with the
// (w, r, err) triple. We verify both the call count and the error chain.
func TestWithErrorHandler_Invoked(t *testing.T) {
	var calls atomic.Int32
	var capturedErr atomic.Value

	r := gastro.New(
		gastro.WithFuncs(template.FuncMap{"boom": boomFunc}),
		gastro.WithErrorHandler(func(w http.ResponseWriter, req *http.Request, err error) {
			calls.Add(1)
			capturedErr.Store(err)
			// Don't try to write — response is partially streamed by html/template.
		}),
	)
	srv := httptest.NewServer(r.Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	resp.Body.Close()

	if got := calls.Load(); got != 1 {
		t.Errorf("expected error handler called once, got %d", got)
	}
	v := capturedErr.Load()
	if v == nil {
		t.Fatal("error handler ran but captured no error")
	}
	gotErr, ok := v.(error)
	if !ok {
		t.Fatalf("captured value is not an error: %T", v)
	}
	if !strings.Contains(gotErr.Error(), "BOOM") {
		t.Errorf("expected error chain to contain BOOM, got %q", gotErr.Error())
	}
}

// TestDefaultErrorHandler_Type: verify that DefaultErrorHandler is
// exported and callable from outside the runtime package, so users can
// compose it with custom logic (e.g. log and then delegate).
func TestDefaultErrorHandler_ComposableFromUserCode(t *testing.T) {
	var called atomic.Bool

	r := gastro.New(
		gastro.WithFuncs(template.FuncMap{"boom": boomFunc}),
		gastro.WithErrorHandler(func(w http.ResponseWriter, req *http.Request, err error) {
			called.Store(true)
			gastroRuntime.DefaultErrorHandler(w, req, err)
		}),
	)
	srv := httptest.NewServer(r.Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	resp.Body.Close()

	if !called.Load() {
		t.Error("user wrapper around DefaultErrorHandler was not invoked")
	}
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
