# Plan: frontmatter package-level scope via auto-hoisting

**Date:** 2026-05-08
**Status:** **Executed 2026-05-09** — commits `eeaca3c`
(feature) and `9719fe8` (sibling go.sum tidy fix) on branch
`feat/frontmatter-package-scope`. All Phase 9 verification steps green:
`go test ./... -race`, `go vet ./...`, `bash scripts/verify-bootstrap`,
`go run ./cmd/auditshadow examples/gastro` (zero diagnostic
regressions across all four examples).
**Author:** agent + m+git@andri.dk planning rounds
**Prerequisite for:** `tmp/drop-markdown-directive-plan.md` (sequenced
after this one).

---

## Why

Today the entire frontmatter of a `.gastro` file is dropped inside the
per-request handler function (`internal/codegen/generate.go:117-148`).
Every declaration — `var`, `const`, `func` — is a function-local
declaration that runs on **every request**. Even something that looks
like Go's universal "init once at package level" idiom:

```go
---
import "regexp"

var slugRE = regexp.MustCompile(`^[a-z0-9-]+$`)
---
```

…runs `regexp.MustCompile` on every page load. Today's workaround is to
move the declaration into a sibling `.go` file in the same package, which
works but feels off when the declaration logically belongs *with* the
page.

The same gap blocks `tmp/drop-markdown-directive-plan.md` from delivering
its zero-startup-cost claim: `var Content = md.Render(rawContent)` at
the top of frontmatter would otherwise re-render markdown on every
request even after the directive bakes the raw bytes in.

This plan adds a single missing piece: codegen detects which top-level
declarations in frontmatter can run at package scope and emits them
there, with a per-page name prefix that makes cross-file collisions
structurally impossible. The user's mental model becomes:

- `var X = expr` / `const X = ...` / `func F()` / `type T` at frontmatter
  top level → **package-level, init-once**.
- `X := ...` and statements → **per-request**.

Which is also Go's default mental model for any file. Frontmatter starts
behaving like the Go file it already syntactically is.

## Decisions locked

| Question | Decision |
| --- | --- |
| Mechanism | **Auto-hoist by declaration form.** No new syntax. `var`/`const`/`func`/`type` at frontmatter top level hoist; `:=` and statements stay in the handler. |
| Per-request reference behaviour | **Strict error.** If a hoisted decl's RHS (or a func body) references `r`, `w`, `gastro.Props()`, `gastro.Context()`, `gastro.From[T](...)`, or `gastro.Children()`, codegen errors with a clear migration hint pointing at `:=`. |
| Cross-file name collision | **Structurally prevented** via per-page prefix mangling. Two pages with `var Title = ...` emit `__page_<idA>_Title` and `__page_<idB>_Title` — different package-level names. |
| Prefix format | **`__page_<sanitized-path>_<Name>`** for pages, **`__component_<sanitized-path>_<Name>`** for components. Path is the file path relative to its top-level dir (`pages/`, `components/`), with `/` and non-ident chars replaced by `_`. |
| User-facing rename | **None.** Mangling is invisible: user writes `Title`, codegen rewrites all in-frontmatter refs to the mangled form, template still references `{{ .Title }}` via the unmangled key in `__data`. |
| `func` hoisting | **In v1.** Same free-variable analysis as `var`/`const`. Closures capturing per-request scope error. |
| `func init()` handling | **Special-cased.** Hoist to package scope but **do not mangle** the `init` name — Go gives `init` special meaning (multiple `init` functions per package allowed; runs in source-file order at package init). Mangling would lose that semantic. |
| Type aliases (`type T = X`) | **Hoist.** Treated identically to `type T struct{...}`; mangle to `__page_<id>_T`. Pure init-time, equally collision-prone across pages. |
| `var (X = 1; Y = 2)` block decls | **Split into individual hoisted decls.** Each ident becomes its own `var __page_<id>_X = 1` / `var __page_<id>_Y = 2`. Simplest mangling and rewriting story; no semantic difference. |
| Existing `HoistTypeDeclarations` | **Replaced** by the new mangling-aware hoister. Today's `funcName + Title-case` rename for component `Props` (e.g. `componentCardProps`) is subsumed by the uniform `__component_<id>_Props` scheme, and arbitrary user types (e.g. `type Comment struct{}` declared on either a page or a component) are now collision-proof too. |
| Backwards compat | **Acceptable break.** Per the markdown plan's "we're the only consumers" stance, any in-tree pattern silently relying on per-request `var X = sideEffect()` semantics is not real. CHANGELOG notes the change. |
| `--no-hoist` build flag | **Not shipped.** No rollback escape hatch. If a regression lands post-merge, surgical revert is cleaner than a permanent toggle. |
| Generic hoisted funcs (`func F[T any]`) | **Treated like any other func.** Free-variable analysis on body catches per-request captures. Verified by `TestHoist_GenericFunc` in Phase 5's test sweep, not assumed-to-work. |
| Page-id collision detection | **Early error in `DerivePageID`** (Phase 2) when two paths sanitise to the same ID. Late mangle-collision check (Phase 5) is a backstop. |
| LSP shadow mangling | **Opt-out via `GenerateOptions.MangleHoisted`.** Production codegen sets `true`; the LSP shadow (`internal/lsp/shadow/workspace.go`) sets `false`. Each shadow file already lives in its own Go subpackage (`workspace.go:31` invariant), so the cross-page collisions that motivate mangling structurally cannot occur in the shadow. Skipping mangling there keeps hover popups, completion labels, diagnostic text, and go-to-def column-accuracy as good as today. |

