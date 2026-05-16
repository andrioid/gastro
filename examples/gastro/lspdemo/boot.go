package lspdemo

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gastro-website/lspclient"
)

// live is the package-global registered Demo, set by Register after
// main.go calls Boot. Page frontmatters read it via Live(). atomic
// because frontmatters run on HTTP goroutines while Register runs on
// the main goroutine — a plain pointer would race under -race.
var live atomic.Pointer[Demo]

// Register publishes d as the current live Demo. Subsequent Live()
// calls return d. Call exactly once from main.go after Boot returns.
func Register(d *Demo) { live.Store(d) }

// Live returns the registered Demo, or a fallback that renders the
// embedded source statically (no LSP) if Register hasn't been called
// yet. The fallback exists so a misordered boot doesn't nil-panic a
// page render; it should never be observed in practice because
// routes are bound after Register.
func Live() *Demo {
	if d := live.Load(); d != nil {
		return d
	}
	return fallbackDemo()
}

var (
	fallbackOnce sync.Once
	fallbackVal  *Demo
)

func fallbackDemo() *Demo {
	fallbackOnce.Do(func() {
		r, err := NewRenderer(source, nil)
		if err != nil {
			// The embedded source is fixed; an error here is a
			// build-time bug. Panic so it surfaces loudly during dev.
			panic(fmt.Sprintf("lspdemo: fallback renderer: %v", err))
		}
		fallbackVal = &Demo{
			renderer:       r,
			degraded:       true,
			degradedReason: "lspdemo.Register() not called before first page render",
		}
	})
	return fallbackVal
}

// Demo is one running live-LSP demo. Built once at app boot, queried
// per-request for hovers, shut down at app exit.
//
// A "degraded" Demo (LSP failed to come up within the boot deadline)
// is still usable: Frontmatter() and Body() render the source with
// hoverable spans, but Hover() returns "" and the spans' tooltips
// will be empty. This is the fail-soft path called out in the plan.
type Demo struct {
	client   *lspclient.Client // nil if degraded
	uri      string            // file:// URI for the demo file
	renderer *Renderer
	degraded bool
	degradedReason string

	mu sync.Mutex // serialises Close
}

// Frontmatter / Body delegate to the snapshotted renderer.
func (d *Demo) Frontmatter() template.HTML { return d.renderer.Frontmatter() }
func (d *Demo) Body() template.HTML        { return d.renderer.Body() }

// Degraded returns true if the demo booted without an LSP subprocess
// (timeout or error during Boot). The handlers use this to skip the
// LSP roundtrip and return an empty tooltip.
func (d *Demo) Degraded() bool { return d.degraded }

// DegradedReason returns a human-readable string explaining why the
// demo is degraded, or "" if it isn't. Surfaced in logs at boot.
func (d *Demo) DegradedReason() string { return d.degradedReason }

// Hover queries the underlying LSP for hover content at (line, char)
// in the demo file. Returns the raw markdown body (caller renders).
// Returns "" if the demo is degraded or the LSP has no hover for
// that position.
func (d *Demo) Hover(ctx context.Context, line, char int) (string, error) {
	if d.degraded || d.client == nil {
		return "", nil
	}
	h, err := d.client.Hover(ctx, d.uri, line, char)
	if err != nil {
		return "", err
	}
	if h == nil {
		return "", nil
	}
	return h.Contents.Value, nil
}

// Close shuts the LSP subprocess down. Idempotent.
func (d *Demo) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.client == nil {
		return nil
	}
	c := d.client
	d.client = nil
	return c.Close()
}

