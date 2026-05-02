# Frictions plan

_Forward plan derived from `frictions.md` (baseline gastro v0.1.15 + commit
`e0a2bc4`) and a 2026-05-02 evaluation that split each item between
library-mode and framework-mode viability (companion HTML report kept
alongside this plan)._

> **Status:** archived 2026-05-02 — all planned waves are closed.
> Originally tracked in `plans/`; moved to `docs/history/` together with
> the source `frictions.md` audit and the `frictions-mode-split.html`
> companion report. See `DECISIONS.md` 2026-05-02 ("Wave 5 closure") for
> the move record.
>
> **Wave progress:** Track B shipped (commits `f356cbc`–`2bb3c9f`).
> Wave 1 shipped (commits `1c229b4`–`133890e`, 2026-05-02). Wave 2 empty
> (D1 deferred). Wave 3 (A5) shipped 2026-05-02 (commit `e64b553`).
> Wave 4 (C2 + C4) shipped 2026-05-02 (commit `fe04a90`) — all C2
> design points resolved in the same session (§7 Q3, Q6, Q7);
> `WithErrorHandler`, `WithMiddleware`, the throwaway-mux pattern
> probe, the per-route `applyMiddleware` wiring, plus
> `docs/error-handling.md` all landed. **Wave 5 empty after Q4 audit
> 2026-05-02 dropped A2** (infeasible as framed; benefit obsolete).
> All planned waves are now closed; ready to archive or fold into
> `docs/`.
>
> **Resolved questions (2026-05-02):** Q1 A4 dropped (selectors are just
> ids, users define their own constants). Q2 A5 simplified — no deprecation
> window. Render API variadic dropped immediately (Go's type checker is
> the migration signal); `__children` dict literal becomes a standard
> unknown-key warning via existing validator path. Q3 C2 `WithMiddleware`
> added (same pattern as `WithOverride`, middleware wraps override). Q4 A2
> needs audit of dev-mode path resolution. Q5 A6 dropped in favour of
> documenting existing `WithOverride("GET /static/", ...)` and `Mux()`
> patterns. Q6 C2 wildcard resolved — adopt Go's `http.ServeMux` pattern
> syntax (`{slug}` segment, `{slug...}` catch-all); validation requires the
> middleware pattern to match ≥1 known auto-route, probed via a throwaway
> mux. Q7 C2 pattern shape resolved — path-only, mirrors `WithOverride`;
> method-specific middleware branches on `r.Method` internally. Track B has
> landed (commit `2bb3c9f` and predecessors). Wave 3 reduced to A5 only
> (A4 removed). Wave 5 A6 removed.

---

## 1. Strategic stance (resolved 2026-05-02)

These four decisions govern every per-item verdict below. They are
recorded here so that future re-proposals can be checked against them
rather than re-litigated.

| # | Question | Resolution |
|---|---|---|
| 1 | Which mode is primary? | **Both modes equal.** Pages are an opinionated convenience for read-heavy apps; library mode (components called from user-owned Go handlers) is a legitimate emergent shape. Ship cheap items in both columns; let usage decide which dominates. |
| 2 | How strict is "no new language to learn"? | **Hard rule.** Frontmatter stays plain Go, template body stays plain `html/template`. Items that propose new keywords, new template syntax, or new authoring concepts are rejected by default. |
| 3 | Is library mode officially supported? | **Emergent and acknowledged.** Wasn't part of the original concept, but real adopters using larger projects were bound to find holes. The Render API is public on purpose; document the shape and close the holes that block it. |
| 4 | How representative is git-pm as a downstream signal? | **Common shape, not universal.** Its frictions are valid for SSE-heavy mutating apps but should not be treated as a mandate for all apps. |

### What this rules out

- **No `slots:` keyword in frontmatter** (rule 2 — adds a second authoring concept alongside components. Named content areas are `template.HTML` Props fields; no special keyword needed.)
- **No `action` keyword in frontmatter** (rule 2 forecloses C1 as written).
- **No second authoring concept** alongside components (rule 2 forecloses
  B1 as written — `snippets/` directory or `snippet: true` flag).
- **No codegen-time rewriting of user template call sites** (rule 2,
  forecloses A3 option 1 — surprises the source-of-truth model).
- **No new template syntax** beyond what `html/template` already provides
  (rule 2, forecloses A3 option 2).

### What this commits us to

- Closing the type-safety hole that works in **both** modes (A5) before any mode-specific lift.
- Acknowledging library mode in the documentation and tooling (B2) without building it into a parallel framework. ~~D1 deferred until an adopter asks.~~
- Holding the line against any item that asks frontmatter or templates
  to learn a new dialect, even when the friction is real.

---

## 2. Anti-goals — what we are explicitly not doing

| ID | Item | Why we are not shipping it |
|---|---|---|
| C1 | Page actions (`action` keyword) | Violates rule 2. The `action` keyword and typed `datastar.Patch` return are off the table. **Underlying friction (mutating handlers in `.gastro` files)** is partly answered by Track B, which makes path-level method branching idiomatic without inventing DSL. |
| C3 | Programmatic page invocation | Premised on C1. Without C1, `Render.X(props)` already covers SSE-handler re-renders. |
| C5 | Test helpers for page handlers | Premised on C1/C3. Six-line per-project helpers are fine; upstreaming a `gastrotest` package without a clear shape is scope creep. |
| A3 | Two render paths consolidation | Both proposed options conflict with rule 2. v0.1.15's build-time `dict` validation already converted the worst failure (blank Card field) into a build error; the residual edit-fatigue is mechanical, not unsafe. |
| B1 | Snippet / include idiom | Adds a second authoring concept (rule 2). File-local `{{define}} / {{template}}` already covers within-file factoring; propless components are valid since v0.1; A2 will reduce the committed-codegen overhead that motivates most of the complaint. |

