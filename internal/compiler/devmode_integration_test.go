package compiler_test

// Integration test for Wave 1 / B2: WithDevMode(bool) option overrides
// the GASTRO_DEV env var. The behaviour we want to lock in:
//
//   WithDevMode(true)  -> Handler() mounts the /__gastro/reload SSE
//                         endpoint regardless of GASTRO_DEV being unset.
//   WithDevMode(false) -> Handler() does NOT mount /__gastro/reload even
//                         when GASTRO_DEV=1.
//
// The Router.isDev field is unexported, so we observe behaviour through
// the generated Handler(): mount the handler on a test server and probe
// /__gastro/reload. If the response is a 200 with text/event-stream, we
// know dev mode is on; if it's 404, we know it's off.
//
// Compiled in a subprocess (similar to race_integration_test.go) because
// generating test code requires a complete .gastro/ output and a go.mod
// pointing at this repo via replace directive. Gated by testing.Short()
// because it spawns the Go toolchain.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andrioid/gastro/internal/compiler"
)

func TestCompile_WithDevModeOverridesEnv(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping dev-mode integration test in -short mode")
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

	// Minimal page so routes.go compiles cleanly.
	mustWriteFile(t, filepath.Join(pagesDir, "index.gastro"),
		"---\nTitle := \"Hi\"\n---\n<h1>{{ .Title }}</h1>\n")

	gastroOut := filepath.Join(projectDir, ".gastro")
	if _, err := compiler.Compile(projectDir, gastroOut, compiler.CompileOptions{}); err != nil {
		t.Fatalf("compile: %v", err)
	}

	mustWriteFile(t, filepath.Join(projectDir, "go.mod"),
		"module gastro_devmode_repro\n\n"+
			"go 1.26.1\n\n"+
			"require github.com/andrioid/gastro v0.0.0\n\n"+
			"replace github.com/andrioid/gastro => "+repoRoot+"\n")

	// Subprocess test that constructs Routers with WithDevMode and probes
	// the dev-reload SSE endpoint to observe whether dev mode is on.
	mustWriteFile(t, filepath.Join(projectDir, "devmode_test.go"), `package devmode_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	gastro "gastro_devmode_repro/.gastro"
)

// probeReloadEndpoint mounts r.Handler() on httptest, GETs /__gastro/reload,
// and returns (statusCode, contentType). A 200 with text/event-stream means
// dev mode is on; 404 means dev mode is off.
func probeReloadEndpoint(t *testing.T, h http.Handler) (int, string) {
	t.Helper()
	srv := httptest.NewServer(h)
	defer srv.Close()

	req, err := http.NewRequest("GET", srv.URL+"/__gastro/reload", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	// Avoid hanging on the SSE stream by using a custom client that closes
	// the body immediately.
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	return resp.StatusCode, resp.Header.Get("Content-Type")
}

// TestWithDevModeTrue_MountsReloadEndpoint: WithDevMode(true) should mount
// /__gastro/reload even when GASTRO_DEV is unset.
func TestWithDevModeTrue_MountsReloadEndpoint(t *testing.T) {
	os.Unsetenv("GASTRO_DEV")
	r := gastro.New(gastro.WithDevMode(true))
	status, ct := probeReloadEndpoint(t, r.Handler())
	if status != http.StatusOK {
		t.Errorf("WithDevMode(true): expected /__gastro/reload to return 200, got %d", status)
	}
	if !strings.Contains(ct, "text/event-stream") {
		t.Errorf("WithDevMode(true): expected SSE content-type, got %q", ct)
	}
}

// TestWithDevModeFalse_OmitsReloadEndpoint: WithDevMode(false) should NOT
// mount /__gastro/reload even when GASTRO_DEV=1.
func TestWithDevModeFalse_OmitsReloadEndpoint(t *testing.T) {
	t.Setenv("GASTRO_DEV", "1")
	r := gastro.New(gastro.WithDevMode(false))
	status, _ := probeReloadEndpoint(t, r.Handler())
	if status != http.StatusNotFound {
		t.Errorf("WithDevMode(false): expected /__gastro/reload to return 404, got %d", status)
	}
}

// TestWithDevModeUnset_FollowsEnv: with WithDevMode not called, behaviour
// follows GASTRO_DEV.
func TestWithDevModeUnset_FollowsEnv(t *testing.T) {
	os.Unsetenv("GASTRO_DEV")
	r := gastro.New()
	status, _ := probeReloadEndpoint(t, r.Handler())
	if status != http.StatusNotFound {
		t.Errorf("default (GASTRO_DEV unset): expected /__gastro/reload to return 404, got %d", status)
	}

	t.Setenv("GASTRO_DEV", "1")
	r = gastro.New()
	status, ct := probeReloadEndpoint(t, r.Handler())
	if status != http.StatusOK {
		t.Errorf("default (GASTRO_DEV=1): expected /__gastro/reload to return 200, got %d", status)
	}
	if !strings.Contains(ct, "text/event-stream") {
		t.Errorf("default (GASTRO_DEV=1): expected SSE content-type, got %q", ct)
	}
}
`)

	cmd := exec.Command("go", "test", "-race", "-count=1", "-run", "TestWithDevMode", "./...")
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
