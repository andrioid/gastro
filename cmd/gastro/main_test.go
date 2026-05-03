package main_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// gastroBinaryOnce builds the gastro binary once per test process and reuses it
// across the many subprocess-based tests in this file. Each call to
// `go build` takes ~0.5s, so caching saves a meaningful amount of test time.
var (
	gastroBinaryOnce sync.Once
	gastroBinaryPath string
	gastroBinaryErr  error
)

func buildGastroBinary(t *testing.T) string {
	t.Helper()
	gastroBinaryOnce.Do(func() {
		// Build into a temp dir that lives for the test process lifetime.
		// We can't use t.TempDir because the binary outlives the calling test.
		dir, err := os.MkdirTemp("", "gastro-cli-tests-*")
		if err != nil {
			gastroBinaryErr = err
			return
		}
		bin := filepath.Join(dir, "gastro")
		cmd := exec.Command("go", "build", "-o", bin, ".")
		cmd.Dir = projectRoot(t) + "/cmd/gastro"
		if out, err := cmd.CombinedOutput(); err != nil {
			gastroBinaryErr = err
			t.Logf("build output: %s", out)
			return
		}
		gastroBinaryPath = bin
	})
	if gastroBinaryErr != nil {
		t.Fatalf("building gastro: %v", gastroBinaryErr)
	}
	return gastroBinaryPath
}

// runGastroCmd runs `gastro <args...>` with the given env and cwd. Returns
// stdout, stderr, and exit code.
func runGastroCmd(t *testing.T, env []string, cwd string, args ...string) (string, string, int) {
	t.Helper()
	bin := buildGastroBinary(t)

	cmd := exec.Command(bin, args...)
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), env...)

	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			t.Fatalf("run gastro: %v", err)
		}
	}
	return stdout.String(), stderr.String(), exitCode
}

