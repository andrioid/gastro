package compiler_test

// Integration test for attribute forwarding (issue #37): a component with
// a gastro.Attrs bag field forwards arbitrary HTML attributes passed by
// the caller, the attrs func escapes/merges them correctly, and
// WithClassMerger swaps the class-merge strategy.
//
// Compiled in a subprocess so the generated component method, MapToStruct
// rest-capture, and the attrs template func are exercised end-to-end
// against the real http.ServeMux — same shape as the other integration
// tests in this package.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andrioid/gastro/internal/compiler"
)

func TestCompile_AttributeForwarding(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping attribute-forwarding integration test in -short mode")
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

	// Button declares one typed prop (Label) plus an attribute bag. Its
	// base attrs set a default type and base classes; the caller's
	// forwarded attributes flow in via .Attrs.
	mustWriteFile(t, filepath.Join(componentsDir, "button.gastro"),
		"---\n"+
			"type Props struct {\n"+
			"\tLabel string\n"+
			"\tAttrs gastro.Attrs\n"+
			"}\n"+
			"\n"+
			"Label := gastro.Props().Label\n"+
			"Attrs := gastro.Props().Attrs\n"+
			"---\n"+
			"<button {{ attrs .Attrs (dict \"class\" \"btn px-4\" \"type\" \"button\") }}>{{ .Label }}</button>\n")

	// The page forwards: type (overrides the base default), class (merges
	// with the base), an escapable data-title, and a safeJS data-on:click
	// (must pass through unescaped).
	mustWriteFile(t, filepath.Join(pagesDir, "index.gastro"),
		"---\n"+
			"import Button \"components/button.gastro\"\n"+
			"---\n"+
			"<main>{{ Button (dict \"Label\" \"Save\" \"type\" \"submit\" \"class\" \"px-2\" \"data-title\" \"a & b\" \"data-on:click\" (safeJS \"@post('/x')\")) }}</main>\n")

	gastroOut := filepath.Join(projectDir, ".gastro")
	if _, err := compiler.Compile(projectDir, gastroOut, compiler.CompileOptions{}); err != nil {
		t.Fatalf("compile: %v", err)
	}

	mustWriteFile(t, filepath.Join(projectDir, "go.mod"),
		"module gastro_attrs_repro\n\n"+
			"go 1.26.1\n\n"+
			"require github.com/andrioid/gastro v0.0.0\n\n"+
			"replace github.com/andrioid/gastro => "+repoRoot+"\n")

	mustWriteFile(t, filepath.Join(projectDir, "attrs_test.go"), `package attrs_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gastro "gastro_attrs_repro/.gastro"
)

func getBody(t *testing.T, h http.Handler) string {
	srv := httptest.NewServer(h)
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return string(body)
}

// TestForwardDefault: with the built-in (plain-concat) merger, forwarded
// attributes appear, the bag overrides the base type, class concatenates
// base+forwarded, a plain value is HTML-escaped, and a safeJS value passes
// through unescaped.
func TestForwardDefault(t *testing.T) {
	body := getBody(t, gastro.New().Handler())
	for _, want := range []string{
		"type=\"submit\"",
		"class=\"btn px-4 px-2\"",
		"data-title=\"a &amp; b\"",
		"data-on:click=\"@post('/x')\"",
		">Save<",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q:\n%s", want, body)
		}
	}
}

// prefixMerge is a conflict-resolving stub: last token wins per "-" prefix.
func prefixMerge(classes ...string) string {
	seen := map[string]string{}
	var order []string
	for _, group := range classes {
		for _, tok := range strings.Fields(group) {
			p := tok
			if i := strings.LastIndex(tok, "-"); i >= 0 {
				p = tok[:i]
			}
			if _, ok := seen[p]; !ok {
				order = append(order, p)
			}
			seen[p] = tok
		}
	}
	out := make([]string, len(order))
	for i, p := range order {
		out[i] = seen[p]
	}
	return strings.Join(out, " ")
}

// TestForwardCustomMerger: WithClassMerger swaps the strategy so the
// forwarded px-2 replaces the base px-4 instead of concatenating.
func TestForwardCustomMerger(t *testing.T) {
	body := getBody(t, gastro.New(gastro.WithClassMerger(prefixMerge)).Handler())
	if !strings.Contains(body, "class=\"btn px-2\"") {
		t.Errorf("custom merger: body missing merged class:\n%s", body)
	}
	if strings.Contains(body, "px-4") {
		t.Errorf("custom merger: px-4 should have been replaced:\n%s", body)
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
	for _, sub := range []string{"TestForwardDefault", "TestForwardCustomMerger"} {
		if !strings.Contains(string(out), sub) {
			t.Errorf("subprocess did not run %s; output:\n%s", sub, out)
		}
	}
}
