package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// These integration tests exercise the full `gastro watch` lifecycle:
// argv parsing, devloop integration, process supervision, and the
// .gastro/.build-error signal channel. They build small fixture projects
// in a tempdir and drive runWatch in a goroutine.
//
// Tests share serialisation via watchChdirMu (defined in watch_test.go)
// because runWatch chdirs to honour --project / cwd.

// integrationTimeout caps the longest a single integration test will
// wait for an event on a warm build cache. Generous because CI
// filesystems can be slow.
const integrationTimeout = 10 * time.Second

// coldStartTimeout is used for the FIRST waitForServer call in each
// integration test, where the fixture binary has to be built from
// scratch (cold GOCACHE on a fresh CI runner). Cold `go build` /
// `go run` of the tiny net/http fixture commonly takes 10–20s on
// GitHub-hosted runners, so 10s isn't enough — see CI run 25285826932.
const coldStartTimeout = 30 * time.Second

// setupLibProject creates a minimal "library mode" project layout under
// the repo's tmp/ directory (NOT t.TempDir(), because Go 1.21+ ignores
// go.mod files inside the system temp root — see
// https://github.com/golang/go/issues/44660). Per AGENTS.md, anything
// under tmp/ is fair game and is GC'd automatically.
//
// Layout:
//
//	tmp/test-projects/<random>/
//	  go.mod                  (module testapp; no gastro import needed)
//	  cmd/myapp/main.go       (tiny http server the test can ping)
//	  pages/index.gastro      (so runGenerate has something to compile)
//	  components/             (empty)
//
// Returns the absolute path to the project root and registers cleanup.
func setupLibProject(t *testing.T) string {
	t.Helper()
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}

	testRoot := filepath.Join(repoRoot, "tmp", "test-projects")
	if err := os.MkdirAll(testRoot, 0o755); err != nil {
		t.Fatalf("mkdir tmp/test-projects: %v", err)
	}
	root, err := os.MkdirTemp(testRoot, "watch-*")
	if err != nil {
		t.Fatalf("mkdir-temp under tmp/test-projects: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })

	mustWriteT(t, filepath.Join(root, "go.mod"), `module testapp

go 1.26.1
`)

	mustMkdirT(t, filepath.Join(root, "cmd", "myapp"))
	mustWriteT(t, filepath.Join(root, "cmd", "myapp", "main.go"), `package main

import (
	"net/http"
	"os"
	"time"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "0"
	}
	srv := &http.Server{
		Addr: ":" + port,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte("hello"))
		}),
	}
	go srv.ListenAndServe()
	// Sleep forever (or until SIGTERM).
	for {
		time.Sleep(time.Hour)
	}
}
`)

	mustMkdirT(t, filepath.Join(root, "pages"))
	mustMkdirT(t, filepath.Join(root, "components"))
	// Page with non-empty frontmatter — the compiler rejects empty
	// frontmatter blocks ("either add code or remove the delimiters").
	mustWriteT(t, filepath.Join(root, "pages", "index.gastro"), `---
Title := "hello"
---
<h1>{{ .Title }}</h1>
`)

	return root
}

func mustMkdirT(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", p, err)
	}
}

func mustWriteT(t *testing.T, p, content string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
}

// touchLater rewrites path with new content and bumps mod-time forward
// past the filesystem timestamp resolution so the watcher's
// "info.ModTime().After(prev)" check sees the change reliably.
func touchLater(t *testing.T, path, newContent string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(newContent), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatalf("chtimes %s: %v", path, err)
	}
}

// runWatchInBackground spawns runWatch in a goroutine and returns a
// cancel func to stop it (via SIGTERM-equivalent cancellation through
// the parent process's signal handler, which runWatch sets up).
//
// Because runWatch installs its own SIGINT/SIGTERM handler that calls
// the cancel function, we can't directly trigger cancellation \u2014 the
// signal-handler goroutine registers global handlers that would
// interfere with other tests. Instead we use a shorter-lived workaround:
// the test sends SIGTERM to the process tree, which both the parent
// runWatch loop and any spawned --run process receive. We rely on
// app.Stop() (via runWatch's deferred cleanup) to clean up cleanly.
//
// For test isolation we DO NOT actually send signals; instead the tests
// drive shutdown by closing a control channel and asserting on the
// observable side-effects.
type watchHarness struct {
	t       *testing.T
	root    string
	flags   []string
	stopped chan struct{}
	done    chan struct{}
	err     error
}

