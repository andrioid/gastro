# Gastro Design Document

Gastro is a file-based component framework for Go. `.gastro` files combine Go
frontmatter with `html/template` markup in a single file. A CLI compiler
(`gastro`) generates type-safe Go code, and a language server (`gastro-lsp`)
provides editor intelligence.

Think: Astro's developer experience, Go's type safety, PHP's file-based routing.

---

## Current Implementation Status

| Phase | Status | Notes |
|-------|--------|-------|
| 1. Parser | Done | 14 tests. Splits frontmatter/body, extracts imports and component imports. |
| 2. Frontmatter codegen | Done | 9 tests. Go AST analysis, variable extraction, gastro marker detection. |
| 3. Template codegen | Done | 12 tests. `{{ Component ... }}` (bare call) / `{{ wrap Component ... }}` to internal template calls, prop parsing, pipe expressions in props, child content extraction into `{{define}}` blocks. |
| 4. Component system | Done | 8 tests. `MapToStruct[T]` with type coercion, component render functions (`func(map[string]any) template.HTML`), per-page init with component FuncMap, `__gastro_render_children` closure for children content. |
| 5. File router | Done | 10 tests. Directory-to-route mapping, `[param]` patterns, func name derivation. |
| 6. Runtime library | Done | 13 tests. Context, DefaultFuncs (18 helpers), Recover. |
| 7. Embedding | Done | `//go:embed` generation wired. Template registry in `routes.go`. Dev/prod FS switching via `GASTRO_DEV`. `WithFuncs` wired. Static assets embedded via copy (Go embed does not follow directory symlinks). |
| 8. CLI: `gastro generate` | Done | 6 tests. Compiler orchestrates parser + codegen + router, writes `.gastro/`. Static file serving detection. |
| 9. CLI: `gastro build` | Done | Runs generate + `go build`. |
| 10. CLI: `gastro dev` | Done | 8 tests. File watcher, debounce, change classification, polling watcher. |
| 11. Tree-sitter grammar | Done | Grammar, highlight queries, injection queries, test corpus. |
| 12. LSP: foundation | Done | Shadow workspace (11 tests), source maps (3 tests), virtual `.go` file generation. |
| 13. LSP: gopls proxy | Done | 16 tests. Hover, completions, diagnostics, and go-to-definition forwarded through gopls with position remapping. Definition results remap all three LSP formats (Location, Location[], LocationLink[]). |
| 14. LSP: template intelligence | Done | 64 tests. Variable/component/function completions, diagnostics for unknown vars/components, component prop validation (missing/unknown props), component signature hover, component go-to-definition. |
| 15. Editor extensions | Done | VS Code extension (TextMate grammar + LSP client), Neovim plugin (tree-sitter + LSP). |
| 16. Example: blog | Done | Working 4-page blog with Layout and PostCard components. Compiles and serves on port 4242. |

**Test count:** 232 passing.

---

## Implementation Guidelines

### Testing

- **Every behaviour must be covered by a unit test.** No feature is complete
  without corresponding test coverage.
- **Tests are for behaviour, not implementation specifics.** Tests verify what
  the code does from the outside (given this input, expect this output), not how
  it does it internally. If an internal refactor doesn't change observable
  behaviour, no tests should break.
- **Red/green (TDD) approach is mandatory.** For every feature: write a failing
  test first, then write the minimum code to make it pass, then refactor. No
  exceptions.
- **Table-driven tests** for parsers and transformers: each test case is a
  struct with input and expected output. Easy to add new cases.
- **Golden files** for code generation: store expected `.go` output in
  `testdata/` directories. Compare generated output against golden files.
  Update with a `-update` flag.
- **Fixture projects** for CLI integration tests: small `.gastro` projects in
  `testdata/` that exercise specific features.
- **`testdata/` directories** live next to the package they test (Go
  convention).

#### What to Test by Phase