## LSP shadow workspace impact

The shadow workspace (`internal/lsp/shadow`) calls
`codegen.GenerateHandler` directly and feeds the result to gopls
verbatim — the audit harness contract is "the shadow inherits codegen's
rule set by construction" (`workspace.go:24`-ish comment block). That
invariant means any name mangling in codegen flows straight into the
virtual `.go` files gopls analyses, and from there into hover popups,
completion menus, and diagnostic messages the user sees over their
`.gastro` source.

The asymmetry that makes mangling cheap to opt-out: each shadow file
lives in **its own Go subpackage** today
(`workspace.go:VirtualFilePath`), one `.gastro` per shadow package. The
cross-page collision risk that motivates mangling in production
(`package gastro` shared across many pages) does not exist there. So
the shadow can take the unmangled path with zero collision risk and
zero divergence from codegen's rules — same code paths, just one
argument different.

### Shape of the opt-out

```go
// internal/codegen/generate.go
type GenerateOptions struct {
    // MangleHoisted controls whether hoisted top-level decls
    // (var/const/func/type) are renamed with a per-page prefix
    // (__page_<id>_Name / __component_<id>_Name).
    //
    // Set to true for production codegen where many pages share one
    // Go package and need collision-proof names. Set to false for
    // the LSP shadow workspace, where each .gastro file is already
    // in its own subpackage and mangling would only degrade
    // hover/completion/diagnostic UX without buying any safety.
    MangleHoisted bool
}

func GenerateHandler(
    file *parser.File,
    info *FrontmatterInfo,
    isComponent bool,
    opts GenerateOptions,
) (string, error)
```

`internal/compiler/compiler.go` (production path) passes
`{MangleHoisted: true}`. `internal/lsp/shadow/workspace.go:UpdateFile`
and `internal/lsp/shadow/shadow.go:GenerateVirtualFile` both pass
`{MangleHoisted: false}`.

### Affected codepaths inside codegen

When `MangleHoisted == false`:
- `HoistedDecl.MangledName` equals `HoistedDecl.Name` (no prefix).
- `RewriteHoistedRefs` becomes a no-op (refs already match the
  emitted decl name).
- `templateData.PropsTypeName` stays `"Props"` (no
  `__component_<id>_Props` rename), so the
  `MapToStruct[{{ .PropsTypeName }}]` template line resolves to the
  user-written name.
- The cross-file collision check in `internal/compiler/compiler.go`
  (Phase 5 step 11) still runs — it's gated on production output
  where many decls share a package. The shadow never hits that path
  because `compiler.go` is only invoked by `gastro generate`.

### Why not demangle in the LSP layer

Considered: keep mangling unconditional in codegen and post-process
hover text, completion labels, and diagnostic messages in
`internal/lsp/server/*` to rewrite `__page_<id>_Name` → `Name`.
Rejected because:
1. Every gopls response path needs the demangler
   (hover/completion/diagnostics/symbol/rename/code-action), and the
   list of paths grows over time.
2. The shadow's per-file subpackage isolation already makes mangling
   structurally unnecessary there. The opt-out matches the existing
   architectural invariant rather than fighting it.
3. The opt-out is a one-line change at two call sites; demangling is
   a recurring tax on every new gopls integration.

## Decisions rejected (with reasoning)

- **Magic comment opt-in (`//gastro:once`)**. Verbose for the common case
  ("everything that LOOKS package-level should BE package-level"),
  awkward interaction with `//gastro:embed` (which already implies
  init-once), and adds another directive to learn.
- **Two-section frontmatter (extra `---` separator).** Three `---`
  separators in one file. Awkward boilerplate for the common case where
  pages have no package-level decls.
- **Per-page sub-package generation.** Would prevent collisions
  naturally but is a bigger architectural change than warranted.
  Routes generation, Render API, dep wiring — all would shift.
- **Detect-and-error on cross-file collisions.** Simpler to implement
  but adds a user-visible failure mode that's invisible with
  prefix-mangling.
- **Per-page sub-struct (`__page_index = struct{...}{...}`)**. Grouping
  state under one name reads nicely but breaks when initializers
  reference each other (Go can't do struct-field-level dep ordering
  inside literals; would need a separate `init()` block).

