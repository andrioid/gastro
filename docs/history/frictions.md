# Gastro friction backlog
_Distilled from `feedback-from-git-pm-project.md` after the v0.1.13–v0.1.15
audit cycle. Items closed during that cycle are not listed here._

Last updated: 2026-05-01  
Baseline: gastro `v0.1.15` + commit `e0a2bc4`

---

## How to read this document

Each item follows the same shape:

```
### {ID} · {short title}
- Priority: P0–P3 (audit scale, P0 highest)
- Effort:   Trivial / Small / Medium / Large / XL
- Status:   open · blocked-by-X · partly-landed-in-vY · …

#### Problem        — what hurts, who notices, when.
#### Proposal       — what done looks like, code where useful.
#### Trade-offs     — what we gain, what we give up, alternatives considered.
#### Implementation — subsystems touched, effort breakdown, dependencies.
```

**Effort labels:**

| Label | Meaning |
|---|---|
| Trivial | < 1 h, few lines |
| Small | half-day, one subsystem |
| Medium | 1–2 days, a few subsystems |
| Large | 3–5 days, design work required |
| XL | week+, architectural |

**Section layout:**

- **Section A — codegen & template ergonomics** (A1–A7)
- **Section B — runtime & router API** (B1–B5)
- **Section C — page-routed apps with SSE morphs** (C1–C5)
- **Section D — tooling** (D1–D2)
- **Section E — research / follow-ups**
- **Section F — project-side (git-pm, not gastro)**

IDs are stable: items are referenced by ID from PRs, decisions, and the
git-pm task tracker, so historical numbering is preserved even when the
section a friction lives under shifts. New items get the next available
ID within their section.

---

## Section A — codegen & template ergonomics

### A1 · CLI not toolchain-pinnable

- Priority: P1
- Effort:   Medium
- Status:   open

#### Problem

`go generate ./internal/web` shells out to a `gastro` binary on `$PATH`
with no version constraint. A contributor who runs `go mod tidy` after
a version bump gets a working build but silently retains an old CLI;
the mismatch only surfaces when generated output diverges or a
subcommand is renamed.

#### Proposal

A `//go:generate go run github.com/andrioid/gastro/cmd/gastro@VERSION
generate` directive, _or_ a go-importable
`gastro/codegen.Run(dir, outDir string)` that a `tools.go`-style file
can call. Either form lets `go mod tidy` track the CLI version
alongside the library.

#### Trade-offs

Going the importable route locks down the codegen entry point as
public API — users will start importing it, so renames and signature
changes become breaking. Today `cmd/gastro` is `package main` and can
change freely. The `go run @VERSION` directive avoids that lock-in
but requires every consumer to remember the directive form.

#### Implementation

- `cmd/gastro` must stay importable or expose a `Run` func.
- `internal/compiler` already does the work; mostly wiring + a public
  entry point.
- Update `docs/getting-started.md` and the scaffold template.
- ~1 day for the extraction, half a day for docs and scaffold.

---

### A2 · Generated code committed

- Priority: P2
- Effort:   Medium
- Status:   blocked-by-A1

#### Problem

Every component edit produces a ~26-file diff under `.gastro/`.
Mitigations in place (`linguist-generated`, `gastro check` CI gate)
reduce the pain but don't eliminate it.

#### Proposal

Two independent sub-options:

1. **Embed indirection** — point `//go:embed` in `embed.go` at the
   _source_ `components/` and `pages/` directories rather than copies
   under `.gastro/templates/`. Cuts the embed half of the committed
   tree; the Go codegen files would still need committing.
2. **Reproducible generate step** — once A1 lands, a CI
   `go generate && git diff --exit-code` step is version-pinned and
   the committed tree becomes optional. The committed tree can then
   move to `.gitignore`.

#### Trade-offs

Option 2 (gitignore) means humans no longer see generated code in
review — harder to spot that a PR's diff implies an unexpected
codegen change. Option 1 keeps the Go files visible but removes the
template duplicates. The two are complementary, not exclusive.

#### Implementation

- Option 1: `Small` — compiler change to `generateEmbedFile` +
  template path adjustments.
