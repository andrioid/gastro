# Roadmap

This file lists work that is known, scoped, and deliberately not yet
done. Each item has an explicit **trigger** — the signal that would
move it from "deferred" to "in progress". If you hit one of these in
practice, please open an issue describing your use case; that's the
fastest way to escalate priority.

For items that are out of scope **by design** (Windows for `gastro
watch`, no third-party runner dependency, dot-imported
`gastro.WithRequestFuncs` not detected by LSP, etc.), see the
relevant `DECISIONS.md` entry — those are not on the roadmap.

For shipped work, see [`CHANGELOG.md`](CHANGELOG.md) and the
chronological log in [`DECISIONS.md`](DECISIONS.md).

---

## Waiting on real adopter request

These items are scoped and could be picked up at any time, but the
maintainer is waiting for a concrete user need before investing —
either to keep the surface area small or to make sure the eventual
shape matches a real workflow rather than a guessed one.

### LSP

- **Dict-key completion suggesting `Children`.** Component-body
  completion of `.Children` already works. Suggesting `Children` as a
  dict key inside `{{ wrap Layout (dict ...) }}` would require
  threading the per-component `HasChildren` flag into the LSP's
  schema map. Reference: `DECISIONS.md` 2026-05-02 Wave 3.

- **Neovim auto-detect of `tool github.com/andrioid/gastro/cmd/gastro`
  in `go.mod`.** VSCode walks up from each workspace folder, finds
  the directive, and launches `go tool gastro lsp` automatically.
  Neovim users currently override `setup({ cmd = ... })` by hand.
  Parsing `go.mod` from Lua adds maintenance for unproven value.
  Reference: `DECISIONS.md` 2026-05-03 (go-tool entry).

- **Quick-fix code action: insert `(dict)` skeleton on
  bare-call-propful diagnostic.** When the editor surfaces
  `component X requires props` (added 2026-05-17 alongside the
  propless-bare-call feature), offer a code action that rewrites
  `{{ X }}` to `{{ X (dict "FieldA" "" "FieldB" 0) }}` with every
  Props field pre-filled at its zero value. Mirrors the existing
  unknown-prop code actions. Reference: `internal/codegen/validate.go`
  "requires props" diagnostic; `internal/lsp/server/code_action.go`
  for the existing patterns.

### Dev mode

- **Visible browser banner UI for build errors.** The
  [`event: build-error`](pkg/gastro/devreload.go) SSE transport ships
  and the client logs failures via `console.warn`. A visible banner is
  pure CSS/JS work with no protocol change. Reference:
  `pkg/gastro/devreload.go:166,216`, `DECISIONS.md` 2026-05-03 phase (e).

- **Configurable SIGTERM grace period in `gastro watch`.** Hardcoded
  to 5 seconds in v1. Reference: `cmd/gastro/process_unix.go:45`.

- **Pure-watch mode for `gastro watch` (no `--run`).** Today every
  `gastro watch` invocation requires `--run`. Composition with
  `watchexec` is the recipe for pure-watch use cases. Reference:
  `DECISIONS.md` 2026-05-03 phase (g), `docs/dev-mode.md`
  "Composing with other runners".

### Runtime / error handling

- **`WithRecoverHandler` for frontmatter panics.** Render errors are
  pluggable today via `WithErrorHandler`; panics inside the generated
  `Recover` deferred function still surface only via the default
  recovery log. The workaround is log-scraping or a sidecar panic
  tracker. Reference: `docs/error-handling.md:274`.

### Codegen / validation

- **Optional Props fields (suppress "missing prop" warnings).** The
  LSP currently warns on every Props field a caller doesn't pass.
  Adding `gastro:"optional"` (or treating zero-default-friendly types
  like `string` as implicitly optional) would let component authors
  silence the warning per field without forcing callers to write
  `"Field" ""` placeholders. Bundles with surfacing missing-prop
  warnings in `gastro generate` itself — today `EmitMissingProps` is
  off in the codegen pipeline (only the LSP opts in), so partial
  dicts never warn at build time. Once optional vs. required is
  expressible, flipping the default becomes safe. Reference:
  `internal/codegen/validate.go` `EmitMissingProps`,
  `ValidateDictKeysFromAST` missing-prop branch.