## Pre-existing issues identified during exploration

1. **Cross-file collisions for arbitrary user types today.** Pages
   don't have `Props` (that's a component-only runtime contract,
   set by `gastro.Props()` in `analyze.go:detectGastroMarkers` which
   also flips `IsComponent = true`). But pages and components alike
   can declare arbitrary helper types in frontmatter (e.g.
   `type Comment struct{}` on a page, `type Helper struct{}` on a
   component). I haven't confirmed whether two such pages with the
   same type name collide today in the shared `package gastro`,
   or whether some pre-existing mechanism keeps them separate.
   For components, today's `funcName + Title-case` Props rename
   already sidesteps the Props-specific case; arbitrary non-Props
   component types may still collide. This plan's mangling fixes
   all of these as a side effect (no separate work). Verification:
   build a fixture with two pages each declaring
   `type Comment struct{}` and confirm both compile after this
   change. Logged here, not addressed separately.

2. **`internal/lsp/server/gopls.go:queryVariableTypes` is a half-no-op.**
   The function is documented as extracting frontmatter variable types
   by scanning `_ = VarName` lines in the virtual file, but production
   codegen emits no such lines for `:=`-declared frontmatter vars
   (verified against `examples/blog/.gastro/pages_blog_index.go`,
   which contains zero `_ = Title`-style lines). This means the
   template-body hover/completion paths that depend on it likely
   return empty type info today for the common case. Out of scope for
   this plan — the brokenness is independent of mangling — but logged
   as something that may be worth fixing separately, perhaps by
   teaching codegen to emit `_ = X` blank-marker lines for every
   exported frontmatter ident (cheap, deterministic, fixes
   queryVariableTypes for both `:=` and hoisted vars).

3. **`workspace.go:scanComponents` `propsType` field looks unused.**
   The Title-case rename at
   `internal/lsp/shadow/workspace.go:506-512` computes a `propsType`
   string per component, but `writeComponentStub` (line 540 onward)
   builds a fresh struct copy and never references `c.propsType`.
   Likely dead code from an earlier shadow design. Phase 7 step 18
   audits this; if confirmed dead, removing it tightens the LSP
   shadow without further work.

---

## Phase 1 — free-variable analysis helper

1. **New file** `internal/codegen/freevars.go`:

   ```go
   // ReferencesPerRequestScope reports whether the given AST node references
   // any of the per-request idents/calls that only exist inside a gastro
   // page handler:
   //
   //   - the bare idents `r` and `w`
   //   - calls to gastro.Props(), gastro.Context(), gastro.Children()
   //   - calls to gastro.From[T](...)
   //
   // It walks the node and any nested closures. The result is purely
   // syntactic — false positives are possible (a variable named `r` not
   // referring to the request) and accepted as a small cost for keeping
   // the check simple.
   func ReferencesPerRequestScope(node ast.Node) (bool, ast.Node) {
       // returns the offending sub-node for error reporting
   }
   ```

   Helper checks include:
   - `*ast.Ident` with name `r` or `w`, **only if not bound by an
     enclosing parameter list**. See free-variable note below.
   - `*ast.SelectorExpr` like `r.URL` or `w.Header`.
   - `*ast.CallExpr` matching `gastro.Props()`, `gastro.Context()`,
     `gastro.Children()`, `gastro.From[T](...)` (the last via
     `*ast.IndexExpr` / `*ast.IndexListExpr`).
   - Recursion into closure bodies: a `*ast.FuncLit` body counts as
     "this node" for capture purposes — a closure assigned to a hoisted
     `var` is unsafe if its body refers to per-request scope.

   **Free-variable semantics.** The walker tracks bound names
   introduced by enclosing `*ast.FuncLit` / `*ast.FuncDecl` parameter
   lists. A bare `r` or `w` only counts as per-request if it is *not*
   shadowed by an enclosing binder. Example:
   `var H = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { ... })`
   is safely hoistable — the inner `r`/`w` are bound by the closure's
   parameter list, not the page handler's. Without this, valid stdlib
   patterns get falsely rejected.

2. **Tests** (`internal/codegen/freevars_test.go`):
   - `TestRefs_BareR_Errors` — `r.URL.Path` flagged.
   - `TestRefs_BareW_Errors` — `w.Header()` flagged.
   - `TestRefs_GastroProps_Errors` — `gastro.Props().Name` flagged.
   - `TestRefs_GastroContext_Errors` — `gastro.Context()` flagged.
   - `TestRefs_GastroFromGeneric_Errors` — `gastro.From[*sql.DB](r.Context())`
     flagged.
   - `TestRefs_GastroChildren_Errors` — `gastro.Children()` flagged.
   - `TestRefs_PureExpr_OK` — `regexp.MustCompile("...")` not flagged.
   - `TestRefs_StdlibCall_OK` — `os.Getenv("X")` not flagged.
   - `TestRefs_ClosureCapturingR_Errors` — `func() string { return r.URL.Path }`
     flagged.
   - `TestRefs_ClosureNotCapturing_OK` — `func(s string) string { return s }`
     not flagged.
   - `TestRefs_ParamNamedR_NotFlagged` — `func(r io.Reader) { _ = r }`
     is *not* flagged because the inner `r` is bound by the closure
     param list. Free-variable analysis walks scopes correctly.
   - `TestRefs_HandlerFuncWrap_OK` — `var H = http.HandlerFunc(func(w
     http.ResponseWriter, r *http.Request) { ... })` is hoistable
     because the inner `w`/`r` are bound.

