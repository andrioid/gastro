# LSP shadow file audit — plan

_Created 2026-05-04 alongside the three fixes captured in §2 below.
**Closed 2026-05-10**: see [§9 Closure](#9-closure-2026-05-10)._

> **Status: ✅ shipped.** Class A (template undefined) and Class B
> (Render.X / XProps undefined) are both resolved. Class B took the
> richer Option B2 path: the shadow projects the real `XProps` shape
> with field-by-field accuracy and propagates needed cross-package
> imports (commit `e8c3ade`). The audit harness lives at
> `cmd/auditshadow` and reports 0 real diagnostics across all four
> `examples/`. Q1–Q3 are answered in §7; §6.1 is fixed; §6.2/§6.3
> remain explicitly deferred per this plan's own "document and
> ignore unless an adopter complains" policy.

## 1. Why this exists

Working in `git-pm` (a downstream gastro consumer) we hit a screenshot's
worth of red/yellow squiggles on a perfectly valid `.gastro` page:

- `gastro.From[wirings.BrowseDeps](r.Context())` lit up red.
- `Layout` in `{{ Layout (dict ...) }}` carried a yellow "missing prop
  Detail" warning even though Detail is `template.HTML` and gated by
  `{{ if .HasDetail }}`.
- `"Children"` in the same dict lit up red as an unknown prop, even
  though it's the synthetic key the codegen injects/recognises.

Three different bugs, two different layers (template diagnostics +
gopls-via-shadow). All three are now fixed (§2). This document is the
plan for the **broader audit** that grew out of the investigation:
sweep every `.gastro` file in a representative project, classify the
remaining shadow-file diagnostics, and close the gaps systematically.

## 2. What already shipped (2026-05-04)

| # | Layer | Fix | File(s) |
|---|---|---|---|
| 1 | `internal/lsp/template` | Skip the synthetic `"Children"` dict key in unknown-prop checks (mirrors `internal/codegen/validate.go:189`). | `completions.go`, `completions_test.go` |
| 2 | `internal/lsp/template` | Suppress missing-prop warnings for fields whose zero value is the natural "absent" representation: `template.HTML`, pointers, slices, maps, channels, funcs, `interface{}`/`any`. Concrete value types still warn. | same as above |
| 3 | `internal/lsp/shadow` | Type-check `gastro.From[T]`, `FromOK`, `FromContext`, `FromContextOK`, `NewSSE`, `Render` by adding package-level helpers in the shadow and a `rewriteGastroSugar` pass that mirrors the codegen allowlist. Also dedupes `net/http` and adds an unconditional `context` import. | `workspace.go`, `workspace_test.go` |

Tests added are behaviour-level: the shadow tests use real
`go/types` against the generated source so a regression looks like
gopls breaking, not like a string-shape assertion failing.

The three fixes are independent and can be reverted independently.

## 3. Audit baseline (2026-05-04)

After the three fixes, run a one-shot harness that walks every
`.gastro` under `git-pm/internal/web/`, generates the shadow with
`shadow.Workspace.UpdateFile`, parses + type-checks it with
`go/types.Config`, and groups the resulting diagnostics by class
(import-resolution failures filtered out — those are an artefact of
running outside the gopls workspace, not a real LSP issue).

Result against `git-pm/internal/web/` (46 `.gastro` files):

```
=== diagnostic classes (count desc) ===
  16  undefined identifier
   8  missing method/field on stub type
```

That's it. Two bounded classes. Each one corresponds to a specific
codegen-emitted symbol that the shadow does not currently project.

### 3.1 The audit harness (rebuild on demand)

The harness was scratch-only and is no longer in the tree. Rebuild
shape:

- A `cmd/auditshadow` (or `tmp/auditshadow`) `main.go` that takes
  a project root, calls `shadow.NewWorkspace` + `UpdateFile` per
  `.gastro`, parses with `go/parser`, type-checks with
  `go/types.Config{Importer: importer.Default()}`, and prints
  diagnostic classes grouped by leading words.
- Filter `could not import` / `cannot find package` errors —
  they're false positives outside the gopls module graph.
- Run with `unset GASTRO_PROJECT` (see §6.1).

Re-run after every shadow change so we always know what the residual
gap surface looks like against a real downstream.

## 4. Class A — `undefined: template`  (8 occurrences)

### 4.1 Where it fires

Every component whose `Props` struct has a `template.HTML` field —
e.g. `components/layout.gastro` (`Detail template.HTML`),
`components/task-description.gastro`, `components/task-panel.gastro`,
etc. The shadow hoists the `Props` struct to package level so
`Props()` can return `*Props`; the hoisting copies the source verbatim,
including `template.HTML`, but the shadow imports only `net/http` and
`context` so `template` is undefined at the package scope.

### 4.2 Fix shape

Add `html/template` to the unconditional shadow import block, alongside
`net/http` and `context`. Suppress unused-import warnings the same way
(`var _ template.HTML`).

Risks:

- A user who already imports `html/template` in their frontmatter
  would currently end up with a duplicate import. The dedup loop
  added in fix #3 already handles this — confirm by extending
  `seenImports` to include `"html/template"`.
- `text/template` is not relevant for `template.HTML` but pages that
  produce `template.HTML` strings sometimes import `text/template/parse`.
  Keep these as user-driven imports; do not add `text/template`
  unconditionally.

### 4.3 TDD shape

```
TestWorkspace_TemplateHTMLPropTypeChecks
  given a component whose Props struct uses template.HTML
  when the shadow is generated and type-checked
  then no `undefined: template` diagnostic appears
```

Use `typeCheckVirtualFile` (already in `workspace_test.go`) to assert
zero diagnostics.

## 5. Class B — `__gastroRender.X undefined` + `undefined: XProps` (16 occurrences)

### 5.1 Where it fires

Every page that calls `gastro.Render.X(props)` — e.g.
`pages/index.gastro` (`Render.Layout(LayoutProps{...})`),
`pages/decisions/[id].gastro` (`Render.DecisionDetail(...)`),
`pages/tasks/[id]/place.gastro` (`Render.BoardColumn(...)`).

The codegen rewrites `gastro.Render.X(p)` → `Render.X(p)` and emits
`Render` plus `XProps = componentXProps` in the project's
`.gastro/render.go`. The shadow does not pull in that file, so:

- `Render.X` becomes `__gastroRender.X` after `rewriteGastroSugar`,
  and `__gastroRender` is `struct{}` with no methods — so every
  `.X` errors as "no field or method".
- `XProps` is genuinely undefined in shadow-package scope.

### 5.2 Fix shape — three options, ordered by ambition

#### Option B1 (cheap): generate stub method declarations per component

At workspace setup time, scan `<projectRoot>/components/*.gastro` (and
nested subdirs) for component files. For each component name `X`,
emit two synthetic declarations into the shadow file:

```go
type __gastroRenderXProps = struct{} // or read the real shape; see B2
type XProps = __gastroRenderXProps   // matches codegen alias name
func (__gastroRenderType) X(__gastroRenderXProps) (string, error) { return "", nil }
```

Define `__gastroRenderType` as a named struct so we can hang methods
off it, replacing the current `var __gastroRender struct{}` line.

Pros: closes the gap immediately, no per-project type knowledge needed,
all 16 `git-pm` diagnostics drop to zero.

Cons: `XProps` is `struct{}`, so `Render.X(LayoutProps{Title: "x"})`
would type-check the call but `Title` field access would fail. Trade
the `XProps undefined` error for an `unknown field Title in struct{}`
error — same severity, different message.

#### Option B2 (richer): project the real `XProps` shape

After scanning each component, parse its frontmatter, run
`codegen.HoistTypeDeclarations` + `codegen.ParseStructFields` (already
exposed for the LSP), and emit the real `XProps` definition into the
shadow. `Render.X(LayoutProps{Title: "x"})` then type-checks fully,
including field-by-field assignability.

Pros: gold-standard fidelity. Same code path the codegen uses, no
risk of drift.

Cons: requires reading every component file when *any* `.gastro` is
opened. Two mitigations: (a) cache by component-file mtime, (b) the
LSP already has `inst.componentPropsCache` via
`resolveAllComponentProps` — extend that cache to back the shadow
projection too, so the cost is paid once per session per component.

#### Option B3 (best of both): typed `Render` value + alias to project's `.gastro` package

Idea: instead of stubbing, have the shadow `import` the project's
generated `.gastro` package (which already exports `Render`,
`LayoutProps`, etc.). Two challenges:

1. The shadow currently uses `var gastro = __gastroLib{}`. To pull in
   the real `Render`, we'd alias `gastro` to the project's generated
   package — which would shadow `__gastroLib` and break `Props()` /
   `Context()`. Resolution: rename our local stub
   (`__gastroLib{}` → `gastrolib{}`) and rewrite the frontmatter so
   `gastro.Props()` → `gastrolib.Props()`. We already do textual
   rewrites for the From sugar; this extends the same machinery.

2. The user might run the LSP before `gastro generate` has produced
   `.gastro/render.go`. Resolution: detect the missing dir and fall
   back to Option B1 stubs.

Pros: zero drift between codegen and shadow, no parsing-during-LSP
cost, full IDE-style "go to definition" on `Render.X` works.

Cons: invasive — touches the rewrite layer, requires a fresh
`gastro generate` to be present, and changes the ergonomics of
"open one .gastro file" because the shadow now depends on the
generated package.

### 5.3 Recommendation

Start with **B1** (stubs at workspace init) to close the visible
squiggles, ship the diagnostic count to zero, and learn what's
actually painful. Promote to **B2** if users complain about
`XProps{Field: ...}` not type-checking field-by-field.

Defer **B3** until we have a story for the "no `.gastro/` dir yet"
state — the cold-start case is still a daily occurrence in
small projects.

### 5.4 TDD shape (B1)

```
TestWorkspace_GastroRenderResolves
  given a project with components/layout.gastro
  when a page uses gastro.Render.Layout(LayoutProps{...})
  then the shadow type-checks the call as a method call returning (string, error)

TestWorkspace_RenderUnknownComponentStillErrors
  given a project with no `components/missing.gastro`
  when a page uses gastro.Render.Missing(...)
  then the shadow surfaces an "no field or method Missing" diagnostic
```

The second test guards against the "stub everything as `any`" trap —
we want unknown components to error.

## 6. Pre-existing issues (handle last per AGENTS.md)

### 6.1 `findProjectRoot` honours leaked `GASTRO_PROJECT` in tests

`internal/lsp/server/util.go:findProjectRoot` reads the env var
unconditionally; `internal/lsp/server/util_test.go` does not isolate
itself. Running `go test ./...` with `GASTRO_PROJECT` set in the
shell (very common — git-pm contributors set it for `mise dev`)
produces ~9 phantom failures in `TestFindProjectRoot_*`.

Two-line fix: `t.Setenv("GASTRO_PROJECT", "")` at the top of every
affected test, or a single `setupTest(t)` helper that does so.

### 6.2 Lowercase locals are not auto-suppressed

`workspace.go` adds `_ = X` lines only for *exported* (Title-cased)
frontmatter vars. A frontmatter that introduces lowercase locals it
doesn't reuse (rare, but possible — e.g. tearing down complex
expressions into stages) gets "declared and not used" diagnostics.

