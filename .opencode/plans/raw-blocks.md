# Compile-time `{{ raw }}...{{ endraw }}` blocks

## Status: Implemented

## Goal

Allow template authors to write literal Go template syntax without manual
escaping. Everything inside `{{ raw }}...{{ endraw }}` is emitted verbatim —
the compiler escapes all `{{` and `}}` so Go's template engine treats them as
text.

## Syntax

```gastro
{{ raw }}
<h1>{{ .Greeting }}</h1>
<p>Hello {{ .Name }}, nice to see you.</p>
{{ endraw }}
```

Compiles to:

```
<h1>{{ "{{" }} .Greeting {{ "}}" }}</h1>
<p>Hello {{ "{{" }} .Name {{ "}}" }}, nice to see you.</p>
```

Whitespace trim variants are supported: `{{- raw }}`, `{{ raw -}}`,
`{{- raw -}}`, and the same for `endraw`.

## Motivation

Showing `.gastro` code examples in templates currently requires manually
escaping every `{{` and `}}`:

```
&lt;h1&gt;{{ "{{ .Greeting }}" }}&lt;/h1&gt;
```

This is tedious and hard to read. The `{{ raw }}` block solves this with a
single wrapper.

## Design decisions

1. **Compile-time, not runtime.** The transformation happens in
   `TransformTemplate()` alongside wrap block rewriting. Zero runtime cost.
   No new template function is registered at runtime.

2. **Standard escaping.** `{{` becomes `{{ "{{" }}` and `}}` becomes
   `{{ "}}" }}`. This is the idiomatic Go template approach.

3. **No nesting.** The first `{{ endraw }}` closes the block. A `{{ raw }}`
   inside a raw block is escaped like everything else.

4. **Comment-safe.** Comment extraction runs before raw block processing, so
   `{{ raw }}` inside `{{/* comments */}}` is ignored.

5. **Manual scanner, not regex.** The content between `{{ raw }}` and
   `{{ endraw }}` contains `{{` and `}}` which would confuse regex matching.
   We use a manual character-by-character scanner, following the same pattern
   as the existing `findMatchingEnd()` function in `template.go`.

6. **Single-pass escaping.** The `{{` → `{{ "{{" }}` and `}}` → `{{ "}}" }}`
   replacements must happen in a single pass to avoid corrupting previously
   inserted delimiters.

## Phases

### Phase 1: Compiler transform (`internal/codegen/template.go`)

Add `escapeRawBlocks(body string) (string, error)`:

- **Manual scanner** (not regex) to find `{{ raw }}` and `{{ endraw }}` markers
- Follow the existing `findMatchingEnd()` / `findActionClose()` pattern
- Find each `{{ raw }}`...`{{ endraw }}` pair by scanning for `{{` then checking
  the keyword inside the action
- Extract content between markers
- **Single-pass escape**: scan content character by character, replacing
  `{{` → `{{ "{{" }}` and `}}` → `{{ "}}" }}` in one traversal
- Replace entire `{{ raw }}...{{ endraw }}` with escaped content (markers removed)
- Whitespace trim dashes on `{{- raw -}}` affect whitespace **around** the raw
  block (before `{{ raw }}` and after `{{ endraw }}`), not content inside
- Error on unmatched `{{ raw }}` (no closing `{{ endraw }}`) — include line number
- Error on unmatched `{{ endraw }}` (no opening `{{ raw }}`) — include line number

**Insertion point in `TransformTemplate()`:**

```go
// Extract comments first — protects them from all transformations
body, comments := extractComments(body)

// Escape raw blocks (after comments so {{ raw }} in comments is safe)
body, err := escapeRawBlocks(body)
if err != nil {
    return "", err
}

// Transform wrap blocks...
```

After comment extraction (so `{{ raw }}` inside comments is ignored), before
wrap transformation (so `{{ wrap }}` inside raw blocks is escaped, not
transformed).

### Phase 2: Tests (`internal/codegen/template_test.go`)

| Test | Input | Expected |
|------|-------|----------|
| Basic escaping | `{{ raw }}{{ .X }}{{ endraw }}` | `{{ "{{" }} .X {{ "}}" }}` |
| Multi-line | `{{ raw }}\n{{ .A }}\n{{ .B }}\n{{ endraw }}` | Escaped on each line |
| Non-template content preserved | `{{ raw }}<h1>Hello</h1>{{ endraw }}` | `<h1>Hello</h1>` |
| Whitespace trim dashes | `{{- raw -}}...{{- endraw -}}` | Trim dashes applied correctly |
| Unmatched raw → error | `{{ raw }}no endraw` | Error with line number |
| Orphan endraw → error | `{{ endraw }}` | Error with line number |
| Outside content untouched | `{{ .Title }}{{ raw }}{{ .X }}{{ endraw }}` | Only content inside raw is escaped |
| Wrap inside raw is escaped | `{{ raw }}{{ wrap X }}{{ endraw }}` | Escaped, not transformed |
| Multiple blocks | Two separate raw blocks | Both escaped independently |
| Verify with Go template parser | Escaped output parses and executes correctly | Literal `{{ .X }}` in rendered HTML |
| Raw inside wrap block | `{{ wrap L (dict) }}{{ raw }}{{ .X }}{{ endraw }}{{ end }}` | Wrap transformed, raw content escaped |
| Adjacent delimiters | `{{ raw }}{{{{ .X }}}}{{ endraw }}` | Both pairs escaped correctly |
| Empty raw block | `{{ raw }}{{ endraw }}` | Markers removed, empty output |
| Raw at file start | `{{ raw }}{{ .X }}{{ endraw }}rest` | Escaped content followed by rest |