## Phase 2 — page-id derivation

3. **New file** `internal/codegen/pageid.go`:

   ```go
   // DerivePageID returns the per-page prefix used to namespace hoisted
   // declarations. It strips the top-level dir (e.g. "pages/" or
   // "components/"), replaces path separators and non-Go-ident chars
   // with "_", and collapses repeated underscores.
   //
   //   pages/admin/index.gastro        → "admin_index"
   //   components/hero.gastro          → "hero"
   //   pages/blog/[slug].gastro        → "blog_slug"
   //
   // Caller composes this with "__page_" or "__component_" to form
   // the full prefix.
   func DerivePageID(relativePath string) string
   ```

4. **Tests** (`internal/codegen/pageid_test.go`):
   - Plain path, nested path, dynamic-segment brackets, hyphens, dots,
     all sanitised.
   - `TestPageID_Collision` — two semantically different paths
     (`blog/[slug]` vs `blog/_slug_`) sanitising to the same ID returns
     the same string. Codegen layer (Phase 5) detects the collision
     and errors.

## Phase 3 — extend the analyzer for hoist eligibility

5. **Modify `internal/codegen/analyze.go`**:

   - Add to `FrontmatterInfo`:

     ```go
     type HoistedDecl struct {
         Kind       HoistKind  // VarKind, ConstKind, FuncKind, TypeKind
         Name       string     // user-visible name (unmangled)
         MangledName string    // __page_<id>_<Name>
         SourceText string     // verbatim declaration text (for emission)
         IsExported bool       // first rune uppercase → goes in __data
         Line       int        // 1-indexed within frontmatter
     }

     type FrontmatterInfo struct {
         // ... existing fields ...
         HoistedDecls []HoistedDecl
     }
     ```

   - Walk frontmatter AST. For each top-level `*ast.GenDecl`
     (`token.VAR`, `token.CONST`, `token.TYPE`) and `*ast.FuncDecl`:
     1. Run `ReferencesPerRequestScope` on the RHS / func body.
     2. If clean: build `HoistedDecl` with mangled name, append to
        `info.HoistedDecls`, **remove from `Frontmatter` source** (the
        per-request body no longer carries it).
     3. If unclean: error with the migration hint.

   - For each `*ast.AssignStmt` with `token.DEFINE` (`:=`): leave it
     alone (per-request).

   - **Type-decl handling**: hoist both `type T struct{...}` and
     `type T = X` aliases identically. Both get the
     `__page_<id>_T` / `__component_<id>_T` prefix.

   - **`var (...)` block decls**: split into individual
     `HoistedDecl` entries. The block syntax is dropped; each ident
     gets its own mangled name. User wrote:
     ```go
     var (
         X = 1
         Y = 2
     )
     ```
     Codegen emits:
     ```go
     var __page_a_X = 1
     var __page_a_Y = 2
     ```

   - **`func init()` special case**: `init` is the one func name that
     does NOT get mangled. Multiple `init` funcs per package are
     valid Go and run at package init in source-file order; mangling
     to `__page_a_init` would silently demote it to a normal function
     that nothing calls. Test: `TestHoist_FuncInit_NotMangled`.

   - **Existing `HoistTypeDeclarations` removed.** Its behaviour is
     subsumed by the new hoister, with the upgrade that types are now
     mangled too. For components this generalises today's
    `funcName + Title-case` Props rename into a uniform
    `__component_<id>_Props` scheme; for both pages and components
    it newly prevents cross-file collisions on arbitrary user
    types (e.g. two pages each declaring `type Comment struct{}`).

6. **Error message format:**

   ```
   pages/index.gastro:12: var "Title" cannot be hoisted to package
   scope because it references per-request state (r.URL.Path).

     12 | var Title = r.URL.Path
                      ^^^^^^^^^^

   Hoisted decls run once at process init; per-request state is only
   available inside the handler. Use `:=` so it runs each request:

       Title := r.URL.Path
   ```

   Implementation: error includes the file, line, ident, and offending
   sub-node (from `ReferencesPerRequestScope`). The migration hint is
   built mechanically from the original LHS + RHS.

## Phase 4 — reference rewriter for hoisted idents

