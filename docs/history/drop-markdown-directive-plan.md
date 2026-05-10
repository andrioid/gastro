# Plan: replace `{{ markdown }}` directive with `//gastro:embed` codegen-time directive

**Date:** 2026-05-08 (revision 3); drift-checked 2026-05-10
**Status:** **Executed 2026-05-10** on branch
`feat/drop-markdown-directive` across five commits:
`7450fc1` feat(codegen)!: replace {{ markdown }} with //gastro:embed,
`572173b` build(deps): drop chroma + goldmark + regexp2,
`7dc0314` feat(examples/gastro): migrate website,
`15b7958` docs: document //gastro:embed,
`74b9a5c` feat(lsp): //gastro:embed diagnostics + hover + completion.
Verification (Phase 7) all green: `mise run test -race`,
`mise run lint`, per-example generate + build + check,
`scripts/verify-bootstrap`, manual smoke (HTTP 200, 27 highlighted
spans on /docs/getting-started). Headline `go.sum` audit: blog,
dashboard, sse have **zero** markdown-stack entries; examples/gastro
carries chroma + goldmark + goldmark-highlighting as **direct**
requires (the user-visible 'bring your own renderer' story).
**Author:** agent + m+git@andri.dk planning rounds
**Prerequisite:** [`frontmatter-package-scope-plan.md`](frontmatter-package-scope-plan.md)
— shipped on `feat/frontmatter-package-scope` (commits `eeaca3c` +
`9719fe8`); the hoister, `RewriteHoistedRefs`, and the hoisted-decl
block in `internal/codegen/generate.go` are all in tree.

> ⚠️ **Read this first.** This plan replaces the `{{ markdown }}` directive
> with a codegen-time `//gastro:embed` directive (Shape A). It assumes
> frontmatter package-scope hoisting is already in place (per the
> prerequisite plan). The 2026-05-04 plan's locked Phases 1–3 (drop
> directive, delete chromalexer, drop deps) mostly still apply, but Phase
> 4 (sub-module helper) is removed and Phase 5 (website migration's 5-way
> fork) collapses to a direct one-liner-per-call-site migration. The
> conversation history that produced this revision is preserved at
> [§ Conversation history](#conversation-history-for-future-you).

---

## Why

Pre–Track B, frontmatter was a thin wrapper around `gastro.Context()` with no
real Go-handler power, so a build-time `{{ markdown "path" }}` directive was
the only ergonomic way for users to render markdown into a page. The
directive bakes four deps into every gastro consumer (`goldmark`,
`goldmark-highlighting/v2`, `chroma/v2`, transitively `dlclark/regexp2`) and
dictates the markdown flavour, extension set, highlighter, and theme for
everyone — symptoms of which surfaced in Wave 1 (2026-05-02 `go.sum` thrash
across all four examples).

Track B's page model v2 (ambient `(w, r)`, arbitrary Go in frontmatter) plus
`WithDeps[T]` / `gastro.From[T]` makes user-side rendering trivial. The
**`//gastro:embed`** directive in this plan is a codegen-time mechanism that
reads `.md` (or any other text/binary file) from disk at generate time and
emits a Go string-literal (or `[]byte`-literal) into the generated code.
The user then renders that raw content with whatever markdown stack they
choose — typically a 30-line `examples/gastro/md/md.go` wrapping
goldmark+chroma, copy-pasteable from the docs.

This shape:

- Keeps goldmark/chroma/regexp2 out of the main module **and** out of any
  sub-module the framework ships (they live in user code if at all).
- Matches `//go:embed`'s ergonomics for users; matches Gastro's "frontmatter
  is real Go" mental model from Track B.
- Sidesteps the previous plan's undecided Phase 5 fork — embed paths are
  resolved relative to the `.gastro` source at codegen time, so `/docs/`
  stays exactly where it is on disk.
- Pays zero runtime cost (string literals in `.rodata`); user renders at
  init via the var-hoister (prerequisite plan).

The directive isn't useful for **dynamic** markdown (a blog post chosen by
URL slug at request time), but the dynamic case is *covered by composition*
— per-request `:=` reads from `embed.FS`/`os.ReadFile`/DB, then the same
`md.Render` function the user wrote for static cases handles the rendering.
No framework feature required.

## Decisions locked

