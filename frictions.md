# Gastro friction backlog
_Distilled from `feedback-from-git-pm-project.md` after the v0.1.13–v0.1.15
audit cycle. Items closed during that cycle are not listed here._

Last updated: 2026-05-01  
Baseline: gastro `v0.1.15` + commit `e0a2bc4`

---

## Notation

**Priority** follows the audit's P0–P3 scale.  
**Effort** is a rough implementation estimate for the gastro maintainer:

| Label | Meaning |
|---|---|
| Trivial | < 1 h, few lines |
| Small | half-day, one subsystem |
| Medium | 1–2 days, a few subsystems |
| Large | 3–5 days, design work required |
| XL | week+, architectural |

---

## Section A — upstream gastro frictions

### A1 · CLI not toolchain-pinnable · P1 · **Medium**

`go generate ./internal/web` shells out to a `gastro` binary on `$PATH` with
no version constraint. A contributor who runs `go mod tidy` after a version
bump gets a working build but silently retains an old CLI; the mismatch only
surfaces when generated output diverges or a subcommand is renamed.

**What done looks like:**
A `//go:generate go run github.com/andrioid/gastro/cmd/gastro@VERSION generate`
directive, _or_ a go-importable `gastro/codegen.Run(dir, outDir string)` that a
`tools.go`-style file can call. Either form lets `go mod tidy` track the CLI
version alongside the library.

**Subsystems touched:** `cmd/gastro` (must stay importable or expose a Run
func), `docs/getting-started.md`, scaffold template.

