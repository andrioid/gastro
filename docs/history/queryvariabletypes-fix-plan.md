# Plan: emit `_ = X` markers for exported frontmatter vars so the LSP can type them

**Date:** 2026-05-09
**Status:** **Executed 2026-05-09** — commit `253c985` on branch
`feat/frontmatter-package-scope`. All Phase 5 verification steps
green: `go test ./... -race`, `go vet ./...`,
`bash scripts/verify-bootstrap`, `mise run audit` against all four
examples (zero diagnostic regressions), `go tool gastro check` clean,
byte-stable regeneration confirmed. Output growth: ~12 bytes per
exported var.
**Author:** agent + m+git@andri.dk
**Related:** Surfaced as a pre-existing issue during the
`tmp/frontmatter-package-scope-plan.md` execution; see
[§ Pre-existing issues](../tmp/frontmatter-package-scope-plan.md) #2.

---

## Why

`internal/lsp/server/gopls.go:queryVariableTypes` is supposed to hand
the LSP a `map[varName]typeString` for every exported frontmatter
variable. The map drives:

- **Template-body hover** (`hover.go:87,101,115`). Hovering `{{ .Title }}`
  should show `string`; today it shows `frontmatter variable` with no
  type.
- **Template-body completion** (`completion.go:350` →
  `scopedFieldCompletions` → `queryFieldsFromGopls`). Inside
  `{{ range .Posts }}`, `{{ .` should suggest `db.Post`'s fields. Today
  it does nothing — the probe-injection at `gopls.go:248-265` looks for
  a `_ = Posts` anchor line and bails when it's absent.
- **Template-body diagnostics** (`diagnostics.go:46`). Unknown-field
  warnings on chained access (`internal/lsp/template/walk.go:277,398`)
  consult the type map. With no types, no warnings — false negatives,
  not false positives, but still a missed feature.

The implementation strategy is sound: scan the virtual `.go` file for
`_ = VarName` lines, send `textDocument/hover` to gopls at the ident
position, parse the type out of the response.

The bug is in the **input**: codegen doesn't emit `_ = X` lines for
user-named frontmatter variables. The only `_ = …` lines that exist in
production output are gastro-internal suppressions
(`_ = template.Must`, `_ = log.Println`, `_ = __props`,
`_ = __children`). User vars end up either as `:=` locals inside the
handler body or — post the package-scope feature — as hoisted package
variables; neither shape produces a `_ = VarName` line.

Verified empirically with a shadow-generation smoke fixture
(`pages/index.gastro` with `Title := "Hello"` and `Posts := []string{}`):
the resulting `vf.GoSource` contains zero matches for `_ = Title` or
`_ = Posts`. So `queryVariableTypes` returns an empty map for the two
vars the template references. Hover, completion, and chain-validation
all silently fall back to "no information".

The fix is one template change: emit `_ = <EmitName>` per exported var
right after the frontmatter body. Same shape codegen already uses for
`__props` / `__children`. queryVariableTypes finds the lines, gopls
returns the type, the LSP populates the cache.

## Decisions locked

| Question | Decision |
| --- | --- |
| What gets `_ = X`? | **Exported vars only.** Both `:=` (handler-local) and hoisted (package-scope). Private vars (`err`, `slug`) don't reach the template; emitting suppression lines for them is noise. |
| Position in the generated file | **In the handler/component func body, after `{{ .Frontmatter }}`, before `if gastroRuntime.BodyWritten(w) { return }`.** Always reachable; identifiers are in scope (package vars and `:=` locals both work); doesn't shift any other line. |
| Value of EmitName | **Same as Phase 5 of the package-scope plan: local name for `:=` vars, mangled name for hoisted vars under `MangleHoisted=true`, unmangled for `MangleHoisted=false`.** Re-uses `exportedVarEmission.EmitName` already on `generateData`. |
| Children handling | **Skip.** `Children` reaches the template as a synthetic dict key, not a user-named var. The component already declares `_ = __children`; queryVariableTypes picks that up if it ever needs to. Template hover for `{{ .Children }}` is a separate, smaller concern (always `template.HTML`) and hardcoded handling in `hover.go` is sufficient. |
| Component-only `__props` | **Already emitted today** (`generate.go:209`). queryVariableTypes finds it; the cache gains `{"__props": "<MangledPropsName>"}`. Useful for chain completion on `{{ .Children }}` — actually, on internal `__props.Field` references, which the template never sees directly. No change needed. |
| Backwards compat | **Pure addition.** New `_ = X` lines after frontmatter; nothing existing moves or changes meaning. Codegen byte output grows by ~12 bytes per exported var. |
| `gastro check` byte-stability | **Preserved.** ExportedVars iteration order is deterministic (source order of decls). Two regenerations produce byte-identical output. |
| `--strict` interaction | **None.** `_ = X` is a Go-recognised void expression; no warnings, no diagnostics. |