7. **New file** `internal/codegen/rewrite_refs.go`:

   ```go
   // RewriteHoistedRefs walks the frontmatter source one more time,
   // finding every reference to a hoisted ident and replacing it with
   // the mangled name. This applies to:
   //
   //   - The hoisted decl's own initializer (other hoisted vars it
   //     depends on)
   //   - Per-request `:=` and statements that reference hoisted vars
   //
   // Returns the rewritten frontmatter source.
   func RewriteHoistedRefs(frontmatter string, hoisted []HoistedDecl) string
   ```

8. Implementation: parse frontmatter as Go via `go/parser`, walk for
   `*ast.Ident`, for each ident matching a hoisted name, record a
   byte-range edit `(start, end, mangledName)`. Apply edits in reverse
   source order to the original text. (Identical edit-application
   pattern to the existing `rewriteGastroMarkers` in
   `internal/codegen/generate.go:295-320`.)

9. **Tests** (`internal/codegen/rewrite_refs_test.go`):
   - `TestRewrite_HoistedRefHoisted` — hoisted `var Y = X` rewrites to
     `var __page_a_Y = __page_a_X` after both are mangled.
   - `TestRewrite_PerRequestRefHoisted` — `:= F(Title)` becomes
     `:= F(__page_a_Title)`.
   - `TestRewrite_TemplateBodyUntouched` — only the frontmatter is
     rewritten; the template body's `{{ .Title }}` is unchanged.
   - `TestRewrite_ShadowedIdent` — a `:=` redeclaration shadowing a
     hoisted name (Go-illegal) is detected/errored at analysis time
     (phase 3), so the rewriter never sees it.

## Phase 5 — emit hoisted decls in the generated file

10. **Add `GenerateOptions` to the codegen API.** Modify
    `internal/codegen/generate.go`:

    ```go
    type GenerateOptions struct {
        MangleHoisted bool // see § LSP shadow workspace impact
    }

    func GenerateHandler(
        file *parser.File,
        info *FrontmatterInfo,
        isComponent bool,
        opts GenerateOptions,
    ) (string, error)
    ```

    The analyzer (Phase 3) computes both `Name` and `MangledName` on
    every `HoistedDecl` unconditionally. Phase 5 emission picks one or
    the other based on `opts.MangleHoisted`:
    - `true` → emit `MangledName`, run `RewriteHoistedRefs` over the
      frontmatter residue, set `templateData.PropsTypeName` to the
      mangled form.
    - `false` → emit `Name`, skip `RewriteHoistedRefs` (or run it as a
      no-op since `Name == MangledName`), keep `PropsTypeName`
      unmangled.

    Update all in-tree callers in the same commit:
    - `internal/compiler/compiler.go` → `{MangleHoisted: true}`.
    - `internal/lsp/shadow/workspace.go:UpdateFile` →
      `{MangleHoisted: false}`.
    - `internal/lsp/shadow/shadow.go:GenerateVirtualFile` →
      `{MangleHoisted: false}`.

11. **Modify the handler/component templates** in
    `internal/codegen/generate.go`:

    Add a new template variable `HoistedDecls` carrying the verbatim
    text block. Position it between the `import` block and the `func`:

    ```go
    var handlerTmpl = template.Must(template.New("handler").Parse(`...
    package {{ .PackageName }}

    import (
        "log"
        "net/http"
        "html/template"
        gastroRuntime "github.com/andrioid/gastro/pkg/gastro"
    {{- range .Imports }}
        "{{ . }}"
    {{- end }}
    )

    var _ = template.Must
    var _ http.ResponseWriter
    var _ = log.Println

    {{ .HoistedDecls }}        ← NEW: package-scope decls

    func (__router *Router) {{ .FuncName }}(w http.ResponseWriter, r *http.Request) {
        w = gastroRuntime.NewPageWriter(w)
        defer gastroRuntime.Recover(w, r)

        {{ .Frontmatter }}     ← still has the per-request residue
                                 (decls were physically removed in Phase 3)

        if gastroRuntime.BodyWritten(w) {
            return
        }

        __data := map[string]any{
        {{- range .ExportedVars }}
            "{{ .Name }}": {{ .MangledOrLocalName }},
        {{- end }}
        }
        ...
    }
    `))
    ```

    The `__data` map's *key* uses the unmangled `Name`; the *value*
    uses `MangledName` (which equals `Name` under
    `MangleHoisted: false`) for hoisted vars or the local name for
    per-request `:=` vars. Template body's `{{ .Title }}` continues to
    work without change in both modes.

12. **Cross-file collision detection.** The codegen pipeline emits one
    `.gen.go` per `.gastro` file. Mangling makes collisions impossible
    in normal cases, but two paths sanitising to the same page-id (per
    `pageid_test.go::TestPageID_Collision`) would collide. Detect at
    the package-level codegen step (`internal/compiler/compiler.go`
    around line 175 where all results are combined): walk all
    `HoistedDecls` across all `compileResult`s; if two share the same
    `MangledName`, error with both source locations.