Not blocking, not in the audit baseline — surfaces only on
contrived shapes. Document and ignore unless a downstream complains.

### 6.3 Source-map column drift after `rewriteGastroSugar`

The sugar rewrite is line-stable (no inserted/removed newlines) but
shifts columns by a few characters per substitution
(`gastro.From` is 11 chars → `__gastroFrom` is 12). The source map
is line-only, so column passes through unchanged — diagnostic
squiggles end up off by ≤ a handful of characters horizontally.

Fix when it actually misbehaves visually. The cleanest path is
length-preserving aliases (e.g. pad helper names to match) rather
than enriching the source map with a column delta table.

## 7. Design questions — resolved

All three were resolved in-code as Class B was implemented; this
section now records the answers.

- **Q1 — cold start without `.gastro/`.** Resolved → silent fallback
  to a bare `*renderAPI` (no methods). Calls to `Render.X` then
  error with "no method X" until `gastro generate` runs and the next
  workspace invocation picks up the components. Rejected the
  warning-banner option because the cold-start state is normal in
  small projects (every fresh `git clone` hits it) and a banner
  would train users to ignore it. Implementation: `Workspace.routerStub`
  emits the bare stub when `componentMD` is empty; doc comment on
  the function records this contract.

- **Q2 — page vs component shape divergence.** Resolved → identical
  stub for both. The `routerStub` is generated for every shadow
  subpackage, including components that never call `Render.X`. The
  saving from gating on `info.IsComponent` is a few hundred bytes
  per shadow file; the cost would be a second code path and a
  shadow shape that depends on a flag derived from frontmatter
  parsing. Not worth it. Implementation: `Workspace.UpdateFile`
  unconditionally writes both the virtual `.go` file and the router
  stub.