### Phase 3: LSP support (`internal/lsp/template/`)

**`parse.go` — add stubs to `buildStubFuncMap()`:**

```go
// raw/endraw are compile-time keywords that appear in untransformed templates.
stubFuncs["raw"] = ""
stubFuncs["endraw"] = ""
```

This prevents Go's template parser from failing on `{{ raw }}` and
`{{ endraw }}` when the LSP parses the raw (pre-compilation) template body.

**`completions.go` — strip raw blocks before diagnostics in `Diagnose()`:**

Add `stripRawBlocks(body string) string` that replaces
`{{ raw }}...{{ endraw }}` with spaces (preserving newlines for line count,
spaces for column positions). Call it at the top of `Diagnose()`:

```go
func Diagnose(templateBody string, ...) []Diagnostic {
    // Strip raw blocks — their content is literal text, not template logic
    templateBody = stripRawBlocks(templateBody)
    // ... existing logic ...
}
```

This prevents false "unknown variable" diagnostics for `{{ .Var }}` inside
raw blocks, since that content is meant to be literal output.

### Phase 4: Update examples (3 files)

**`examples/gastro/components/hero.gastro` (lines 30-31):**

Replace:
```
    &lt;h1&gt;{{ "{{ .Greeting }}" }}&lt;/h1&gt;
    &lt;p&gt;Hello {{ "{{ .Name }}" }}, nice to see you.&lt;/p&gt;
```
With:
```
{{ raw }}
    &lt;h1&gt;{{ .Greeting }}&lt;/h1&gt;
    &lt;p&gt;Hello {{ .Name }}, nice to see you.&lt;/p&gt;
{{ endraw }}
```

**`examples/gastro/pages/docs/components.gastro` (lines 64, 68):**

Replace inline `{{ "{{ .Children }}" }}` with `{{ raw }}{{ .Children }}{{ endraw }}`.

**`examples/gastro/pages/docs/getting-started.gastro` (line 39):**

Replace inline `{{ "{{ .Title }}" }}` and `{{ "{{ .Year }}" }}` with
`{{ raw }}{{ .Title }}{{ endraw }}` and `{{ raw }}{{ .Year }}{{ endraw }}`.

### Phase 5: Documentation

Update the Gastro skill reference (`SKILL.md`) to document the
`{{ raw }}...{{ endraw }}` syntax under Template Functions or a new section.

## Edge cases

| Case | Behavior |
|------|----------|
| Empty raw block `{{ raw }}{{ endraw }}` | Markers removed, empty output |
| `{{ raw }}` inside a Go template comment | Comment extracted first, so it's ignored |
| `{{ raw }}` at end of file without `{{ endraw }}` | Error with line number |
| `{{ endraw }}` without `{{ raw }}` | Error with line number |
| Nested `{{ raw }}{{ raw }}{{ endraw }}` | Inner `{{ raw }}` is escaped; first `{{ endraw }}` closes the block |
| Content with `}}` inside raw block | Escaped to `{{ "}}" }}` |
| Adjacent `}}{{` inside raw block | Both are escaped correctly |
| `{{ raw }}` inside `{{ wrap }}` | Works: comments extracted → raw escaped → wrap transformed |
| `{{ raw }}` inside string literals | Scanner should skip quoted strings to avoid false matches |
| Adjacent `{{{{` or `}}}}` | Each pair escaped independently |

## Files changed

| File | Change |
|------|--------|
| `internal/codegen/template.go` | Add `escapeRawBlocks()`, wire into `TransformTemplate()` |
| `internal/codegen/template_test.go` | ~14 new tests |
| `internal/lsp/template/parse.go` | Add `"raw"` and `"endraw"` stubs |
| `internal/lsp/template/completions.go` | Add `stripRawBlocks()`, call in `Diagnose()` |
| `examples/gastro/components/hero.gastro` | Use `{{ raw }}` block |
| `examples/gastro/pages/docs/components.gastro` | Use `{{ raw }}` for `{{ .Children }}` references |
| `examples/gastro/pages/docs/getting-started.gastro` | Use `{{ raw }}` for `{{ .Title }}` and `{{ .Year }}` |

## Files NOT changed

- `pkg/gastro/funcs.go` — no runtime function needed
- `internal/parser/parser.go` — raw blocks are in the template body, not frontmatter
- `internal/compiler/compiler.go` — transformation is in `TransformTemplate()`, already called