13. **Tests** (extend `internal/codegen/generate_test.go`):
    - `TestGenerate_HoistedVar` — `var X = 42` produces a package-scope
      `var __page_<id>_X = 42` and `__data["X"] = __page_<id>_X`.
    - `TestGenerate_HoistedVarReferencingHoistedVar` — initializer
      ordering preserved, refs rewritten.
    - `TestGenerate_HoistedConst` — works analogously.
    - `TestGenerate_HoistedFunc` — `func F() string {...}` becomes
      `func __page_<id>_F() string {...}`; calls inside frontmatter
      rewritten.
    - `TestGenerate_HoistedInit` — `func init() {...}` becomes a
      package-level `func init() {...}` (no mangling needed; multiple
      `init` funcs are valid Go).
    - `TestGenerate_HoistedType_Page` — page-level
      `type Comment struct {...}` becomes
      `type __page_<id>_Comment struct {...}`. (Pages cannot declare
      `Props`; that's component-only — `Props` is only set by
      `gastro.Props()` which flips `IsComponent = true` in
      `analyze.go:detectGastroMarkers`.)
    - `TestGenerate_HoistedType_ComponentProps` — component with
      `type Props struct {...}` becomes
      `type __component_<id>_Props struct {...}`. The
      `MapToStruct[Props]` call inside the component handler is
      rewritten to reference the mangled name.
    - `TestGenerate_HoistedFuncCapturingR_Errors` —
      `var H = func() string { return r.URL.Path }` errors with the
      migration hint.
    - `TestGenerate_PerRequestUntouched` — `:=` decls and statements
      remain in the handler body.
    - `TestGenerate_MangleHoistedFalse_NoRename` — with
      `GenerateOptions{MangleHoisted: false}`, hoisted decls keep
      their user-written names; `RewriteHoistedRefs` produces
      byte-identical output to its input.
    - `TestGenerate_MangleHoistedFalse_PropsTypeName` — component
      with `type Props struct{...}` emits
      `MapToStruct[Props](propsMap)` (not
      `MapToStruct[__component_<id>_Props]`).

## Phase 6 — handle the type-mangling fallout

*Scope reminder: this entire phase is component-only. `Props` is set
only when `gastro.Props()` appears in frontmatter, which flips
`IsComponent = true`. Pages have no Props, no XProps alias, and no
Render API surface to thread mangled names through.*

13. **`type Props struct{...}` rename ripples (components only).**
    The existing codegen emits
    `gastroRuntime.MapToStruct[Props](propsMap)` inside the component
    handler (`internal/codegen/generate.go:194`). With Props mangled
    to `__component_<id>_Props`, that call must use the mangled name.
    Update the component template to reference the `MangledName`
    from the hoisted Props decl rather than the literal `Props`.

14. **Render API surface (components only).** `XProps` types in
    `render.go` (the package-level Render API) reference the
    user-named struct (e.g. `CardProps` from a component named
    `Card`). Today codegen renames the in-component `Props` to
    `componentCardProps` (via `funcName + Title-case`); `render.go`
    aliases that to the exported `CardProps`. With mangling, the
    source struct becomes `__component_card_Props`. The render
    template needs to:
    - Reference `__component_card_Props` internally when generating
      `type CardProps = ...` aliases.
    - Keep the public `gastro.Render.Card(CardProps{...})` API
      unchanged — the alias bridges mangled-internal to
      unmangled-public.
    - Smoke test added in Phase 9: `gastro.Render.Card(gastro.CardProps{...})`
      from `examples/blog` or `examples/dashboard` compiles.

15. **`PropsTypeName`** field in `internal/codegen/generate.go`
    `templateData` — currently a string like `"Props"` (component
    only; empty for pages). Becomes the mangled form
    (`__component_<id>_Props`) under `MangleHoisted: true`, stays
    `"Props"` under `MangleHoisted: false`. All uses in templates
    need to reference whichever the resolved name is.

16. **`gastro check` byte-stability.** The existing `gastro check`
    command byte-compares committed `.gen.go` against fresh codegen.
    Mangled names must be **deterministic across machines** for this
    to keep working. The page-id derivation (Phase 2) uses
    file-path-relative-to-project-root + lexicographic sanitisation,
    which is fully deterministic. Add a regression test in Phase 9
    that runs codegen twice and asserts byte-identical output.

## Phase 7 — LSP shadow integration

Goal: the shadow's virtual files emit unmangled hoisted decls so
gopls returns user-readable hover/completion/diagnostic text, while
still exercising the same `GenerateHandler` codepath as production.

17. **Wire `MangleHoisted: false` through the shadow callers.**

    - `internal/lsp/shadow/workspace.go:UpdateFile` and
      `internal/lsp/shadow/shadow.go:GenerateVirtualFile` both call
      `codegen.GenerateHandler(parsed, info, isComponent,
      codegen.GenerateOptions{MangleHoisted: false})`.
    - Smoke: existing shadow tests
      (`internal/lsp/shadow/shadow_test.go`,
      `workspace_test.go`) keep passing without behavioural change
      — since the unmangled output matches today's pre-mangling
      output, the shadow's external contract is preserved.