If any of these is re-proposed, the proposal must either show a path that
respects rule 2 or argue that rule 2 itself should be relaxed. The
default answer is no.

---

## 3. Ordered work plan

Five waves, ordered by dependency and risk. Each wave is independently
shippable; later waves do not assume earlier waves landed beyond the
explicit `depends-on` notes.

### Wave 1 — Pure wins (no design surface) — ✅ SHIPPED 2026-05-02

No goal-tensions, no migration cost, no public API churn beyond additive
options. Shipped in commits `1c229b4`–`133890e`.

| ID | Title | Effort | Status |
|---|---|---|---|
| A7 | Component name collision warning | Small | ✅ Pre-pass in `Compile()` derives `ExportedName` purely from path; emits `FileWarning` for duplicates, promoted to error in strict mode. Runs *before* per-file Go output is written, so failed strict-mode compiles leave `.gastro/` clean. Three tests in `compiler_test.go`. |
| B2 | Per-router dev mode | Trivial | ✅ New `WithDevMode(bool)` option overrides `GASTRO_DEV`. `config.devMode *bool` distinguishes absent vs explicit-false; `New()` falls back to `gastroRuntime.IsDev()` when nil. Integration test (`devmode_integration_test.go`) probes `/__gastro/reload` through the generated `Handler()` for all three cases. Documented in `docs/pages.md` §"Forcing dev or production mode" with library-mode as the headline use case. |

**Pre-existing fix bundled with Wave 1:** `examples/*/go.sum` were missing
chroma/goldmark transitive deps (regression from the markdown-directive
merge). Fixed in commit `1c229b4`. All four examples now pass
`gastro check` cleanly.