// BootOptions configures Boot.
type BootOptions struct {
	// GastroSourceRoot is the absolute path to the gastro module
	// source tree on the host. Required: the temp project's go.mod
	// uses a `replace github.com/andrioid/gastro => <this>` directive
	// so the shadow generator's `gastro.Props()` rewrite type-checks
	// without network access. In dev we resolve this by walking up
	// from cwd; in prod the Docker image bundles the source under
	// /opt/gastro (see the Dockerfile).
	GastroSourceRoot string

	// LSPCommand is the command to run the gastro LSP. Typically
	// []string{"gastro", "lsp"} in prod (binary on PATH) or
	// []string{"go", "run", "github.com/andrioid/gastro/cmd/gastro", "lsp"}
	// in dev. The first element is used as the executable; the rest
	// are args.
	LSPCommand []string

	// BootTimeout caps how long Boot blocks waiting for the LSP to
	// start + open the file + publish its first diagnostics. Per the
	// plan: 15s, with fail-soft on timeout.
	BootTimeout time.Duration

	// Logger receives boot progress + degraded-mode warnings. nil
	// means use log.Printf (the default).
	Logger func(format string, args ...any)
}

// Boot performs the full startup sequence:
//
//  1. Materialise the embedded demo into a temp project layout.
//  2. Spawn the LSP subprocess.
//  3. Send initialize / initialized / didOpen.
//  4. Wait for the first publishDiagnostics frame to arrive.
//  5. Snapshot the diagnostics into a Renderer.
//
// If anything fails or BootTimeout elapses, Boot returns a degraded
// Demo (no LSP client, but renderer still works). The error is
// non-nil only for programming mistakes (missing source root, etc.)
// — runtime failures degrade gracefully.
//
// The caller MUST eventually call Close on the returned Demo.
func Boot(ctx context.Context, opts BootOptions) (*Demo, error) {
	logf := opts.Logger
	if logf == nil {
		logf = log.Printf
	}
	if opts.GastroSourceRoot == "" {
		return nil, errors.New("lspdemo.Boot: GastroSourceRoot is required")
	}
	if len(opts.LSPCommand) == 0 {
		return nil, errors.New("lspdemo.Boot: LSPCommand is required")
	}
	if opts.BootTimeout <= 0 {
		opts.BootTimeout = 15 * time.Second
	}

	// Always return *some* Demo, even on failure. Pre-build the
	// renderer with no diagnostics so callers in the degraded path
	// still get the source rendered.
	emptyRenderer, err := NewRenderer(source, nil)
	if err != nil {
		// The embedded source is fixed, so a parse failure here is
		// a build-time bug.
		return nil, fmt.Errorf("lspdemo: renderer for embedded source: %w", err)
	}
	demo := &Demo{renderer: emptyRenderer}

	// Materialise a temp project that contains:
	//   <tmp>/go.mod    (linked to opts.GastroSourceRoot via `replace`)
	//   <tmp>/components/greeting.gastro
	//
	// Path canonicalisation (EvalSymlinks) is required so the URI we
	// send via didOpen matches the path the LSP server stores
	// internally — see tmp/lsp-demo-plan.md 'Pre-existing issues'
	// for the underlying macOS /var vs /private/var bug.
	rawDir, err := os.MkdirTemp("", "gastro-lspdemo-")
	if err != nil {
		degrade(demo, logf, "MkdirTemp failed: %v", err)
		return demo, nil
	}
	projectDir, err := filepath.EvalSymlinks(rawDir)
	if err != nil {
		degrade(demo, logf, "EvalSymlinks(%q): %v", rawDir, err)
		return demo, nil
	}

	if err := writeTempProject(projectDir, opts.GastroSourceRoot); err != nil {
		degrade(demo, logf, "writeTempProject: %v", err)
		return demo, nil
	}
	demoPath := filepath.Join(projectDir, "components", Filename)
	if err := os.WriteFile(demoPath, []byte(source), 0o644); err != nil {
		degrade(demo, logf, "writing demo file: %v", err)
		return demo, nil
	}

	// Spawn the LSP. Boot-time deadline applies to the full
	// initialize + didOpen + first-diagnostics flow.
	bootCtx, cancel := context.WithTimeout(ctx, opts.BootTimeout)
	defer cancel()

	cmd := exec.Command(opts.LSPCommand[0], opts.LSPCommand[1:]...)
	cmd.Dir = projectDir

	client, err := lspclient.Start(bootCtx, cmd, "file://"+projectDir)
	if err != nil {
		degrade(demo, logf, "starting LSP: %v", err)
		return demo, nil
	}

	uri := "file://" + demoPath
	if err := client.OpenFile(uri, "gastro", source); err != nil {
		_ = client.Close()
		degrade(demo, logf, "OpenFile: %v", err)
		return demo, nil
	}

	// Wait for the LSP to settle. We loop because gopls typically
	// pushes an initial empty publishDiagnostics, then a populated
	// one ~100ms later. WaitForDiagnostics returns on the first one,
	// so a single call is usually empty; we then poll the cache for
	// up to ~2s after first contact to give the real diagnostics
	// time to land. Bounded by bootCtx so the 15s outer deadline
	// still applies.
	if _, err := client.WaitForDiagnostics(bootCtx, uri); err != nil {
		_ = client.Close()
		degrade(demo, logf, "WaitForDiagnostics: %v", err)
		return demo, nil
	}
	settleDeadline := time.Now().Add(2 * time.Second)
	var diags []lspclient.Diagnostic
	for time.Now().Before(settleDeadline) {
		select {
		case <-bootCtx.Done():
			_ = client.Close()
			degrade(demo, logf, "boot context cancelled during settle: %v", bootCtx.Err())
			return demo, nil
		case <-time.After(150 * time.Millisecond):
		}
		diags = client.Diagnostics(uri)
		if len(diags) > 0 {
			break
		}
	}

	// Rebuild the renderer with the now-known diagnostics.
	rendered, err := NewRenderer(source, diags)
	if err != nil {
		_ = client.Close()
		degrade(demo, logf, "rendering with diagnostics: %v", err)
		return demo, nil
	}
	demo.renderer = rendered
	demo.client = client
	demo.uri = uri
	logf("lspdemo: ready (project=%s, %d diagnostic(s))", projectDir, len(diags))
	return demo, nil
}