18. **Reconcile the existing component-Props rename.** Today
    `workspace.go:scanComponents` (around line 506) reproduces
    codegen's pre-existing `funcName + Title-case` Props rename so
    the stub's XProps targets the right symbol. Once Phase 6 lands,
    the codegen rename scheme for production becomes
    `__component_<id>_Props`. The shadow's mirror only needs to
    track the **unmangled** name (`Props`) because the shadow runs
    with `MangleHoisted: false`. Update `scanComponents` accordingly,
    or remove the rename step entirely (it appears unused in
    `writeComponentStub`, which builds a fresh struct copy — audit
    in Phase 9).

19. **`queryVariableTypes` and hoisted vars.**
    `internal/lsp/server/gopls.go:queryVariableTypes` finds
    frontmatter var types by scanning `_ = VarName` lines in the
    virtual file. Today no such lines exist for `:=` vars in
    production output — the function is a half-no-op for the common
    case (verified via `examples/blog/.gastro/pages_blog_index.go`,
    which contains zero `_ = Title`-style lines). Hoisting changes
    the picture:

    - Hoisted exported `var` decls now exist at package scope. Without
      a `_ = Title` line, gopls still won't tell
      `queryVariableTypes` their type via the `_ = ...` scan path.
    - **Decision**: do not change `queryVariableTypes` in this plan.
      The function's brokenness for `:=` vars is a pre-existing issue
      logged here and deferred. If Phase 9 verification reveals the
      hoisted-var case actually does something useful (e.g. via the
      probe-injection paths in `queryFieldsFromGopls`), document it
      and move on.
    - **Pre-existing issue logged**: `queryVariableTypes` likely
      returns empty for `:=`-declared frontmatter vars today. Surface
      area: template-body hover/completion type-info. Out of scope
      for this plan; tracked separately.

20. **LSP regression test sweep** (per AGENTS.md skill bar). New tests:

    - `internal/lsp/shadow/shadow_hoist_test.go`:
      - `TestShadow_HoistedVar_HoverShowsUnmangled` — generate the
        shadow for a fixture with `var Title = "x"` in frontmatter,
        type-check via `go/types`, query the hover at the var
        decl's position, assert the symbol name in the hover output
        is `Title` (not `__page_<id>_Title`).
      - `TestShadow_HoistedVar_GoToDefLandsOnSourceLine` — build a
        shadow with two pages each declaring `var Title = ...` (the
        cross-page collision case), confirm both shadow files
        compile (each in its own subpackage), confirm go-to-def on
        a `:=` line that references `Title` lands on the hoisted
        var line.
      - `TestShadow_HoistedFunc_CallSiteRewritten` — frontmatter has
        `func slug(s string) string { ... }` and `:= slug(Title)`;
        shadow keeps both as `slug` (no mangling) so gopls resolves
        the call.
      - `TestShadow_HoistedType_PropsAliasResolves` — component with
        `type Props struct { Items []string }` in frontmatter:
        shadow's `MapToStruct[Props]` line type-checks against the
        shadow's package-level `Props` decl, no
        `__component_<id>_Props` leakage.

    - `internal/lsp/server/completion_hoist_test.go`:
      - `TestCompletion_FrontmatterIdent_NoMangledLabels` — trigger
        completion mid-frontmatter where a hoisted `Title` is in
        scope; assert no completion item label contains `__page_`
        or `__component_`.
      - `TestCompletion_TemplateBodyVar_TypeResolved` — frontmatter
        has `var Posts []db.Post = db.ListAll()`; template body
        completion after `{{ range .Posts }}` returns `db.Post`
        fields. (Confirms the hoist did not break the existing
        template-body completion path.)

    - `internal/lsp/server/diagnostics_hoist_test.go`:
      - `TestDiagnostic_HoistedTypeMismatch_MessageClean` — frontmatter
        has `var Title int = "x"`; assert the diagnostic message
        contains `Title` (not `__page_<id>_Title`).

    - `cmd/auditshadow` regression: re-run against
      `examples/blog`, `examples/dashboard`, `examples/sse`,
      `examples/gastro`. Assert the diagnostic-class baseline from
      `tmp/lsp-shadow-audit.md §3` does not regress: the count of
      `undefined identifier` and `missing method/field on stub type`
      classes must be ≤ their pre-change values.

## Phase 8 — docs

21. **Update `docs/pages.md`**:
    - New section: "Frontmatter scope: package-level vs per-request."
    - Spell out the mental model: `var`/`const`/`func`/`type` at top
      level → init-once package scope; `:=` and statements →
      per-request.
    - Worked example showing both halves in one frontmatter.
    - Note: identifier mangling is invisible; users see/write/reference
      original names.
    - Foot-gun: `var X = expensive()` in frontmatter slows startup,
      not request handling. Profile init time if the binary boots
      slowly.