- **Q3 — `gastro.Render.X` when `X` is a page.** Resolved →
  page names are not callable on `Render` because
  `codegen.ScanComponents` only walks `components/`, so pages never
  reach `componentMD`. The result is the desired error: "no method
  X". Pinned by
  `TestWorkspace_RenderAPIRejectsPageName` in
  `internal/lsp/shadow/workspace_test.go`, which builds a project
  with `pages/about.gastro` + `components/card.gastro`, calls
  `gastro.Render.About(...)`, and asserts the build error mentions
  `About`. The audit harness does not need a separate diagnostic
  class because the existing `missing-method-or-field` class
  already catches it.

## 8. Ordering — actual sequence

Plan-as-written said "Class A → Q1–Q3 → Class B Option B1 → re-run
harness → archive". Reality:

1. **Class A** — shipped via the unconditional `html/template`
   import in the per-package `routerStub` (lives in the synthesised
   `router_stub.go` companion file), with `var _ template.HTML` to
   suppress unused-import warnings. Low risk, single file, exactly
   as scoped.
2. **Class B** — went straight to Option B2 instead of B1. The
   richer projection (real `XProps` fields, real types,
   per-component-needed imports via `neededImportsForFields`) was
   no more code than B1's struct{} stubs once `codegen.ScanComponents`
   was promoted to a public helper, and avoided the documented B1
   regression ("trade `XProps undefined` for `unknown field Title`").
   B3 stays out of scope: requires a generated `.gastro/` package
   present, which the cold-start case (Q1) explicitly does not
   guarantee. Implementation in `internal/lsp/shadow/workspace.go`,
   commit `e8c3ade` (with subsequent module-root walk-up fix in the
   same commit).