| Question | Decision |
| --- | --- |
| Shape | **Hard drop** of the `{{ markdown }}` directive, its codegen pass, and the four deps from the main module. Replaced by **`//gastro:embed`** in frontmatter. |
| `pkg/chromalexer/gastro` | **Delete** — no in-repo consumer remains after the drop. |
| Dev-watcher `.md` tracking | **Keep** the existing `ExternalDeps` plumbing, repurposed for the new directive. Rename to be name-neutral (`MarkdownDeps` → `EmbedDeps`, etc.). |
| Helper sub-module | **Drop entirely.** No `github.com/andrioid/gastro/md` shipped. Users wire goldmark themselves; docs ship a copy-paste 30-line snippet. Revisit if a real dynamic-markdown user surfaces. |
| Directive name & syntax | **`//gastro:embed PATH`** in frontmatter, on the line **directly above** a `var` declaration of type `string` or `[]byte`. **Immediate adjacency required** — no blank line between directive and decl, matching `//go:embed` convention. |
| Variable types supported | **`string` and `[]byte`** only. `template.HTML` deliberately rejected to avoid the foot-gun of users embedding raw markdown into a `template.HTML` var. |
| Directive grammar (v1, strict) | **Single-spec, uninitialized only.** Accepted: `//gastro:embed PATH\nvar X string` or `var X []byte`. Rejected with clear errors: explicit initializer (`var X string = "..."`), parenthesized groups (`var ( A string; B string )`), multi-name spec (`var A, B string`), stacked directives, types other than `string`/`[]byte`/`interface{}`/`any`. Closest to `//go:embed`'s mental model. |
| Embedded byte handling | **Preserve bytes exactly.** Bake whatever `os.ReadFile` returns; no trailing-newline stripping, no whitespace normalization. Matches `//go:embed`. Downstream renderers handle any artifact. |
| LSP scope in this PR | **Full v1**: path autocompletion, missing-file / module-boundary / var-type diagnostics, hover with resolved absolute path, var-type quick-fix. |
| Init-time render panics | **Document, don't mitigate.** `var Content = md.MustRender(...)` panics at process init if the `.md` is malformed; that's the price of static embedding. Add a paragraph to `docs/markdown.md` recommending CI smoke (`go tool gastro check` + `go build`). No framework code change. |
| Path semantics | **Relative to the `.gastro` source file**, not the generated file's location. Matches `//go:embed` mental model and gives library-mode users intuitive ergonomics. |
| Module-boundary check | **Reject paths escaping the user's Go module.** Codegen walks up from the source for `go.mod`; resolved absolute path must be inside that root. |
| Symlinks | **Followed.** Codegen uses `os.ReadFile` at gen time, so symlinks resolve naturally (unlike `//go:embed`). |
| Glob support | **Out of scope for v1.** Add only if a real iteration use case appears. |
| Var hoisting | **Spun out as a separate plan** (`tmp/frontmatter-package-scope-plan.md`); sequenced before this one. This plan assumes hoisting is in place. |
| Dynamic markdown (blog posts by slug) | **Covered by user composition**, not a separate framework feature. Per-request `:=` reads from `embed.FS`/`os.ReadFile`/DB; same user `md.Render` handles both static and dynamic. Worked example in `docs/markdown.md`. |
| Component conversion (`components/hero.gastro`) | Add a `Hero template.HTML` Props field; caller passes the rendered HTML in. |
| Page conversion (`pages/index.gastro`) | Frontmatter `//gastro:embed pages/intro-hero.md` + `var HeroRaw string` + `var Hero = md.MustRender(HeroRaw)`. Both hoisted; `Hero` available as `{{ .Hero }}`. |
| 8 doc pages migration | Frontmatter `//gastro:embed ../../../docs/X.md` + `var ContentRaw string` + `var Content = md.MustRender(ContentRaw)`. All hoisted to package scope; `{{ .Content }}` in body. |
| Render eagerness | Recommend init-time (`var X = md.Render(...)` — auto-hoisted to package scope by prerequisite plan). Document the per-request foot-gun (`X := md.Render(...)`) explicitly. |
| LSP support for `//gastro:embed` | **Full experience in v1**: parse the directive, autocomplete file paths, missing-file diagnostics, module-boundary diagnostics. |
| `examples/gastro` layout | **Flat** — `examples/gastro/md/md.go` (no `internal/`). Match existing example layout convention. |
| `{{ markdown }}` backwards-compat error | **No special error message** — we're the only consumers. Standard template-parse error is fine. |
| Windows support for the example | **Ignored** — not a supported platform for examples. |
| Final smoke test bar | **Spot-check** that pages render and code blocks have syntax classes. Don't byte-diff against current output. |
| DX: auto-rebuild on `.md` edits | **Keep working** via the existing `ExternalDeps` watcher (renamed). Edit `.md` → re-codegen → rebuild → restart. |
| `__data` map size optimisation | **Not in scope.** Hoisted vars are added to `__data` per request (N pointer copies) for template access. Cheap; optimise (compute once, reuse) only if a profile flags it. |
| `findModuleRoot` helper | **Coordinate with sibling plans.** Both `docs/history/gastro-watch-go-scope-plan.md` (shipped) and the prerequisite hoisting plan reference this helper. The watch-scope plan landed first and owns it; reuse from there. |

## Decisions rejected (with reasoning)

- **Phase 4 sub-module helper (`github.com/andrioid/gastro/md`).** The
  original plan's anchor. Made redundant by `//gastro:embed`: the
  sub-module's only motivation was "goldmark/chroma need a non-main-module
  home"; with `//gastro:embed` the framework no longer needs that home at
  all. Sub-module cost (cross-module release tagging, lint/test/CI plumbing,
  ongoing version coordination) buys nothing once the codegen path exists.
- **Reuse `//go:embed` syntax (with codegen rewriting paths).** Considered.
  Rejected because the same comment would have different semantics in
  `.gastro` frontmatter (gen-time read, baked literal) vs regular Go files
  (compile-time embed via Go toolchain). Confusing for users, fragile for
  Go tooling that doesn't know about `.gastro`. The visual link to
  `//go:embed` is preserved by the `:embed` suffix; the `gastro:` namespace
  prefix removes ambiguity.
- **Shape B: codegen renders to HTML at gen time.** Closest to today's
  directive but pulls goldmark+chroma into the gastro CLI's deps, which
  under Go 1.24+ tool dependencies may leak into a user's `go.sum` via
  `go tool gastro` — exactly the Wave-1 problem we're trying to solve.
  Plus it forces a single hardcoded markdown stack on all users.