| Phase | What to Test | Approach |
|-------|-------------|----------|
| 1. Parser | Frontmatter extraction, `---` delimiter handling, component import declarations, edge cases (empty frontmatter, missing delimiters, `---` inside strings) | Table-driven: input `.gastro` string -> expected frontmatter + template body |
| 2. Frontmatter codegen | Import extraction (Go and component), uppercase/lowercase variable separation, struct pointer detection, generated Go code output | Table-driven: frontmatter string -> expected AST results. Golden files: frontmatter -> generated `.go` code |
| 3. Template codegen | `{{ Component ... }}` (bare call) / `{{ wrap Component ... }}` transformation, prop parsing via `(dict ...)`, passthrough of `{{ }}` | Table-driven: template body input -> expected transformed template output |
| 4. Component system | Props struct detection, `mapToStruct[T]()` coercion (string->bool, string->int, type mismatches), render function generation | Unit tests for `mapToStruct` with all coercion paths. Golden files for generated component code |
| 5. File router | Directory walking, route table generation, `[param]` pattern mapping, `index.gastro` handling, route ordering | Table-driven: directory tree structure -> expected route table |
| 6. Runtime library | `Context` methods, `DefaultFuncs()` behaviour (each built-in helper), `WithFuncs()` override semantics, `Recover` panic handling | Standard unit tests per function. Integration test: full request cycle through a generated handler |
| 7. Embedding | FS abstraction (dev vs prod mode), static file serving, template loading from embedded FS | Test both `GASTRO_DEV=1` (disk) and production (embedded) paths |
| 8-10. CLI | `generate` produces expected output, `build` produces a binary, `dev` detects file changes | Integration tests: run CLI against fixture projects, verify output |
| 11. Tree-sitter | Grammar correctness: parse sample `.gastro` files, verify AST structure | Tree-sitter's built-in `corpus/` test framework |
| 12-14. LSP | Shadow file generation, position mapping, gopls proxy round-trips, completion results, diagnostic output | Unit tests for source mapping. Integration tests: send LSP protocol messages, verify responses |

### Code Style

- Write idiomatic Go.
- Prefer functional-style composition over inheritance.
- Return early, avoid nested if/else chains.
- Comments explain why, not what.
- Dead code must be deleted.

### LSP Analysis

- **Prefer AST over regex** for template analysis. Go's `text/template/parse`
  package provides a full AST that handles nesting, quoting, and edge cases
  correctly. Regex is fragile against nested parentheses, string escapes, and
  template comments.
- **Regex fallbacks** are acceptable when the AST is unavailable (e.g. during
  editing when the template is syntactically incomplete). The pattern is:
  AST-based primary path, regex-based fallback when `ParseTemplateBody` fails.

---

## Table of Contents

