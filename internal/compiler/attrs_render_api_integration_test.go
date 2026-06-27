package compiler_test

// Integration test for attribute forwarding through the Go Render API
// (issues #41 and #42). Distinct from attrs_integration_test.go, which
// exercises the template dispatch path ({{ Button (dict ...) }}).
//
//   - #42: Render.X(XProps{Attrs: ...}) must forward the bag. The bug
//     nested the bag under its own "Attrs" key, where MapToStruct's
//     rest-capture dropped it.
//   - #41: a component that renders {{ .Children }} AND declares a
//     gastro.Attrs field must produce compilable render.go. The bug
//     emitted a verbatim `gastro.Attrs` field in the inline XProps
//     struct, but render.go is package gastro with no gastro import.
//
// Compiled and run in a subprocess so the generated render.go, the
// per-component method, MapToStruct rest-capture, and the attrs template
// func are all exercised end-to-end.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andrioid/gastro/internal/compiler"
)

func TestCompile_RenderAPIAttributeForwarding(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping render-API attribute-forwarding integration test in -short mode")
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

	// Button: typed prop + attribute bag, no children. Exercises #42 via
	// the alias XProps path (type ButtonProps = __component_button_Props).
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
			"<button {{ attrs .Attrs }}>{{ .Label }}</button>\n")

	// Alert: attribute bag + {{ .Children }}. Exercises #41 (inline XProps
	// struct must compile) and #42 (bag forwarded) together.
	mustWriteFile(t, filepath.Join(componentsDir, "alert.gastro"),
		"---\n"+
			"type Props struct {\n"+
			"\tVariant string\n"+
			"\tAttrs   gastro.Attrs\n"+
			"}\n"+
			"\n"+
			"Variant := gastro.Props().Variant\n"+
			"Attrs := gastro.Props().Attrs\n"+
			"---\n"+
			"<div {{ attrs .Attrs (dict \"class\" .Variant) }}>{{ .Children }}</div>\n")

	// A trivial page so the project has a route to mount; it does not use
	// the components (the Render API is what we test).
	mustWriteFile(t, filepath.Join(pagesDir, "index.gastro"),
		"<main>ok</main>\n")

	gastroOut := filepath.Join(projectDir, ".gastro")
	if _, err := compiler.Compile(projectDir, gastroOut, compiler.CompileOptions{}); err != nil {
		t.Fatalf("compile: %v", err)
	}

	mustWriteFile(t, filepath.Join(projectDir, "go.mod"),
		"module gastro_render_attrs_repro\n\n"+
			"go 1.26.1\n\n"+
			"require github.com/andrioid/gastro v0.0.0\n\n"+
			"replace github.com/andrioid/gastro => "+repoRoot+"\n")

	mustWriteFile(t, filepath.Join(projectDir, "render_attrs_test.go"), `package render_attrs_test

import (
	"html/template"
	"strings"
	"testing"

	gastro "gastro_render_attrs_repro/.gastro"
	gastroRuntime "github.com/andrioid/gastro/pkg/gastro"
)

// TestRenderAPIForwardsAttrs covers #42: the bag passed to Render.Button
// must reach the rendered <button>, not be silently dropped.
func TestRenderAPIForwardsAttrs(t *testing.T) {
	r := gastro.New()
	html, err := r.Render().Button(gastro.ButtonProps{
		Label: "Save",
		Attrs: gastroRuntime.Attrs{"type": "submit", "data-x": "1"},
	})
	if err != nil {
		t.Fatalf("render button: %v", err)
	}
	for _, want := range []string{`+"`"+`type="submit"`+"`"+`, `+"`"+`data-x="1"`+"`"+`, ">Save<"} {
		if !strings.Contains(html, want) {
			t.Errorf("button html missing %q:\n%s", want, html)
		}
	}
}

// TestRenderAPIForwardsAttrsWithChildren covers #41 (the project compiles
// at all with a children+Attrs component) and #42 (bag forwarded) on the
// inline-XProps path.
func TestRenderAPIForwardsAttrsWithChildren(t *testing.T) {
	r := gastro.New()
	html, err := r.Render().Alert(gastro.AlertProps{
		Variant:  "warn",
		Attrs:    gastroRuntime.Attrs{"role": "alert", "data-x": "1"},
		Children: template.HTML("<p>hi</p>"),
	})
	if err != nil {
		t.Fatalf("render alert: %v", err)
	}
	for _, want := range []string{`+"`"+`role="alert"`+"`"+`, `+"`"+`data-x="1"`+"`"+`, "<p>hi</p>", "warn"} {
		if !strings.Contains(html, want) {
			t.Errorf("alert html missing %q:\n%s", want, html)
		}
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
	for _, sub := range []string{"TestRenderAPIForwardsAttrs", "TestRenderAPIForwardsAttrsWithChildren"} {
		if !strings.Contains(string(out), sub) {
			t.Errorf("subprocess did not run %s; output:\n%s", sub, out)
		}
	}
}