- **Symlink at `examples/gastro/docs → ../../docs`.** `//go:embed` does not
  follow symbolic links ([Go issue #59924](https://go.dev/issue/59924)).
  Moot under Shape A (codegen reads at gen time, follows symlinks fine);
  noting for archival purposes.
- **Symlink at `/docs → examples/gastro/docs` (reversed direction).** GitHub
  doesn't reliably render through symlinks; refs in
  ([github/markup #21](https://github.com/github/markup/issues/21),
  [#1158](https://github.com/github/markup/issues/1158),
  [gitea #36847](https://github.com/go-gitea/gitea/issues/36847)). Moot
  under Shape A; archived.
- **`{{ markdown }}` extended for runtime/dynamic loading** (D1/D2/D3 from
  the prior plan's conversation). Either makes the same syntax do two
  completely different things or pulls goldmark/chroma into the main
  module at runtime.
- **Glob support in v1** (`//gastro:embed pages/*.md` with `var x map[string]string`).
  No current iteration use case in tree. Add when needed.
- **`template.HTML` as a supported var type for the directive.**
  `template.HTML` implies the contents are pre-rendered HTML, but
  `//gastro:embed` bakes raw bytes. Allowing `template.HTML` would invite
  users to embed raw markdown into a `template.HTML` var, which would
  bypass html-escaping in templates. Foot-gun.

## Pre-existing issues

None encountered during exploration.

## Drift check (2026-05-10)

Verified against current tree before kicking off Phase 1. All exact
matches except the items listed below; the affected sections of this
plan have been edited in place to reference the current line numbers.

| Plan reference | Current location | Status |
| --- | --- | --- |
| `internal/codegen/markdown.go` (176 lines) | same | ✓ |
| `internal/codegen/markdown_test.go` (246 lines) | same | ✓ |
| `compiler.go` mdCtx block 317–327 | **374–387** | drifted, plan updated |
| `compiler.go` `allMarkdownDeps` line 69 | same | ✓ |
| `compiler.go` `dedupeStrings(allMarkdownDeps)` line 175 | **189** | drifted, plan updated |
| Extra `markdownDeps` rename sites in `compiler.go` (33–36, 134, 312, 478, 501) | n/a | not enumerated in original plan; added |
| `internal/lsp/template/completions.go:94` | same | ✓ |
| `internal/lsp/template/parse.go:60`, `:66` | same | ✓ |
| `internal/lsp/template/parse_test.go:76–94` | same | ✓ |
| `internal/lsp/template/completions_test.go:89/105/121` | same | ✓ |
| `internal/lsp/server/completion.go:122` | same | ✓ |
| `pkg/chromalexer/gastro/gastro.go` (111) + `gastro_test.go` (130) | same | ✓ |
| `cmd/gastro/main.go:569`, `watch.go:377` | same | ✓ |
| `internal/devloop/watcher.go` markdown identifiers | same shape (`markdownCache`, `markdownDepsVersion`, `syncMarkdownCache`, `seedMarkdown`) | ✓ |
| `internal/devloop/devloop.go` `Generate func() (markdownDeps []string, err error)` line 83 | same | ✓ |
| `internal/watcher/watcher.go:62–65` doc-comment with `{{ markdown "..." }}` | same | ✓ (rename in Phase 3 step 10) |
| `internal/lsp/shadow/workspace_test.go:607` | **:683** | drifted, plan updated |
| `docs/architecture.md:167–170` and `:330–331` | **:203–206** and **:366** | drifted, plan updated |
| `docs/dev-mode.md:84` | same | ✓ |
| `docs/getting-started-library.md:264` | same | ✓ |
| `docs/deployment.md:135` | same | ✓ |
| Root `go.mod` carries `chroma/v2`, `goldmark`, `goldmark-highlighting/v2` (direct) and `regexp2` (indirect) | same versions | ✓ |
| All four `examples/*/go.mod` carry chroma/goldmark/regexp2 as `// indirect` | confirmed | ✓ (Phase 4 — audit target) |
| Hoister machinery in tree (`RewriteHoistedRefs`, hoisted-decl block, mangled name table in `internal/codegen/generate.go`) | confirmed | ✓ prerequisite landed |
| `findModuleRoot` helper | only at `internal/lsp/shadow/workspace.go:318–324` (unexported, package-local) | not yet shared — Phase 2 step 7 updated to recommend duplicating |

---

## Phase 1 — remove the old `{{ markdown }}` directive

1. **Delete** `internal/codegen/markdown.go` (176 lines, confirmed
   2026-05-10) and `internal/codegen/markdown_test.go` (246 lines,
   confirmed).

2. **`internal/compiler/compiler.go`** (line refs current as of
   2026-05-10; anchor on identifiers in case of further drift):
   - Remove the `mdCtx` / `ProcessMarkdownDirectives` block (lines
     **374–387**, immediately after the `TemplateRendersChildren` call).
     `TransformTemplate` consumes `file.TemplateBody` directly again.
   - **Keep** `CompileResult.MarkdownDeps` and the `markdownDeps` internal
     field, but rename them to `EmbedDeps` and `embedDeps` respectively
     (Phase 2 will populate them via the new `//gastro:embed` pass).
     Rename touchpoints (all in `compiler.go`):
     - Lines **33–36**: `CompileResult.MarkdownDeps` field and its doc
       comment (rewrite the `{{ markdown "..." }}` reference to mention
       `//gastro:embed`).
     - Line **69**: `var allMarkdownDeps []string` accumulator.
     - Line **134**: `allMarkdownDeps = append(..., result.markdownDeps...)`.
     - Line **189**: `dedupeStrings(allMarkdownDeps)` in the
       `CompileResult` literal.
     - Line **312**: `markdownDeps []string` field on the internal
       `compileResult` struct.
     - Lines **478** and **501**: `markdownDeps: markdownDeps` in the two
       `compileResult` returns at the bottom of `compileFile`.

3. **LSP — drop the old directive**:
   - `internal/lsp/template/completions.go:94`: drop the `markdown` directive
     entry (4-line block).
   - `internal/lsp/template/parse.go`: drop `stubFuncs["markdown"] = ""`
     (line 66) and update the comment at line 60 that lists compile-time
     keywords.
   - `internal/lsp/template/parse_test.go`: delete
     `TestParseTemplateBody_MarkdownDirective` (lines 76–94).
   - `internal/lsp/template/completions_test.go`: drop `markdown` from the
     assertion lists in three spots (~lines 89, 105, 121).
   - `internal/lsp/server/completion.go:122`: update the comment listing
     compile-time directives (drop `markdown`).

4. **No special backwards-compat error.** We're the only consumers;
   standard template-parse error ("function 'markdown' not defined") is
   acceptable when somebody hits a stale `{{ markdown }}`. The CHANGELOG
   entry covers the migration.

## Phase 2 — implement `//gastro:embed` in codegen

*(Note: the prerequisite hoisting plan provides the var-hoisting pass.
Once it lands, the `var X = "literal"` declarations emitted by the embed
directive are automatically lifted to package scope by the hoister, with
mangled names. This plan only adds the directive itself.)*

5. **New file** `internal/codegen/embed.go`:

   ```go
   // Package-level docstring explaining the directive's contract:
   //
   //   //gastro:embed PATH
   //   var <ident> string   // or []byte
   //
   // PATH is resolved relative to the .gastro source file. Path must
   // resolve inside the user's Go module. Symlinks are followed.
   ```

   Public surface:

   ```go
   type EmbedContext struct {
       SourceFile string  // absolute path to the .gastro file
       ModuleRoot string  // absolute path to the user's module root
   }

   type EmbedDirective struct {
       VarName  string
       VarType  string  // "string" or "[]byte"
       Path     string  // user-supplied, relative
       Resolved string  // absolute, after path resolution
   }

   // ProcessEmbedDirectives parses //gastro:embed comments out of the
   // frontmatter source, validates each, reads the referenced files,
   // and returns rewritten frontmatter source + deps.
   func ProcessEmbedDirectives(frontmatter string, ctx EmbedContext) (
       rewritten string,
       deps []string,
       err error,
   )
   ```

   Implementation outline:

   1. Use `go/parser.ParseFile` with `parser.ParseComments` on the
      frontmatter source.
   2. Walk top-level `*ast.GenDecl` nodes with `tok == token.VAR`.
   3. For each `var X T` declaration, look at the comment group
      immediately preceding it. If it contains a `//gastro:embed PATH`
      line, treat that decl as a directive target.
   4. **Grammar checks (strict, v1):** the target `*ast.GenDecl` must
      satisfy all of:
      - `tok == token.VAR` and `len(Specs) == 1` (reject parenthesized
        groups).
      - The single `*ast.ValueSpec` has `len(Names) == 1` (reject
        `var A, B string`).
      - `Values == nil` (reject explicit initializers like
        `var X string = "fallback"`).
      - `Type` is `*ast.Ident` with `Name` in `{"string"}` or
        `*ast.ArrayType` with no length and `Elt` of name `"byte"`.
        Reject `interface{}`, `any`, `template.HTML`, anything else.
      - The directive comment line is the **last** line of the doc
        comment group attached to the decl, and **only one**
        `//gastro:embed` line appears in that group (reject stacked).
      Each rejection produces an error pointing at the source line and
      naming the offending form (e.g. `"//gastro:embed: explicit
      initializer not allowed on line N; remove the = expression"`).
   5. Resolve `PATH` relative to `ctx.SourceFile`'s directory using
      `filepath` (OS separators). Reject absolute paths and paths
      containing `..` segments that escape `ctx.SourceFile`'s dir before
      symlink resolution (cheap pre-check; the post-resolution check
      below is the security-critical one).
   6. **Symlink + boundary check (post-resolution).** Run
      `filepath.EvalSymlinks(Resolved)` to get the real path. Verify
      that real path is inside `ctx.ModuleRoot` via `filepath.Rel` with
      no `..` prefix. Reject otherwise. This is the security-critical
      check — the pre-resolution check in step 5 is just an early-exit
      nicety.
   7. `os.ReadFile(Resolved)`. Wrap errors with line context.
   8. **Byte preservation: bake exactly what was read** — no
      trailing-newline stripping, no normalization. For `string` vars:
      validate UTF-8 (markdown promise), emit
      `var X = "<strconv.Quote-escaped contents>"`. UTF-8 round-trips
      losslessly through `strconv.Quote`.
   9. For `[]byte` vars: emit `var X = []byte("<strconv.Quote…>")`
      when bytes are valid UTF-8 (more compact source); else emit a
      `[]byte{0x..., ...}` literal. Either form preserves bytes
      exactly.
   10. Add `Resolved` (post-symlink) to the returned `deps` slice so
       the watcher tracks the real file, not the symlink.
   11. Replace the `var X T` declaration text with the rewritten
       version in `frontmatter`.

   The resulting `var X = "..."` declaration is then picked up by the
   var-hoister (prerequisite plan) and lifted to package scope with
   mangling. The two passes compose cleanly — embed bakes content; the
   hoister places the declaration where it runs once.

6. **`internal/compiler/compiler.go`** — wire the new pass after
   frontmatter parsing but before var-hoisting:

   ```go
   embedCtx := codegen.EmbedContext{
       SourceFile: file.AbsolutePath,
       ModuleRoot: moduleRoot,  // resolved once at compile-result level
   }
   rewritten, embedDeps, err := codegen.ProcessEmbedDirectives(
       file.Frontmatter, embedCtx,
   )
   if err != nil {
       return compileResult{}, fmt.Errorf("embed: %w", err)
   }
   file.Frontmatter = rewritten
   ```

   Wire `embedDeps` into the existing `compileResult.embedDeps` /
   `CompileResult.EmbedDeps` field (renamed in Phase 1).

7. **`findModuleRoot` helper.** Walks up from a path looking for the
   nearest `go.mod`. Today the only implementation lives at
   `internal/lsp/shadow/workspace.go:318–324` as an unexported
   package-local helper. Two options when wiring this phase:
   (a) duplicate ~7 lines into `internal/codegen/embed.go` (cheap, no
   cross-package coupling); or
   (b) extract to `internal/modroot` (or similar) and have both shadow
   and codegen import it.
   Recommend (a) for v1 — the helper is tiny and codegen importing from
   `internal/lsp/...` would be the wrong dependency direction. Revisit
   if a third caller appears.

8. **LSP — full experience in v1** (`internal/lsp/...`):
   - Frontmatter parser recognises `//gastro:embed PATH` and surfaces
     it as a structured directive (not just a comment).
   - **Path autocompletion**: when typing inside a `//gastro:embed `
     argument, suggest files relative to the source file's directory
     (filtered to text-ish extensions; `.md` first).
   - **Missing-file diagnostic**: red-squiggle the path if
     `os.Stat(resolved)` fails. Hover shows the resolved absolute path.
   - **Module-boundary diagnostic**: red-squiggle if the resolved path
     escapes the module root.
   - **Var-type diagnostic**: red-squiggle the var type if it's neither
     `string` nor `[]byte`. Quick-fix offers to rewrite the type.
   - LSP tests: extend the existing `internal/lsp/...` test fixtures
     with `//gastro:embed` cases covering each diagnostic.

9. **Tests** (`internal/codegen/embed_test.go`):
   - `TestEmbed_StringVar` — `var X string` with embedded contents.
   - `TestEmbed_BytesVar` — `var X []byte` round-trip.
   - `TestEmbed_TrailingNewlinePreserved` — `.md` with a trailing
     `\n` round-trips byte-exact (locks the byte-handling decision).
   - `TestEmbed_TemplateHTMLRejected` — error mentions `template.HTML`
     and points at supported types.
   - `TestEmbed_OtherTypeRejected` — `var X int`, `var X any`,
     `var X interface{}` each error with a clear message.
   - `TestEmbed_InitializerRejected` — `var X string = "fallback"`
     with directive errors with line number.
   - `TestEmbed_ParenthesizedGroupRejected` — `var ( A string; B string )`
     with directive errors.
   - `TestEmbed_MultiNameSpecRejected` — `var A, B string` with
     directive errors.
   - `TestEmbed_StackedDirectivesRejected` — two `//gastro:embed`
     lines above one decl errors.
   - `TestEmbed_PathRelativeToSource` — directive in a deep `.gastro`
     resolves relative to that source's directory.
   - `TestEmbed_AbsolutePathRejected` — absolute path in directive
     errors before opening the file.
   - `TestEmbed_PathOutsideModule_Rejected` — `../../../../etc/passwd`
     errors before opening the file.
   - `TestEmbed_MissingFile` — error wraps the resolved path.
   - `TestEmbed_SymlinkInsideModule_Followed` — symlink whose target
     is inside the module resolves; `deps` slice contains the
     post-`EvalSymlinks` path.
   - `TestEmbed_SymlinkEscapingModule_Rejected` — symlink whose
     target sits outside the module is rejected by the
     post-resolution boundary check.
   - `TestEmbed_InvalidUTF8_StringVar_Rejected` — `string` var with
     non-UTF-8 file errors; `[]byte` var with same file succeeds and
     emits a `[]byte{...}` literal.
   - `TestEmbed_MultipleInOneFile` — two directives in one frontmatter
     both work and both end up in `deps`.
   - `TestEmbed_Integration` — full end-to-end through compiler with
     a fixture in `internal/compiler/testdata/`.

## Phase 3 — keep the watcher dep tracking (renamed)

10. **`internal/watcher/watcher.go`**:
    - `ExternalDeps` keeps its name (still describes the concept of
      external file deps).
    - Update doc comments to reference `CompileResult.EmbedDeps`
      instead of `MarkdownDeps`. The mechanism is name-neutral; only
      the comment + log strings change.
    - **Keep** all five `TestExternalDeps_*` tests in `watcher_test.go`
      (`SetSnapshot`, `Dedupe`, `VersionUnchangedOnEqualSet`, `Symlink`,
      `ConcurrentAccess`). Update test fixtures to use `.md` files
      embedded via `//gastro:embed` instead of `{{ markdown }}` syntax.

11. **`internal/devloop/`**:
    - `watcher.go`: rename `markdownCache` → `embedCache`,
      `markdownDepsVersion` → `embedDepsVersion`,
      `syncMarkdownCache()` → `syncEmbedCache()`. Logic unchanged.
    - `devloop.go`: rename `markdownDeps` → `embedDeps` in
      `Generate func() (markdownDeps []string, err error)` →
      `Generate func() (embedDeps []string, err error)`. Logic
      unchanged.
    - `devloop_test.go`: rename `TestRun_MarkdownDepsTracked` →
      `TestRun_EmbedDepsTracked`. Update fixture to use
      `//gastro:embed` directive in a `.gastro` frontmatter.

12. **`cmd/gastro/`**:
    - `main.go:569` and `watch.go:377`: rename the `markdownDeps`
      return variable to `embedDeps`. Logic unchanged; the `Generate`
      signature stays `(deps []string, err error)`.

## Phase 4 — delete the chroma lexer and main-module deps

13. Remove `pkg/chromalexer/gastro/` entirely (`gastro.go` 111 lines,
    `gastro_test.go` 130 lines, the empty package dir). The only consumer
    today is the side-effect import in `internal/codegen/markdown.go`,
    which is being deleted in Phase 1.

14. Remove from root `go.mod`:
    - `github.com/alecthomas/chroma/v2`
    - `github.com/yuin/goldmark`
    - `github.com/yuin/goldmark-highlighting/v2`
    - `github.com/dlclark/regexp2` (transitive of chroma; falls out
      automatically).

15. `go mod tidy` at root.

16. **Per-example `go.mod` cleanup.** `examples/blog`, `examples/dashboard`,
    `examples/sse` carry chroma/goldmark only as `// indirect` because
    gastro's build pipeline referenced them. After this phase they drop
    entirely. Run `go mod tidy` in each. **`examples/gastro` re-acquires
    goldmark/chroma as direct deps** because it ships a local
    `examples/gastro/md/md.go` (Phase 5) — that's the user-visible "you
    ship your own renderer" part of the design, and confirmed acceptable.

## Phase 5 — migrate the website (`examples/gastro`)

17. **Add `examples/gastro/md/md.go`** (~30 lines, copy-pasteable
    from the new `docs/markdown.md`; flat layout, no `internal/`):

    ```go
    // Package md renders markdown to HTML using goldmark + chroma syntax
    // highlighting. Copy and modify for your own gastro project; see
    // docs/markdown.md for the rationale.
    package md

    import (
        "bytes"
        "fmt"
        "html/template"

        chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
        "github.com/yuin/goldmark"
        "github.com/yuin/goldmark/extension"
        highlighting "github.com/yuin/goldmark-highlighting/v2"
    )

    var renderer = goldmark.New(
        goldmark.WithExtensions(
            extension.GFM,
            extension.Footnote,
            highlighting.NewHighlighting(
                highlighting.WithStyle("github"),
                highlighting.WithFormatOptions(chromahtml.WithClasses(true)),
            ),
        ),
    )

    func MustRender(src string) template.HTML {
        h, err := Render(src)
        if err != nil {
            panic(fmt.Errorf("md: %w", err))
        }
        return h
    }

    func Render(src string) (template.HTML, error) {
        var buf bytes.Buffer
        if err := renderer.Convert([]byte(src), &buf); err != nil {
            return "", err
        }
        return template.HTML(buf.String()), nil
    }
    ```

18. **Migrate the 10 directive call sites:**

    - **8 doc pages** (`getting-started`, `getting-started-library`,
      `pages`, `components`, `sse`, `helpers`, `deployment`,
      `comparison`):

      ```gastro
      ---
      import "gastro-website/md"

      //gastro:embed ../../../docs/getting-started.md
      var ContentRaw string
      var Content = md.MustRender(ContentRaw)
      ---
      <article class="prose">
        {{ .Content }}
      </article>
      ```

      Both `ContentRaw` and `Content` are hoisted to package scope by
      the prerequisite plan's hoister. `md.MustRender` runs once at
      process init. Per-request frontmatter is empty. Template
      references `{{ .Content }}` via the auto-populated `__data` map.

    - **`components/hero.gastro`** — add a `Hero template.HTML` field
      to its Props, render `{{ .Hero }}`, drop the
      `{{ markdown "pages/intro-hero.md" }}` line.

    - **`pages/index.gastro`** — needs **two** embed pairs (verified
      against current tree: `pages/index.gastro:62` references
      `pages/intro-component.md`, and `components/hero.gastro:20`
      references `pages/intro-hero.md`; the latter migration moves to
      this page since hero now takes a prop):

      ```gastro
      ---
      import "gastro-website/md"

      //gastro:embed pages/intro-hero.md
      var IntroHeroRaw string
      var IntroHero = md.MustRender(IntroHeroRaw)

      //gastro:embed pages/intro-component.md
      var IntroComponentRaw string
      var IntroComponent = md.MustRender(IntroComponentRaw)
      ---
      ```

      Pass `IntroHero` as the `Hero` prop into the Hero component
      invocation; reference `{{ .IntroComponent }}` directly in the
      page body where the old `{{ markdown "pages/intro-component.md" }}`
      line lived.

19. **Smoke test** (`examples/gastro/md/md_test.go`):
    - `TestMustRender_GFM` — table renders, footnotes, strikethrough.
    - `TestMustRender_CodeFenceHighlight` — chroma classes on Go fenced
      block; `<pre>` tag has `chroma` class.
    - One end-to-end test that imports a real generated page and
      asserts `<h1>` + chroma classes appear in the rendered HTML.

20. **Build verification.** `go tool gastro generate && go build ./...`
    in `examples/gastro/` must succeed without any sync/move/symlink/
    `go generate` step. Confirms the main pitch — the original plan's
    5-way fork has dissolved.

## Phase 6 — docs

21. **New file `docs/markdown.md`** (~150 lines):
    - **The user's renderer**: ship the 30-line `md/md.go` snippet
      front-and-centre; "copy this into your project; modify the
      goldmark options for a different stack."
    - **Static markdown** (the canonical use case):
      - `//gastro:embed PATH` directive contract (path relative to
        `.gastro` source, `string` or `[]byte`, must stay inside the
        Go module, single-spec uninitialized var only, bytes preserved
        exactly).
      - Render at init via package-scope `var X = md.Render(rawX)`;
        the codegen hoister lifts these to package scope automatically.
      - **Two foot-guns to call out side-by-side:**
        - *Per-request waste:* `X := md.Render(rawX)` in per-request
          frontmatter renders on every request, wasting CPU. Use
          `var =` for static content.
        - *Init-time panic:* `var X = md.MustRender(rawX)` runs at
          process startup; a malformed `.md` will panic and block
          deploy. Recommend running `go tool gastro check` plus a
          `go build ./...` smoke step in CI to catch regressions
          before they reach prod.
      - Worked example: a `pages/about.gastro` with embed + render +
        body access.
    - **Dynamic markdown** (slug-based blog posts, etc.):
      - Section showing the canonical pattern — `pages/blog/[slug].gastro`
        with `embed.FS`, per-request `:=` to load the right post, and
        the *same* `md.Render` for rendering.
      - Worked example pasted in full so a user can copy + modify.
      - Note: the framework provides routing + ambient `(w, r)`; the
        user provides storage + renderer. No framework helpers needed.
    - **The mental model**: a short table of the three cases
      (static / dynamic-from-FS / dynamic-from-DB), each showing
      where `md.Render` is called and what the data source is.
    - **When to reach for a runtime helper sub-module**: "there isn't
      one yet. If you have a use case that doesn't fit this page, file
      an issue — a sub-module is on the table when a real need
      surfaces."

22. **Update existing docs:**
    - `docs/dev-mode.md:84` — drop `{{ markdown ... }}` reference;
      replace with one-line note: "`.md` files referenced via
      `//gastro:embed` are tracked the same way `.gastro` files are
      — edits trigger re-codegen + rebuild."
    - `docs/getting-started-library.md:264` and the "what gastro watch
      is and isn't" section — same one-word swap (`{{ markdown }}` →
      `//gastro:embed`).
    - `docs/architecture.md:203–206` and `:366` (line numbers refreshed
      2026-05-10) — drop the `ExternalDeps`-as-markdown narrative;
      document the new "users
      render markdown via their own helper; framework provides
      codegen-time `//gastro:embed`" position. Keep the `ExternalDeps`
      mechanism description (still accurate, just renamed).
    - `docs/deployment.md:135` — drop `{{ markdown }}` from the build-
      artefacts description; replace with a one-liner about
      `//gastro:embed` baking content at codegen time.
    - `README.md` — add a one-paragraph "rendering markdown" entry
      pointing at `docs/markdown.md`.
    - `docs/pages.md` and `docs/components.md` — cross-link
      `docs/markdown.md` from the relevant frontmatter examples.

23. **Update `internal/lsp/shadow/workspace_test.go:683`** (line
    refreshed 2026-05-10) — the comment
    "including all transitive runtime dependencies (chroma, goldmark, etc.)"
    becomes "no markdown deps — gastro framework leaves rendering to user
    code".

24. **CHANGELOG entry** showing one before/after migration (e.g.
    `pages/docs/getting-started.gastro`) so downstream readers see the
    shape at a glance. Mark as breaking under the pre-1.0 BC posture.
    Note that the markdown dep stack has dropped from the framework
    entirely.

## Phase 7 — verification

25. `mise run test` (root) green with `-race`. **No sub-module test
    recursion needed** — there's no sub-module in this plan.
26. `mise run lint` clean.
27. Per-example: `go tool gastro generate && go build ./...` for
    `blog`, `dashboard`, `sse`, **and `gastro`**. `gastro` is the new
    proof-point that the example needs nothing beyond plain `go build`.
28. `go tool gastro check` clean against all four examples.
29. `bash scripts/verify-bootstrap` passes.
30. **Manual smoke**: build `examples/gastro`, hit `/docs/getting-started`,
    confirm the rendered docs page looks right (spot-check that `<h1>`
    exists and code blocks have chroma syntax classes — don't byte-diff
    against current output).
31. **`go.sum` audit:** two assertions, both must pass:
    - `examples/blog`, `examples/dashboard`, `examples/sse`: **zero**
      chroma/goldmark/regexp2 entries (direct or indirect). This is
      the Wave-1 outcome we're chasing.
    - `examples/gastro`: chroma + goldmark + goldmark-highlighting
      appear as **direct** requires (`require (...)` block, no
      `// indirect` comment); regexp2 may appear as indirect.
      Confirms the user-visible "bring your own renderer" story —
      these deps live in user code now, not the framework.

## Rollback plan

- Phases 1, 4 (deletions): `git revert`.
- Phase 2 (new directive): remove `internal/codegen/embed.go` + its
  caller in `compiler.go`; revert the `MarkdownDeps`→`EmbedDeps` rename.
- Phase 3 (renames): pure refactor; `git revert` is safe.
- Phase 5 (website migration): independently revertable; if blocked, a
  frozen-HTML-snapshot fallback for the 10 call sites is a one-off
  mechanical edit.

If Phase 5 needs to roll back alone, Phases 1–4 can stay in place — the
example would temporarily ship without the doc pages, but the framework
change is sound on its own.

## Out of scope

- **A framework-shipped runtime markdown helper / sub-module**
  (`github.com/andrioid/gastro/md`). Not shipped in this round. Dynamic
  markdown is *covered by user composition* (per-request `:=` data
  load + same `md.Render`); the question is whether to ship a
  pre-canned helper to save users the 30-line copy-paste. Defer until
  somebody actually asks.
- **Caching for dynamic rendering.** A blog with hundreds of posts may
  want an LRU cache over `md.Render` calls. User wires their own; not
  a framework concern.
- **Auto-rebuild on `.md` edits via a user-defined hook.** Existing
  `ExternalDeps` watcher tracks `//gastro:embed` deps automatically; no
  user-defined hook needed. (The original plan's "user-defined
  file→command watch hooks" follow-up is no longer motivated by markdown
  but may still be useful for other extension cases.)
- **Multiple markdown styles or themes shipped by the framework.** Users
  pick their own renderer entirely.
- **Glob support for `//gastro:embed`.** Add when an iteration use case
  appears.
- **Cross-module release tagging tooling.** Moot — no sub-module to tag.
- **Relocating `examples/gastro` out of `examples/` (to top-level
  `website/`).** Tempting but scope creep — out of this plan.

---

## Open: nothing remaining

All decisions locked. Outstanding items are coordination, not
plan-shape — captured in the decisions table above (`__data` map
optimisation deferred; `findModuleRoot` ownership coordinated with
sibling plans).

---

## Conversation history (for future-you)

This plan went through several major iterations. The trail, briefly:

1. **Original plan (2026-05-04 first pass):** sync + embed (Option A).
   Authoring agent picked it as the "obvious" answer.
2. **First review:** flagged ambiguities (test count typo, sync wiring,
   intro fragments, Props plumbing). Resolved most; surfaced that
   user-side intro fragments couldn't be embedded without a move.
3. **"Don't we have docs in one place?"** — user's instinct against the
   sync's duplication. Explored move/symlink alternatives.
4. **Symlink attempt #1** (`examples/gastro/docs → ../../docs`):
   eliminated — Go's `//go:embed` doesn't follow symlinks.
5. **Codegen exploration (G1):** standard Go convention, reads any path,
   self-contained binary. Real option.
6. **DX layer discussion:** Layer 2 (mise wrapper) feasible; Layer 3
   (framework hook) right answer eventually but out of scope.
7. **"Maybe directives aren't bad" + blog use case:** revealed that any
   blog needs runtime markdown anyway, making the directive redundant for
   the static case once a runtime helper exists. Led to D4 sub-module
   helper.
8. **Symlink attempt #2** (`/docs → examples/gastro/docs`, reversed
   direction): GitHub doesn't reliably render through symlinks. Killed.
9. **2026-05-04 plan locked Phases 1–4 (sub-module helper); Phase 5
   undecided** between A / B-explicit / F' / G1 / D.
10. **2026-05-08 reframe:** user proposed "import (raw)" as a codegen-time
    alternative. Worked through the design as Shape A
    (`//gastro:embed PATH` baking raw string), realised it dissolves
    Phase 5's 5-way fork because embed paths are resolved relative to
    the `.gastro` source at gen time. No sub-module needed.
11. **Performance + library-mode analysis** (HTML doc at
    `tmp/markdown-import-raw-tradeoffs.html`): confirmed Shape A is
    cheapest on every dimension that matters at typical site sizes,
    and is strictly better in library mode (codegen-time path resolution
    sidesteps the runtime CWD / `//go:embed`-from-generated-file
    ergonomic problems).
12. **2026-05-08 mid-pass:** user verified package-scope frontmatter
    behaviour today (it's all per-request); confirmed Shape A's perf
    needs hoisting; spun var-hoisting out as
    `tmp/frontmatter-package-scope-plan.md` with prefix-mangling to
    structurally prevent cross-file collisions. This plan now has that
    plan as a prerequisite.
13. **Decisions locked (this revision):** drop sub-module entirely; ship
    copy-paste snippet in docs; `//gastro:embed PATH`; `string` and
    `[]byte` only; full LSP experience in v1; `examples/gastro` flat
    layout; goldmark/chroma direct deps in the example are fine.