## Decisions rejected (with reasoning)

- **Emit `_ = X` for private vars too.** Private vars never appear in
  templates, so no LSP feature benefits. Adds two-three lines per page
  without payoff.
- **Replace the `_ = …` scan with an AST walk.** Tempting (more
  precise, would also catch `:=` vars without needing emission), but
  it shifts complexity from the trivial codegen template to a
  full AST-walking analyzer in `gopls.go`. The current scan-based
  approach is simple, fast, and works once the input shape is right.
- **Do the type lookup at codegen time and embed it in the metadata
  the LSP consumes directly.** Faster (no gopls round-trip per var)
  but creates a parallel type-derivation path that has to keep up with
  Go's type inference rules. Better to keep gopls as the source of
  truth.
- **Inject `_ = X` only into the LSP shadow, not production codegen.**
  Cleaner production output but introduces a divergence between what
  `gastro generate` writes and what the shadow synthesises. The plan
  prizes "shadow inherits codegen by construction"
  (`internal/lsp/shadow/workspace.go:24-ish` comment block); divergence
  is the wrong direction. Production cost is ~12 bytes per exported
  var, which is invisible.

## Pre-existing issues identified during exploration

1. **Existing `TestLSP_TemplateHover` does not check type resolution.**
   `cmd/gastro/lsp_integration_test.go:154` asserts the hover
   response contains the literal string `"frontmatter variable"`,
   which `templateHover` always emits regardless of whether type info
   was found. So today's tests pass even though hover is incomplete.
   Logged here; the new test in Phase 3 closes the gap by asserting
   the type itself is present.

2. **`queryFieldsFromGopls` probe injection has the same dependency.**
   Looks for `_ = VarName` to anchor the probe insertion. Same fix
   makes it work; covered in Phase 3.

---

## Phase 1 — codegen template change

1. **Modify `internal/codegen/generate.go`** handler/component templates
   to emit one `_ = <EmitName>` line per exported var, right after the
   frontmatter body:

   ```go
   var handlerTmpl = template.Must(template.New("handler").Parse(`
   ...
       {{ .Frontmatter }}

       // Suppress unused-var warnings for exported frontmatter vars
       // and provide hover-type anchors for the LSP. Free at runtime.
       {{- range .ExportedVars }}
       _ = {{ .EmitName }}
       {{- end }}

       if gastroRuntime.BodyWritten(w) {
           return
       }
       ...
   `))
   ```

   Same insertion in `componentTmpl` between the frontmatter and the
   `__data` map literal.

2. **Idempotency check.** `ExportedVars` may legitimately be empty
   (page with no exports). The template's `{{- range .ExportedVars }}`
   produces no output in that case; the surrounding line stays clean.
   Test: `TestGenerate_NoExportedVars` should still produce identical
   output to today.

3. **Compile-time visibility.** The `_ = X` line references X by its
   handler-scope identifier (local for `:=` vars, the mangled name for
   hoisted vars). `EmitName` already encodes this distinction
   (Phase 5 of the package-scope plan). No new fields needed.

## Phase 2 — verify queryVariableTypes consumers

4. **`hover.go:templateHover`** — already handles the cache lookup;
   no change needed. With non-empty `types`, the hover response gains
   a code block `\n```go\nstring\n``` `.

5. **`completion.go:scopedFieldCompletions`** — same; the existing
   path now actually finds the container type and proceeds to the
   probe-injection step.

6. **`gopls.go:queryFieldsFromGopls`** — the probe-injection logic
   that looks for `_ = VarName` lines now finds them and successfully
   probes for fields. No change to the function itself; the test in
   Phase 3 confirms the path is exercised.