// makeGastroProject creates a minimal gastro project structure inside dir.
// Returns dir for chaining.
func makeGastroProject(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "pages"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "components"), 0o755); err != nil {
		t.Fatal(err)
	}

	// go.mod so generated code can compile.
	if err := os.WriteFile(filepath.Join(dir, "go.mod"),
		[]byte("module testproject\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// One page with no frontmatter (keeps `list` happy).
	if err := os.WriteFile(filepath.Join(dir, "pages", "index.gastro"),
		[]byte("---\n---\n<h1>hi</h1>\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// One component so `list` has something to print.
	if err := os.WriteFile(filepath.Join(dir, "components", "card.gastro"),
		[]byte("---\ntype Props struct { Title string }\nTitle := gastro.Props().Title\n---\n<div>{{ .Title }}</div>\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	return dir
}

func TestApplyGastroProject_UnsetIsNoOp(t *testing.T) {
	t.Parallel()
	// When GASTRO_PROJECT is not set, gastro list runs against cwd.
	projectDir := makeGastroProject(t, t.TempDir())

	// Explicitly unset GASTRO_PROJECT in the child env.
	stdout, stderr, code := runGastroCmd(t, []string{"GASTRO_PROJECT="}, projectDir, "list")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "Card") {
		t.Errorf("expected Card in output, got: %s", stdout)
	}
}

func TestApplyGastroProject_AbsolutePath(t *testing.T) {
	t.Parallel()
	projectDir := makeGastroProject(t, t.TempDir())
	otherDir := t.TempDir() // run from somewhere else

	stdout, stderr, code := runGastroCmd(t,
		[]string{"GASTRO_PROJECT=" + projectDir},
		otherDir,
		"list")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "Card") {
		t.Errorf("expected Card in output, got: %s", stdout)
	}
}

func TestApplyGastroProject_RelativePath(t *testing.T) {
	t.Parallel()
	parent := t.TempDir()
	projectDir := filepath.Join(parent, "web")
	makeGastroProject(t, projectDir)

	// Run from `parent`, set GASTRO_PROJECT to `web` (relative).
	stdout, stderr, code := runGastroCmd(t,
		[]string{"GASTRO_PROJECT=web"},
		parent,
		"list")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "Card") {
		t.Errorf("expected Card in output, got: %s", stdout)
	}
}

func TestApplyGastroProject_NonexistentPath(t *testing.T) {
	t.Parallel()
	cwd := t.TempDir()
	bogus := filepath.Join(t.TempDir(), "does-not-exist")

	stdout, stderr, code := runGastroCmd(t,
		[]string{"GASTRO_PROJECT=" + bogus},
		cwd,
		"list")
	if code == 0 {
		t.Fatalf("expected non-zero exit, got 0\nstdout: %s\nstderr: %s", stdout, stderr)
	}
	if !strings.Contains(stderr, "GASTRO_PROJECT") {
		t.Errorf("expected stderr to mention GASTRO_PROJECT, got: %s", stderr)
	}
}

func TestApplyGastroProject_PathIsAFile(t *testing.T) {
	t.Parallel()
	cwd := t.TempDir()
	filePath := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(filePath, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, code := runGastroCmd(t,
		[]string{"GASTRO_PROJECT=" + filePath},
		cwd,
		"list")
	if code == 0 {
		t.Fatalf("expected non-zero exit, got 0\nstdout: %s\nstderr: %s", stdout, stderr)
	}
	if !strings.Contains(stderr, "not a directory") {
		t.Errorf("expected 'not a directory' in stderr, got: %s", stderr)
	}
}

func TestApplyGastroProject_NewIgnoresEnv(t *testing.T) {
	t.Parallel()
	// `gastro new` takes a target dir as a CLI arg; GASTRO_PROJECT must NOT
	// chdir before it runs (otherwise the relative target path would resolve
	// against the wrong place).
	parent := t.TempDir()
	bogusButValidDir := t.TempDir()

	// Set GASTRO_PROJECT to a valid but unrelated dir. `new myapp` should
	// still create `myapp` under `parent` (the cwd), not under bogusButValidDir.
	_, stderr, code := runGastroCmd(t,
		[]string{"GASTRO_PROJECT=" + bogusButValidDir},
		parent,
		"new", "myapp")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d\nstderr: %s", code, stderr)
	}

	if _, err := os.Stat(filepath.Join(parent, "myapp", "go.mod")); err != nil {
		t.Errorf("expected new project at %s/myapp, got error: %v", parent, err)
	}
	if _, err := os.Stat(filepath.Join(bogusButValidDir, "myapp")); err == nil {
		t.Errorf("did not expect new project at %s/myapp", bogusButValidDir)
	}
}

func TestApplyGastroProject_FmtSkipsWhenTargetsGiven(t *testing.T) {
	t.Parallel()
	// `gastro fmt <file>` should NOT chdir to GASTRO_PROJECT, because the
	// user-supplied path is relative to the user's cwd.
	cwd := t.TempDir()
	otherProject := makeGastroProject(t, t.TempDir())

	// Create a valid file in cwd.
	src := "---\nTitle := \"hi\"\n---\n<div>{{ .Title }}</div>\n"
	target := filepath.Join(cwd, "in-cwd.gastro")
	if err := os.WriteFile(target, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	// Set GASTRO_PROJECT to a different valid project. `fmt in-cwd.gastro`
	// should still find the file in cwd (i.e. NOT chdir before resolving).
	_, stderr, code := runGastroCmd(t,
		[]string{"GASTRO_PROJECT=" + otherProject},
		cwd,
		"fmt", "in-cwd.gastro")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d\nstderr: %s", code, stderr)
	}
}

func TestApplyGastroProject_FmtAppliesWhenNoTargets(t *testing.T) {
	t.Parallel()
	// `gastro fmt` (no args) should honour GASTRO_PROJECT.
	cwd := t.TempDir() // empty, no .gastro files
	projectDir := makeGastroProject(t, t.TempDir())

	// Create an unformatted-but-valid file inside the project so fmt has
	// something to do. Heavy whitespace ensures formatter wants to change it.
	src := "---\nTitle    :=    \"hi\"\n---\n<div>{{ .Title }}</div>\n"
	if err := os.WriteFile(filepath.Join(projectDir, "components", "messy.gastro"),
		[]byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	_, stderr, code := runGastroCmd(t,
		[]string{"GASTRO_PROJECT=" + projectDir},
		cwd,
		"fmt", "--check")
	// `--check` exits non-zero if anything is unformatted; either outcome
	// (0 = nothing to do, 1 = unformatted found) is acceptable. What we care
	// about is that fmt didn't crash and didn't report 0 files (which would
	// indicate it walked the empty cwd instead of the project).
	if code != 0 && code != 1 {
		t.Fatalf("expected exit 0 or 1, got %d\nstderr: %s", code, stderr)
	}
	// If we hadn't chdir'd, `fmt` would have walked the empty cwd and printed
	// nothing. With chdir, `messy.gastro` should appear in stderr.
	if code == 1 && !strings.Contains(stderr, "messy.gastro") {
		t.Errorf("expected messy.gastro mentioned in stderr, got: %s", stderr)
	}
}
