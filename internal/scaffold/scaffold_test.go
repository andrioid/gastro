package scaffold_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andrioid/gastro/internal/scaffold"
)

func TestGenerate_CreatesExpectedStructure(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "myapp")

	if err := scaffold.Generate("myapp", target, "0.1.0"); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	expectedDirs := []string{"pages", "components", "static"}
	for _, d := range expectedDirs {
		info, err := os.Stat(filepath.Join(target, d))
		if err != nil {
			t.Errorf("expected directory %s to exist: %v", d, err)
			continue
		}
		if !info.IsDir() {
			t.Errorf("expected %s to be a directory", d)
		}
	}

	expectedFiles := []string{"pages/index.gastro", "main.go", "go.mod", ".gitignore", "static/.gitkeep", "README.md"}
	for _, f := range expectedFiles {
		if _, err := os.Stat(filepath.Join(target, f)); err != nil {
			t.Errorf("expected file %s to exist: %v", f, err)
		}
	}
}

func TestGenerate_GoModContainsModuleName(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "testproject")

	if err := scaffold.Generate("testproject", target, "0.1.0"); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(target, "go.mod"))
	if err != nil {
		t.Fatalf("read go.mod: %v", err)
	}

	text := string(content)
	if !strings.Contains(text, "module testproject") {
		t.Errorf("go.mod should contain module name, got:\n%s", text)
	}
	if !strings.Contains(text, "go "+scaffold.GoVersion) {
		t.Errorf("go.mod should contain go version %s, got:\n%s", scaffold.GoVersion, text)
	}
	if !strings.Contains(text, "github.com/andrioid/gastro v0.1.0") {
		t.Errorf("go.mod should require gastro v0.1.0, got:\n%s", text)
	}
	if !strings.Contains(text, "tool github.com/andrioid/gastro/cmd/gastro") {
		t.Errorf("go.mod should declare the gastro CLI as a tool, got:\n%s", text)
	}
}

func TestGenerate_GoModDevVersion(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "devproject")

	if err := scaffold.Generate("devproject", target, "dev"); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(target, "go.mod"))
	if err != nil {
		t.Fatalf("read go.mod: %v", err)
	}

	text := string(content)
	if !strings.Contains(text, "github.com/andrioid/gastro v0.0.0") {
		t.Errorf("go.mod should use v0.0.0 for dev version, got:\n%s", text)
	}
	if !strings.Contains(text, "replace github.com/andrioid/gastro") {
		t.Errorf("go.mod should contain commented replace directive for dev, got:\n%s", text)
	}
	if !strings.Contains(text, "tool github.com/andrioid/gastro/cmd/gastro") {
		t.Errorf("go.mod should declare the gastro CLI as a tool even in dev mode, got:\n%s", text)
	}
}

func TestGenerate_MainGoImportsGastroPackage(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "coolapp")

	if err := scaffold.Generate("coolapp", target, "0.1.0"); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(target, "main.go"))
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}

	text := string(content)
	if !strings.Contains(text, `"coolapp/.gastro"`) {
		t.Errorf("main.go should import coolapp/.gastro, got:\n%s", text)
	}
	if !strings.Contains(text, "gastro.Routes()") {
		t.Errorf("main.go should call gastro.Routes(), got:\n%s", text)
	}
	if !strings.Contains(text, "//go:generate go tool gastro generate") {
		t.Errorf("main.go should contain go:generate directive for gastro generate, got:\n%s", text)
	}
}

func TestGenerate_IndexPageHasFrontmatterAndTemplate(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "myapp")

	if err := scaffold.Generate("myapp", target, "0.1.0"); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(target, "pages", "index.gastro"))
	if err != nil {
		t.Fatalf("read index.gastro: %v", err)
	}

	text := string(content)
	if !strings.Contains(text, "---") {
		t.Errorf("index.gastro should have frontmatter delimiters")
	}
	if !strings.Contains(text, "{{ .Title }}") {
		t.Errorf("index.gastro should reference .Title in template")
	}
}

func TestGenerate_GitignoreContentsCorrect(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "myapp")

	if err := scaffold.Generate("myapp", target, "0.1.0"); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(target, ".gitignore"))
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}

	text := string(content)
	if !strings.Contains(text, ".gastro/") {
		t.Errorf(".gitignore should include .gastro/")
	}
	if !strings.Contains(text, "app") {
		t.Errorf(".gitignore should include app binary")
	}
}

func TestGenerate_ErrorWhenTargetIsFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "blockingfile")

	// Create a file where the directory should go.
	if err := os.WriteFile(target, []byte("I'm a file"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	err := scaffold.Generate("myapp", target, "0.1.0")
	if err == nil {
		t.Error("expected error when target is a file, got nil")
	}
}

func TestGenerate_DifferentModuleNames(t *testing.T) {
	tests := []struct {
		name       string
		moduleName string
	}{
		{"simple", "myapp"},
		{"with-dash", "my-app"},
		{"domain-path", "github.com/user/myapp"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			target := filepath.Join(dir, "project")

			if err := scaffold.Generate(tt.moduleName, target, "0.1.0"); err != nil {
				t.Fatalf("Generate(%q) failed: %v", tt.moduleName, err)
			}

			content, err := os.ReadFile(filepath.Join(target, "go.mod"))
			if err != nil {
				t.Fatalf("read go.mod: %v", err)
			}
			if !strings.Contains(string(content), "module "+tt.moduleName) {
				t.Errorf("go.mod should contain 'module %s'", tt.moduleName)
			}

			mainContent, err := os.ReadFile(filepath.Join(target, "main.go"))
			if err != nil {
				t.Fatalf("read main.go: %v", err)
			}
			if !strings.Contains(string(mainContent), tt.moduleName+"/.gastro") {
				t.Errorf("main.go should import %s/.gastro", tt.moduleName)
			}
		})
	}
}
