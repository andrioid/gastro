package compiler_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andrioid/gastro/internal/compiler"
)

// writeGoDirectiveProject creates a one-page project whose go.mod
// declares the given go directive line (empty = no go directive).
func writeGoDirectiveProject(t *testing.T, goLine string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "pages"), 0o755); err != nil {
		t.Fatal(err)
	}
	mod := "module example.com/m\n"
	if goLine != "" {
		mod += "\n" + goLine + "\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(mod), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pages", "index.gastro"), []byte("<h1>hi</h1>\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestCompile_RejectsGoDirectiveBelowMinimum(t *testing.T) {
	for _, goLine := range []string{"go 1.26", "go 1.26.4", "go 1.22", ""} {
		t.Run(goLine, func(t *testing.T) {
			dir := writeGoDirectiveProject(t, goLine)
			outputDir := t.TempDir()

			_, err := compiler.Compile(dir, outputDir, compiler.CompileOptions{})
			if err == nil {
				t.Fatal("expected Compile to reject a module below go 1.27")
			}
			msg := err.Error()
			for _, want := range []string{"go.mod", "1.27"} {
				if !strings.Contains(msg, want) {
					t.Errorf("error should mention %q; got: %v", want, err)
				}
			}
			if _, statErr := os.Stat(filepath.Join(outputDir, "routes.go")); statErr == nil {
				t.Error("no code should be generated for an unsupported go directive")
			}
		})
	}
}

func TestCompile_AcceptsGoDirectiveAtOrAboveMinimum(t *testing.T) {
	for _, goLine := range []string{"go 1.27", "go 1.27.2", "go 1.28rc1", "go 1.30"} {
		t.Run(goLine, func(t *testing.T) {
			dir := writeGoDirectiveProject(t, goLine)
			if _, err := compiler.Compile(dir, t.TempDir(), compiler.CompileOptions{}); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}