**Decision record:** `DECISIONS.md` entry dated 2026-05-02 ("Wave 1 —
component name collision warning (A7) and per-router dev mode (B2)").

### Wave 2 — Acknowledge library mode

~~Closes the friction that blocks library mode from being a usable shape.~~
~~Cheap, signals that "components-as-typed-Go-API" is supported.~~

Wave 2's only item (D1, `gastro watch`) has been deferred. B2 (`WithDevMode`)
ships in Wave 1 and already signals that library mode is acknowledged.
This wave is currently empty; if D1 or another library-mode item is
revived, it ships here.

### Wave 3 — Close type-safety gap (A5) — ✅ SHIPPED 2026-05-02 (commit `e64b553`)

A4 was dropped (Q1: selectors are id constants, users define their own).
A5 remained the highest-leverage type-safety item; it improves both
framework mode and library mode equally. Shipped without the
originally-planned deprecation window (Q2 simplified mid-implementation
— see DECISIONS.md 2026-05-02 entry for the rationale).

| ID | Title | Effort | Status |
|---|---|---|---|
| A5 | Typed children plumbing | Small | ✅ Renamed the magic `__children` dict key to `Children`; XProps definition moved from per-component file to centralized `render.go`; `Children template.HTML` field synthesized on the generated XProps when the template uses `{{ .Children }}`; Render API's `children ...template.HTML` variadic dropped (callers pass via `XProps{Children: html}`). Touched `internal/codegen/template.go`, `internal/codegen/generate.go`, `internal/codegen/validate.go`, `internal/compiler/compiler.go`. Named content areas (sidebar, footer) are explicit `template.HTML` fields — no `slots:` keyword. **No deprecation window**: the variadic drop is a hard break (Go compiler reports old call sites); user-authored `__children` in dict literals produces a targeted hint via the existing `validate.go` unknown-prop path. New tests: `TestValidateDictKeys_OldChildrenSentinelHinted`, `TestCompile_RenderXPropsShapes`. **Docs updated:** `docs/components.md` (Children-as-Props-field examples), `docs/sse.md` (same), `docs/design.md` §6 (records the rename rationale). See §3.1 below for the full mechanism as designed. |

### 3.1 A5 mechanism (designed and shipped 2026-05-02)

_The design recorded below is what shipped, with one deviation: the
plan's table called for `internal/codegen/validate.go:187` to emit a
"deprecation warning on `__children`". In practice this became a
targeted unknown-prop hint (no two-version contract), per the Q2
simplification recorded in §7 below._

**Today's flow — three names for one value:**

```
wrap transform          → injects "__children" into dict
component function      → extracts "__children", deletes from map, stores as local
component function      → re-adds as "Children" in template data context
template                → user writes {{ .Children }}
```

The `__children` key exists only to avoid colliding with real Props
fields. It's an implementation detail that leaked into the dict key-space.

**After A5:**

| File | Change |
|------|--------|
| `internal/codegen/template.go:144` | Injected key `"__children"` → `"Children"` |
| `internal/codegen/generate.go:202` | Extract `"Children"` from propsMap instead of `"__children"` |
| `internal/codegen/generate.go:204` | Remove the `delete(propsMap, "__children")` |
| `internal/codegen/validate.go:187` | Recognize `"Children"` as valid key; deprecation warning on `"__children"` |
| `internal/codegen/generate.go` renderTmpl | When `{{ .Children }}` detected in template, generate Props struct with `Children template.HTML` + user fields instead of a bare type alias. Render API drops `children ...template.HTML` variadic parameter — Children is now a Props field. |

**Props struct generation:**

Today render.go aliases the user's frontmatter struct:

```go
// generated — today
type CardProps = __card_props

func (r *renderAPI) Card(props CardProps, children ...template.HTML) (string, error)
```

After A5, when the component template uses `{{ .Children }}`:

```go
// generated — after A5
type CardProps struct {
    Title    string          // copied from user's frontmatter
    Children template.HTML   // auto-added by codegen
}

func (r *renderAPI) Card(props CardProps) (string, error)
```

Callers update from `Render.Card(CardProps{Title: "Hello"}, childHTML)`
to `Render.Card(CardProps{Title: "Hello", Children: childHTML})`.

The user's frontmatter `type Props struct { Title string }` is unchanged —
`gastro.Props()` still returns the user's source type. Codegen reads the
user's struct fields from the AST and regenerates a separate exported
struct with the same fields plus `Children`. `MapToStruct` targets this
generated struct, so Children populates naturally from the dict.

**Migration (no deprecation window — Q2 resolved 2026-05-02):**

- **Render API variadic dropped immediately.** Old call sites like
  `Render.Card(CardProps{Title: "Hello"}, childHTML)` fail to compile
  with a clear Go-level error. Go's type checker is the migration signal;
  no parallel runtime path needed. Matches Track B's hard-flip precedent
  and the pre-1.0 BC posture.
- **`__children` dict literal stops being recognized.** The existing
  unknown-key validator (`validate.go:187`) gains a special case: when
  the unknown key is exactly `__children`, the warning message suggests
  `Children` instead of listing valid fields. This is ergonomic guidance,
  not a deprecation contract — there is no "two minor versions" obligation.
- All examples/docs update in the same release (pre-1.0 churn, same pattern as Track B)
- LSP already recognizes `"Children"` as valid (completions.go:240) — no change needed

### Wave 4 — Framework-mode additives (no new language) — ✅ SHIPPED 2026-05-02

Both items are additive options that respect rule 2 — no new syntax in
frontmatter or templates, just additional `Option` values on `New(...)`.
Independent of the held C1. Shipped together in one Wave 4 commit set.

| ID | Title | Effort | Status |
|---|---|---|---|
| C2 | Route middleware composition | Small | ✅ New `WithMiddleware(pattern, func(http.Handler) http.Handler)` option in `internal/compiler/compiler.go`. Patterns use Go's `http.ServeMux` syntax (Q6) — `{slug}` matches a segment, `{slug...}` matches a trailing subtree, `/{path...}` is the canonical catch-all. Path-only (Q7), mirroring `WithOverride`; method-specific middleware branches on `r.Method` internally. Validation runs in `pkg/gastro.ValidateMiddlewarePattern` via a throwaway-mux probe — register the pattern with a sentinel handler, synthesise a concrete URL for each known auto-route, dispatch through the throwaway mux, and check whether the sentinel ever wins. Reuses Go's stdlib pattern semantics 1:1. Codegen template's mux build loop generates a per-route `applyMiddleware(route, h)` closure that walks `cfg.middleware` in reverse so registration-order composes outermost-to-innermost. Middleware wraps override (Q3) by virtue of being applied after the override resolution. Tests: 4 unit tests in `pkg/gastro/middleware_test.go` (probe semantics, error wording, applies, type shape) + 5 integration tests in `internal/compiler/middleware_integration_test.go` (exact match, wildcard subtree, composition order, middleware-wraps-override, unknown-pattern panic). Smoke example in `examples/sse/main.go` adds `logRequests` middleware via `WithMiddleware("/{path...}", ...)`. Documented in `docs/pages.md` §"Wrapping routes with middleware" and the README quick-reference. |
| C4 | Page render error contract | Trivial (mostly docs) | ✅ New `WithErrorHandler(gastro.PageErrorHandler)` option. Generated handler routes `template.Execute` errors through `__router.__gastro_handleError` which dispatches to the user-supplied handler or `gastroRuntime.DefaultErrorHandler`. Default mirrors `Recover`'s gating: log always, write 500 only when the response is uncommitted (`HeaderCommitted(w) || BodyWritten(w)` both false). Replaces the per-handler `log.Printf("... template execution failed ...")` line with centralised dispatch. New: `pkg/gastro/error_handler.go` (`PageErrorHandler` type + `DefaultErrorHandler`), `pkg/gastro/error_handler_test.go` (4 cases covering uncommitted/committed/header-only/plain-writer). Integration test `internal/compiler/error_handler_integration_test.go` proves end-to-end dispatch + error chain. New `docs/error-handling.md` enumerates the five failure modes (parse-prod, parse-dev, render error, frontmatter panic, missing dep) with default behaviour, user knob, and recipes. `gastro.FromContextOK[T]` promoted as the graceful-degradation path for optional deps. |

### Wave 5 — Follow-ups

Wave 5 is empty. Both originally-planned items have been dropped.

| ID | Title | Effort | Notes |
|---|---|---|---|
| ~~A2~~ | ~~Embed indirection (option 1)~~ | ~~Small~~ | **Dropped 2026-05-02 (Q4 audit).** A2 as written is infeasible: `.gastro/templates/*.html` are not copies of source `.gastro` files — they're the transformed template body (frontmatter stripped, markdown directives expanded, `{{ wrap }}` rewritten to compiled component calls, filenames flattened). Pointing `//go:embed` at source `pages/`/`components/` would embed raw `.gastro` files that `html/template` cannot parse. The stated benefit ("cuts the embed half of the committed tree") is also obsolete: `.gastro/` is gitignored by default at the repo root and in the scaffold, and `git ls-files | grep '\.gastro/'` returns zero tracked files. For downstream projects that opt to commit `.gastro/`, the per-project remedy is to add `.gastro/templates/` to their own `.gitignore` plus `gastro check` in CI — no framework change needed. |
| ~~A6~~ | ~~Static-asset hashing~~ | ~~Medium~~ | **Dropped (Q5).** Frontend `?v=` query params handle cache-busting. For custom headers on static assets, document the existing `WithOverride("GET /static/", ...)` and `Mux()` escape hatches rather than adding framework surface. |

### Deferred — ship when an adopter asks

| ID | Title | Why deferred |
|---|---|---|
| A1 | CLI toolchain pinning | Nobody's asking for `go:generate` workflow. Gastro users run `gastro dev` or `gastro build`. Adding `go:generate` support is theoretical completeness, not friction relief. |
| D1 | `gastro watch` for library-mode users | Library-mode users can wire `fsnotify` or use `watchexec` with `gastro generate`. No adopter has asked for this. Adding a public command is permanent API surface for an unvalidated use case. |

---

## 4. Track B — Page model v2: ambient `(w, r)` + conditional render

Track B is a foundational refactor of the page handler model, decided
through theorycraft on 2026-05-02. It runs **parallel** to the lettered
waves: it doesn't block them and they don't block it. Its size and BC
implications mean it's tracked separately rather than slotted as a
single wave.

### 4.1 Why a separate track

- **Pre-1.0 breaking change.** Existing pages migrate from `gastro.Context()` to ambient `(w, r)`. Per the BC posture in `DECISIONS.md` (2026-04-26), pre-1.0 churn is acceptable; this is the largest such churn since the handler-instance refactor.
- **Foundational.** Touches codegen, runtime, LSP, all four `examples/`, scaffold, and multiple docs. Not a one-PR feature.
- **Independent of A/B/C/D items.** A5 (children) operates on components, which Track B doesn't touch. ~~A4 (selectors) was dropped (Q1).~~ C2 (middleware) operates on the router, which Track B doesn't redesign. C4 (error handler) interacts mildly — see §4.6.

### 4.2 The decided model

Pages become method-aware Go handlers with `html/template` rendering as
the default success path. Concretely:

- **Frontmatter has ambient `w http.ResponseWriter` and `r *http.Request`.** No `gastro.Context()` marker.
- **One `.gastro` file per path.** `pages/board.gastro` registers `/board` for all methods; frontmatter branches on `r.Method`.
- **Conditional render.** Codegen wraps `w` in a tracking writer. After frontmatter completes:
  - If the **body** has been written → skip template render.
  - Else → render template with upper-case frontmatter vars (today's behaviour). Custom status from `WriteHeader` is preserved.
- **`gastro.Context()` deprecated.** Build warning for two minor versions, then removed. All docs and examples migrate to `(w, r)` immediately.

#### Example shape

```gastro
--- pages/board.gastro
import Board "components/board.gastro"

if r.Method == "POST" {
    if err := svc.Place(r); err != nil {
        http.Error(w, err.Error(), http.StatusConflict)
        return
    }
    sse := datastar.NewSSE(w, r)
    sse.PatchElements(rendered)
    return
}

filter := boardFilterFromQuery(r)
deps := gastro.From[BoardDeps](r.Context())

Columns := buildColumns(deps.State(), filter)
Filter := filter
---
{{ Board (dict "Columns" .Columns "Filter" .Filter) }}
```

POST path: writes a body, template skipped. GET path: doesn't write a
body, template renders with `Columns` and `Filter`. No new keywords.

### 4.3 Decisions resolved (theorycraft 2026-05-02)

| Q | Question | Decision |
|---|---|---|
| Q1 | Path-level vs method-level files | **Path-level.** One file per path; frontmatter branches on `r.Method`. |
| Q2 | Hard break on `gastro.Context()` or compat window? | **Compat window.** Mark deprecated, emit build warning, migrate all docs and examples immediately. Remove after two minor versions. |
| Q3 | What signal triggers conditional render skip? | **Body write.** `Write(...)` is the trigger; `WriteHeader(...)` alone is not. `Header().Set(...)` never triggers. |
| Q3a | Track `headerCommitted` and `bodyWritten` separately? | **Yes.** Pattern "set custom status, then render" works (e.g. `WriteHeader(201)` followed by template render emits 201 + rendered HTML). |
| Q3b | Interface preservation for `Flusher` / `Hijacker` / `Pusher`? | **Flusher + Hijacker only, no Pusher.** Pusher is HTTP/2-server-push-only; browsers deprecated it (Chrome 106+). The modern replacement (HTTP 103 Early Hints) needs no wrapper support. ~130 LOC saved vs. supporting all three; if Pusher revives, adding it later is a 10-minute change. |

### 4.4 Implementation outline (shipped — commit f356cbc through 2bb3c9f)

All changes below have landed. This section is retained as a reference for
future readers.

| Subsystem | Change |
|---|---|
| `internal/parser` | No changes to grammar. |
| `internal/codegen` (handler template) | Drop `gastro.Context()` injection; inject `w *gastroWriter, r *http.Request` directly. After frontmatter, check `w.bodyWritten` and conditionally call template render with stored status. |
| `internal/codegen` (marker rewriting) | Extend marker rewriting beyond `gastro.Context()` / `gastro.Props()` to cover the runtime symbols frontmatter reaches for: `gastro.From`, `gastro.FromOK`, `gastro.FromContext`, `gastro.NewSSE`, and `gastro.Render.X`. Full rewrite table in §4.10. Other `gastro.X` references are compile-time errors. |
| `internal/codegen` (validation) | Detect `gastro.Context()` usage in frontmatter; emit deprecation warning until removal. |
| `pkg/gastro` | New unexported `gastroWriter` implementing `http.ResponseWriter`, plus `http.Flusher` and `http.Hijacker` via interface composition. Combinatorial wrapper-type pattern: 4 concrete types (base, +Flusher, +Hijacker, +both) dispatched at construction time based on what the underlying writer supports. ~120 LOC. |
| `pkg/gastro/Recover` | Consume the same writer; if `bodyWritten`, log only (no double-write). |
| `internal/analysis/respwrite.go` (new) | Shared syntactic AST pass: identify response-write sites and check each is followed by `return` (or is the last statement of its block). Used by both codegen and LSP. See §4.9 for rules. |
| `internal/codegen/validate.go` | Call the shared analysis; emit warnings into the existing `Warnings` channel. Strict mode promotes to error (matches dict-key validation precedent). |
| `internal/lsp/template` | Replace the `ctx`-without-`gastro.Context()` warning with the shared "writes-to-`w`-without-return" diagnostic. |
| `examples/blog`, `examples/gastro` | Migrate `gastro.Context()` references (3 + 4 pages) to ambient `(w, r)`. |
| `examples/sse` | Beyond migration: refactor at least one of the guestbook variants to demonstrate the new pattern — page handles both `GET` (initial render) and `POST` (Datastar patch) in the same `.gastro` file by branching on `r.Method`. This is Track B's headline use case; the example needs to show it. |
| `examples/dashboard` | Audit only; no `gastro.Context()` references today. Verify nothing else needs adjusting. |
| `internal/scaffold/template` | Current `pages/index.gastro` is propless and needs no migration. Decide whether to enrich it to teach the new pattern (lean: keep minimal; users encounter the pattern in `docs/pages.md` and `examples/sse`). |
| **Primary docs (rewrite)** | `docs/pages.md` (rewrite the page-authoring section, show method branching), `docs/sse.md` (update "Datastar Page" section: same file is now the SSE endpoint), `docs/design.md` §21 (new sub-section recording the model change). |
| **Secondary docs (touch-up)** | `README.md` (Quick Start page snippet), `docs/getting-started.md` (walkthrough), `docs/components.md` (page/component split section), `docs/architecture.md` (data-flow diagram + sample frontmatter), `docs/dev-mode.md` (any page-change examples), `docs/comparison.md` (the "Gastro" code sample). |

**Effort:** ~~Large+.~~ Shipped across 8 commits (f356cbc–2bb3c9f). Codegen + runtime + LSP + four example migrations + primary and secondary doc updates.

### 4.5 Migration path (executed)

Shipped as a hard flip in a single release branch (commits f356cbc–2bb3c9f),
matching the precedent set by the 2026-04-26 handler-instance refactor.
No feature flag was used.

1. ✅ Runtime, codegen, analyzer, and LSP changes landed as coordinated commits.
2. ✅ All examples and docs migrated. `examples/sse` refactor (§4.10) is the canonical demonstration. `gastro check` passes all four.
3. ✅ Merged and shipped. `DECISIONS.md` entry + `docs/design.md` §21 sub-section written. `gastro.Context()` emits a build-time deprecation warning (per `docs/contributing.md` Deprecation Policy, which was added in the same release).
4. 🔜 **Remove `gastro.Context()`** after two minor versions (currently on v0.1.15; Track B is queued for the next release).

**Known leftover:** `examples/gastro/pages/examples/guestbook-plain.gastro:50` still
contains `gastro.Context()`. The deprecation warning catches it at build time;
it should be migrated in the next sweep to avoid warning noise for demo users.

### 4.6 Interactions with other items

- **A5** is independent — it operates on components, not pages. ~~A4 was dropped (Q1).~~
- **C2** (middleware) is independent — middleware wraps `Handler()`, not the page-internal frontmatter execution.
- **C4** (error contract) becomes more useful: `WithErrorHandler` is the natural home for translating frontmatter errors into responses once `ctx.Error` is gone. Consider whether `gastro.Throw(w, code, msg)` or similar should exist as one-line sugar over `http.Error(w, msg, code) + return`. Out of scope for Track B itself; revisit in Wave 4.
- **Held C1** stays held but its underlying friction (mutating handlers in `.gastro` files) is **partially answered** by Track B. A POST handler can now live in the same `.gastro` file as the GET that renders the page, sharing locals and helpers. The bit C1 *added* — co-located route declarations and a typed `datastar.Patch` return type — stays out of scope (rule 2).

### 4.7 Open sub-questions for Track B

**Resolved 2026-05-02:**

- **`gastro.Throw` sugar: no.** Trust users to write `return` after response writes. Hazard mitigated by codegen-time + LSP warnings (see §4.9).
- **Codegen-time missing-return detection: yes.** Implemented as a syntactic AST pass shared between `internal/codegen/validate.go` and the LSP, matching the dict-key validation precedent (2026-04-30). Heuristic: calls passing the literal identifiers `w` / `r`, or method calls on `w`, must be followed by `return` or be the last statement of their enclosing block. Strict mode promotes warnings to errors (`generate` / `build` / `check`); `dev` warns only.
- **Q3b: Flusher + Hijacker, no Pusher.** Pusher is the Go API for HTTP/2 server push only — not SSE (Flusher), not WebSocket upgrade (Hijacker), not Early Hints. Browser support was removed (Chrome 106, Firefox shortly after); the modern replacement is HTTP 103 Early Hints, which works through plain `WriteHeader` and needs no wrapper support. ~130 LOC saved vs. Option C; if Pusher revives, adding it later is a 10-minute change.
- **Naming: `gastroWriter`.** Unexported, appears in stack traces.

**Acknowledged limitations / out-of-scope (not pending work):**

- **LSP analysis depth for user helpers.** Identifier-based detection (passes literal `w` / `r`) covers stdlib and direct user helpers. Helpers that wrap `w` opaquely (e.g. middleware that captures `w` in a closure) are not detected. Acceptable for v1; revisit if real adopters report misses.
- **Component access to request.** Components today have no access to `w` or `r`. That stays — components are framework-agnostic. Whether component frontmatter should be able to receive a `*http.Request` as a typed dependency via `WithDeps[T]` is out of scope for Track B and flagged as a follow-up.

### 4.8 Acceptance criteria for Track B (all met ✅)

All 11 criteria were verified on the Track B branch before merge.
See `DECISIONS.md` 2026-05-02 entry for the full verification record.

1. ✅ Wrapper exposes Flusher and Hijacker via 4-combination type pattern (no Pusher).
2. ✅ `bodyWritten` and `headerCommitted` tracked independently; "`WriteHeader`-then-render" preserves custom status.
3. ✅ `gastro.Recover` checks `bodyWritten`; concurrent panic + partial write does not double-respond.
4. ✅ Codegen emits "writes-to-`w`-without-return" warnings; strict promotes to error; dev warns.
5. ✅ LSP emits the same warning via shared `internal/analysis/respwrite.go`.
6. ✅ Examples migrated — 3 of 4 examples zero `gastro.Context()` references. **Known leftover:** `examples/gastro/pages/examples/guestbook-plain.gastro:50` has one remaining `gastro.Context()` call (caught by build warning, lower priority than the SSE and blog examples). `examples/sse` demonstrates single-file GET+POST pattern. All four pass `gastro check`.
7. ✅ `gastro.Context()` deprecated: build warning emitted; build succeeds.
8. ✅ Primary docs rewritten: `docs/pages.md`, `docs/sse.md`, `docs/design.md` §21.
9. ✅ Secondary docs touched up: all six files show new pattern with zero `gastro.Context()` references.
10. ✅ Scaffold reviewed: kept minimal.
11. ✅ `gastro.X` marker rewriting covers the enumerated runtime symbols; unknown references produce compile-time error.

### 4.9 Missing-return detection rules

Applied by both codegen (`internal/codegen/validate.go`) and LSP
(`internal/lsp/template/diagnose.go`) via shared analysis in
`internal/analysis/respwrite.go`. Strictly syntactic; no type-checking
required.

**A frontmatter call is a *write site* if any of:**

- Any argument is the literal identifier `w` (e.g. `http.Error(w, ...)`, `datastar.NewSSE(w, r)`).
- Any argument is the literal identifier `r` *and* the call is recognised as a redirect helper. *(Conservative: today this means `http.Redirect`. If a stdlib pattern emerges that takes `r` and writes, add it explicitly rather than broadening the rule.)*
- The call is a method on `w` (`w.Write(...)`, `w.WriteHeader(...)`, `w.Header().Set(...)`).
  - Note: `w.Header().Set(...)` does **not** commit the response at runtime, but for the purposes of this lint, treating it the same is harmless because it's almost always followed by an actual write.

**A write site needs no `return` if:**

- The next statement in its enclosing block is `return`, *or*
- It is the last statement of its enclosing block, *or*
- It is the last statement of the frontmatter top-level (the codegen-wrapped function returns naturally).

**Otherwise the analyser emits:**

```
gastro: pages/board.gastro:5: response was written but no return follows;
  frontmatter execution will continue and any uppercase variables computed
  after this point are dead code. Add `return` to short-circuit.
```

**Known false-positive class:** a helper whose first parameter is
`http.ResponseWriter` but does not actually write (e.g.
`extractRequestID(w, r) string`). Rare in practice; mitigation is to
remove the unused parameter.

---

### 4.10 Reference spike: GET-renders + POST-patches in one file

This is the headline shape Track B targets and the canonical demonstration
for `examples/sse/` (acceptance criterion §4.8 #6). Recorded here so the
implementation has a concrete reference and any deviation in the shipped
example needs explicit justification.

### Page (the headline)

```gastro
--- pages/counter.gastro
import (
    "net/http"

    Layout  "components/layout.gastro"
    Counter "components/counter.gastro"

    "github.com/andrioid/gastro/pkg/gastro/datastar"
)

state := gastro.From[*AppState](r.Context())

if r.Method == "POST" {
    n := state.Count.Add(1)

    html, err := gastro.Render.Counter(CounterProps{Count: int(n)})
    if err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }

    sse := datastar.NewSSE(w, r)
    sse.PatchElements(html)
    return
}

// GET — fall through to template render.
Title := "Counter"
Count := int(state.Count.Load())
---
{{ wrap Layout (dict "Title" .Title) }}
    {{ Counter (dict "Count" .Count) }}
    <button data-on:click="@post('/counter')">+1</button>
{{ end }}
```

### Supporting components

```gastro
--- components/counter.gastro
---
type Props struct {
    Count int
}
p := gastro.Props()
Count := p.Count
---
<div id="counter">
    Count: <strong>{{ .Count }}</strong>
</div>
```

```gastro
--- components/layout.gastro
---
type Props struct {
    Title string
}
p := gastro.Props()
Title := p.Title
---
<!doctype html>
<html>
<head>
    <title>{{ .Title }}</title>
    <script type="module"
        src="https://cdn.jsdelivr.net/gh/starfederation/datastar@main/bundles/datastar.js"></script>
</head>
<body>{{ .Children }}</body>
</html>
```

### Wiring (`main.go`)

```go
package main

import (
    "log"
    "net/http"
    "sync/atomic"

    gastro "myapp/.gastro"
    gastroRuntime "github.com/andrioid/gastro/pkg/gastro"
)

type AppState struct {
    Count *atomic.Int64
}

func main() {
    state := &AppState{Count: &atomic.Int64{}}

    router := gastro.New(gastroRuntime.WithDeps(state))

    log.Fatal(http.ListenAndServe(":4242", router.Handler()))
}
```

### What happens at runtime

**`GET /counter`:**
1. `state := gastro.From[*AppState](r.Context())` retrieves the typed dep.
2. `r.Method == "POST"` is false; the `if` block is skipped.
3. `Title` and `Count` are computed (uppercase → exported to template).
4. Frontmatter ends. `gastroWriter.bodyWritten == false` → template renders.
5. Browser receives HTML with the Datastar script attached.

**`POST /counter`** (triggered by `data-on:click="@post('/counter')"`):
1. Same `gastro.From[...]` retrieves the dep.
2. `r.Method == "POST"` is true; enter the branch.
3. `state.Count.Add(1)` increments the atomic counter.
4. `gastro.Render.Counter(...)` renders only the Counter component to `template.HTML` (type-safe, compile-checked).
5. `datastar.NewSSE(w, r)` writes SSE headers and calls `WriteHeader(200)` — `headerCommitted = true`, `bodyWritten` still false.
6. `sse.PatchElements(html)` writes the SSE event body — `bodyWritten = true`.
7. `return` exits frontmatter (the analyzer in §4.9 enforces this).
8. Frontmatter ends. `bodyWritten == true` → **template render is skipped.**
9. Datastar's client patches `#counter` in the DOM.

### Track B mechanisms this exercises

- **Ambient `(w, r)`** — no `gastro.Context()` marker; `r` and `w` are function parameters injected by codegen.
- **Conditional render** — the POST branch's body write skips template rendering (§4.2). The GET branch's lack of write triggers template rendering with the staged `Title` / `Count`.
- **Missing-return analysis** (§4.9) — the analyzer enforces `return` after `sse.PatchElements(html)` and after `http.Error`. The example would fail `gastro generate --strict` if either `return` were removed.
- **Type-safe component rendering** — `gastro.Render.Counter(CounterProps{...})` is checked at compile time. Renaming a Props field breaks the build, not silently the runtime.
- **`gastroWriter` interface composition** — `datastar.NewSSE(w, r)` requires the underlying writer to implement `http.Flusher`. Track B's wrapper preserves it (§4.7 Q3b: Option B).

### Implementation note: `gastro.X` resolution from frontmatter

The spike calls `gastro.From[*AppState](r.Context())` and
`gastro.Render.Counter(...)` directly. Today only `gastro.Context()` and
`gastro.Props()` resolve from frontmatter (both as marker rewrites); other
`gastro.X` references would not compile. **Track B must extend marker
rewriting to cover the runtime API surface used from frontmatter:**

| Frontmatter call | Rewritten to |
|---|---|
| `gastro.Context()` | (stripped — deprecated, emits warning) |
| `gastro.Props()` | `__props` (existing) |
| `gastro.From[T](ctx)` | `gastroRuntime.From[T](ctx)` |
| `gastro.FromOK[T](ctx)` | `gastroRuntime.FromOK[T](ctx)` |
| `gastro.FromContext[T](rctx)` | `gastroRuntime.FromContext[T](rctx)` |
| `gastro.NewSSE(w, r)` | `gastroRuntime.NewSSE(w, r)` |
| `gastro.Render.X(...)` | `Render.X(...)` (generated package contains `Render` directly) |

Any other `gastro.X` reference is an error: "unknown gastro runtime
symbol; did you mean to import a package?". This keeps the implicit
`gastro` namespace finite and predictable.

### Things this spike deliberately does not show

- **CSRF on POST.** Real apps need it. Once C2 (route middleware composition) ships: `gastroRuntime.WithMiddleware("POST /counter", csrf.Require)`.
- **Selector constants.** ~~A4 was dropped; users define their own id constants.~~ `datastar.PatchElements(html)` matches `#counter` implicitly via `id=`; explicit selector constants are the user's responsibility.
- **Typed `Children`.** ~~`{{ wrap Layout ... }}` currently smuggles children through `dict` `__children`. Once A5 ships,~~ As of A5 (commit `e64b553`), `wrap` sets `Children` directly on the dict, and the typed Render API exposes children as `XProps{Children: html}`. Named content areas (sidebar, footer) are just `template.HTML` Props fields — no `slots:` keyword.

None of these are needed for the page above to function; they make it
safer or cleaner.

---

## 5. Per-wave acceptance criteria

A wave is "done" when **all** of the following hold for every item in it:

1. Item ships behind a public API change documented in `DECISIONS.md` with a
   dated entry, the same shape as existing entries.
2. Public API additions are reflected in:
   - `docs/components.md` (for component-side changes: A5)
   - `docs/pages.md` (for router-option changes: B2, C2, C4)
   - `docs/error-handling.md` (new file, for C4)
   - `README.md` quick-reference (for additive `Option` values)
3. At least one example app under `examples/` exercises the new surface
   (smoke proof; not a full-scale demo).
4. Tests cover the behaviour with `-race` (per `AGENTS.md`).
5. `gastro check` against all four `examples/` produces no drift.

---

## 6. Pre-work / blockers to clear before each wave

| Wave / Track | Blocker | Resolution |
|---|---|---|
| Wave 1 | ✅ Shipped (commits `1c229b4`–`133890e`, 2026-05-02). | Done. |
| Wave 2 | ~~None.~~ Wave 2 is empty — its only item (D1) was deferred. B2 ships in Wave 1. | ~~D1 only depends on existing `runDev` code.~~ |
| Wave 3 | ✅ Shipped 2026-05-02. A5 deprecation policy was simplified (Q2): no parallel runtime path; Go's type checker handles variadic-drop migration, validator emits a targeted hint for `__children` dict literals. | Done. |
| Wave 4 | ✅ Shipped 2026-05-02. Audit conclusion: `WithOverride`'s exact-match validation (`internal/compiler/compiler.go:737`) stays untouched — C2's validation diverges fundamentally (throwaway-mux probe vs map lookup), so it lives as a sibling block, not a refactor. Throwaway-mux probe lives in `pkg/gastro/middleware.go` for unit-testability; codegen template stays thin. | Done. |
| Wave 5 | ✅ Empty after 2026-05-02. A2 dropped per Q4 audit (infeasible as framed; benefit obsolete because `.gastro/` is already gitignored by default). A6 dropped per Q5. | Done. |
| Track B | ✅ Shipped (commit `2bb3c9f`). Deprecation policy already in `docs/contributing.md`. All sub-questions resolved 2026-05-02 (§4.7). | Done. |

---

## 7. Open questions (track and answer before the relevant wave)

| # | Question | Owner | Block on |
|---|---|---|---|
| ~~Q1~~ | ~~A4: selector behaviour~~ | **Dropped** | A4 dropped |
| ~~Q2~~ | ~~A5: deprecation warning format~~ | **Resolved 2026-05-02:** No deprecation window. Render API variadic dropped immediately (Go compiler is the migration signal); `__children` dict literal becomes a `did you mean Children?` variant of the existing unknown-key warning. See §3.1 Migration. | A5 done |
| Q3 | C2: precedence between `WithOverride` and `WithMiddleware` | **Resolved:** middleware wraps override. | Wave 4 |
| ~~Q4~~ | ~~A2: does `//go:embed` at source dirs change `gastro dev` path resolution?~~ | **Resolved 2026-05-02:** Q4 was the wrong question — the audit revealed A2 itself is infeasible (see Wave 5 row). Dev-mode path resolution is a one-line change in `pkg/gastro/fs.go:38`, but moot because there's no viable target to repoint at. A2 dropped. | Wave 5 done |
| ~~Q5~~ | ~~A6: hashed-asset URL discoverability~~ | **Dropped** | A6 dropped |
| ~~Q6~~ | ~~C2: how does `*` wildcard interact with the existing pattern-validation table?~~ | **Resolved & shipped 2026-05-02:** Adopted Go's `http.ServeMux` pattern syntax — `{slug}` for a segment, `{slug...}` for a trailing catch-all. Bare catch-all is `"/{path...}"`. Validation via `gastroRuntime.PatternMatchesAnyRoute` (throwaway-mux probe in `pkg/gastro/middleware.go`). | Wave 4 done |
| ~~Q7~~ | ~~C2: are middleware patterns method-scoped or path-only?~~ | **Resolved & shipped 2026-05-02:** Path-only, mirrors `WithOverride`. Method-specific middleware branches on `r.Method` internally. | Wave 4 done |

Track B's open sub-questions live in §4.7 (kept inline with the track
because they're tightly coupled to the design rather than independent
follow-ups).

---

## 8. Re-evaluation triggers

The strategic stance should be re-opened if any of these happens:

- A second downstream project independently hits the **full C1 friction**
  — wanting co-located route declarations and a typed `datastar.Patch`
  return type that Track B does not address — and reaches the same
  library-mode workaround as git-pm. Two data points is signal; one is
  anecdote. (Track B partially defuses the simpler form of this trigger:
  mutating handlers can now live in `.gastro` files without inventing DSL.)
- An adopter shows a concrete way to close the residual C1 gap that
  respects rule 2 (no new language).
- A real performance issue is identified in the `Render` map round-trip
  (deferred per the 2026-04-26 decision); that re-opens the question of
  typed render bypass, which sits adjacent to A3.

Until then, the held items in §2 stay held.

---

## 9. What this plan does not cover

- **Section E** (file issues against gastro for A2) — administrative,
  handle when shipping the relevant wave. ~~A1 deferred.~~
- **Section F** (project-side items in git-pm) — out of scope for the
  gastro repository.
- **The "Items considered but deferred" notes** in `docs/design.md` §21
  (typed Render bypassing the map round-trip; frontmatter
  `Deps := gastro.Deps[T]()` declaration) — both are deferred there for
  reasons that still hold; revisit independently if benchmarks or real
  usage say otherwise.
