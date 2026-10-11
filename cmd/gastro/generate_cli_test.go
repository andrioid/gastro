package main_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerate_RejectsGoDirectiveBelowMinimum(t *testing.T) {
	t.Parallel()
	dir := makeGastroProject(t, t.TempDir())
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module testproject\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, stderr, code := runGastroCmd(t, nil, dir, "generate")
	if code == 0 {
		t.Fatalf("expected gastro generate to fail for go 1.26; stderr: %s", stderr)
	}
	if !strings.Contains(stderr, "go 1.26") || !strings.Contains(stderr, "requires go 1.27") {
		t.Errorf("stderr should name the declared and the required version; got: %s", stderr)
	}
}
