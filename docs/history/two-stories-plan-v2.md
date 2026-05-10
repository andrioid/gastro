# Two Stories — Plan (v2.1)

> Status: **✅ complete — all six phases shipped 2026-05-03**. Recorded in DECISIONS.md.
> Author: brainstorming session 2026-05-03; revised same-day after Q&A; v2.1 closes the browser-surface gap surfaced during plan review.
> Supersedes: `tmp/two-stories-plan.md` (v1), `tmp/two-stories-plan-v2.md` (v2 — same file, history in §Iteration scratchpad).
> Companion brief: `tmp/go-tool-tradeoffs.html` (kept as snapshot of prior thinking; banner added pointing here).
>
> **Implementation status (per phase):**
> - Phase 1 — ✅ shipped (`writeGoFile` skip-if-equal + atomic write; 3 tests)
> - Phase 2 — ✅ shipped (`internal/devloop` extracted; 11 tests; `gastro dev` behaviourally equivalent)
> - Phase 3 — ✅ shipped (empty pages/components warning; 4 tests)
> - Phase 4 — ✅ shipped (`gastro watch` + process-group exec + `build-error` SSE + flag rejection on `dev`; 14 unit + 6 integration + 3 SSE tests)
> - Phase 5 — ✅ shipped (README two-mode quick start; `docs/getting-started-library.md` new; framework `getting-started.md` updated; `dev-mode.md` reorganised; `contributing.md` framework/library section; scaffold README mentions `gastro watch`; `examples/gastro` sidebar + new docs page)
> - Phase 6 — ✅ shipped (this DECISIONS entry; `tmp/go-tool-tradeoffs.html` banner; full verification suite green)
>
> **Notable deviations from the plan-as-written:**
> - Phase 2 seed ordering: plan said "OnRestart called once at startup, before the watcher starts processing events." Implementation initially did `Generate → OnRestart → seed → watch`, but tests caught a race where the seed could see post-edit state. Re-ordered to `Generate → seed → OnRestart → watch` so callers waiting on the initial OnRestart can trust the seed is complete. The plan's contract is preserved (and arguably strengthened); documented inline in `internal/devloop/devloop.go`.
> - Phase 4 — R3 cancel-restart at the build level: plan said "cancel the in-flight build's `context.Context`, which cancels the underlying `exec.Cmd`." Implementation does this via a `buildCancel` per-call. Build commands are NOT placed in their own process group (only `--run` is), so compound shell builds like `sh -c 'sleep 5; touch x'` may leak orphaned children when cancelled. Acceptable for v1: the plan's risk #2 explicitly notes "build commands run with cancellable context; partial-write protection comes from Phase 1; user-`--build` outputs (typically Go binaries) are atomic via `go build`." If users start running long-lived/forking builds, revisit.
> - Phase 4 — integration tests under `tmp/test-projects/` (NOT `t.TempDir()`): Go 1.21+ ignores `go.mod` in the system temp root (e.g. macOS `/var/folders/...`), so `t.TempDir()` doesn't work for projects that need to build. Per AGENTS.md, `tmp/` is the right location and is auto-GC'd.

## 0. What this plan covers

Document gastro's two project shapes — **framework mode** (scaffolded
greenfield) and **library mode** (gastro added to an existing Go project)
— as peer stories across the README, getting-started docs, and CLI. The
runtime API stays identical between modes; what differs is the bootstrap,
the layout, and the command used for the dev loop.

The plan also ships the load-bearing CLI change that makes the library
story work: a new `gastro watch` subcommand that gives library-mode
projects a one-command hot-reload experience comparable to what framework
projects get from `gastro dev`. Crucially, `gastro watch` watches both
`.gastro` sources **and** the user's `*.go` files (with sensible excludes)
so library-mode users editing `cmd/myapp/main.go` get the same loop as
they do for `internal/web/pages/dashboard.gastro`.