// degrade marks the demo as fallback-only and logs the reason. The
// passed-in demo always keeps its pre-built static renderer so the
// homepage still renders something on a degraded boot.
func degrade(d *Demo, logf func(string, ...any), format string, args ...any) {
	d.degraded = true
	d.degradedReason = fmt.Sprintf(format, args...)
	logf("lspdemo: degraded \u2014 "+format, args...)
}

// writeTempProject sets up go.mod (and go.sum if present) for a temp
// project rooted at dir, with `replace github.com/andrioid/gastro =>
// <gastroRoot>` so the shadow generator can resolve gastro types.
//
// We copy the gastro example's go.mod (it already declares the
// dependency at the right version) and rewrite its relative `=> ../..`
// directive to an absolute path. If we ever drift from the example's
// dep set, the shadow may stop type-checking with a "module not in
// dependency requirements" error; sync this with the example's
// go.mod when that happens.
func writeTempProject(dir, gastroRoot string) error {
	exampleGoMod := filepath.Join(gastroRoot, "examples", "gastro", "go.mod")
	src, err := os.ReadFile(exampleGoMod)
	if err != nil {
		return fmt.Errorf("reading %s: %w", exampleGoMod, err)
	}
	patched := strings.Replace(string(src), "=> ../..", "=> "+gastroRoot, 1)
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(patched), 0o644); err != nil {
		return err
	}

	// go.sum is optional but lets gopls skip a `go mod download` on
	// first analysis. Best-effort copy.
	if sum, err := os.ReadFile(filepath.Join(gastroRoot, "examples", "gastro", "go.sum")); err == nil {
		_ = os.WriteFile(filepath.Join(dir, "go.sum"), sum, 0o644)
	}

	if err := os.MkdirAll(filepath.Join(dir, "components"), 0o755); err != nil {
		return err
	}
	return nil
}
