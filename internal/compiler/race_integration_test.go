package compiler_test

// This integration test is the audit P0 #2 reproducer turned into a
// regression test. It spawns multiple goroutines that concurrently call
// gastro.New() and router.Render(Card, …) — on their own router and on a
// shared one — under the Go race detector. The original bug was a
// package-level active-router pointer written by every New().
//
// The test compiles a tiny gastro project to a temp directory, writes a
// minimal go.mod that points at this repo via a replace directive, and
// shells out to `go test -race`. It is gated by testing.Short() because
// it spawns the Go toolchain.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andrioid/gastro/internal/compiler"
)

func TestCompile_GeneratedCodeIsRaceFree(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping race integration test in -short mode")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not in PATH")
	}

	repoRoot := findRepoRoot(t)

	projectDir := t.TempDir()
	pagesDir := filepath.Join(projectDir, "pages")
	compDir := filepath.Join(projectDir, "components")
	if err := os.MkdirAll(pagesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(compDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Minimal page so routes.go compiles cleanly.
	mustWriteFile(t, filepath.Join(pagesDir, "index.gastro"),
		"---\nTitle := \"Hi\"\n---\n<h1>{{ .Title }}</h1>\n")

	// One component with a single Props field so render.go has something to
	// dispatch and componentCard is a real method on *Router.
	mustWriteFile(t, filepath.Join(compDir, "card.gastro"),
		"---\n"+
			"type Props struct {\n"+
			"\tTitle string\n"+
			"}\n\n"+
			"p := gastro.Props()\n"+
			"Title := p.Title\n"+
			"---\n"+
			`<div>{{ .Title }}</div>`+"\n")

	gastroOut := filepath.Join(projectDir, ".gastro")
	if _, err := compiler.Compile(projectDir, gastroOut, compiler.CompileOptions{}); err != nil {
		t.Fatalf("compile: %v", err)
	}

	// go.mod that pulls in the gastro runtime via a local replace pointing
	// at the repository under test.
	mustWriteFile(t, filepath.Join(projectDir, "go.mod"),
		"module gastro_race_repro\n\n"+
			"go 1.27.2\n\n"+
			"require github.com/andrioid/gastro v0.0.0\n\n"+
			"replace github.com/andrioid/gastro => "+repoRoot+"\n")

	// Test file that spins up many concurrent New() + Render(Card, …)
	// callers, on per-goroutine routers and on one shared router.
	mustWriteFile(t, filepath.Join(projectDir, "race_test.go"), `package race_test

import (
	"strings"
	"sync"
	"testing"

	gastro "gastro_race_repro/.gastro"
)

func TestRender_NoRace(t *testing.T) {
	const goroutines = 32
	const iters = 10

	shared := gastro.New()
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				for _, r := range []*gastro.Router{gastro.New(), shared} {
					html, err := r.Render(gastro.Card, gastro.CardProps{Title: "router"})
					if err != nil {
						t.Errorf("Render(Card): %v", err)
						return
					}
					if !strings.Contains(string(html), "router") {
						t.Errorf("Render(Card) missing payload: %q", html)
						return
					}
				}
			}
		}()
	}
	wg.Wait()
}
`)

	cmd := exec.Command("go", "test", "-race", "-count=1", "-run", "TestRender_NoRace", "./...")
	cmd.Dir = projectDir
	cmd.Env = append(os.Environ(), "GOFLAGS=") // strip outer -race etc.
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go test -race failed: %v\n%s", err, out)
	}
	if strings.Contains(string(out), "DATA RACE") {
		t.Fatalf("data race detected in generated code:\n%s", out)
	}
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

// findRepoRoot walks up from this test's working directory looking for
// the gastro repo's go.mod.
func findRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		modPath := filepath.Join(dir, "go.mod")
		if data, err := os.ReadFile(modPath); err == nil {
			if strings.Contains(string(data), "module github.com/andrioid/gastro") {
				return dir
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not locate gastro repo root from " + dir)
		}
		dir = parent
	}
}