- Option 2: `Trivial` once A1 is done.

---

### A3 · Two render paths to maintain

- Priority: P2
- Effort:   Large
- Status:   partly-landed-in-v0.1.15 · structural

#### Problem

Adding a Props field means editing both the typed `Props` struct
(Go-side consumers) and every `{{ X (dict ...) }}` call site in
templates (untyped). v0.1.15 now catches unknown dict keys at
`gastro generate` time, which converts runtime surprises into build
errors — the path is finite and mechanical, but still manual.

#### Proposal

At codegen time, emit a static assertion or generated helper that
makes "add a Props field, re-run codegen, fix the build errors" fully
mechanical — no hand-editing of template call sites. Two paths:

1. **Codegen-time migration** that rewrites `dict` call sites to
   include the new key (with zero value or a `// FIXME` marker).
2. **Move away from `dict`** toward a typed call syntax in `.gastro`
   templates: `{{ Board props.Columns props.Filter }}` or similar.

#### Trade-offs

Option 1 modifies user code at codegen time, which is surprising and
hard to undo. Option 2 means a new template syntax users have to
learn and the existing `dict` form has to coexist (or migrate) — both
are architectural moves to the template model. v0.1.15's build-time
key validation is the cheap middle ground; it doesn't fully close
the gap but is the least invasive way forward until a full design
emerges.

#### Implementation

Significant design work; depends on whether the template syntax is
extended or a codegen migration is preferred. Not a quick fix.

---

### A4 · Selector ↔ component decoupling

- Priority: P2
- Effort:   Small
- Status:   open

#### Problem

SSE morph handlers hardcode selectors as Go strings
(`"#pm-decisions-list"`) alongside a `Render.X(props)` call. If the
component's root element `id` changes the patch silently targets
nothing.

#### Proposal

A `selector` annotation in component frontmatter:

```
---
selector: "#pm-decisions-list"
type Props struct { ... }
...
---
```

Codegen surfaces it as `Render.DecisionsList.Selector` (a `string`
constant). SSE handlers can then write
`datastar.WithSelector(gastro.Render.DecisionsList.Selector)`.

#### Trade-offs

Encourages a 1:1 component↔selector relationship — fine for
top-level regions, awkward for components mounted twice on a page
under different parents. The selector becomes a _default_ (good for
the common case) rather than a contract. If a page needs two
instances, callers fall back to literal selectors.

#### Implementation

- `internal/parser` (new frontmatter field).
- `internal/codegen` (generate the constant).
- `render.go` template.
- No runtime work.

---

### A5 · Untyped children plumbing

- Priority: P1
- Effort:   Medium
- Status:   open

#### Problem

`__children` is a magic map key injected by the `{{ wrap }}`
transform. The public API exposes `children ...template.HTML` but
internally the value is smuggled through an untyped
`map[string]any`. Named slots require pre-rendered `template.HTML`
fields on Props (e.g. `LayoutProps.Detail` in git-pm).

#### Proposal

A typed `Children template.HTML` field generated on every
component's Props struct when `{{ .Children }}` is referenced in the
template body. Named slots declared in frontmatter
(`slots: [detail, sidebar]`) generate corresponding typed fields.
The `__children` map key becomes an internal detail hidden behind
generated accessors.

#### Trade-offs

Existing code that constructs Props manually and passes children via
the `dict` map will break (or at least need migration). Frontmatter
gains a new field (`slots:`), increasing surface area. The win is
removing a class of "I forgot to escape this" bugs and making slots
discoverable from the Props type alone.

#### Implementation

- `internal/parser` (slots field).
- `internal/codegen/generate.go` (component template).
- `internal/compiler` (detect slot usage from template body).
- `pkg/gastro/props.go` (helpers).

---

### A6 · No static-asset hashing

- Priority: P2
- Effort:   Large
- Status:   open

#### Problem

`GetStaticFS` serves `/static/...` with no content fingerprint.
Long-lived `Cache-Control` headers are unsafe; the workaround is
`no-cache` + ETag (tracked in git-pm backlog task `01KQ3530`).

#### Proposal