**Effort breakdown:** `cmd/gastro` is `package main`; exposing it as a library
requires extracting a `gastro/codegen` public package (internal/compiler already
does the work — it's mostly wiring + a public entry point). One day for the
extraction, half a day for docs and scaffold update.

---

### A2 · Generated code committed · P2 · **Medium** _(blocked by A1)_

Every component edit produces a ~26-file diff under `.gastro/`. Mitigations
in place (`linguist-generated`, `gastro check` CI gate) reduce the pain but
don't eliminate it.

**What done looks like (two independent sub-options):**

1. **Embed indirection** — point `//go:embed` in `embed.go` at the _source_
   `components/` and `pages/` directories rather than copies under
   `.gastro/templates/`. Cuts the embed half of the committed tree; the Go
   codegen files would still need committing. Effort: **Small** (compiler
   change to `generateEmbedFile` + template path adjustments).

2. **Reproducible generate step** — once A1 lands, a CI `go generate && git
   diff --exit-code` step is version-pinned and the committed tree becomes
   optional. The committed tree can then be moved to `.gitignore`. Effort:
   **Trivial** (once A1 is done).

---

### A3 · Two render paths to maintain · P2 · **Large** _(structural)_

Adding a Props field means editing both the typed `Props` struct (Go-side
consumers) and every `{{ X (dict ...) }}` call site in templates (untyped).
A0 (v0.1.15) now catches unknown dict keys at `gastro generate` time, which
converts runtime surprises into build errors — the path is finite and
mechanical, but still manual.

**What done looks like:** At codegen time, emit a static assertion or
generated helper that makes "add a Props field, re-run codegen, fix the build
errors" fully mechanical — no hand-editing of template call sites. This likely
requires either (a) a codegen-time migration that rewrites `dict` call sites,
or (b) moving away from `dict` toward a typed call syntax in `.gastro`
templates. Both are architectural changes to the template model.

**Effort breakdown:** Significant design work; depends on whether the template
syntax is extended or a codegen migration is preferred. Not a quick fix.

---

### A4 · Selector ↔ component decoupling · P2 · **Small**

SSE morph handlers hardcode selectors as Go strings (`"#pm-decisions-list"`)
alongside a `Render.X(props)` call. If the component's root element `id`
changes the patch silently targets nothing.

**What done looks like:** A `selector` annotation in component frontmatter:

```
---
selector: "#pm-decisions-list"
type Props struct { ... }
...
---
```

Codegen surfaces it as `Render.DecisionsList.Selector` (a `string` constant).
SSE handlers can then write `datastar.WithSelector(gastro.Render.DecisionsList.Selector)`.

**Subsystems touched:** `internal/parser` (new frontmatter field),
`internal/codegen` (generate the constant), `render.go` template.  
**Effort:** parser + codegen + render template change; no runtime work.

---

### A5 · Untyped children plumbing · P1 · **Medium**

`__children` is a magic map key injected by the `{{ wrap }}` transform. The
public API exposes `children ...template.HTML` but internally the value is
smuggled through an untyped `map[string]any`. Named slots require pre-rendered
`template.HTML` fields on Props (e.g. `LayoutProps.Detail`).

**What done looks like:** A typed `Children template.HTML` field generated
on every component's Props struct when `{{ .Children }}` is referenced in the
template body. Named slots declared in frontmatter (`slots: [detail, sidebar]`)
generate corresponding typed fields. The `__children` map key becomes an
internal detail hidden behind generated accessors.

**Subsystems touched:** `internal/parser`, `internal/codegen/generate.go`
(component template), `internal/compiler` (detect slot usage), `pkg/gastro/props.go`.

---

### A6 · No static-asset hashing · P2 · **Large**

`GetStaticFS` serves `/static/...` with no content fingerprint. Long-lived
`Cache-Control` headers are unsafe; the workaround is `no-cache` + ETag
(tracked in git-pm backlog task `01KQ3530`).

**What done looks like:** A `WithHashedStatic()` router option that at startup
reads each file in `staticAssetFS`, computes an 8-hex-char content hash,
registers redirect routes `/static/<hash>/file.js → /static/file.js`, and
rewrites `src`/`href` references in generated templates to the hashed form.

**Subsystems touched:** compiler (template reference rewriting at codegen
time), `pkg/gastro/fs.go`, generated `routes.go` template (hash redirect
routes or a custom file server).  
**Effort:** build-time rewriting is the hard part; a pure runtime approach
(hash on first serve, redirect) is smaller but doesn't let templates embed
hashed URLs statically.

---

### A7 · Component name collision warning · P2 · **Small**

Two component files that produce the same exported Render method name
(e.g. `decision.gastro` and `task-decisions.gastro` both map to `Decision`)
result in a compile error that isn't attributed to the naming conflict. The
current workaround is manual file renaming.

**What done looks like:** At compile time, before writing any output,
`Compile()` checks the full set of `ExportedComponentName(funcName)` values
for duplicates and returns a descriptive error:

```
gastro: component name collision: "Decision" would be generated by both
  components/decision.gastro and components/task-decisions.gastro
  rename one of them to avoid the conflict
```

**Subsystems touched:** `internal/compiler/compiler.go` (one pre-pass over
`components` before writing output).  
**Effort:** trivial loop + error message; no codegen changes.

---

### B1 · No snippet / include idiom · P2 · **Small**

Small shared markup fragments pay full component cost: a Props struct,
a `dict` call at every consumer, a new codegen entry, and a new `.gastro/`
file in the committed tree. The overhead exceeds the benefit for
10–20 line shared fragments (e.g. the `pm-md-editor` markup in git-pm).

**What done looks like:** A `snippets/` directory (or a frontmatter flag
`snippet: true`) for `.gastro` files with no Props struct. Callable as
`{{ include "snippets/md-editor.gastro" . }}` — the dot passes through the
caller's data without a `dict` map. No typed Render API entry, no `__children`
plumbing, no committed `.go` file; just a named template registered at startup.

**Subsystems touched:** `internal/compiler` (new discover + compile path for
snippets), `internal/router` (register snippet templates in the FuncMap),
template transform (recognise `{{ include }}`).  
**Effort:** new directory convention + a simpler codegen path; no parser
changes.

---

### B7a · `gastro dev` unusable for embedded-package projects · P2 · **Small**

`gastro dev` always builds `.gastro/dev-server` from a `main.go` it expects
alongside the gastro project root. Projects where the process entry point
lives elsewhere (e.g. `cmd/pm`) get a misleading "permission denied" error
because the binary simply doesn't exist.

**What done looks like:** A `gastro watch` subcommand (or `gastro dev
--no-server` flag) that runs _only_ the source-watch → regenerate →
signal-file loop, leaving the binary lifecycle to the host project. The
`writeReloadSignal` + watcher goroutine already exist in `runDev`; extracting
them into a standalone `runWatch` is mostly refactoring.

**Subsystems touched:** `cmd/gastro/main.go` (new subcommand or flag),
`printUsage`, `docs/dev-mode.md` (already documents the workaround;
update once the native flag exists).  
**Effort:** extract the watcher loop from `runDev` into a shared helper;
add the flag/subcommand; update docs.

---

### A8 · Dev mode coupling · P3 · **Trivial**

`IsDev()` reads `GASTRO_DEV` globally; there is no per-router dev flag. In
practice this is fine for single-process apps but makes parallel test setups
where one router should behave as "dev" and another as "prod" awkward.

**What done looks like:** A `WithDevMode(bool)` router option that overrides
the env-var check for that router instance. The global `IsDev()` remains for
backward compat.

**Subsystems touched:** generated `routes.go` template (`New()` option +
`isDev` field already exists; just expose the override).

---

## Section D — open research task (original deliverable)

The narrower task this audit superseded asked: **skim gastro's open issues /
discussions / DESIGN.md for A1, A2, A4; if silent, file one GitHub issue per
item.**

That step has not been done. Recommended framing when filing:

- **A1 issue title:** "CLI version not tracked by go.mod: tools.go or
  importable codegen.Run()"
- **A2 issue title:** "Embed indirection: point //go:embed at source
  components/ instead of .gastro/templates/ copies"
- **A4 issue title:** "Expose component selector as a codegen constant for
  SSE morph handlers"

Link each filed issue back from this document.

---

## Project-side (git-pm, not gastro)

These require no upstream change. Ordered by effort.

| ID | Task | Effort |
|---|---|---|
| C5 | Pin `gastro install` to tag in `internal/web/README.md` (currently `@latest`) | Trivial |
| C3 | Add one-liner to `internal/web/README.md` + gastro skill: "HTML → `.gastro`; Go packages → content rendering only" | Trivial |
| C2 | Add "Gastro integration shape" subsection to README naming decisions `01KQ45N0`, `01KQH34Q`, `01KQH35F` | Small |
| B5 | Migrate `decisions.go` and `experiences.go` from `gastroweb.Render.X(...)` to `d.deps.Router.Render().X(...)` | Small |
| C4 | Extract `pm-md-editor` markup into a `.gastro` component (accept per-component overhead until B1 lands) | Small |