22. **Update `docs/components.md`** with the same scope rules.

23. **Update `docs/architecture.md`**:
    - "How frontmatter compiles" section gains a paragraph on hoisting.
    - Reference the prefix scheme so future debugging of `.gen.go`
      files is straightforward.
    - Note the LSP shadow's `MangleHoisted: false` opt-out and the
      reason (per-file subpackage isolation makes mangling
      structurally unnecessary).

24. **CHANGELOG entry** showing one before/after of frontmatter that
    benefits (e.g. a page with a compiled regex or env-var read).
    Mark as a behavioural change — declarations that previously ran
    per-request now run once. Note the per-request-reference error
    mode.

## Phase 9 — verification

25. `mise run test` (root) green with `-race`.
26. `mise run lint` clean.
27. Per-example: `go tool gastro generate && go build ./...` for
    `blog`, `dashboard`, `sse`, `gastro`. Specifically check
    `examples/blog`, since blog's pages may have multiple pages with
    similarly-named props/vars and exercise the cross-file
    collision-prevention path.
28. `go tool gastro check` clean against all four examples.
29. `bash scripts/verify-bootstrap` passes.
30. **Performance smoke**: pick one example page that has a
    non-trivial init expression (or add one — `var slugRE =
    regexp.MustCompile(...)` is enough). Confirm via inspection of
    the generated `.gen.go` that the regex compile is a package-scope
    `var`, not inside the handler body.
31. **LSP shadow byte comparison**: build the shadow for one fixture
    page and one fixture component, dump the virtual `.go` source,
    confirm zero `__page_` / `__component_` prefixes appear (proves
    `MangleHoisted: false` is wired correctly through both shadow
    entry points).
32. **End-to-end LSP smoke** (manual, one-time post-merge): open
    `examples/blog/pages/blog/index.gastro` in an editor with the
    gastro LSP attached, hover over a hoisted ident, confirm the
    popup shows the unmangled name. Document the procedure in
    `docs/contributing.md` so future regressions are catchable
    without code changes.
33. **Pre-existing-issue verification**: try a fixture with two pages
    in the same package both declaring `type Props struct{...}` and
    confirm both compile after this change. If they didn't compile
    before this change either, log as "pre-existing issue resolved as
    a side effect."
34. **Pre-existing issue logged**:
    `internal/lsp/server/gopls.go:queryVariableTypes` likely returns
    empty for `:=`-declared frontmatter vars (no `_ = VarName` lines
    are emitted by codegen for that shape). Out of scope for this
    plan; document in the CHANGELOG "known issues" section if it
    isn't already tracked elsewhere.

## Rollback plan

The change is internal codegen + analyzer; no public API surface is
removed or renamed.

- `git revert` on the codegen / analyzer changes.
- Revert the doc changes.
- The CHANGELOG entry stands as a "did happen, was reverted" note.

If a regression is found that affects a specific case, the more
surgical option is to disable hoisting via a build-time flag (if we
add one — see open question 1 below) and ship the partial revert.

## Out of scope

- **Hoisting across pages** (a hoisted `var` in page A referenced from
  page B). The mangling deliberately prevents this; if shared state is
  needed, it lives in a sibling `.go` file in the same package.
- **Smart per-page lazy init** (compute hoisted vars on first use
  rather than at package init). Adds complexity for little benefit
  given the static-content workload Shape A targets.
- **Goroutine-safety of hoisted vars.** Same as any package-level Go
  var: read-only after init is fine; mutable shared state needs the
  user's own sync. Document the convention.
- **Hoisting partial decls (e.g. only the LHS).** Out of scope; either
  the whole decl hoists or none of it does.
- **A `//gastro:nohoist` escape hatch** to opt a `var` decl out of
  hoisting. Defer until somebody actually needs it. The strict-error
  default + `:=` migration covers the cases I can think of.

---

## Open: nothing remaining

All decisions locked. Outstanding items are:

- **Pre-existing issue verification** (cross-file collisions on
  arbitrary user types in frontmatter — e.g. two pages each
  declaring `type Comment struct{}`, or two components with non-Props
  helper types of the same name) — deferred to execution time per
  AGENTS.md "handle pre-existing issues last" guidance. Note: cross-file
  Props collisions specifically cannot occur, since today's codegen
  already renames component Props to `funcName + Title-case`
  (`componentCardProps`) and pages don't have Props at all. The
  remaining collision surface is arbitrary user-declared types. The
  mangling preserves correct behaviour either way; we just want to
  log what the prior state was for the CHANGELOG.
- **`findModuleRoot` coordination** with the markdown directive plan
  and `tmp/gastro-watch-go-scope-plan.md` — whichever lands first
  owns the helper, the others reuse. No conflict; just don't
  duplicate.
