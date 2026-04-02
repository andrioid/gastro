# Replace `render` keyword with bare function calls

## Status: Ready to implement

## Goal

Simplify leaf component invocation from `{{ render Card (dict "Title" .Name) }}` to
`{{ Card (dict "Title" .Name) }}`. One less custom keyword, closer to idiomatic Go
templates. The `wrap` keyword stays for components with children (Go templates have
no native mechanism for dynamic children).

## Syntax

| Pattern | Current | New |
|---|---|---|
| Leaf component | `{{ render Card (dict "Title" .Name) }}` | `{{ Card (dict "Title" .Name) }}` |
| Component with children | `{{ wrap Layout (dict ...) }}...{{ end }}` | Unchanged |

## How it works

Leaf components are registered directly in the FuncMap under their PascalCase
import alias (`"Card"` instead of `"__gastro_Card"`). The template body
`{{ Card (dict "Title" .Name) }}` is already valid Go template syntax -- no
transformation needed.

For `wrap`, the transformer still extracts children into `{{define}}` blocks
but emits `{{ Layout (dict ...) }}` instead of `{{ __gastro_Layout (dict ...) }}`.

The `__gastro_render_children` internal function keeps its prefix since it's not
user-facing.

## Name collision prevention

Convention: components are PascalCase, functions are camelCase. All Go template
builtins are lowercase (`and`, `or`, `eq`, `len`). All Gastro helpers are
camelCase (`dict`, `safeHTML`, `timeFormat`). Collisions are essentially impossible.

## Phases

### Phase 1: Transformer (`internal/codegen/template.go`)

- Delete `renderRegex` variable (line 12)
- Delete `transformRender()` function (lines 90-111)
- Remove `transformRender()` call from `TransformTemplate()` (line 66)
- Remove `render` from `TransformTemplate` doc comment
- Update `transformOneWrap()` line 163: change format string from
  `{{ __gastro_%s (%s ...) }}` to `{{ %s (%s ...) }}`
- Update old-syntax detection: remove `render` references

### Phase 2: FuncMap registration (`internal/compiler/compiler.go`)

The FuncMap wiring lives in the `routesTmpl` code generation template. Changes:
- Line 361: `fm["__gastro_{{ .Name }}"]` → `fm["{{ .Name }}"]`
- Keep `fm["__gastro_render_children"]` unchanged (line 363)
- No changes needed in `internal/codegen/generate.go`

### Phase 3: Error messages (`internal/compiler/compiler.go`)

Replace `template.Must` with explicit error handling in generated code:

- Change `__gastro_parseTemplate` signature to return `(*template.Template, error)`
  instead of `*template.Template`
- Remove `template.Must` wrapper (line 393), use explicit parse + error check
- Add `enhanceTemplateError` helper that detects PascalCase undefined functions
  via regex on the error message (`function "Crad" not defined`) and rewrites to:
  `unknown component "Crad" (did you forget to import it?)`
- Update `Routes()` to handle the error: `log.Fatalf("gastro: %v", err)` for both
  dev and prod modes (fail fast at startup)
- Update `__gastro_getTemplate()` dev-mode re-parse to also handle errors with
  `log.Fatalf` (same strategy)

### Phase 4: Collision check (`internal/compiler/compiler.go`)

In the generated `Routes()` function, before template parsing:
- Collect all component names from template metadata
- Check if any user `WithFuncs()` names match component names
- Log warning: `gastro: user function "Card" shadows component "Card"`
- Components win over user funcs (user funcs are copied first into FuncMap,
  then component funcs overwrite in `__gastro_buildFuncMap`)

### Phase 5: Transformer tests (`internal/codegen/template_test.go`)

- Delete `TestTransformTemplate_RenderLeafComponent` (line 42)
- Delete `TestTransformTemplate_RenderNoProps` (line 59)
- Delete `TestTransformTemplate_RenderWithPipeline` (line 250)
- Delete `TestTransformTemplate_UnknownComponentRender` (line 201)
- Add `TestTransformTemplate_BareFunctionCallPassthrough`: `{{ Card (dict ...) }}`
  passes through `TransformTemplate` unchanged
- Update `TestTransformTemplate_MixedHTMLAndComponents` (line 112): use bare
  `{{ Card }}` syntax, assert no transformation occurs
- Update all `wrap` test expectations: `__gastro_Layout` → `Layout`,
  `__gastro_Card` → `Card` (affects ~8 tests)
- Update `TestTransformTemplate_OutputParseable` (line 295): stub FuncMap uses
  `"Card"` key instead of `"__gastro_Card"`
- Update `TestTransformTemplate_WrapOutputParseable` (line 327): same
- Update `TestTransformTemplate_DuplicateWrapParseable` (line 362): same

### Phase 6: Compiler & codegen tests

