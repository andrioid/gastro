package lspclient_test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"gastro-website/lspclient"
	"gastro-website/lspdemo"
)

// safeBuf is a goroutine-safe bytes.Buffer for capturing LSP stderr
// concurrently with the test goroutine reading it.
type safeBuf struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *safeBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *safeBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// typoSource is a deliberately-broken .gastro source used by
// TestSmoke to exercise the diagnostic round-trip. It mirrors the
// shape of lspdemo.Source() but reintroduces a `p.NAme` typo on
// line 7 (0-indexed) so gopls has something to complain about. We
// keep this inline rather than reading the demo file so the demo
// source can stay clean (it's rendered on the public landing page).
const typoSource = `---
type Props struct {
	Name string
}

p := gastro.Props()
Name := p.Name
Greeting := "Hi, " + p.NAme
---
<section>
	<h1>{{ .Greeting }}</h1>
	<p>Hello {{ .Name }}, nice to see you.</p>
</section>
`

// TestSmoke spins up a real `gastro lsp` subprocess against a temp
// project that contains a typo'd .gastro file, then exercises the
// three things main.go will rely on at app boot:
//
//  1. Start performs the initialize/initialized handshake.
//  2. OpenFile + WaitForDiagnostics surfaces gopls's complaint about
//     the deliberate `p.NAme` typo (see typoSource above).
//  3. Hover at a known identifier returns non-empty markdown.
//
// The test is intentionally one big round-trip rather than three
// micro-tests: standing up gopls + the gastro LSP costs ~2-5s of
// real time and the demo's wiring is more useful to verify
// end-to-end than per-method.
func TestSmoke(t *testing.T) {
	if testing.Short() {
		t.Skip("smoke test spawns gopls; skipped in -short")
	}

	// Build the gastro binary fresh. The integration tests in
	// cmd/gastro do the same — using `go run` directly would be
	// slower per-iteration and noisier because `go run`'s wrapper
	// process intercepts signals.
	binDir := t.TempDir()
	binPath := filepath.Join(binDir, "gastro")
	build := exec.Command("go", "build", "-o", binPath, ".")
	build.Dir = filepath.Join(repoRoot(t), "cmd", "gastro")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building gastro binary:\n%s\nerror: %v", out, err)
	}

	// Materialise a throwaway Go project the LSP can root itself in.
	//
	// The project must have github.com/andrioid/gastro available as a
	// dependency so the shadow generator's `gastro.Props()` rewrite
	// type-checks. We copy examples/gastro/go.mod (with the relative
	// replace directive patched to an absolute path), same pattern as
	// createGastroLinkedProject in
	// internal/lsp/shadow/workspace_test.go.
	//
	// The demo file lives under components/ (not pages/) because the
	// shadow generator only synthesises __props for components.
	projectDir := t.TempDir()
	repo := repoRoot(t)
	exampleGoMod, err := os.ReadFile(filepath.Join(repo, "examples", "gastro", "go.mod"))
	if err != nil {
		t.Fatalf("reading examples/gastro/go.mod: %v", err)
	}
	patchedGoMod := strings.Replace(string(exampleGoMod), "=> ../..", "=> "+repo, 1)
	if err := os.WriteFile(filepath.Join(projectDir, "go.mod"), []byte(patchedGoMod), 0o644); err != nil {
		t.Fatal(err)
	}
	if sum, err := os.ReadFile(filepath.Join(repo, "examples", "gastro", "go.sum")); err == nil {
		_ = os.WriteFile(filepath.Join(projectDir, "go.sum"), sum, 0o644)
	}
	componentsDir := filepath.Join(projectDir, "components")
	if err := os.MkdirAll(componentsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	demoPath := filepath.Join(componentsDir, lspdemo.Filename)
	if err := os.WriteFile(demoPath, []byte(typoSource), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	rootURI := "file://" + projectDir
	uri := "file://" + demoPath

	cmd := exec.Command(binPath, "lsp")
	cmd.Dir = projectDir
	stderrBuf := &safeBuf{}
	client, err := lspclient.StartWithStderr(ctx, cmd, rootURI, stderrBuf)
	if err != nil {
		t.Fatalf("starting client: %v", err)
	}

	var diagLogMu sync.Mutex
	var diagLog []string
	client.SetDiagnosticsHook(func(uri string, diags []lspclient.Diagnostic) {
		diagLogMu.Lock()
		defer diagLogMu.Unlock()
		msgs := make([]string, len(diags))
		for i, d := range diags {
			msgs[i] = d.Message
		}
		diagLog = append(diagLog, uri+" -> "+strings.Join(msgs, " | "))
	})

	t.Cleanup(func() {
		_ = client.Close()
		if t.Failed() {
			t.Logf("LSP stderr:\n%s", stderrBuf.String())
			diagLogMu.Lock()
			t.Logf("publishDiagnostics observed: %d\n%s", len(diagLog), strings.Join(diagLog, "\n"))
			diagLogMu.Unlock()
		}
	})

	if err := client.OpenFile(uri, "gastro", typoSource); err != nil {
		t.Fatalf("OpenFile: %v", err)
	}

	// gopls often pushes an initial empty diagnostics frame, then a
	// second one with the actual analysis result. Spin until we see
	// the NAme typo or the context deadline fires.
	deadline := time.Now().Add(20 * time.Second)
	var diags []lspclient.Diagnostic
	for time.Now().Before(deadline) {
		waitCtx, waitCancel := context.WithTimeout(ctx, 3*time.Second)
		diags, err = client.WaitForDiagnostics(waitCtx, uri)
		waitCancel()
		if err != nil && err != context.DeadlineExceeded {
			t.Fatalf("WaitForDiagnostics: %v", err)
		}
		if hasNAmeDiagnostic(diags) {
			break
		}
		// Re-arm: subsequent waits return the cached frame
		// instantly; we want to give the dispatcher a chance to
		// receive the next publishDiagnostics. A short sleep here
		// is fine — the round-trip latency for gopls re-analysis
		// is on the order of 100ms.
		time.Sleep(150 * time.Millisecond)
		diags = client.Diagnostics(uri)
		if hasNAmeDiagnostic(diags) {
			break
		}
	}
	if !hasNAmeDiagnostic(diags) {
		t.Fatalf("expected a diagnostic mentioning NAme, got %d diagnostics: %+v", len(diags), diags)
	}

	// Hover on the `Greeting` identifier in the frontmatter. In
	// typoSource, line 7 (0-indexed) is:
	//   Greeting := "Hi, " + p.NAme
	// Column 0 is 'G'. Hovering at character 0 lands on the variable
	// declaration.
	hover, err := client.Hover(ctx, uri, 7, 0)
	if err != nil {
		t.Fatalf("Hover: %v", err)
	}
	if hover == nil {
		t.Fatal("expected hover result for `Greeting`, got nil")
	}
	if hover.Contents.Value == "" {
		t.Fatal("expected non-empty hover contents")
	}
	if !strings.Contains(hover.Contents.Value, "Greeting") {
		t.Errorf("expected hover to mention 'Greeting', got: %s", hover.Contents.Value)
	}
}

func hasNAmeDiagnostic(diags []lspclient.Diagnostic) bool {
	for _, d := range diags {
		// gopls phrasing varies across versions; match on the
		// identifier name itself, which is what visitors see in
		// the squiggle's hover anyway.
		if strings.Contains(d.Message, "NAme") {
			return true
		}
	}
	return false
}

// repoRoot walks up from the test's working dir until it finds the
// repo-level go.mod. The gastro example module's go.mod sits at
// examples/gastro/go.mod, so we keep walking past it until we hit the
// outer one. We detect the outer one by checking for cmd/gastro.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "cmd", "gastro", "main.go")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find repo root (no cmd/gastro/main.go ancestor)")
		}
		dir = parent
	}
}
