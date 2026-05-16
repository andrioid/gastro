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
// is still usable: Render() returns the source with hoverable spans,
// but Hover() returns "" and the spans' tooltips will be empty. This
// is the fail-soft path called out in the plan.
type Demo struct {
	// client is loaded on every Hover and swapped to nil by Close.
	// It's atomic because public-internet HTTP goroutines call Hover
	// while a shutdown goroutine may call Close concurrently — a
	// plain pointer would race under -race and risk an NPE between
	// the nil-check and the deref.
	client         atomic.Pointer[lspclient.Client]
	uri            string // file:// URI for the demo file
	projectDir     string // temp project root, removed on Close
	renderer       *Renderer
	degraded       bool
	degradedReason string

	// hoverCache memoises HoverOrDiagnostic results keyed by
	// (line, char). The embedded source is fixed for the lifetime
	// of the Demo, so the response set is a finite map of identifier
	// positions (~30 entries for greeting.gastro). This is the rate-
	// limit story for the public /api/lsp-demo/hover endpoint:
	// without it, an attacker can spin gopls by spamming requests;
	// with it, every request after the first for a given position
	// is a memory hit.
	hoverCache sync.Map // hoverKey -> string

	mu sync.Mutex // serialises Close
}

// hoverKey is the cache key for hoverCache. The URI is fixed per
// Demo so it isn't part of the key.
type hoverKey struct{ line, char int }

// Render delegates to the snapshotted renderer, returning the entire
// .gastro file's HTML in a single macOS-style window.
func (d *Demo) Render() template.HTML { return d.renderer.Render() }

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
	client := d.client.Load()
	if d.degraded || client == nil {
		return "", nil
	}
	h, err := client.Hover(ctx, d.uri, line, char)
	if err != nil {
		return "", err
	}
	if h == nil {
		return "", nil
	}
	return h.Contents.Value, nil
}

// HoverOrDiagnostic is Hover with a fallback: when the LSP returns
// no hover content but a snapshotted diagnostic's range covers the
// position, return the diagnostic message instead. This surfaces
// useful info on identifiers gopls can't hover (typically the
// undefined-reference case, e.g. `p.NAme`), where the diagnostic
// is the only thing the LSP has to say about that span.
//
// The returned string is plain text in that fallback path; md.Render
// handles plain text the same as markdown (it's all goldmark input),
// so the handler doesn't need to switch on which path produced the
// string.
//
// Results are memoised in d.hoverCache keyed by (line, char). The
// embedded source is fixed for the Demo's lifetime so caching is
// safe and bounds the per-request gopls work to one roundtrip per
// distinct position. Errors are NOT cached so a transient LSP
// failure (timeout, gopls restart) can be retried on the next
// request.
func (d *Demo) HoverOrDiagnostic(ctx context.Context, line, char int) (string, error) {
	key := hoverKey{line: line, char: char}
	if v, ok := d.hoverCache.Load(key); ok {
		return v.(string), nil
	}
	hover, err := d.Hover(ctx, line, char)
	if err != nil {
		return "", err
	}
	result := hover
	if result == "" {
		if msg := d.renderer.diagnosticAt(line, char); msg != "" {
			result = msg
		}
	}
	d.hoverCache.Store(key, result)
	return result, nil
}