**`internal/compiler/compiler_test.go`:**
- `TestCompile_RoutesContainsTemplateFuncMapWiring` (line 204): assert
  `fm["Badge"] = componentBadge` and `fm["Card"] = componentCard`
  instead of `fm["__gastro_Badge"]` / `fm["__gastro_Card"]`

**`internal/codegen/generate_test.go`:**
- No `__gastro_` FuncMap key assertions exist here, so minimal changes
  (only if test templates use `render` syntax)

### Phase 7: LSP updates

**`internal/lsp/template/parse.go`:**
- `stubFuncs["__gastro_"+u.Name]` → `stubFuncs[u.Name]` (line ~55)
- Remove `stubFuncs["render"]` (line ~62)
- Keep `stubFuncs["wrap"]` (still a keyword)
- Keep `stubFuncs["__gastro_render_children"]`

**`internal/lsp/template/completions.go`:**
- Replace `componentInvocationRegex` with AST-based detection:
  - Primary: walk `ParseTemplateBody` tree, find `*parse.IdentifierNode` in
    `*parse.CommandNode` where the identifier is PascalCase and matches a
    known component from `uses`
  - Fallback: regex for incomplete/unparseable templates during editing
- Update `diagnoseUnknownComponents` to use the AST-based approach
- Keep `wrap` detection for wrap-specific diagnostics

**`internal/lsp/template/completions_test.go`:**
- Update all `{{ render Card ... }}` → `{{ Card ... }}` in test templates
- Update expected diagnostic positions (column numbers will shift)

**`internal/lsp/template/parse_test.go`:**
- `TestParseTemplateBody_WithComponentFunctions` (line 60): change template from
  `{{ __gastro_Card (dict ...) }}` to `{{ Card (dict ...) }}`

**`cmd/gastro-lsp/main.go`:**
- Update `componentNameRegex` (line 888): match PascalCase identifiers after `{{`
  without requiring `render`/`wrap` prefix. Pattern:
  `\{\{\s*(?:wrap\s+)?([A-Z][a-zA-Z0-9]*)`
- Update `componentHover()` (line 894): adjust offset calculations for cursor
  position matching (no more `render ` prefix to skip)
- Update `componentDefinition()` (line 1015): same offset adjustments

**`cmd/gastro-lsp/lsp_integration_test.go`:**
- `TestLSP_ComponentPropDiagnostics` (line 897): update template body
- `TestLSP_ComponentHover` (line 971): update template body and cursor position
  (char offset shifts left by 7 characters — length of `render `)
- `TestLSP_ComponentDefinition` (line 1033): update template body and cursor position

### Phase 8: Migrate `.gastro` files

14 files, 49 occurrences: `{{ render X (dict ...) }}` → `{{ X (dict ...) }}`

Files:
- `examples/gastro/pages/docs/*.gastro` (7 files)
- `examples/gastro/pages/index.gastro`
- `examples/dashboard/components/dashboard.gastro`
- `examples/blog/components/post-card.gastro`
- `examples/blog/pages/index.gastro`
- `examples/blog/pages/blog/index.gastro`
- `internal/compiler/testdata/composition/pages/index.gastro`
- `internal/compiler/testdata/composition/components/card.gastro`

### Phase 9: Content and docs

- `examples/gastro/content/docs.go`: update 6 code example strings that contain
  `{{ render ... }}` syntax (lines 143, 239, 266, 269, 272, and surrounding context)
- `docs/components.md`: update syntax documentation (lines 126-131, 276)
- `docs/pages.md`: update example (line 102)
- `docs/design.md`: update ~15 references to `render` syntax throughout
- `docs/architecture.md`: update transformation examples (line 96, 102)
- `docs/sse.md`: no template syntax changes needed (uses `gastro.Render` Go API,
  not the template keyword)
- `DECISIONS.md`: new entry documenting the syntax change

### Phase 10: Rebuild + verify

- Rebuild CLI, regenerate all examples, build all examples
- Full test suite with `-race`
- Website smoke test

## Risk assessment

| Risk | Severity | Mitigation |
|---|---|---|
| Loss of compile-time component validation | HIGH | Enhanced error messages (Phase 3) + LSP diagnostics |
| FuncMap collision with `WithFuncs()` | LOW | Runtime warning in `Routes()` (Phase 4) |
| LSP detection without `render` keyword | MEDIUM | AST-based detection with regex fallback (Phase 7) |
| Migration breakage | LOW | Mechanical replacement, verified by rebuild |
| `wrap` asymmetry | LOW | Justified by Go template limitations, documented |
| Error message signature change | LOW | Generated code, no user-facing API change |

## Reviewed by

- golang-pro agent: approved with conditions (error messages, collision check, LSP robustness)
- Implementation review: refined Phase 2 location, Phase 3 error handling strategy,
  Phase 7 AST-based detection, corrected Phase 8 file count (14 files, 49 occurrences)