A `WithHashedStatic()` router option that at startup reads each file
in `staticAssetFS`, computes an 8-hex-char content hash, registers
redirect routes `/static/<hash>/file.js → /static/file.js`, and
rewrites `src`/`href` references in generated templates to the
hashed form.

#### Trade-offs

Build-time path rewriting requires parsing template strings for
`src=` / `href=` — fragile, easy to miss edge cases (CSS
`url(...)`, JS-built URLs). A pure runtime approach (hash on first
serve, redirect) is simpler but doesn't let templates embed hashed
URLs statically, so the long-cache win is partially lost. Worth
shipping the runtime variant first and revisiting build-time
rewriting once it's clear which template forms need coverage.

#### Implementation

- Compiler (template reference rewriting at codegen time).
- `pkg/gastro/fs.go`.
- Generated `routes.go` template (hash redirect routes or a custom
  file server).

---

### A7 · Component name collision warning

- Priority: P2
- Effort:   Small
- Status:   open

#### Problem

Two component files that produce the same exported Render method
name (e.g. `decision.gastro` and `task-decisions.gastro` both map to
`Decision`) result in a compile error that isn't attributed to the
naming conflict. Workaround: manual file renaming, after a confused
round of debugging.

#### Proposal

At compile time, before writing any output, `Compile()` checks the
full set of `ExportedComponentName(funcName)` values for duplicates
and returns a descriptive error:

```
gastro: component name collision: "Decision" would be generated by both
  components/decision.gastro and components/task-decisions.gastro
  rename one of them to avoid the conflict
```

#### Trade-offs

Practically none — pure error-message improvement. The only cost is
that the check runs on every codegen pass; a hash-set sweep over the
component list is constant-time per file.

#### Implementation

- `internal/compiler/compiler.go` (one pre-pass over `components`
  before writing output).
- Trivial loop + error message; no codegen changes.

---

## Section B — runtime & router API

### B1 · No snippet / include idiom

- Priority: P2
- Effort:   Small
- Status:   open

#### Problem

Small shared markup fragments pay full component cost: a Props
struct, a `dict` call at every consumer, a new codegen entry, and a
new `.gastro/` file in the committed tree. The overhead exceeds the
benefit for 10–20 line shared fragments (e.g. the `pm-md-editor`
markup in git-pm).

#### Proposal

A `snippets/` directory (or a frontmatter flag `snippet: true`) for
`.gastro` files with no Props struct. Callable as
`{{ include "snippets/md-editor.gastro" . }}` — the dot passes
through the caller's data without a `dict` map. No typed Render API
entry, no `__children` plumbing, no committed `.go` file; just a
named template registered at startup.

#### Trade-offs

Adds a second authoring mechanism alongside components — users have
to learn _when_ to use which. The line is "Props or no Props," which
is fine in principle but easy to get wrong (a snippet that grows
into a component is a refactor with no automated path). Document
the heuristic explicitly: snippets for shared markup with no
parameters; components for anything that takes typed data.

#### Implementation

- `internal/compiler` (new discover + compile path for snippets).
- `internal/router` (register snippet templates in the FuncMap).
- Template transform (recognise `{{ include }}`).
- No parser changes.

---

### B2 · Dev mode coupling _(was A8)_

- Priority: P3
- Effort:   Trivial
- Status:   open

#### Problem

`IsDev()` reads `GASTRO_DEV` globally; there is no per-router dev
flag. In practice this is fine for single-process apps but makes
parallel test setups where one router should behave as "dev" and
another as "prod" awkward.

#### Proposal

A `WithDevMode(bool)` router option that overrides the env-var check
for that router instance. The global `IsDev()` remains for backward
compat.

#### Trade-offs

Per-router dev mode means tests can configure dev/prod independently,
which is the intended use, but also makes it possible for two
routers in the same process to disagree about whether to hot-reload
templates — confusing if a developer hits the wrong one. Document
the precedence clearly: option overrides env var.

#### Implementation

- Generated `routes.go` template (`New()` option + `isDev` field
  already exists; just expose the override).

---

## Section C — page-routed apps with SSE morphs