Pure-watch composition (gastro as a piece in someone else's pipeline)
stays on the existing `watchexec` recipe — no new surface for that
audience.

Foundational install-method work (`tool` directive in examples + scaffold,
VSCode auto-detect, LSP version logging, equal-peer install docs) shipped
earlier on 2026-05-03 and is the foundation this plan builds on.

## 1. Goals

1. A user reading the README within 30 seconds can identify which mode
   applies to them and reach the right deeper page.
2. Library-mode users get a **one-command** dev loop with hot reload —
   covering both `.gastro` and `*.go` edits — without picking a
   third-party file watcher.
3. The runtime stays identical between modes. No "library-mode-only" or
   "framework-mode-only" runtime APIs.
4. `gastro dev` stays a no-flags command, forever. The scaffold's
   batteries-included experience is preserved.
5. Smart classification (template-only edit ≠ binary restart) works in
   both modes.
6. **Build-failure resilience:** when a build fails after a code change,
   the previously-running binary stays alive so the user can keep
   clicking through their app while they fix the error. The browser-side
   surface for v1 is a `console.warn()` over the existing SSE channel; a
   full visible banner is deferred (see §10).

## 2. Anti-goals

- **Adding a third-party runner dependency** (air, wgo, watchexec, etc.).
  Library-mode users get gastro-only tooling out of the box. We are
  building a focused, gastro-aware subset of what `air` does so users
  don't have to install `air`.
- **Making `gastro dev` configurable.** The scaffold default is the only
  thing it knows. Configuration lives on `gastro watch`.
- **Shipping a "pure watcher" command** (`gastro watch` without `--run`).
  Power users compose with the existing `watchexec` recipe; first-class
  pure-watch surface is deferred until an adopter asks.
- **An IPC service or daemon** between LSP and CLI. Independent processes
  with disjoint write paths are sufficient (verified: LSP writes only to
  `os.MkdirTemp("", "gastro-shadow-*")`).
- **Reframing the runtime around "framework mode" vs "library mode" types
  or options.** The mode distinction is documentary and ergonomic, not
  architectural.
- **Windows support in `gastro watch` v1.** No project doc currently
  claims Windows; we don't add the cross-platform process-group
  complexity yet.

## 3. Resolved decisions (was: open questions)

The brainstorm raised nine open questions; all are now resolved. Recap
below for traceability.

| # | Decision |
|---|---|
| Q1 (naming) | **framework / library**, used everywhere — CLI hints, anti-pattern guardrails, doc bodies. |
| Q2 (decision surface) | Both. README quick-start asks; `docs/getting-started.md` *stays as the framework page* (today's content) and a new sibling page covers library mode. |
| Q3 (framework primary install) | All three install methods stay as peers (already shipped). Framework getting-started leads with global install (`mise` / `go install`); library getting-started leads with `go get -tool`. |
| Q4 (library-mode shape) | **Components-only first, embedded-package second.** Smallest possible integration leads ("render one component into your existing handler"); full `gastro.New(...)` wiring is the larger pattern shown second. |
| Q5 (`gastro dev` flag rejection) | Reject with the multi-line hint shown in Phase 4 (`gastro dev: unknown flag --build` / `gastro dev takes no flags. For custom build/run commands, use:` / `  gastro watch --build '...' --run '...'`). One canonical wording, defined in Phase 4 and referenced from tests. |
| Q6 (`gastro watch` flag shape) | `--run COMMAND` required (single); `--build COMMAND` optional and **repeatable** for multi-step pipelines (e.g. tailwind + go build). |
| Q7 (dev → watch mechanism) | Shared `internal/devloop` package. No subprocess. One binary to test. |
| Q8 (`tmp/go-tool-tradeoffs.html` update) | Leave as snapshot. Add a one-line banner at the top pointing to this plan. |
| Q9 (library-mode anchor use case) | Lead with **admin UIs in existing services**; explicitly mention internal tools, dashboards, status pages, and server-rendered marketing pages alongside an API-first product. Framing umbrella: *"add a UI to your otherwise-headless Go service."* |

### Additional decisions surfaced during round-2 follow-ups

| # | Decision |
|---|---|
| R1 | `gastro watch` watches `*.go` in addition to `.gastro` / markdown / static. Without this, library-mode users editing `cmd/myapp/main.go` would hit a UX cliff. |
| R2 | `*.go` watching uses **hardcoded excludes** (`.gastro/`, `vendor/`, `node_modules/`, `.git/`, `tmp/`) plus a repeatable `--exclude PATH` flag for per-project additions. |
| R3 | Mid-build change handling: **cancel the in-flight build process**, start fresh with the latest sources. Fastest convergence on rapid edits. |
| R4 | Build-failure semantics: **previous `--run` stays alive** while user fixes the error. Failure surfaces in terminal *and* via the existing browser SSE channel as a `console.warn()` (browser DevTools console). A full visible banner UI is **deferred** to a follow-up — closing the smallest gap that makes the failure visible in the browser without designing/testing a UI surface in this plan. |
| R5 | User-facing wording: **framework / library** appears in headings and body copy throughout, not just internal naming. |
| R6 | Shell-string splitting: **approve `github.com/google/shlex`** as a new third-party dependency. ~150 LOC, BSD-3, zero transitive deps. The first new runtime/CLI dep added under this plan; called out explicitly per AGENTS.md. |

## 4. Strategic stance

### What changed since the 2026-05-02 frictions-plan resolutions

The 2026-05-02 strategic-stance entry already resolved that **both modes
are equal**. This plan acts on that resolution by splitting the
documentation surface accordingly. No reversal of any prior decision.

The **D1 (`gastro watch` for library-mode users)** item that was deferred
in `frictions-plan.md:241` is now reactivated, but with a different shape
than originally proposed:

- Original D1: pure watcher, no process management, library-mode users
  compose with their own runner.
- This plan: `gastro watch` manages the binary via user-supplied
  `--run` / `--build` commands, watches both `.gastro` *and* `*.go`
  sources, keeps the previous binary alive on build failure, and gives
  library-mode users the same one-command experience framework users
  have. No third-party runner needed.

The reasoning that originally deferred D1 ("no adopter has asked +
permanent API surface for unvalidated use case") is addressed:

- The two-stories framing makes library-mode a peer story, not niche.
- The API surface validates against a clear use case (one-command dev for
  library-mode projects) rather than power-user composition.
- Power-user composition (the original D1's audience) stays on the
  existing watchexec recipe; no new surface for that audience.

### The "we built a focused subset of `air`" framing

The library getting-started page should say plainly: *"We built a small,
focused, gastro-aware version of what `air` does so you don't have to
install `air`."* This actively defends anti-goal #1 ("no third-party
runner dependency") rather than merely declaring it. It also sets correct
expectations: `gastro watch` is not a general-purpose Go file watcher;
it's the gastro project's hot-reload tool that happens to also watch your
`*.go` files because the alternative would be a UX cliff.

### Rule preserved

The 2026-05-02 strategic-stance rule 2 ("frontmatter stays plain Go,
template body stays plain `html/template`") is unaffected.

## 5. Anti-pattern guardrails

If during implementation we find ourselves doing any of the following,
stop and re-discuss:

- Adding mode-specific runtime APIs (e.g. `gastro.NewLibrary(...)` vs
  `gastro.NewFramework(...)`)
- Adding flags to `gastro dev`
- Adding a third dev command (we have `dev` and `watch`; that's it)
- Building any kind of process-coordination layer between LSP and CLI
- Recommending a specific third-party runner in CLI output
- Growing `gastro watch` into a general-purpose file watcher
  (e.g. arbitrary file extensions beyond `.gastro` / `.md` / `.go` /
  `static/*` — out of scope for this plan)
- Adding Windows-specific code paths to `gastro watch` v1
- Adding a second new third-party dependency on top of `google/shlex`
  without re-confirming per AGENTS.md

## 6. Work plan

Six phases, ordered by dependency. Phases 1–3 are foundational and can
ship independently. Phase 4 depends on 1+2. Phases 5–6 follow.

### Phase 1 — `writeGoFile` skip-if-equal + atomic write

**File:** `internal/compiler/compiler.go` (function `writeGoFile`, ~line 1138).

**Change:**
```go
func writeGoFile(path string, src []byte) error {
    formatted, err := goformat.Source(src)
    if err != nil { return … }

    // Skip the write if bytes match — preserves smart-classification
    // benefits for external runners watching mod-times, and reduces git
    // churn in committed-.gastro/ setups.
    if existing, err := os.ReadFile(path); err == nil && bytes.Equal(existing, formatted) {
        return nil
    }
    // Atomic write: write to temp, then rename. Prevents half-written
    // .gastro/*.go files if the watcher is killed mid-write.
    tmp := path + ".tmp"
    if err := os.WriteFile(tmp, formatted, 0o644); err != nil {
        return err
    }
    return os.Rename(tmp, path)
}
```

**Tests:** add to `internal/compiler/compiler_test.go`:
- `TestWriteGoFile_SkipsWhenIdentical` — calling twice with the same
  content does not bump mod-time on the second call.
- `TestWriteGoFile_AtomicOnFailure` — simulating a write failure
  (e.g. read-only target) leaves no half-written file.

**Acceptance:** `mise run test` green with `-race`; `gastro check` clean
against all four examples after a regen pass.

**Independence:** ships standalone. Useful regardless of whether the
rest of the plan ships.

**Pre-existing issue note:** this fixes a real issue independent of the
two-stories plan — committed-`.gastro/` setups currently get spurious
git diffs after every `gastro generate`, which is friction for downstream
teams that opted into committing the tree.

### Phase 2 — Extract watcher loop into `internal/devloop`

**Files:**
- New: `internal/devloop/devloop.go`
- New: `internal/devloop/devloop_test.go`
- Modified: `cmd/gastro/main.go` (`runDev`)

**Behavioural goal:** `gastro dev`'s observable behaviour is preserved
(same files watched, same debounce, same restart-class semantics, same
reload signal, same logs). The runtime is the same binary path; nothing
changes from the user's perspective. Existing `gastro dev` integration
tests pass unchanged.

**Refactor surface:**
```go
package devloop

type Config struct {
    ProjectRoot   string
    DebounceDelay time.Duration       // default 100ms
    Strict        bool                // false for dev/watch
    OnRestart     func(ctx context.Context) error // called on restart-class events
    Logger        *slog.Logger
    Quiet         bool                // suppresses per-change logs

    // R1 + R2 — only set by gastro watch; gastro dev leaves these zero
    // and the package falls back to .gastro / markdown / static only.
    WatchGoFiles  bool
    ExtraExcludes []string            // additional paths to ignore
}

// Run blocks until ctx is cancelled. Watches sources, regenerates,
// writes the reload signal, and invokes OnRestart on restart-class
// changes. OnRestart is also called once on initial start so the caller
// doesn't need to bootstrap the binary itself. Returns nil on clean
// cancellation.
func Run(ctx context.Context, cfg Config) error
```

`runDev` is rewritten to call `devloop.Run` with `OnRestart` set to a
closure that invokes `startApp` (the build + exec lifecycle). `WatchGoFiles`
is `false` for `gastro dev`. Behaviour of `gastro dev` is **behaviourally
equivalent** to today (same observable behaviour under the existing test
suite plus a new framework-mode smoke); the internal call graph differs
because the watcher loop now lives in `internal/devloop`.

**`writeReloadSignal()` placement:** stays in `cmd/gastro/main.go` and
is injected into `devloop.Config` as a new `OnReload func()` hook called
after every successful regeneration. Keeping it in `cmd/gastro` avoids
making `internal/devloop` depend on the project root path-resolution
logic (`.gastro/.reload` is written relative to the cwd, which the CLI
already owns via `applyGastroProject`). `gastro watch`'s caller passes
the same hook.

**`OnRestart` contract** (sharpened from v1):
- Called once at startup with a fresh context, before the watcher
  starts processing events. This lets the framework `gastro dev` path
  start its scaffold binary without a separate bootstrap call.
- Called again on every restart-class change. The `ctx` argument is
  cancelled when a *newer* change arrives mid-build (R3 cancel-restart).
- Errors are logged but do not terminate `Run`. Caller decides whether
  to keep the previous binary alive (R4) or take other action.

**Tests:** existing `gastro dev` integration tests must pass unchanged.
New unit tests for `devloop.Run` covering:
- Clean cancellation on `ctx.Done`
- Debouncing (burst of 5 changes within 100ms = 1 regen)
- OnRestart invoked exactly once on initial start
- OnRestart invoked exactly once per restart-class change after that
- OnRestart not invoked for template-only changes
- Markdown deps tracked correctly (regression coverage for `extDeps`)
- With `WatchGoFiles=true`: editing `cmd/myapp/main.go` triggers a
  restart; editing `vendor/foo/bar.go` does not; editing
  `tmp/whatever.go` does not (default exclude); editing
  `custom/path/x.go` does not when `ExtraExcludes` includes `custom/`
- With `WatchGoFiles=true` and a mid-build change: the first OnRestart's
  `ctx` is cancelled before the second OnRestart starts

**Acceptance:** `mise run test` green with `-race`; `examples/blog &&
gastro dev` works end-to-end (manual smoke).

### Phase 3 — Empty-`pages/components` warning

**File:** `internal/devloop/devloop.go`.

**Change:** at startup, check whether `pages/` and `components/` exist
and contain any `.gastro` files. If empty, emit a warning and continue:

```
gastro: pages/ has no .gastro files yet — watching anyway, will pick up new ones
gastro: components/ has no .gastro files yet — watching anyway, will pick up new ones
gastro: watching /repo/internal/web
```

**Behaviour:** non-fatal warning. Polling watcher already detects new
files via existing logic, so no new code needed beyond the warning.

**Tests:** new test that runs `devloop.Run` against a tempdir with empty
`pages/` and `components/`, verifies stderr contains the warning, verifies
the run does not exit early.

**Independence:** can ship with phase 2 or as a follow-up; touches the
same code.

### Phase 4 — `gastro watch` subcommand

This is the biggest phase. Sized **L** (revised up from M in v1) given
the breadth of process supervision concerns surfaced in Q&A.

**Files:**
- Modified: `cmd/gastro/main.go` (new subcommand handler `runWatch` +
  flag-rejection on `runDev` + new `writeBuildErrorSignal()` helper
  alongside the existing `writeReloadSignal()`)
- Modified: `cmd/gastro/main.go` (`printUsage`)
- New: `cmd/gastro/process.go` (process-group exec + signalling helpers,
  factored out of `runDev`'s existing logic)
- Modified: `pkg/gastro/devreload.go` (new `build-error` event type +
  `.gastro/.build-error` signal-file polling + 1-line addition to
  `reloadScript` for the `console.warn` listener)
- Modified: `pkg/gastro/devreload_test.go` (new tests below)
- Modified: `go.mod` / `go.sum` (add `github.com/google/shlex`)

**Surface:**
```
$ gastro watch --run COMMAND [--build COMMAND]... [flags]

Watch .gastro and Go sources, regenerate on change, signal browser
reloads, and manage your application binary with smart classification.
For projects where 'gastro new' conventions don't apply.

We built this so you don't have to install air, wgo, or watchexec
just to get a hot-reload loop for a gastro-in-Go-project setup.
For more advanced or composed workflows, see the watchexec recipe
in docs/dev-mode.md.

Required:
  -r, --run COMMAND       Command to run your binary
                          (e.g. "go run ./cmd/myapp" or "tmp/app --port 8080")

Optional:
  -b, --build COMMAND     Command to compile before each (re)start.
                          Repeat for multi-step pipelines:
                            --build "tailwindcss -i in.css -o out.css"
                            --build "go build -o tmp/app ./cmd/myapp"
                          On build failure, the previous --run keeps running.
  -p, --project PATH      Path to the gastro project root
                          (defaults to GASTRO_PROJECT env, then cwd)
      --exclude PATH      Path to ignore when watching .go files.
                          Repeat for multiple. Hardcoded defaults already
                          exclude .gastro/, vendor/, node_modules/,
                          .git/, and tmp/.
      --debounce DUR      Coalesce burst changes (default 100ms)
  -q, --quiet             Suppress per-change logs
```

**`gastro dev` flag rejection (Q5) — canonical wording:**
```
gastro dev: unknown flag --build
gastro dev takes no flags. For custom build/run commands, use:
  gastro watch --build '...' --run '...'
```

This is the single source of truth for the rejection wording; tests
(`TestDev_RejectsUnknownFlags`) assert against this exact string.

#### 4a. File-watching scope (R1 + R2)

`devloop.Run` is called with `WatchGoFiles=true` and `ExtraExcludes` set
from the user's `--exclude` flags. The watcher scans:

- `*.gastro` everywhere under `ProjectRoot`
- `*.md` everywhere under `ProjectRoot` (existing behaviour, drives
  markdown-import regen via `extDeps`)
- `static/**` (existing behaviour)
- **NEW:** `*.go` everywhere under `ProjectRoot` *except* paths matching
  the exclude set

**Hardcoded excludes** (always on, not user-removable in v1):
```
.gastro/      // generated tree — would loop forever
vendor/       // 3rd party
node_modules/ // npm projects in the same repo
.git/         // self-evident
tmp/          // convention; build outputs typically go here
```

**Edit classification for `.go` files:** all `.go` changes are
**restart-class** (no smart partial). The smart classification machinery
in `internal/watcher` continues to apply only to `.gastro` files.

**Build-output collision warning (revised from v1 risk row 5):** at
startup, if any `--build` command appears to write into a watched path
(simple substring check for `-o ./tmp/`, `-o tmp/`, or paths under
known-watched dirs other than `tmp/`), emit a warning. Non-fatal —
the user may have a good reason — but visible.

#### 4b. Process supervision

`cmd/gastro/process.go` exposes a small helper:
```go
type App struct {
    cmd    *exec.Cmd
    pgid   int
    cancel context.CancelFunc
}

// Start spawns the process in its own process group (setpgid) so we
// can signal the whole tree on shutdown. shlex-splits the command.
func Start(ctx context.Context, command string, env []string) (*App, error)

// Stop sends SIGTERM to the process group, waits up to 5s, then SIGKILL.
// Idempotent and safe to call on a nil receiver.
func (a *App) Stop()

// Wait blocks until the process exits.
func (a *App) Wait() error
```

**Why process groups:** `--run "go run ./cmd/myapp"` spawns `go run`,
which spawns the actual binary as a grandchild. Without `setpgid` +
`Kill(-pgid, SIGTERM)`, killing `go run` leaves the grandchild bound to
the port. This is the canonical foot-gun for hot-reload tools and the
single most likely source of "first restart works, second one says port
in use" bug reports.

**SIGTERM grace period:** 5s, then SIGKILL. Not configurable in v1.

**`go run` interaction note:** even with process groups handled
correctly, `--run "go run …"` is slower than `--build "go build -o
tmp/app …" --run "tmp/app"` because `go run` re-links every invocation.
Documented as a tradeoff, not enforced.

#### 4c. Build phase

For each restart-class change (after debounce):

1. If a build is already in flight, **cancel it** (R3). The build's
   `context.Context` is cancelled, which cancels the underlying
   `exec.Cmd`. Wait briefly for it to exit cleanly.
2. Run each `--build` command in order. If any returns non-zero:
   - Log the failure with the failing command + stderr.
   - Emit a `build-error` SSE event so the browser tab logs a
     `console.warn("gastro: build failed — previous version still
     serving\n<command>\n<stderr>")` (no visible banner in v1; see
     §4d for the devreload changes).
   - **Do not stop the previously-running `--run` process** (R4).
   - Return to watching. The next change kicks off a fresh attempt.
3. If all `--build` commands succeed:
   - Stop the previously-running `--run` process (5s grace).
   - Start a fresh `--run`.
   - Trigger a normal reload (the existing `reload` SSE event), which
     navigates the page and implicitly clears any prior `build-error`
     state on the client.

#### 4d. Devreload SSE: new `build-error` event type

**File:** `pkg/gastro/devreload.go` (+ `pkg/gastro/devreload_test.go`).

The existing devreload infrastructure has one event type (`reload`) and
a content-less signal file (`.gastro/.reload`). Phase 4c needs a second
event type so the browser learns about build failures without a full
page reload.

**Wire format:** add a second signal file `.gastro/.build-error`. When
the CLI writes it, the file's contents are the failure message (failing
command + stderr, UTF-8). The watcher in `pkg/gastro/devreload.go`
polls for both signal files in the existing tick loop, deletes
whichever exists, and broadcasts a typed event to subscribers.

**Subscribe API change:** the existing channel-of-`struct{}` Subscribe
becomes a channel of a small `reloadEvent` struct:

```go
type reloadEvent struct {
    Kind string // "reload" or "build-error"
    Data string // empty for reload; failure message for build-error
}
```

`HandleSSE` switches on `Kind` and emits either `event: reload` (data
`reload`, unchanged on the wire) or `event: build-error` (data is the
failure message, newline-stripped per SSE rules).

**Client script change:** `reloadScript` adds one listener:

```js
e.addEventListener("build-error", function(ev){
  console.warn("gastro: build failed — previous version still serving\n" + ev.data);
});
```

The existing `reload` handler (which calls `location.reload()`)
implicitly clears the warning state on the client by navigating away.
No separate `build-ok` event in v1.

**CLI side:** `cmd/gastro/main.go` gets a sibling to `writeReloadSignal()`:

```go
func writeBuildErrorSignal(msg string) error
```

Written atomically (write-to-tmp + rename) so the watcher never reads a
half-written file. `gastro watch`'s build-failure path calls it; `gastro
dev` does not (no per-build user step in framework mode that can fail
in a user-recoverable way — `go build` failures already surface
through the existing `startApp` log path).

**Banner UI is explicitly out of scope** (see §10). v1 ships the
transport layer plus a `console.warn` so the failure is visible to any
developer with DevTools open. Adding a styled banner means designing it,
adding CSS to `reloadScript` (currently a single `<script>` line, no
styles), and dealing with banner-position conflicts with arbitrary user
stylesheets — explicitly deferred.

**Partial-write protection during cancel-restart:** Phase 1's atomic
`writeGoFile` (write-to-tmp + rename) ensures cancellation never leaves
the `.gastro/` tree half-written. User `--build` outputs are the user's
responsibility — `go build` itself is atomic, so the common case is fine.

#### 4e. Tests

New integration tests in `cmd/gastro/`:

- `TestWatch_RunsAndRestartsOnFrontmatterChange` — builds a temp
  project, runs `gastro watch --run 'sleep 60'`, modifies frontmatter,
  verifies the sleep is killed and re-spawned.
- `TestWatch_DoesNotRestartOnTemplateOnlyChange` — same setup, modifies
  template body only, verifies the sleep is *not* killed (smart
  classification holds).
- `TestWatch_RestartsOnGoFileChange` — modifies `cmd/myapp/main.go`,
  verifies restart.
- `TestWatch_DoesNotRestartOnExcludedGoFile` — `vendor/x/y.go` and
  `tmp/leftover.go` changes are ignored.
- `TestWatch_HonoursExtraExcludes` — `--exclude custom/` works.
- `TestWatch_RequiresRunFlag` — `gastro watch` with no `--run` exits
  with an error pointing at the docs.
- `TestWatch_BuildSequence` — multiple `--build` flags execute in order;
  failure of step N skips step N+1 and the `--run` invocation.
- `TestWatch_KeepsAppAliveOnBuildFailure` — start app, break code so
  build fails, verify previous app process is still alive after the
  build error.
- `TestWatch_CancelsInflightBuildOnRapidEdits` — start a slow `--build`
  (e.g. `sleep 5`), trigger a second change before it completes, verify
  the first build's process gets SIGTERM and the second build runs to
  completion. Race-sensitive; uses synchronisation channels rather than
  sleeps.
- `TestWatch_KillsProcessGroup` — `--run 'go run ./cmd/myapp'` against a
  fixture binary that binds a port; restart; verify the new binary can
  bind the same port (proves the grandchild was reaped).
- `TestWatch_ShlexHandlesQuotedArgs` — `--build "go build -ldflags '-X
  main.version=1.0' ./..."` parses the way `sh` would.
- `TestWatch_BuildErrorEmitsSSEEvent` — start a `--run`, trigger a
  build failure, subscribe to `/__gastro/reload` from a test client,
  verify a `build-error` event arrives with the failure message in the
  data field.
- `TestDev_RejectsUnknownFlags` — `gastro dev --build x` exits with the
  exact canonical hint defined in §4 above (string compare against the
  same constant the production code uses).

New unit tests in `pkg/gastro/devreload_test.go`:

- `TestDevReloader_BroadcastsBuildError` — write `.gastro/.build-error`
  with a payload, verify subscribers receive a `reloadEvent{Kind:
  "build-error", Data: <payload>}`.
- `TestDevReloader_HandleSSE_BuildErrorWireFormat` — connect to
  `HandleSSE`, broadcast a build-error, verify the response body
  contains `event: build-error\ndata: <message>\n\n` per SSE spec
  (newlines in the message are folded to multiple `data:` lines).
- `TestDevReloader_ReloadStillWorks` — regression: existing `reload`
  event format is unchanged on the wire.

**Acceptance:** all tests green with `-race`; manual smoke against
`examples/blog` (framework) confirms `gastro dev` unchanged; manual smoke
against a hand-crafted library layout confirms `gastro watch --run …`
works end-to-end including the typo-survival case (R4) and the rapid-edit
case (R3).

### Phase 5 — Documentation: split framework vs library

**Files:**
- Rewritten: `README.md` (Quick Start section)
- Updated: `docs/getting-started.md` (stays as the **framework** page,
  lightly updated; keep the URL `/docs/getting-started`)
- New: `docs/getting-started-library.md` (the **library** page;
  proposed URL `/docs/getting-started-library`)
- Updated: `docs/dev-mode.md` (`gastro watch` section; existing watchexec
  recipe stays as the "pure-watch composition" power-user note)
- Updated: `docs/contributing.md` (notes the framework/library framing)
- Updated: `internal/scaffold/template/README.md.tmpl` (mention `gastro dev`
  primary, link to `gastro watch` for non-scaffold layouts)
- Updated: `examples/gastro/components/docs-layout.gastro` (sidebar gets
  a new "Getting Started (existing project)" entry under "Documentation")
- New: `examples/gastro/pages/docs/getting-started-library.gastro`
  (renders `docs/getting-started-library.md`)

**README Quick Start shape:**
```
## Two ways to use gastro

### Framework mode — building a new site

# install (pick one):
mise use github:andrioid/gastro@latest
# or: go install github.com/andrioid/gastro/cmd/gastro@latest

gastro new myapp && cd myapp
gastro dev
→ Framework getting started: docs/getting-started.md

### Library mode — adding gastro to an existing Go project

cd your-project
go get -tool github.com/andrioid/gastro/cmd/gastro
mkdir -p internal/web/{pages,components}
# wire one component into an existing handler (smallest possible step)
# OR wire gastro.New(...) into your main.go for the full pattern
gastro watch --run 'go run ./cmd/myapp'
→ Library getting started: docs/getting-started-library.md
```

**`docs/getting-started-library.md` content outline (Q4 + Q9):**

1. **Why this mode.** "Add a UI to your otherwise-headless Go service."
   Concrete cases: admin UIs, internal tools and dashboards, status
   pages, server-rendered marketing pages alongside an API-first
   product.
2. **Install the CLI as a project tool** (`go get -tool …`). Pinned per
   project; survives `go.mod` upgrades cleanly.
3. **Smallest possible integration: render one component** (Q4 leads with
   components-only). Show how to compile `internal/web/components/foo.gastro`
   and render it from an existing `http.HandlerFunc`. ~20 lines of setup.
4. **The full pattern: `gastro.New(...)` in main.go.** Show how to wire
   the framework-style runtime into an existing service that already has
   its own router, middleware, and lifecycle.
5. **The dev loop: `gastro watch --run … [--build …]`.** Document both
   shapes (one-step `go run` vs faster two-step `go build` + exec).
   Explain the typo-survival property (R4).
6. **Production build:** `go generate ./... && go build`. No special
   gastro-specific build step in production.
7. **The `gastro watch` mental model.** A short, honest paragraph: "We
   built this so you don't need to install `air`. It's not a
   general-purpose Go file watcher — it's the gastro project's
   hot-reload tool that happens to also watch your `*.go` files because
   the alternative would be a UX cliff. For composed workflows, see the
   watchexec recipe in `dev-mode.md`."
8. Pointer to `dev-mode.md` for `GASTRO_PROJECT` / `GASTRO_DEV_ROOT`
   env vars and advanced setups.
9. Pointer to the runtime reference (component syntax, props, funcs —
   shared with framework).

**`docs/getting-started.md` (framework) updates:**

- Add a one-line "if you're adding gastro to an existing project, see
  [getting-started-library.md] instead" near the top.
- Section on install narrows to global install (`mise` / `go install`)
  with a pointer to library page for `go get -tool`.
- Otherwise minimal change. Most of the page is fine as-is.

**Sidebar update in `examples/gastro/components/docs-layout.gastro`:**

Replace the single "Getting Started" entry with two:
```
- Getting Started (new project)         → /docs/getting-started
- Getting Started (existing project)    → /docs/getting-started-library
```

(Plain wording in sidebar labels per common-sense readability, even
though the body copy uses framework / library terms — these labels
function as pre-introduction wayfinding.)

### Phase 6 — DECISIONS.md entry + verification

**File:** `DECISIONS.md` (one new entry, top of file).

**Content:** records:
- (a) the documentation split into framework + library pages,
- (b) `gastro watch` shipping with `--run` (required) + `--build`
  (repeatable optional), `*.go` watching, hardcoded excludes plus
  `--exclude`, cancel-restart on mid-build changes, keep-alive on
  build failure,
- (c) `gastro dev` unchanged + rejects unknown flags,
- (d) the `writeGoFile` skip-if-equal + atomic-write patch,
- (e) the explicit decision *not* to ship pure-watch mode or a
  runner-detection hint,
- (f) the explicit decision *not* to make `gastro dev` configurable,
- (g) the explicit decision *not* to support Windows in `gastro watch`
  v1,
- (h) the new approved third-party dependency `github.com/google/shlex`
  with rationale (Phase 4 needs shell-string splitting; alternatives
  were rejected as either bug-prone or worse process-tree behaviour),
- (i) strategic-stance link to the 2026-05-02 "both modes equal"
  resolution.

**Verification (full suite):**
- `mise run test` — green with `-race`
- `mise run lint` — clean
- `bash scripts/verify-bootstrap` — passes (existing scaffold check)
- `gastro check` — clean against all four examples after regen
- Manual smoke (framework): `cd examples/blog && gastro dev` — unchanged
  behaviour
- Manual smoke (library — basic): hand-crafted layout with
  `cmd/myapp/main.go` + `internal/web/pages/`; `gastro watch --run 'go
  run ./cmd/myapp'` produces hot reload; template-only edits don't
  restart binary; frontmatter edits do; `*.go` edits do
- Manual smoke (library — typo survival): break a `.go` file
  syntactically, verify the previous binary keeps serving and the
  browser shows the build-failure banner; fix the syntax, verify the
  banner clears and the new binary takes over
- Manual smoke (library — rapid edits): with a slow `--build` (insert
  a `sleep 2`), make three rapid changes, verify only the latest one
  gets a successful run
- Manual smoke (library — process group): `--run 'go run ./cmd/myapp'`
  binding a port; restart 5 times in a row; verify no "address already
  in use" errors
- Manual smoke (committed-`.gastro/`): `gastro generate` twice in a row
  leaves no git diff after the second run (proves Phase 1)
- Manual smoke (`tmp/go-tool-tradeoffs.html` banner present)

## 7. Acceptance criteria

A user reading docs alone (no code spelunking) can:

1. **Framework path:** identify "I'm building a new gastro site" within
   the README quick-start, run two commands, and have a working dev
   server with hot reload in under 60 seconds. (No regression from today.)
2. **Library path:** identify "I'm adding gastro to my existing service"
   within the README quick-start, follow the library getting-started
   page, and have a working dev server with hot reload after wiring
   either a single component or `gastro.New(...)` into their existing
   project and running `gastro watch --run …`. **No third-party runner
   installed.** Edits to both `*.gastro` and `*.go` trigger the loop.
3. **Build resilience:** introducing a syntax error in a `.go` file
   produces a clear terminal error and a `console.warn` in the browser
   DevTools (no visible banner in v1); the previous build keeps serving
   until the user fixes it. (Library path only — `gastro dev` doesn't
   currently have build-failure semantics to preserve.)
4. Either path: never see a flag on `gastro dev`. Never read about
   composing watchexec unless they actively chose to.
5. The runtime documentation (component syntax, props, frontmatter,
   template funcs, error handling, middleware, deps injection) is
   single-source and applies identically in both modes.

## 8. Risks

| # | Risk | Likelihood | Mitigation |
|---|---|---|---|
| 1 | **Process-group leakage on `go run`-style commands** — SIGTERM hits `go run`, grandchild keeps the port bound, second restart fails with "address in use" | High without explicit handling | Phase 4b uses `setpgid` + `Kill(-pgid, SIGTERM)`. Dedicated test (`TestWatch_KillsProcessGroup`) exercises the rebind case. |
| 2 | Cancel-restart races (R3) leave half-built artifacts or zombie processes | Medium | Build commands run with cancellable context; partial-write protection on the `.gastro/` tree comes from Phase 1; user-`--build` outputs (typically Go binaries) are atomic via `go build`. Tested in `TestWatch_CancelsInflightBuildOnRapidEdits` with explicit synchronisation. |
| 3 | shlex edge cases not covered by `google/shlex` | Low | The library is widely used (air, ko, kustomize, etc.). Test with a `-ldflags '-X main.version=...'` case explicitly. |
| 4 | Naming choice (Q1 → framework / library) drifts: someone uses standalone/embedded in new docs by accident | Low | One-time grep + rename pass during Phase 5; doc style note in `contributing.md`. Internal package name (`devloop`) stays neutral so it doesn't need renaming. |
| 5 | Library-mode users who *did* want to compose with their own runner feel pushed off the happy path | Low | The watchexec recipe in `dev-mode.md` stays; document `gastro watch`'s scope explicitly in dev-mode.md ("for one-command hot reload; for composition with existing runners, see below"). |
| 6 | User's `--build` writes the binary into a watched directory, causing reload loops | Low (silly path choice but easy to do) | Startup warning when `--build` substring matches a watched path other than `tmp/`. Non-fatal. |
| 7 | False rebuilds from `*.go` watching when user runs `go test` (test cache writes, etc.) | Low | `go test` doesn't modify source files. `go generate` does, and is typically explicit; documented as expected behaviour. |
| 8 | Empty `tmp/` exclude collides with users who put working files there | Low | Documented in the `--exclude` section: "default excludes are not user-removable in v1; rename your dir or live with it." Could revisit if it bites. |
| 9 | `gastro watch` becomes the "default" because it's more powerful, eroding `gastro dev`'s no-config story | Low | Anti-pattern guardrail: never recommend `gastro watch` for scaffolded layouts. Both getting-started pages are explicit about which command to use. |

## 9. Pre-existing issues identified during brainstorm

(Per AGENTS.md: surface these for explicit handling.)

- **`writeGoFile` always writes**, even when bytes are unchanged. Causes
  spurious git churn for downstream teams who commit `.gastro/`. Fixed by
  Phase 1.
- **`docs/getting-started.md` edits during the prior session cause
  `examples/gastro` drift in `.gastro/templates/`** because it embeds
  that doc as content. Re-running `gastro generate` brings it back in
  sync; this is expected behaviour (the example renders the live docs)
  but worth noting for any future edit to `docs/getting-started.md`
  during this plan's Phase 5.
- **No pre-existing test coverage** for the `runDev` watcher loop's
  smart-classification path; the existing tests cover individual
  classifier helpers (`watcher.ClassifyChange`,
  `DetectChangedSection`) but not the end-to-end "frontmatter edit
  causes restart, template edit does not" behaviour. Phase 4 tests fill
  this gap (and Phase 2 tests fill it for the framework path too).
- **No process-group handling in current `runDev`.** The scaffold
  default uses `go build` + direct exec, which dodges the issue
  accidentally — there's no parent `go run` to leak through. `gastro
  watch` exposes the foot-gun, so Phase 4b makes it explicit and tested.

## 10. Out of scope

- LSP changes (already shipped earlier today; verified not needed here).
- VSCode extension changes (already shipped earlier today).
- New runtime APIs.
- A `gastro fmt` / `gastro lint` / `gastro check` audit (separate work).
- Publishing tagged release binaries (separate consideration; would help
  the install-method story but is not on this plan's critical path).
- A library-mode example in `examples/`. Probably wanted as a follow-up
  but adds scope; the library getting-started page can use inline code
  blocks for now.
- Telemetry on which mode users pick (we'd want this to validate the
  split, but it's not part of this plan).
- **Windows support for `gastro watch`.** Process groups, signal
  handling, and `sh`-style splitting all differ on Windows. Defer
  until an adopter asks.
- **Pure-watch mode** (`gastro watch` without `--run`). Reserved for
  the existing watchexec recipe. Defer until an adopter asks.
- **Configurable SIGTERM grace period** (currently hardcoded 5s).
  Defer until someone reports a slow-shutdown binary that needs longer.
- **Visible browser banner for build failures.** v1 surfaces failures
  via `console.warn` over a new `build-error` SSE event; a styled,
  positioned banner that survives across navigations is deferred. The
  transport layer (event type, signal file, client listener) ships in
  this plan so the banner is a UI-only follow-up — no protocol changes
  needed when it lands.

## 11. Estimate

Rough phase sizes:

| Phase | Size | Notes |
|---|---|---|
| 1 | XS | One function, two tests |
| 2 | M | ~250 LOC moved + new file-watching surface for `*.go`, full test sweep |
| 3 | XS | Warning + one test |
| 4 | **L** (revised up from M in v1; held at L in v2.1 — devreload SSE event type adds ~1 day but stays inside L) | New subcommand; process-group exec + cancellation + keep-alive + shlex + 12 integration tests + 3 devreload unit tests; new SSE event type; flag rejection on `dev` |
| 5 | M-L | Doc rewrite is the bulk; new content for getting-started-library.md; sidebar + new docs page in examples/gastro |
| 6 | XS | Decisions entry + verification |

Total: roughly **2–3 days** of focused work (revised up from v1's
1–2 days), dominated by Phase 4 (process supervision corner cases) and
Phase 5 (docs). Each phase is independently shippable.

---

## Iteration scratchpad

(Post-resolution notes; v1's Q1–Q9 are now closed in §3.)

- **2026-05-03 round 1:** resolved Q1/Q2/Q3/Q4/Q5/Q6/Q7/Q8/Q9 + R1 + R4
  via initial Q&A.
- **2026-05-03 round 2:** resolved R2 (.go watch scope), R3 (mid-build),
  R5 (wording).
- **2026-05-03 round 3:** resolved Q6 final shape (--run required +
  --build repeatable optional) and R6 (approve `google/shlex`).
- **2026-05-03 v2 review:** plan-review pass surfaced one substantive
  gap — Phase 4c referenced a "browser banner" channel that didn't
  exist in `pkg/gastro/devreload.go` (single content-less reload
  event, no event types or payload). Two minor wording inconsistencies
  also flagged: §3 Q5 vs Phase 4 flag-rejection wording, and Phase 2's
  "byte-identical" overstatement.
- **v2.1 resolutions:**
  - Banner downgraded to `console.warn` for v1; full banner explicitly
    deferred to follow-up (§10). Transport layer (`build-error` SSE
    event + `.gastro/.build-error` signal file) ships now under new
    Phase 4e so the future banner is UI-only work.
  - §3 Q5 reworded to point at Phase 4 as the single source of truth.
  - Phase 2's "byte-identical" softened to "behaviourally equivalent."
  - Phase 2 also clarifies where `writeReloadSignal()` lives after the
    refactor (stays in `cmd/gastro`, injected via a new `OnReload`
    hook on `devloop.Config`).
- Open after v2.1: nothing blocking. SIGTERM grace period, Windows
  support, and visible banner UI are explicit out-of-scope decisions,
  not unknowns.