- **Promote dict-key `SeverityError` diagnostics to hard failures in
  non-strict mode.** Today `unknown prop` and `requires props` are
  emitted at `SeverityError` severity but flow through the
  `[]Warning` channel, so `gastro generate` (default) and
  `gastro dev` (`runGenerate(false)`) only print them — they don't
  fail the build. The LSP shows them as red squiggles and
  `gastro build` / `gastro check` (strict) fail correctly, but a
  user without an editor + a dev workflow still hits the runtime
  crash. Either route true-error diagnostics through a separate
  error channel that bypasses the Strict flag, or flip dev-mode
  generation to strict-by-default for `SeverityError` only.
  Reference: `internal/compiler/compiler.go` dictWarnings handling
  (~L500), `cmd/gastro/main.go:600` (`runGenerate(false)`),
  `internal/codegen/validate.go` SeverityError emit sites.

- **Frontmatter `Deps := gastro.Deps[T]()` declaration.** A nicer
  surface than the runtime `gastro.From[T](ctx)` accessor — would let
  pages declare typed dependencies at the top of the frontmatter
  rather than reaching for them inside the handler body. Requires
  cross-package type resolution in the compiler, which is the
  expensive part. Reference: `docs/design.md:1106–1109`.

---

## Waiting on a performance signal

Work where the design is clear but the cost to the user is currently
invisible. Will be picked up the first time a benchmark or profile
points at one of these.

- **Typed `Render` bypassing the `map[string]any` round-trip.**
  `Render.Layout(props)` today copies `props` into a map, calls the
  unexported `componentLayout(map)`, and that function reverses the
  trip via `MapToStruct[T]`. Invisible on a normal page render but
  measurable in SSE hot paths re-rendering single components per
  event. Reference: `docs/design.md:1094–1104`.

---

## Waiting until an adopter complains

Cosmetic / low-impact items documented during prior audits and
explicitly parked under a "ignore unless someone hits it" policy.

- **LSP §6.2** — lowercase frontmatter locals are not auto-suppressed.
  Surfaces only on contrived shapes (a local declared and never
  reused, with a lowercase name). Reference:
  `docs/history/lsp-shadow-audit.md` §6.2.

- **LSP §6.3** — source-map column drift after `rewriteGastroSugar`.
  Squiggles can be off by ≤ a handful of characters horizontally on
  lines that the sugar rewriter touched. The cleanest fix is
  length-preserving aliases, which is invasive enough to warrant
  waiting for a real complaint. Reference:
  `docs/history/lsp-shadow-audit.md` §6.3.

- **`examples/i18n/` SEO-perfect route mirroring.** The example runs
  every page at every locale through one set of files (locale picked
  at request time). Adopters who want per-locale URLs write
  `pages/[lang]/index.gastro` manually; the example doesn't
  auto-generate them. Reference: `docs/i18n.md:265–272`,
  `examples/i18n/main.go:21`.

---

## Larger roadmap items

Open-ended directions that aren't framed as a single deferred unit.

- **Go-native HTML completions in the LSP.** v1 delegates HTML
  intelligence to the editor's built-in / tree-sitter integration.
  Migrating to a gastro-native source of HTML completions is on the
  long-term roadmap but is not currently scoped. Reference:
  `docs/design.md:794,859`.

---

## Out of scope (do not propose)

For the catalogue of items that are deliberately **not** on the
roadmap (no Windows for `gastro watch`, no third-party runner
dependency, no mode-specific runtime API, etc.) see the
"non-decisions" sections inside `DECISIONS.md` — most notably the
2026-05-03 entry for `gastro watch` and the 2026-05-14 entry for
`WithRequestFuncs`. Those are design choices, not deferred work.