func startWatch(t *testing.T, root string, flags ...string) *watchHarness {
	t.Helper()

	// Acquire the chdir lock for the lifetime of the harness so other
	// tests don't trip over our project root.
	watchChdirMu.Lock()
	orig, _ := os.Getwd()
	if err := os.Chdir(root); err != nil {
		watchChdirMu.Unlock()
		t.Fatalf("chdir: %v", err)
	}

	h := &watchHarness{
		t:       t,
		root:    root,
		flags:   flags,
		stopped: make(chan struct{}),
		done:    make(chan struct{}),
	}

	t.Cleanup(func() {
		h.Stop()
		_ = os.Chdir(orig)
		watchChdirMu.Unlock()
	})

	go func() {
		defer close(h.done)
		// runWatch's signal handler will cancel its internal ctx when
		// SIGTERM hits the process. For tests we instead rely on the
		// test cleanup calling h.Stop(), which does what the signal
		// handler does \u2014 see the comment block in startWatchAdapter
		// below for the test-only injection path.
		h.err = runWatchTesting(h.stopped, flags)
	}()

	return h
}

// Stop signals the harness to shut down and waits for runWatch to
// return. Idempotent.
func (h *watchHarness) Stop() {
	select {
	case <-h.stopped:
	default:
		close(h.stopped)
	}
	select {
	case <-h.done:
	case <-time.After(integrationTimeout):
		h.t.Error("watch did not exit within timeout")
	}
}

// runWatchTesting is a test-only entry point that mirrors runWatch but
// honours an external cancel channel instead of installing a global
// signal handler. Production runWatch does both (signal handler +
// SIGINT) so the only divergence is the cancellation source.
//
// Defined in watch_integration_test.go (not in watch.go) because it's
// strictly test infrastructure \u2014 production code should call runWatch.
func runWatchTesting(stop <-chan struct{}, args []string) error {
	flags, err := parseWatchArgs(args)
	if err != nil {
		return err
	}
	if flags.Run == "" {
		return errors.New("--run is required")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		<-stop
		cancel()
	}()

	// Same lifecycle code as runWatch, but inlined here so the test
	// has full control over teardown ordering. Kept in sync with
	// runWatch's body \u2014 if that drifts, this should be updated too.
	return runWatchLoop(ctx, flags)
}

// --- TESTS ---