// Close shuts the LSP subprocess down and removes the temp project
// directory. Idempotent.
func (d *Demo) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	// Clean up the temp project even on a degraded Demo (where
	// projectDir was created but the LSP never came up). Doing this
	// before swapping the client means we still RemoveAll on
	// double-close (no-op the second time).
	if d.projectDir != "" {
		_ = os.RemoveAll(d.projectDir)
		d.projectDir = ""
	}

	c := d.client.Swap(nil)
	if c == nil {
		return nil
	}
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
	// We don't pre-canonicalise the temp dir path: the LSP server
	// canonicalises URIs at every handler boundary, so /var vs
	// /private/var (and other symlinked-path variations) round-trip
	// correctly. See canonicalizeURI in internal/lsp/server/util.go.
	// Reject obviously-bogus GastroSourceRoot values before we splice
	// them into the temp go.mod. A path containing newlines or quotes
	// would let an operator (or a misconfigured Dockerfile) inject
	// arbitrary go.mod directives that gopls would then honour. This
	// is a misconfiguration check, not an attack mitigation — the
	// env var is operator-controlled — but it's free defence in depth.
	if err := validateGastroRoot(opts.GastroSourceRoot); err != nil {
		degrade(demo, logf, "GastroSourceRoot: %v", err)
		return demo, nil
	}

	projectDir, err := os.MkdirTemp("", "gastro-lspdemo-")
	if err != nil {
		degrade(demo, logf, "MkdirTemp failed: %v", err)
		return demo, nil
	}
	// Tighten perms: MkdirTemp creates 0o700 already, but we re-chmod
	// for clarity and so a future change to the helper can't loosen
	// it silently. The temp project is a single-process artefact;
	// nothing else needs to read it.
	_ = os.Chmod(projectDir, 0o700)
	demo.projectDir = projectDir

	if err := writeTempProject(projectDir, opts.GastroSourceRoot); err != nil {
		degrade(demo, logf, "writeTempProject: %v", err)
		_ = os.RemoveAll(projectDir)
		demo.projectDir = ""
		return demo, nil
	}
	demoPath := filepath.Join(projectDir, "components", Filename)
	if err := os.WriteFile(demoPath, []byte(source), 0o600); err != nil {
		degrade(demo, logf, "writing demo file: %v", err)
		_ = os.RemoveAll(projectDir)
		demo.projectDir = ""
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
		_ = os.RemoveAll(projectDir)
		demo.projectDir = ""
		return demo, nil
	}

	uri := "file://" + demoPath
	if err := client.OpenFile(uri, "gastro", source); err != nil {
		_ = client.Close()
		degrade(demo, logf, "OpenFile: %v", err)
		_ = os.RemoveAll(projectDir)
		demo.projectDir = ""
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
		_ = os.RemoveAll(projectDir)
		demo.projectDir = ""
		return demo, nil
	}
	settleDeadline := time.Now().Add(2 * time.Second)
	var diags []lspclient.Diagnostic
	for time.Now().Before(settleDeadline) {
		select {
		case <-bootCtx.Done():
			_ = client.Close()
			degrade(demo, logf, "boot context cancelled during settle: %v", bootCtx.Err())
			_ = os.RemoveAll(projectDir)
			demo.projectDir = ""
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
		_ = os.RemoveAll(projectDir)
		demo.projectDir = ""
		return demo, nil
	}
	demo.renderer = rendered
	demo.client.Store(client)
	demo.uri = uri
	logf("lspdemo: ready (project=%s, %d diagnostic(s))", projectDir, len(diags))
	return demo, nil
}

// validateGastroRoot rejects values that would corrupt the temp
// go.mod when interpolated into a `replace` directive. We require
// an absolute, clean path with no characters that could break out
// of the directive (newline, carriage return, quote).
func validateGastroRoot(root string) error {
	if root == "" {
		return errors.New("empty")
	}
	if !filepath.IsAbs(root) {
		return fmt.Errorf("must be absolute, got %q", root)
	}
	if strings.ContainsAny(root, "\n\r\"") {
		return fmt.Errorf("contains forbidden characters: %q", root)
	}
	return nil
}

// degrade marks the demo as fallback-only and logs the reason. The
// passed-in demo always keeps its pre-built static renderer so the
// homepage still renders something on a degraded boot.
func degrade(d *Demo, logf func(string, ...any), format string, args ...any) {
	d.degraded = true
	d.degradedReason = fmt.Sprintf(format, args...)
	logf("lspdemo: degraded \u2014 "+format, args...)
}

// writeTempProject sets up a minimal go.mod for the temp project so
// gopls can resolve the embedded demo file's only non-stdlib import
// (github.com/andrioid/gastro, used by `gastro.Props()` in the
// shadow generator's rewrite).
//
// We deliberately do NOT copy the example's go.mod. The example
// pulls in chroma + goldmark + their transitive deps to render its
// pages — the demo file imports none of that. Bundling all those
// deps' module sources into the production image costs ~150MB for
// no analysis benefit. Keeping the temp project's go.mod minimal
// means the runtime module cache only needs what gastro itself
// transitively requires (gastro.go.mod → github.com/google/shlex,
// a few hundred KB).
//
// We DO copy the example's go.sum verbatim. A minimal go.mod with
// no go.sum makes `go run` (dev mode) reject the build with
// "missing go.sum entry" — having extra hashes for unused modules
// is harmless, but missing hashes for required ones is fatal. The
// example's go.sum is a comfortable superset.
//
// The replace directive resolves the gastro module to gastroRoot on
// disk. gastroRoot's own go.mod is used by gopls to walk gastro's
// own dependency graph from there.
func writeTempProject(dir, gastroRoot string) error {
	goMod := "module gastro-lspdemo\n\n" +
		"go 1.26.1\n\n" +
		"require github.com/andrioid/gastro v0.0.0\n\n" +
		"replace github.com/andrioid/gastro => " + gastroRoot + "\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0o644); err != nil {
		return err
	}

	// Best-effort: copy the example's go.sum so `go run` doesn't bail
	// on missing hashes for shlex (the one transitive dep). If the
	// example's go.sum isn't where we expect (e.g. an unusual
	// GASTRO_SOURCE_ROOT layout), continue without it — gopls will
	// still work for hover/diagnostics, only `go run`-based dev
	// fallback will complain.
	if sum, err := os.ReadFile(filepath.Join(gastroRoot, "examples", "gastro", "go.sum")); err == nil {
		_ = os.WriteFile(filepath.Join(dir, "go.sum"), sum, 0o644)
	}

	if err := os.MkdirAll(filepath.Join(dir, "components"), 0o755); err != nil {
		return err
	}
	return nil
}