3. **Audit harness** — promoted from scratch to a real binary at
   `cmd/auditshadow` (commit `e549346`), wired into `mise run audit`,
   and run as part of every LSP-touching plan's verification
   checklist. Reports 0 real diagnostics across all four `examples/`.
4. **Q1–Q3** — answered in §7, retroactively recorded after the
   implementation settled.
5. **§6.1 (test env leak)** — fixed by `internal/lsp/server/setup_test.go`
   adding a package-level `TestMain` that unsets `GASTRO_PROJECT`
   once before `m.Run()`. Confirmed by
   `GASTRO_PROJECT=/tmp go test -race ./internal/lsp/server/...`
   going from 10 phantom failures → green.
6. **§6.2 / §6.3** — explicitly deferred per this plan's own policy
   ("document and ignore unless an adopter complains"). No adopter
   has complained.
7. **Archive** — this doc moved to `docs/history/lsp-shadow-audit.md`
   on 2026-05-10; `cmd/auditshadow/main.go` and
   `internal/lsp/template/completions_test.go` updated to point at
   the new path. The 2026-05-08 reference inside
   `tmp/frontmatter-package-scope-plan.md` is left untouched
   because that plan is itself a dated record (precedent: 2026-05-02
   DECISIONS.md entry on the frictions plan archival).

## 9. Closure (2026-05-10)

All work tracked by this plan is shipped. The auditshadow harness is
the ongoing CI gate against regression: any new shadow drift would
show up as a non-zero diagnostic count in `mise run audit`.