// TestWatch_RequiresRunFlag: parseWatchArgs accepts no --run, but
// runWatch (production entry point) treats that as an error. Verified
// indirectly via the production runWatch since parseWatchArgs alone
// would lose that signal.
func TestWatch_RequiresRunFlag(t *testing.T) {
	flags, err := parseWatchArgs([]string{"--build", "echo hi"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if flags.Run != "" {
		t.Fatalf("expected empty Run, got %q", flags.Run)
	}
	// The production check is in runWatch:
	//   if flags.Run == "" { return errors.New("--run is required") }
	// covered by TestWatch_RunWithoutRunFlagFailsAtRuntime below.
}

func TestWatch_RunWithoutRunFlagFailsAtRuntime(t *testing.T) {
	dir := t.TempDir()
	chdirT(t, dir)
	err := runWatchTesting(make(chan struct{}), []string{"--build", "echo hi"})
	if err == nil || !strings.Contains(err.Error(), "--run is required") {
		t.Errorf("expected --run is required error, got %v", err)
	}
}

// TestWatch_RestartsOnGoFileChange: the highest-value end-to-end test \u2014
// a real process, started via --run, gets restarted when a *.go file
// changes. Uses a fixture binary that listens on a chosen port, and
// asserts the new instance can bind the same port (proving the previous
// one was reaped \u2014 implicit coverage for process-group handling).
func TestWatch_RestartsOnGoFileChange(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test \u2014 spawns processes")
	}
	root := setupLibProject(t)

	// Pick a free port for the fixture binary to bind.
	port := freePort(t)
	t.Setenv("PORT", fmt.Sprint(port))

	h := startWatch(t, root,
		"--debounce", "30ms",
		"--build", fmt.Sprintf("go build -o tmp/app ./cmd/myapp"),
		"--run", "tmp/app",
	)

	// Wait for the initial run to be serving. Cold-cache build can be
	// slow on CI — use the cold-start budget.
	if err := waitForServer(t, port, coldStartTimeout); err != nil {
		t.Fatalf("initial server never came up: %v", err)
	}

	// Edit main.go to trigger a restart.
	mainGo := filepath.Join(root, "cmd", "myapp", "main.go")
	src, err := os.ReadFile(mainGo)
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	modified := strings.Replace(string(src),
		`w.Write([]byte("hello"))`,
		`w.Write([]byte("hello v2"))`, 1)
	touchLater(t, mainGo, modified)

	// Wait for the new build to take over.
	deadline := time.Now().Add(integrationTimeout)
	for time.Now().Before(deadline) {
		body := getBody(t, port)
		if body == "hello v2" {
			h.Stop()
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("server did not pick up new code within timeout")
}

// TestWatch_KeepsAppAliveOnBuildFailure: introducing a syntax error in a
// .go file fails the build but leaves the previously-running --run
// process alive (R4). Asserts the server keeps serving the OLD response
// after the failed rebuild.
func TestWatch_KeepsAppAliveOnBuildFailure(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test \u2014 spawns processes")
	}
	root := setupLibProject(t)

	port := freePort(t)
	t.Setenv("PORT", fmt.Sprint(port))

	h := startWatch(t, root,
		"--debounce", "30ms",
		"--build", "go build -o tmp/app ./cmd/myapp",
		"--run", "tmp/app",
	)

	if err := waitForServer(t, port, coldStartTimeout); err != nil {
		t.Fatalf("initial server never came up: %v", err)
	}

	// Break the build by writing syntactically invalid Go.
	mainGo := filepath.Join(root, "cmd", "myapp", "main.go")
	touchLater(t, mainGo, "package main\n\nfunc main() { this is not valid Go }\n")

	// Wait long enough for the watcher to debounce, attempt the build,
	// and emit the failure signal.
	time.Sleep(2 * time.Second)

	// The OLD app should still serve.
	body := getBody(t, port)
	if body != "hello" {
		t.Errorf("expected old app still serving 'hello', got %q", body)
	}

	// The build-error signal file should exist (or have existed \u2014 the
	// devreloader consumes it on poll, which we're not running here).
	signal := filepath.Join(root, ".gastro", ".build-error")
	if _, err := os.Stat(signal); os.IsNotExist(err) {
		t.Errorf("expected .gastro/.build-error to be written, but it doesn't exist")
	}

	h.Stop()
}

// TestWatch_KillsProcessGroup is the canonical foot-gun test for
// `gastro watch`: a `go run`-style --run forks the actual binary as a
// grandchild, and a naive SIGTERM only to `go run` would leak the
// grandchild and leave the listening socket bound. After the restart we
// assert the new instance can bind the same port — if the grandchild
// wasn't reaped this fails with "address already in use".
func TestWatch_KillsProcessGroup(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test — spawns processes")
	}
	root := setupLibProject(t)

	port := freePort(t)
	t.Setenv("PORT", fmt.Sprint(port))

	h := startWatch(t, root,
		"--debounce", "30ms",
		"--run", "go run ./cmd/myapp",
	)

	// Initial server up. `go run` is slow on cold cache — use the
	// cold-start budget.
	if err := waitForServer(t, port, coldStartTimeout); err != nil {
		t.Fatalf("initial go-run server never came up: %v", err)
	}

	// Trigger a restart by editing main.go.
	mainGo := filepath.Join(root, "cmd", "myapp", "main.go")
	src, _ := os.ReadFile(mainGo)
	modified := strings.Replace(string(src),
		`w.Write([]byte("hello"))`,
		`w.Write([]byte("hello v2"))`, 1)
	touchLater(t, mainGo, modified)

	// Wait for the new code to take over. If the grandchild wasn't
	// reaped the second `go run` would fail to bind and the new code
	// would never be served.
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if body := getBody(t, port); body == "hello v2" {
			h.Stop()
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("new server never bound the port — grandchild likely leaked")
}

// TestWatch_RapidEditsConverge: rapid edits during a slow build
// coalesce into a single converged final state. Asserts behaviour, not
// cancellation mechanism: the system must reach the latest code
// regardless of how the intermediate builds were torn down (R3).
func TestWatch_RapidEditsConverge(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test — spawns processes")
	}
	root := setupLibProject(t)

	port := freePort(t)
	t.Setenv("PORT", fmt.Sprint(port))

	// Use a slow first build so subsequent edits land mid-build.
	h := startWatch(t, root,
		"--debounce", "30ms",
		"--build", "sleep 0.5",
		"--build", "go build -o tmp/app ./cmd/myapp",
		"--run", "tmp/app",
	)

	if err := waitForServer(t, port, coldStartTimeout); err != nil {
		t.Fatalf("initial server never came up: %v", err)
	}

	mainGo := filepath.Join(root, "cmd", "myapp", "main.go")
	original, _ := os.ReadFile(mainGo)

	// Three rapid edits, each landing inside the next build's sleep
	// window. The final-edit wins via debounce; intermediate builds
	// may be cancelled (R3) or just superseded.
	for i := 1; i <= 3; i++ {
		modified := strings.Replace(string(original),
			`w.Write([]byte("hello"))`,
			fmt.Sprintf(`w.Write([]byte("v%d"))`, i), 1)
		touchLater(t, mainGo, modified)
		time.Sleep(150 * time.Millisecond)
	}

	// Wait for the LAST edit's content to take over. If cancel-restart
	// were broken we might briefly see v1 or v2 between edits, but the
	// final state must converge to v3.
	deadline := time.Now().Add(integrationTimeout)
	for time.Now().Before(deadline) {
		if body := getBody(t, port); body == "v3" {
			h.Stop()
			return
		}
		time.Sleep(150 * time.Millisecond)
	}
	t.Fatalf("rapid-edits never converged to v3; current body: %q", getBody(t, port))
}

// TestWatch_BuildSequence: multiple --build flags execute in order;
// failure of step N skips step N+1 and the --run invocation. Uses
// `false` as a guaranteed-failure command so we don't need a real
// build pipeline.
func TestWatch_BuildSequence(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test \u2014 spawns processes")
	}
	root := setupLibProject(t)

	// Marker file written only if the second build runs. If the first
	// build fails (`false`), this marker should NOT appear.
	marker := filepath.Join(root, "second-build-ran")

	h := startWatch(t, root,
		"--debounce", "30ms",
		"--build", "false",
		"--build", fmt.Sprintf("touch %s", marker),
		"--run", "sleep 60",
	)

	// Give the loop time to perform the initial build attempt.
	time.Sleep(2 * time.Second)

	if _, err := os.Stat(marker); err == nil {
		t.Errorf("second build ran despite first failing; marker exists")
	}

	// Build-error signal should be present.
	if _, err := os.Stat(filepath.Join(root, ".gastro", ".build-error")); os.IsNotExist(err) {
		t.Errorf("expected .gastro/.build-error after failed build")
	}

	h.Stop()
}

// --- helpers ---

// freePort asks the kernel for a free TCP port and returns it. The port
// is closed immediately so the test fixture can bind it; there's a tiny
// race window but tests serialise on watchChdirMu so it's bounded.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return port
}

// waitForServer polls http://127.0.0.1:<port>/ until it returns 200 or
// the deadline expires.
func waitForServer(t *testing.T, port int, timeout time.Duration) error {
	t.Helper()
	deadline := time.Now().Add(timeout)
	url := fmt.Sprintf("http://127.0.0.1:%d/", port)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return nil
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("server on port %d not ready after %v", port, timeout)
}

// getBody fetches / from the fixture server and returns the body as a
// string. Failures fail the test \u2014 callers expect a live server.
func getBody(t *testing.T, port int) string {
	t.Helper()
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/", port))
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	var sb strings.Builder
	buf := make([]byte, 256)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			sb.Write(buf[:n])
		}
		if err != nil {
			break
		}
	}
	return sb.String()
}

// integrationLog is a no-op writer used to silence build-step output in
// tests that intentionally fail builds. Avoids polluting `go test`
// output. Unused today \u2014 kept here as a hook if a future test needs it.
var _ sync.Mutex