7. **`diagnostics.go`** — no change. `runTemplateDiagnostics` already
   passes `types` to the walker; the walker already gates on
   non-empty type strings.

## Phase 3 — tests

8. **`internal/codegen/generate_test.go`** — extend an existing
   `TestGenerate_*` to assert the new `_ = X` lines are present:

   - `TestGenerate_PageHandler` — adds
     `assertContains(t, output, "_ = Title")`.
   - `TestGenerate_MultipleExportedVars` — asserts two exported vars
     both produce `_ = ` lines and they are in source order.
   - `TestGenerate_NoExportedVars` — confirms zero `_ = ` lines
     (other than the existing internal suppressions, which the test
     should not match against).
   - `TestGenerate_HoistedVar` (existing in
     `hoist_integration_test.go`) — extended to assert the body
     contains `_ = __page_index_SlugRE` (mangled form).

9. **`internal/lsp/shadow/shadow_hoist_test.go`** — new test
   `TestShadow_ExportedVarsHaveSuppressionLines` confirming the
   shadow output (MangleHoisted=false) emits `_ = Title`,
   `_ = Posts` for typical frontmatter shapes.

10. **`cmd/gastro/lsp_integration_test.go`** — extend
    `TestLSP_TemplateHover` to assert the hover content contains the
    type string `string`, not just the description text. Closes the
    pre-existing issue logged in §Pre-existing #1.

11. **New `cmd/gastro/lsp_integration_test.go::TestLSP_TemplateRangeFieldCompletion`**
    — covers the probe-injection path. Fixture: a page with
    `Posts := []post{ ... }` where `post` has a `Title` field;
    completion request inside `{{ range .Posts }}{{ .Tit` returns
    `Title` as a candidate. This is the smoke test that the
    end-to-end chain (queryVariableTypes → queryFieldsFromGopls →
    parseFieldCompletions) works.

## Phase 4 — docs

12. **No public-API surface change**, so no doc rewrite needed. The
    `_ = X` lines are an internal codegen artefact; users don't write
    or read them.

13. **CHANGELOG entry** under the LSP section noting that template-body
    hover and completion now resolve frontmatter variable types
    end-to-end. One-line description; no migration steps required.

## Phase 5 — verification

14. `mise run test` (root) green with `-race`.
15. `mise run lint` clean.
16. `mise run audit` (cmd/auditshadow) shows zero diagnostic
    regressions across all four examples.
17. Per-example: `gastro generate && go build ./...` for `blog`,
    `dashboard`, `sse`, `gastro`.
18. `gastro check` clean against all four examples (regenerated
    output matches committed).
19. **Manual LSP smoke check** (one-time post-merge): open
    `examples/blog/pages/blog/index.gastro` in an editor with the
    gastro LSP attached. Hover over `{{ .Posts }}`: confirm the
    popup shows `[]db.Post` (not just "frontmatter variable").
    Trigger completion inside `{{ range .Posts }}{{ . `: confirm
    `Title`, `Slug`, `Author` appear as candidates. Document the
    procedure in `docs/contributing.md` alongside the hoist-aware
    LSP smoke check that already exists there.

## Rollback plan

The change is internal codegen only; a single-commit revert restores
the prior behaviour (queryVariableTypes returns empty cache, LSP
falls back to its current minimal hover/completion).

## Out of scope

- **`_ = X` for private vars.** Decided against; private vars don't
  reach the template.
- **Type lookup at codegen time** (avoiding the gopls round-trip).
  Out of scope; current performance is acceptable, and gopls is the
  authoritative type source.
- **Replacing the `_ = X` text scan with AST traversal.** Out of
  scope; the scan works fine once the input shape is right.
- **Range over arbitrary chain expressions** (`{{ range .Posts.Comments }}`).
  Already partially supported via `walk.go:resolveWithScope` /
  `resolveRangeScope` and `probeFieldsViaChain`. The fix in this plan
  unblocks the foundational `{{ range .Posts }}` case; chained range
  improvements (if any are still needed after this fix) are a
  separate concern.

---

## Open: nothing remaining

All decisions locked. Outstanding items are:

- **Manual LSP smoke check** — one-time editor verification after
  merge. Documented in Phase 5 step 19.