1. [File Format](#1-file-format)
2. [Project Structure](#2-project-structure)
3. [Pages and Components](#3-pages-and-components)
4. [Variable Visibility](#4-variable-visibility)
5. [Component Imports and Invocation](#5-component-imports-and-invocation)
6. [Children](#6-children)
7. [Template Helpers (FuncMap)](#7-template-helpers-funcmap)
8. [Named Templates](#8-named-templates)
9. [Runtime API](#9-runtime-api)
10. [File-Based Routing](#10-file-based-routing)
11. [Code Generation Pipeline](#11-code-generation-pipeline)
12. [Embedding and Deployment](#12-embedding-and-deployment)
13. [CLI](#13-cli)
14. [Tree-sitter Grammar](#14-tree-sitter-grammar)
15. [Language Server (LSP)](#15-language-server-lsp)
16. [Resolved Design Decisions](#16-resolved-design-decisions)
17. [Implementation Phases](#17-implementation-phases)
18. [Repository Structure](#18-repository-structure)

---

## 1. File Format

A `.gastro` file has two sections separated by `---` delimiters:

```
---
<Go frontmatter: imports, logic, variables>
---
<HTML template body>
```

**Frontmatter rules:**

- No `package` declaration -- the code generator handles this.
- Imports use standard Go `import` syntax.
- The `gastro` package is implicitly available (never needs importing).
- Component imports use `import ComponentName "path/to/component.gastro"`
  (distinguished from Go imports by the `.gastro` file extension).
- **Uppercase** local variables are exported to the template.
- **Lowercase** local variables are private to the frontmatter logic.
- `gastro.Context()` and `gastro.Props()` are code-gen markers, not real
  function calls. The compiler rewrites them into the generated handler code.

**Template body rules:**

- Uses standard Go `html/template` syntax (`{{ }}`).
- `{{ Component (dict ...) }}` (bare calls) and `{{ wrap Component (dict ...) }}...{{ end }}` invoke components (imported via `import`).
- `{{ .Children }}` renders child content passed by a parent component.
- No custom expression shorthand -- `{{ }}` only.
- `{{define}}` / `{{template}}` are supported but scoped to the file.

---

## 2. Project Structure

```
myapp/
  pages/                       <- file-based routing (fixed location)
    index.gastro               -> GET /
    about/
      index.gastro             -> GET /about
    blog/
      index.gastro             -> GET /blog
      [slug].gastro            -> GET /blog/{slug}
  components/                  <- reusable components
    card.gastro
    header.gastro
    layout.gastro
  static/                      <- static assets, embedded into binary
    styles.css
    favicon.ico
    images/
      logo.png
  .gastro/                     <- generated code (gitignored)
    routes.go
    embed.go
    templates/
      page_index.html
      component_card.html
    pages/
      *.go
    components/
      *.go
  main.go
  go.mod
```

**Fixed conventions:**

- `pages/` -- file-based routing root.
- `components/` -- reusable component directory.
- `static/` -- static assets, served at the `/static/` URL prefix. Embedded
  into the binary in production via `//go:embed`.
- `.gastro/` -- generated output (gitignored, never hand-edited).

---

## 3. Pages and Components

### Pages (live in `pages/`)

Pages handle HTTP requests. They have access to `gastro.Context()`.

```gastro
// pages/blog/[slug].gastro
---
import (
    "myapp/db"

    Layout "components/layout.gastro"
)

ctx := gastro.Context()
slug := ctx.Param("slug")

post, err := db.GetPost(slug)
if err != nil {
    ctx.Error(404, "Post not found")
    return
}

Title := post.Title
Body  := post.Body
Author := post.Author
---
{{ wrap Layout (dict "Title" .Title) }}
    <article>
        <h1>{{ .Title }}</h1>
        <p class="author">By {{ .Author }}</p>
        <div>{{ .Body }}</div>
    </article>
{{ end }}
```

### Components (live in `components/` or anywhere)

Components receive typed props via `gastro.Props()`. They do not have access
to `gastro.Context()`.

```gastro
// components/card.gastro
---
type Props struct {
    Title  string
    Body   string
    Urgent bool
}

p := gastro.Props()

CSSClass := "card"
if p.Urgent {
    CSSClass += " card--urgent"
}
Title := p.Title
Body  := p.Body
---
<div class="{{ .CSSClass }}">
    <h2>{{ .Title }}</h2>
    <p>{{ .Body }}</p>
    {{ .Children }}
</div>
```

---

## 4. Variable Visibility

Mirrors Go's own export rule: **uppercase = exported to template, lowercase =
private to frontmatter**.

```gastro
---
ctx := gastro.Context()       // lowercase -> private
err := doSomething()          // lowercase -> private
temp := compute()             // lowercase -> private

Title := "Hello"              // Uppercase -> {{ .Title }}
Items := fetchItems()         // Uppercase -> {{ .Items }}
---
```

The code generator parses the frontmatter AST, identifies all variable
declarations, and captures only uppercase variables into the template data map:

```go
__data := map[string]any{
    "Title": Title,
    "Items": Items,
}
```

For struct-typed variables, the code generator stores **pointers** in the data
map to ensure pointer-receiver methods are accessible in the template.

---

## 5. Component Imports and Invocation

### Importing Components

Components must be explicitly imported in the frontmatter using `import`
with a `.gastro` path:

```gastro
---
import (
    Card "components/card.gastro"
    Layout "components/layout.gastro"
    PostCard "components/blog/post-card.gastro"
)
---
```

- Explicit imports avoid ambiguity from name collisions.
- Component imports are distinguished from Go imports by the `.gastro` file
  extension.
- The LSP converts component import lines to comments in the virtual .go
  file sent to gopls, preserving line numbers.

### Invoking Components

Components are invoked with Go template actions:

```
{{ Card (dict "Title" .post.Title "Body" .post.Summary "Urgent" .post.IsHot) }}
```

**Prop passing syntax:**

- `.expr` -- Go template expression evaluated in the parent's data context.
- `"literal"` -- string literal.
- Component name must match an imported component name.
- Props use `(dict "Key" value ...)` syntax.

**Prop type coercion:** Component props are passed via `dict` (producing
`map[string]any`) and converted to the typed `Props` struct at runtime using
reflection. Type coercion rules:

- `string -> string`: direct.
- `string -> bool`: "true"/"false" parsing.
- `string -> int/float`: strconv parsing.
- Matching types: no coercion needed.
- Struct/slice/map: passed by reference.
- Mismatch: runtime error with a clear message naming the component, prop,
  expected type, and received type.

The compiler can also emit **compile-time warnings** for obvious type mismatches
by analyzing the frontmatter AST.

Leaf components use bare function calls, which are already valid Go template
syntax — no transformation is needed:

```
{{ Card (dict "Title" .Name) }}
```

The compiler registers the component function in the FuncMap under its
PascalCase name (`"Card"`).

---

## 6. Children

Components accept children via `{{ .Children }}`. Child content is pre-rendered to
`template.HTML` in the **parent's** data context, then passed to the component
as a special `__children` prop.

**Usage:**

```gastro
// components/layout.gastro
---
type Props struct {
    Title string
}
Title := gastro.Props().Title
---
<html>
<head><title>{{ .Title }}</title></head>
<body>
    {{ .Children }}
</body>
</html>
```

**Caller:**

```
{{ wrap Layout (dict "Title" .Title) }}
    <p>{{ .Greeting }}</p>
{{ end }}
```

**Implementation:**

The compiler transforms this to:

```
{{ __gastro_Layout (dict "Title" .Title "__children" (__gastro_render_children "layout_caller_children" .)) }}
```

Where `__gastro_render_children` executes a sub-template with the parent's data
context and returns rendered HTML. Inside the component, `{{ .Children }}`
outputs the rendered HTML (`template.HTML`, safe, not escaped).

**Implication:** Children content is opaque HTML. The child component cannot inspect
or manipulate it -- it can only place it. This matches Astro's behavior.

Only unnamed children are supported for v1. Named children may be added later.

---

## 7. Template Helpers (FuncMap)

### Built-in Defaults

Gastro ships with a default set of template functions:

| Function     | Example                                 | Description              |
|--------------|-----------------------------------------|--------------------------|
| `upper`      | `{{ .Name \| upper }}`                  | `strings.ToUpper`        |
| `lower`      | `{{ .Name \| lower }}`                  | `strings.ToLower`        |
| `title`      | `{{ .Name \| title }}`                  | Capitalize words         |
| `trim`       | `{{ .Bio \| trim }}`                    | `strings.TrimSpace`      |
| `contains`   | `{{ if contains .Tags "go" }}`          | `strings.Contains`       |
| `replace`    | `{{ .Text \| replace "old" "new" }}`    | `strings.ReplaceAll`     |
| `join`       | `{{ .Items \| join ", " }}`             | `strings.Join`           |
| `split`      | `{{ .CSV \| split "," }}`               | `strings.Split`          |
| `safeHTML`   | `{{ .Rich \| safeHTML }}`               | Cast to `template.HTML`  |
| `safeAttr`   | `{{ .Attr \| safeAttr }}`               | Cast to `template.HTMLAttr` |
| `safeURL`    | `{{ .Link \| safeURL }}`                | Cast to `template.URL`   |
| `safeCSS`    | `{{ .Style \| safeCSS }}`               | Cast to `template.CSS`   |
| `safeJS`     | `{{ .Code \| safeJS }}`                 | Cast to `template.JS`    |
| `dict`       | `{{ dict "k1" .V1 "k2" .V2 }}`         | Build `map[string]any`   |
| `list`       | `{{ list 1 2 3 }}`                      | Build `[]any`            |
| `default`    | `{{ .Name \| default "Anonymous" }}`    | Fallback if empty/zero   |
| `json`       | `{{ .Data \| json }}`                   | `json.Marshal` to string |
| `timeFormat` | `{{ .At \| timeFormat "Jan 2, 2006" }}` | `time.Time.Format`       |

### User Registration

Users register additional helpers (or override defaults) via `main.go`:

```go
routes := gastro.Routes(
    gastro.WithFuncs(template.FuncMap{
        "formatEUR": func(cents int) string {
            return fmt.Sprintf("%.2f EUR", float64(cents)/100)
        },
    }),
)
```

User-provided functions override built-ins with the same name.

---

## 8. Named Templates

Go's `{{define}}` and `{{template}}` actions are supported within `.gastro`
files. Named templates are **scoped to the file they are defined in** -- they
are not visible to other `.gastro` files.

```gastro
---
Items := []string{"one", "two", "three"}
---
{{define "list-item"}}
    <li class="item">{{ . }}</li>
{{end}}

<ul>
{{ range .Items }}
    {{ template "list-item" . }}
{{ end }}
</ul>
```

For shared reusable fragments across files, use components (with explicit
`import` declarations).

---

## 9. Runtime API

The `gastro` runtime package provides the API used in frontmatter code:

```go
// Pages only -- access the HTTP request
func Context() *Context

type Context struct{}
func (c *Context) Request() *http.Request
func (c *Context) Param(name string) string     // URL path parameters
func (c *Context) Query(name string) string     // Query string parameters
func (c *Context) Redirect(url string, code int)
func (c *Context) Error(code int, msg string)
func (c *Context) Header(key, val string)

// Components only -- receive typed props
func Props() Props
```

**Important:** `gastro.Context()` and `gastro.Props()` are **code-gen
markers**. They look like function calls but are rewritten by the compiler into
the generated handler code. They are not callable from normal `.go` files.

### Error Handling

- `ctx.Error(code, msg)` calls `http.Error(w, msg, code)` -- plain text. It
  does **not** stop execution. The user **must** call `return` after
  `ctx.Error()`.
- The LSP can lint for `ctx.Error()` not followed by `return` as a warning.
- Generated handlers are wrapped in panic recovery:
  `defer gastro.Recover(w, r)` catches panics, logs them, and returns 500.
- Custom error pages and recovery middleware can be provided by wrapping
  `gastro.Routes()`. See [compression.md](compression.md) for response
  compression middleware examples.

### Route Configuration

```go
func Routes(opts ...Option) http.Handler

type Option func(*config)

func WithFuncs(fm template.FuncMap) Option  // register template helpers
```

---

## 10. File-Based Routing

| File                          | Route               |
|-------------------------------|----------------------|
| `pages/index.gastro`          | `GET /`              |
| `pages/about/index.gastro`    | `GET /about`         |
| `pages/blog/index.gastro`     | `GET /blog`          |
| `pages/blog/[slug].gastro`    | `GET /blog/{slug}`   |

- `[param]` in filenames maps to `{param}` URL patterns (Go 1.22+).
- Only `GET` is supported for v1. Other HTTP methods should be handled via
  normal Go handlers wired in `main.go`.

Generated router (`.gastro/routes.go`):

```go
package gastro

import (
    "io/fs"
    "net/http"
)

func Routes(opts ...Option) http.Handler {
    cfg := defaultConfig()
    for _, opt := range opts {
        opt(&cfg)
    }

    mux := http.NewServeMux()

    // Page routes
    mux.HandleFunc("GET /", pageIndex)
    mux.HandleFunc("GET /about", pageAboutIndex)
    mux.HandleFunc("GET /blog", pageBlogIndex)
    mux.HandleFunc("GET /blog/{slug}", pageBlogSlug)

    // Static assets from static/
    staticFS, _ := fs.Sub(staticAssetFS, "static")
    mux.Handle("GET /static/",
        http.StripPrefix("/static/", http.FileServerFS(staticFS)))

    return mux
}
```

User's `main.go`:

```go
package main

import (
    "fmt"
    "net/http"
    "os"

    gastro "myapp/.gastro"
)

func main() {
    port := os.Getenv("PORT")
    if port == "" {
        port = "4242"
    }
    fmt.Printf("Listening on :%s\n", port)
    http.ListenAndServe(":"+port, gastro.Routes())
}
```

---

## 11. Code Generation Pipeline

```
.gastro file
    |
    +- 1. Parse: split at --- delimiters -> frontmatter + template body
    |
    +- 2. Analyze frontmatter (Go AST):
    |      - Collect import declarations
     |      - Collect component imports (`.gastro` paths)
     |      - Identify all local variable declarations
     |      - Separate uppercase (exported) from lowercase (private)
     |      - Detect Props struct (components) or Context() call (pages)
    |
    +- 3. Analyze template body:
     |      - Bare `{{ Component (dict ...) }}` calls pass through unchanged
     |      - Transform `{{ wrap Component ... }}...{{ end }}` into function call + {{define}} blocks
     |      - Register component functions in FuncMap under PascalCase names
     |      - Pass through {{ .Children }} and other {{ }} expressions unchanged
    |
    +- 4. Generate Go source:
    |      - Wrap frontmatter in handler func (pages) or render func (components)
    |      - Build data map from uppercase variables (pointers for structs)
    |      - Embed compiled html/template
    |      - Register component template functions
    |
    +- 5. Generate router (pages only):
           - Walk pages/ directory tree
           - Map file paths to HTTP routes
           - [param] -> {param} pattern variables
           - index.gastro -> directory route
           - Write Routes() function with static asset serving
```

---

## 12. Embedding and Deployment

The generated code uses `//go:embed` to bake templates and static assets into
the binary:

```go
// .gastro/embed.go (generated)
package gastro

import "embed"

//go:embed templates/*
var templateFS embed.FS

//go:embed static/*
var staticAssetFS embed.FS
```

### Dev vs Production

```go
func getTemplateFS() fs.FS {
    if os.Getenv("GASTRO_DEV") == "1" {
        return os.DirFS(".gastro/templates")
    }
    return templateFS
}

func getStaticFS() fs.FS {
    if os.Getenv("GASTRO_DEV") == "1" {
        return os.DirFS("static")
    }
    sub, _ := fs.Sub(staticAssetFS, "static")
    return sub
}
```

- `gastro dev` sets `GASTRO_DEV=1` -- templates and assets are read from disk
  (enabling live reload for template-only changes).
- `gastro build` produces a binary using the embedded FS.
- Templates are embedded as raw strings and parsed at startup.

### Build Output

`gastro build` runs `gastro generate` followed by `go build`, producing a
single self-contained binary. Output name defaults to the Go module name.

**Requirement:** `gastro generate` must run before `go build`. This is standard
for code-generation projects (like protobuf, sqlc, ent). CI/CD pipelines must
include this step.

---

## 13. CLI

```
gastro generate    Compile all .gastro files -> .gastro/ directory
gastro build       gastro generate + go build -> single deployable binary
gastro dev         Watch mode: regenerate on change, run Go server
```

**Future:** `gastro new` -- scaffold a new gastro project (not yet implemented).

### `gastro dev` Behavior

- **Port:** Default `4242`. Overridden by the `PORT` environment variable.
- **Template body changes:** Reload from disk FS (no process restart).
- **Frontmatter changes:** Regenerate code, recompile, restart Go process.
- Sets `GASTRO_DEV=1` so templates are read from disk.

### `gastro build` Behavior

Runs `gastro generate` followed by `go build`, producing a single
self-contained binary. Output name defaults to the Go module name.

For custom build flags (cross-compilation, ldflags, CGO, etc.), users run
the steps separately:

```sh
gastro generate
GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o ./dist/myblog .
```

There is no configuration file. Every gastro project is a Go project -- Go's
own tooling handles build customisation.

---

## 14. Tree-sitter Grammar

The `tree-sitter-gastro` grammar handles the mixed-language structure with
language injection:

```
document
  frontmatter_delimiter   ---
  frontmatter             -> injects tree-sitter-go
  frontmatter_delimiter   ---
  template_body           -> injects tree-sitter-html
    template_expression   {{ ... }}
    component_call        {{ Component (dict ...) }}
    component_wrap        {{ wrap Component (dict ...) }} ... {{ end }}
```

Tree-sitter is prioritized first, providing syntax highlighting in Neovim, Zed,
Helix, and increasingly VS Code.

---

## 15. Language Server (LSP)

### Binary

Separate binary: `gastro-lsp`. Versioned and distributed independently from the
CLI.

### Architecture: gopls Proxy Model

```
Editor
  |
  v
gastro-lsp
  +-- Shadow workspace: extracts frontmatter -> virtual .go files
  +-- Proxies Go requests to gopls
  |     (completions, diagnostics, hover, go-to-def)
  +-- Position mapping: .gastro line <-> virtual .go line
  +-- Component imports: commented out in virtual file, preserving line numbers
  +-- Template intelligence (own logic):
  |     - {{ .Var }} completions (from uppercase frontmatter vars)
  |     - {{ .Var.Field }} completions (via gopls type analysis)
  |     - {{ func }} / {{ | func }} completions (from FuncMap registry)
  |     - Type-aware hover on {{ }} expressions
  |     - Component name completions (from component imports)
  |     - Component prop completions (from Props struct)
  |     - Component go-to-definition
  |     - Diagnostics: unknown vars, unknown components, wrong props
  +-- HTML: relies on editor built-in + tree-sitter (v1)
            Go-native HTML completions on the roadmap
```

### Virtual Go File Generation

When a `.gastro` file is opened, the LSP:

1. Parses `---` delimiters.
2. Extracts frontmatter.
3. Converts component import lines to comments (preserving line numbers).
4. Wraps the frontmatter in a valid Go function with package, imports, and
   function signature.
5. Writes to shadow directory (e.g. `/tmp/gastro-lsp-shadow/`).
6. Sends to gopls for analysis.

Position mapping (source maps) translates between `.gastro` line numbers and
virtual `.go` line numbers.

### Feature Matrix

| Feature                          | Region            | Source               |
|----------------------------------|-------------------|----------------------|
| Go completions                   | Frontmatter       | gopls proxy          |
| Go diagnostics                   | Frontmatter       | gopls proxy          |
| Go hover                         | Frontmatter       | gopls proxy          |
| Go go-to-definition              | Frontmatter       | gopls proxy          |
| HTML completions                 | Template body     | Editor built-in (v1) |
| `{{ .Var }}` completions         | Template exprs    | gastro-lsp (AST)     |
| `{{ .Var.Field }}` completions   | Template exprs    | gastro-lsp via gopls |
| `{{ func }}` / pipe completions  | Template exprs    | gastro-lsp (FuncMap) |
| Type-aware hover on `{{ }}`      | Template exprs    | gastro-lsp via gopls |
| Component name completions       | `{{ Component/wrap }}` | gastro-lsp           |
| Component prop completions       | `{{ Component/wrap }}` | gastro-lsp           |
| Component go-to-definition       | `{{ Component/wrap }}` | gastro-lsp           |
| Unknown variable diagnostic      | Template exprs    | gastro-lsp           |
| Unknown component diagnostic     | `{{ Component/wrap }}` | gastro-lsp           |
| Missing/wrong prop diagnostic    | `{{ Component/wrap }}` | gastro-lsp           |
| Component import completions     | Frontmatter       | gastro-lsp           |

---

## 16. Resolved Design Decisions

| #  | Topic                     | Decision                                                                 |
|----|---------------------------|--------------------------------------------------------------------------|
| 1  | Template engine           | Go `html/template` (standard library)                                    |
| 2  | Data contract             | Frontmatter locals, uppercase exported to template                       |
| 3  | Generated code location   | `.gastro/` hidden directory (gitignored)                                 |
| 4  | Routing                   | Auto-generated file router from `pages/`                                 |
| 5  | HTTP framework            | `net/http` (Go 1.22+)                                                    |
| 6  | Expression syntax         | `{{ }}` only, no shorthand sugar                                         |
| 7  | Prop expression type      | Go template expressions via `(dict "Key" .Value ...)` syntax            |
| 8  | Component resolution      | Explicit `import` with `.gastro` paths in frontmatter                    |
| 9  | Children                  | `{{ .Children }}` unnamed only (v1). Pre-rendered to `template.HTML`     |
| 10 | Package declaration       | None. Code generator handles it                                          |
| 11 | Frontmatter validity      | Code-gen markers (not independently compilable Go)                       |
| 12 | Prop type coercion        | Runtime reflection via `mapToStruct[T]()`                                |
| 13 | Error handling            | `ctx.Error()` + manual `return`. Panic recovery via `gastro.Recover`     |
| 14 | Pointer receivers         | Code generator stores struct pointers in data map                        |
| 15 | HTTP methods              | GET only (v1). Other methods in normal Go handlers                       |
| 16 | Dev restart strategy      | Hybrid: disk FS for templates, restart for frontmatter                   |
| 17 | Named template scope      | File-local only                                                          |
| 18 | Template helpers          | Runtime registration via `gastro.WithFuncs()` + built-in defaults        |
| 19 | LSP Go intelligence       | Proxy to gopls via shadow workspace                                      |
| 20 | LSP HTML intelligence     | Editor built-in (v1), Go-native on the roadmap                           |
| 21 | LSP template intelligence | Own logic: variable/function/component completions, type-aware hover     |
| 22 | LSP binary                | Separate `gastro-lsp` binary                                             |
| 23 | Syntax highlighting       | Tree-sitter first, with Go/HTML language injection                       |
| 24 | Static assets             | `static/` directory, embedded via `//go:embed`                           |
| 25 | Build output              | `gastro build` = generate + `go build` -> single binary                  |
| 26 | Component import + LSP    | Component imports commented out in gopls virtual file, handled by gastro-lsp |

---

## 17. Implementation Phases

| Phase | Deliverable                    | Description                                                                                                                                         |
|-------|--------------------------------|-----------------------------------------------------------------------------------------------------------------------------------------------------|
| 1     | **Parser**                     | `.gastro` file parser: split frontmatter from template body. Handle `---` delimiters, component import declarations, edge cases.                     |
| 2     | **Frontmatter codegen**        | Go AST analysis: extract imports (Go and component), uppercase variable capture. Generate handler functions with data maps. Store struct pointers.   |
| 3     | **Template codegen**           | Parse template body: bare `{{ Component (dict ...) }}` calls pass through unchanged; transform `{{ wrap Component ... }}` actions into function call + `{{define}}` blocks. Components registered in FuncMap under PascalCase names. Pipe expressions in props wrapped in parens. |
| 4     | **Component system**           | Props struct detection, `gastro.Props()` codegen, `MapToStruct[T]` runtime helper, component render functions, per-page init with component FuncMap registration, `__gastro_render_children` closure for children content. End-to-end working. |
| 5     | **File router**                | Walk `pages/`, generate route table, handle `[param]` patterns, generate `Routes()` function with options.                                          |
| 6     | **Runtime library**            | `gastro` package: `Context`, `Props`, `Recover`, `DefaultFuncs()`, `WithFuncs()` option, dev/prod FS abstraction.                                   |
| 7     | **Embedding & static assets**  | `//go:embed` for templates and `static/`, `fs.FS` abstraction, static file serving, dev vs prod mode.                                               |
| 8     | **CLI: `gastro generate`**     | One-shot code generation to `.gastro/`.                                                                                                             |
| 9     | **CLI: `gastro build`**        | Generate + `go build` -> single deployable binary. Configurable output.                                                                             |
| 10    | **CLI: `gastro dev`**          | File watcher, hybrid restart (template reload vs process restart), `GASTRO_DEV=1`.                                                                  |
| 11    | **Tree-sitter grammar**        | `tree-sitter-gastro` with Go and HTML language injection. Highlights and injection queries.                                                          |
| 12    | **LSP: foundation**            | `gastro-lsp` binary. Document sync, `.gastro` file parsing, shadow workspace with virtual `.go` files, position mapping (source maps).               |
| 13    | **LSP: gopls proxy**           | Spawn and manage gopls subprocess. Proxy completions, diagnostics, hover, go-to-def for frontmatter. Translate positions. Handle component import commenting. |
| 14    | **LSP: template intelligence** | Variable/field/function completions, pipe completions, type-aware hover, component name/prop completions, component go-to-def, diagnostics.         |
| 15    | **Editor extensions**          | Neovim plugin (tree-sitter + LSP config), VS Code extension (tree-sitter via WASM + LSP client).                                                    |
| 16    | **Example: blog**              | `examples/blog/` -- a complete working blog project demonstrating pages, dynamic routes, template helpers, and static assets.                        |

---

## 18. Repository Structure

```
gastro/
  cmd/
    gastro/                <- CLI binary (generate, build, dev)
      main.go
    gastro-lsp/            <- LSP binary (gopls proxy + template intelligence)
      main.go
      lsp_integration_test.go
  internal/
    parser/                <- .gastro file parser
    codegen/               <- Go code generation (analyze, generate, template)
    router/                <- file-based route generation
    compiler/              <- orchestrates parser + codegen + router
    watcher/               <- file watching for dev mode
    lsp/
      proxy/               <- gopls subprocess management + JSON-RPC forwarding
      shadow/              <- virtual .go file generation + shadow workspace
      sourcemap/           <- position mapping between .gastro and virtual .go
      template/            <- template body completions + diagnostics
  pkg/
    gastro/                <- runtime library (Context, Props, FuncMap, Recover)
  tree-sitter-gastro/      <- tree-sitter grammar
    grammar.js
    queries/
      highlights.scm
      injections.scm
    test/corpus/
  editors/
    vscode/                <- VS Code extension
    neovim/                <- Neovim plugin
  examples/
    blog/                  <- complete working blog example
      pages/
        index.gastro
        about/
          index.gastro
        blog/
          index.gastro
          [slug].gastro
      static/
        styles.css
      db/
        posts.go
      main.go
      go.mod
  mise.toml                <- tooling (go, gopls)
  go.mod
  go.sum
```

---

## Interfacing with Existing Code

Gastro files are **consumers, not providers**. They import from normal Go
packages but never export to them. All business logic, models, database code,
and services live in normal `.go` files.

```
main.go --imports--> .gastro/  (generated: routes, handlers)
                        |
                        | imports
                        v
                   your packages (models/, db/, services/, lib/)
                        |
                        | imports
                        v
                   third-party (go.mod dependencies)
```

Standard Go imports work in frontmatter:

```gastro
---
import "myapp/db"
import "myapp/services/auth"
import "github.com/yuin/goldmark"
---
```

Utility functions that accept `*gastro.Context` can be written in normal Go
packages and called from page frontmatter for shared patterns (e.g.,
authentication guards).

---

## 20. Future Considerations

- **Page and component template unification (evaluated, rejected).** Considered
  merging `handlerTmpl` and `componentTmpl` into a single template with
  conditionals. Rejected because pages stream directly to `http.ResponseWriter`
  via `Execute(w, ...)` while components buffer into `bytes.Buffer` and return
  `template.HTML`. The streaming model is more efficient for HTTP handlers and
  follows Go's `http.Handler` conventions. The shared code (~10 lines: frontmatter,
  data map, template execution) is minimal and acceptable duplication (WET). A
  unified template would force pages to buffer unnecessarily and add conditional
  complexity without meaningful benefit. Instead, both templates now handle
  `Execute` errors (previously silently discarded).