_Added 2026-05-01 from a git-pm conversation about whether to revisit
decision `01KQ45N0` ("board page renders via Go handler calling gastro
components directly"). The framing question was: "is there no way to
handle SSE morphing with the generated routes?" The answer turned out
to be "SSE morphs aren't the blocker — these are." Items here describe
the gaps that together prevent an event-sourced app from living
predominantly inside `.gastro` files when most of its work is mutating
handlers._

### C1 · Page actions (mutating handlers in `.gastro` files)

- Priority: P1
- Effort:   Large
- Status:   open · architectural · pairs-with-A4 · prereq-A3

#### Problem

Gastro's mental model today is "pages render HTML, you bring your
own handlers for everything mutating." For an event-sourced app
where 80% of the interesting work is mutating handlers
(`POST /tasks`, `POST /tasks/{id}/place`,
`DELETE /tasks/{id}/comments/{cid}`, …) this split forces nearly all
code to live outside `.gastro` files. The page that renders the
board can't share filter parsing, dep lookup, or projection slicing
with the morph handler that re-renders one of its columns — both end
up duplicating the logic, one in frontmatter, one in a Go file.

This is the single largest reason git-pm decided (`01KQ45N0`) to
skip page routes entirely and call gastro components directly from
Go handlers.

#### Proposal

Astro/Remix-style actions in page frontmatter, sharing the page's
deps and helpers and emitting Datastar morph patches as a typed
return value:

```
---
import Board "components/board.gastro"

ctx := gastro.Context()
deps := gastro.From[BoardDeps](ctx)
filter := boardFilterFromQuery(ctx.Request())

action place(form PlaceForm) datastar.Patch {
    if err := deps.Service().Place(form.ID, form.Status, form.Rank, form.Base); err != nil {
        return datastar.Error(409, "stale base")
    }
    return datastar.PatchSelector("#pm-col-"+form.Status,
        Board.Column(columnProps(deps.State(), form.Status, filter)))
}
---

{{ Board (dict "Columns" .columns "Filter" .filter) }}
```

Codegen emits `POST /tasks/{id}/place` mounted on the same router,
resolves the form via the existing dict/Props machinery, threads
deps through, and converts the typed `datastar.Patch` return into
the appropriate SSE event.

#### Trade-offs

**Wins:** the page and its mutating endpoints share one set of
helpers, deps, and filter logic. Code that today lives in a Go file
referencing a separately-defined gastro component collapses into one
file. The contract between selector and rendered fragment becomes
checkable at codegen time (combined with A4's selector constants).

**Costs:** new authoring idiom — users have to learn `action`
declarations alongside frontmatter, action testing semantics, error
return conventions. The frontmatter language is now doing real
control-flow work, which makes errors in frontmatter more impactful
than today's "render this template" failures. A3 (typed dict
validation) is effectively a prerequisite — render errors inside a
mutating action are programmer bugs that today have to be swallowed,
and for a mutating endpoint that's a worse failure mode than for a
read-only page.

**Alternatives considered:** keep handlers in Go forever (status
quo, what git-pm does); add page-level deps via `WithDeps[T]` only
(landed in v0.1.15) but leave mutations in Go (halfway house — fixes
the GET path, leaves the duplication for morphs). Neither closes the
gap; they only move where it lives.

#### Implementation

- Parser (action declaration syntax).
- Codegen (generate POST handlers per action, generate form-binding
  shims).
- Router runtime (dispatch, deps attachment for actions).
- `pkg/gastro/datastar` (typed `Patch` return type, error response
  variants).
- Design doc + examples-app spike before committing to syntax.
- Without this item, page-routed code stays a minority strategy in
  any SSE-heavy app.

---

### C2 · Route middleware / decorator composition

- Priority: P2
- Effort:   Small
- Status:   open · pairs-with-C1

#### Problem

`WithOverride(pattern, h)` _replaces_ a generated handler; there is
no way to _wrap_ one. Per-route middleware (CSRF `Require` vs
`RequireOrigin`, request-scoped logging, tracing, auth) has to
either wrap the entire `Router.Handler()` outside (coarse — every
page gets the same middleware regardless of method/path) or fully
take over the route via `WithOverride` (loses the generated
handler).

#### Proposal

A composition option that wraps named routes without claiming
ownership:

```go
gastro.New(
    gastro.WithMiddleware("GET /{$}",                 csrf.Require),
    gastro.WithMiddleware("POST /tasks/{id}/place",   csrf.Require),
    gastro.WithMiddleware("GET /sse",                 csrf.RequireOrigin),
    gastro.WithMiddleware("*",                        logging.Middleware),
)
```

Same typo-safety contract as `WithOverride` (panic on unknown
patterns; `*` opt-in for catch-all).

#### Trade-offs

Adds another option to the router constructor surface, which is
already non-trivial. The `*` wildcard is a foot-gun if users assume
it composes — clarify ordering: explicit-pattern middleware runs
before `*` middleware. Worth considering whether to converge on a
single middleware-chain API rather than option-per-pattern (à la
`gin` / `chi` / `httprouter`); option-per-pattern is more
declarative but harder to express "X for these N routes."

#### Implementation

- Generated `routes.go` template (`config` struct + `New()` wiring;
  the mux is already there, this is a wrapping pass at registration
  time).
- Pure additive — no codegen changes beyond the option plumbing.

---

### C3 · Programmatic page invocation

- Priority: P2
- Effort:   Medium
- Status:   open · depends-on-C1

#### Problem

Once filter parsing, deps lookup, and projection slicing live in a
page's frontmatter (via C1), an SSE morph handler that wants to
re-render the same page region can't reuse them — it can only call
`router.Render().Board(props)` and rebuild `props` from scratch in
Go, duplicating the page's own logic.

#### Proposal

Codegen exposes each page handler as a deps-aware, callable function
returning HTML and an error, bypassing the mux:

```go
html, err := router.RenderPage.Board(ctx, gastroweb.BoardPageRequest{
    Query: r.URL.Query(),
})
```

Or, more general,
`router.Invoke("GET /{$}", req) (*httptest.ResponseRecorder, error)`.

#### Trade-offs

Blurs the boundary between "page" (HTTP-routed surface) and "render
function" (callable from Go). Users may abuse it as a generic render
helper, defeating the point of components. Mitigation: name it
`RenderPage` so the scope is clear; document that page invocation
includes deps attachment but skips middleware (which is the actual
useful property — call the page logic without re-running CSRF,
logging, etc.).

#### Implementation

- Codegen (export an additional shim per page that skips routing but
  threads deps).
- Generated `routes.go` template (new `RenderPage` accessor
  mirroring `Render`).
- The page handler already exists; the work is exposing it under a
  typed accessor.

---

### C4 · Page render error contract

- Priority: P2
- Effort:   Trivial
- Status:   open · mostly-docs

#### Problem

Go handlers can return contextual 500s with logging on render
failure. What a generated page does when render fails, or when a
`gastro.From[T]` lookup is missing, isn't documented and isn't
customisable. For an app currently relying on per-handler logging
context, this is the kind of detail that sinks a migration after
the fact.

#### Proposal

1. A
   `WithErrorHandler(func(http.ResponseWriter, *http.Request, error))`
   router option, called for any panic / template error /
   missing-deps condition during page dispatch. Default behaviour:
   log + 500.
2. Documented graceful path for missing deps: `gastro.FromOK[T]`
   already exists; promote it as the idiomatic way for a page to
   render a degraded view (e.g. git-pm's no-repo placeholder)
   rather than panicking.
3. A short `docs/error-handling.md` enumerating each failure mode
   (parse error in dev, render error in prod, missing dep, panic in
   action handler from C1) and the contract for each.

#### Trade-offs

Documenting more behaviour means committing to more behaviour — if
the defaults change later, users have to adapt. The win is making
the failure contract explicit so users can decide whether gastro's
defaults match their needs (or if they need `WithErrorHandler` from
day one). Pure additive; no breakage.

#### Implementation

- Generated `routes.go` template (error handler option + dispatch
  wrapping).
- `pkg/gastro/recover.go` (already does panic recovery; needs the
  option hook).
- New docs page.

---

### C5 · Test helpers for page handlers

- Priority: P3
- Effort:   Trivial
- Status:   open · pairs-with-C3

#### Problem

A git-pm-style test suite (~30 board/task tests) currently calls
`board.Page(w, r)` directly with a hand-built deps struct.
Migrating those tests to the page-routed shape today means setting
up a full `httptest.NewRequest` + `router.Handler().ServeHTTP`
dance per test, plus `WithDeps` for every distinct deps
configuration.

#### Proposal

A small `gastrotest` helper that hides the httptest/router plumbing
for the common case:

```go
resp := gastrotest.New(t, router).
    WithDeps(BoardDeps{State: stateFn, Store: s, Actor: "alice"}).
    Get("/")

// resp exposes Status(), Body(), HeaderGet(...), Document() (for goquery), etc.
```

With C3 in place, a sibling `RenderPage` form lets handler tests
skip HTTP entirely and assert on the rendered HTML directly.

#### Trade-offs

Yet another package to maintain. Alternative: users write their own
6-line helper per project. The case for shipping it upstream is
network effects — if every gastro user writes the same helper, may
as well bless the common form. Keep it small and unopinionated;
resist the urge to add fluent-DSL features.

#### Implementation

- New `pkg/gastrotest` package; no changes to core runtime or
  codegen.
- Pure additive helper package; the core functionality already
  exists, this just wraps it.

---

## Section D — tooling

### D1 · `gastro dev` unusable for embedded-package projects _(was B7a)_

- Priority: P2
- Effort:   Small
- Status:   open

#### Problem

`gastro dev` always builds `.gastro/dev-server` from a `main.go` it
expects alongside the gastro project root. Projects where the
process entry point lives elsewhere (e.g. `cmd/pm`) get a misleading
"permission denied" error because the binary simply doesn't exist.

#### Proposal

A `gastro watch` subcommand (or `gastro dev --no-server` flag) that
runs _only_ the source-watch → regenerate → signal-file loop,
leaving the binary lifecycle to the host project. The
`writeReloadSignal` + watcher goroutine already exist in `runDev`;
extracting them into a standalone `runWatch` is mostly refactoring.

#### Trade-offs

Two ways to run dev mode means a docs split and a "which one do I
want" question for new users. Once the standalone watcher is
proven, `gastro dev` can deprecate to a thin wrapper around `gastro
watch` + a host-project-specific binary command — collapsing back
to one mental model.

#### Implementation

- `cmd/gastro/main.go` (new subcommand or flag).
- `printUsage`.
- `docs/dev-mode.md` (already documents the workaround; update once
  the native flag exists).
- Extract the watcher loop from `runDev` into a shared helper.

---

## Section E — research / follow-ups

### E1 · File issues against gastro for A1, A2, A4

The narrower task this audit superseded asked: **skim gastro's open
issues / discussions / DESIGN.md for A1, A2, A4; if silent, file one
GitHub issue per item.**

That step has not been done. Recommended framing when filing:

- **A1 issue title:** "CLI version not tracked by go.mod: tools.go
  or importable codegen.Run()"
- **A2 issue title:** "Embed indirection: point //go:embed at source
  components/ instead of .gastro/templates/ copies"
- **A4 issue title:** "Expose component selector as a codegen
  constant for SSE morph handlers"

Link each filed issue back from this document.

---

## Section F — project-side (git-pm, not gastro)

These require no upstream change. Ordered by effort. The IDs in this
table are git-pm's own backlog identifiers and are unrelated to the
A/B/C/D friction IDs above.

| ID | Task | Effort |
|---|---|---|
| C5 | Pin `gastro install` to tag in `internal/web/README.md` (currently `@latest`) | Trivial |
| C3 | Add one-liner to `internal/web/README.md` + gastro skill: "HTML → `.gastro`; Go packages → content rendering only" | Trivial |
| C2 | Add "Gastro integration shape" subsection to README naming decisions `01KQ45N0`, `01KQH34Q`, `01KQH35F` | Small |
| B5 | Migrate `decisions.go` and `experiences.go` from `gastroweb.Render.X(...)` to `d.deps.Router.Render().X(...)` | Small |
| C4 | Extract `pm-md-editor` markup into a `.gastro` component (accept per-component overhead until B1 lands) | Small |
