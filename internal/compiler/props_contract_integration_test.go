package compiler_test

// Integration test for the render-time props contract (plan §4.6/§4.7,
// decision D6): a gastro:"required" prop holding its zero value, and a
// --typecheck-props mismatch, fail the render in dev mode and only log a
// slog warning in production.
//
// Compiled and run in a subprocess so the generated component method,
// the FuncMap closures and the page error path are exercised end-to-end.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andrioid/gastro/internal/compiler"
)

func TestCompile_PropsContractAtRenderTime(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping props-contract integration test in -short mode")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not in PATH")
	}

	repoRoot := findRepoRoot(t)

	projectDir := t.TempDir()
	pagesDir := filepath.Join(projectDir, "pages")
	componentsDir := filepath.Join(projectDir, "components")
	for _, d := range []string{pagesDir, componentsDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	mustWriteFile(t, filepath.Join(componentsDir, "card.gastro"),
		"---\n"+
			"type Props struct {\n"+
			"\tTitle string `gastro:\"required\"`\n"+
			"\tCount int\n"+
			"}\n"+
			"\n"+
			"Title := gastro.Props().Title\n"+
			"Count := gastro.Props().Count\n"+
			"---\n"+
			"<h1>{{ .Title }}</h1><span>{{ .Count }}</span>\n")
	mustWriteFile(t, filepath.Join(componentsDir, "layout.gastro"),
		"<section>{{ .Children }}</section>\n")

	// Present-but-empty required prop: passes generate, fails at render.
	mustWriteFile(t, filepath.Join(pagesDir, "index.gastro"),
		"---\n"+
			"import Card \"components/card.gastro\"\n"+
			"---\n"+
			"<main>{{ Card (dict \"Title\" \"\" \"Count\" 1) }}</main>\n")
	// The same violation inside a wrap slot must reach the page too.
	mustWriteFile(t, filepath.Join(pagesDir, "wrapped.gastro"),
		"---\n"+
			"import (\n"+
			"\tCard \"components/card.gastro\"\n"+
			"\tLayout \"components/layout.gastro\"\n"+
			")\n"+
			"---\n"+
			"{{ wrap Layout }}{{ Card (dict \"Title\" \"\") }}{{ end }}\n")
	// A dynamic value the generate-time literal check cannot see.
	mustWriteFile(t, filepath.Join(pagesDir, "typed.gastro"),
		"---\n"+
			"import Card \"components/card.gastro\"\n"+
			"\n"+
			"Three := \"three\"\n"+
			"---\n"+
			"<main>{{ Card (dict \"Title\" \"ok\" \"Count\" .Three) }}</main>\n")

	gastroOut := filepath.Join(projectDir, ".gastro")
	if _, err := compiler.Compile(projectDir, gastroOut, compiler.CompileOptions{TypecheckProps: true}); err != nil {
		t.Fatalf("compile: %v", err)
	}

	mustWriteFile(t, filepath.Join(projectDir, "go.mod"),
		"module gastro_props_contract_repro\n\n"+
			"go 1.27.2\n\n"+
			"require github.com/andrioid/gastro v0.0.0\n\n"+
			"replace github.com/andrioid/gastro => "+repoRoot+"\n")

	mustWriteFile(t, filepath.Join(projectDir, "props_test.go"), `package props_test

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	gastro "gastro_props_contract_repro/.gastro"
)

// get serves path through a Router in the given mode and returns the
// error handed to the page error handler (nil when the render succeeded)
// and the response body.
func get(t *testing.T, dev bool, path string) (error, string) {
	t.Helper()
	var (
		mu       sync.Mutex
		captured error
	)
	r := gastro.New(
		gastro.WithDevMode(dev),
		gastro.WithErrorHandler(func(w http.ResponseWriter, r *http.Request, err error) {
			mu.Lock()
			captured = err
			mu.Unlock()
			http.Error(w, "render failed", http.StatusInternalServerError)
		}),
	)
	srv := httptest.NewServer(r.Handler())
	defer srv.Close()
	resp, err := http.Get(srv.URL + path)
	if err != nil {
		t.Fatalf("get %s: %v", path, err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	mu.Lock()
	defer mu.Unlock()
	return captured, string(body)
}

func captureSlog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

func TestRequiredEmptyErrorsInDev(t *testing.T) {
	for _, path := range []string{"/", "/wrapped"} {
		err, _ := get(t, true, path)
		if err == nil {
			t.Fatalf("%s: dev mode must fail the render for an empty required prop", path)
		}
		if !strings.Contains(err.Error(), "component Card") || !strings.Contains(err.Error(), "\"Title\"") {
			t.Errorf("%s: error should name the component and field: %v", path, err)
		}
	}
}

func TestRequiredEmptyLogsInProduction(t *testing.T) {
	logs := captureSlog(t)
	err, body := get(t, false, "/")
	if err != nil {
		t.Fatalf("production must render, got error: %v", err)
	}
	if !strings.Contains(body, "<h1></h1><span>1</span>") {
		t.Errorf("production must render with the zero value, got body %q", body)
	}
	out := logs.String()
	if !strings.Contains(out, "level=WARN") || !strings.Contains(out, "Card") || !strings.Contains(out, "Title") {
		t.Errorf("expected a slog warning naming Card and Title, got %q", out)
	}
}

func TestTypeMismatchErrorsInDev(t *testing.T) {
	err, _ := get(t, true, "/typed")
	if err == nil {
		t.Fatal("dev mode must fail the render for a prop type mismatch")
	}
	if !strings.Contains(err.Error(), "prop \"Count\": cannot use string as int") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestTypeMismatchLogsInProduction(t *testing.T) {
	logs := captureSlog(t)
	if err, _ := get(t, false, "/typed"); err != nil {
		t.Fatalf("production must not fail the page, got: %v", err)
	}
	if out := logs.String(); !strings.Contains(out, "level=WARN") || !strings.Contains(out, "Count") {
		t.Errorf("expected a slog warning naming Count, got %q", out)
	}
}
`)

	cmd := exec.Command("go", "test", "-race", "-count=1", "-v", "-run", "Test", "./...")
	cmd.Dir = projectDir
	cmd.Env = append(envWithout(os.Environ(), "GASTRO_DEV", "GASTRO_DEV_ROOT", "GASTRO_PROJECT"), "GOFLAGS=")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go test -race failed: %v\n%s", err, out)
	}
	for _, sub := range []string{
		"TestRequiredEmptyErrorsInDev",
		"TestRequiredEmptyLogsInProduction",
		"TestTypeMismatchErrorsInDev",
		"TestTypeMismatchLogsInProduction",
	} {
		if !strings.Contains(string(out), "--- PASS: "+sub) {
			t.Errorf("subtest %s did not pass:\n%s", sub, out)
		}
	}
}

// envWithout returns env minus the named variables, so the subprocess
// does not inherit a developer's gastro dev-mode settings.
func envWithout(env []string, names ...string) []string {
	out := make([]string, 0, len(env))
outer:
	for _, kv := range env {
		for _, n := range names {
			if strings.HasPrefix(kv, n+"=") {
				continue outer
			}
		}
		out = append(out, kv)
	}
	return out
}
