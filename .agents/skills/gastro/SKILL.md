---
name: gastro
description: Build pages and components with the Gastro framework. Use when creating, editing, or debugging .gastro files, pages, components, routing, SSE handlers, or working with the gastro dev server.
---

## Context

Gastro is a file-based component framework for Go. `.gastro` files combine Go frontmatter with `html/template` markup, compiled to type-safe Go code with automatic file-based routing. Think: Astro's developer experience, Go's type safety, PHP's file-based routing.

Generated code lives in `.gastro/` (gitignored, never hand-edited). The runtime library `pkg/gastro/` is the only dependency of generated code.

## Project Structure

Every Gastro project follows this layout:

```
myapp/
  pages/           # .gastro files that become HTTP routes (optional for component-only projects)
  components/      # reusable .gastro components
  static/          # CSS, images, assets served at /static/
  .gastro/         # generated Go code (gitignored, never edit)
  main.go          # application entry point
  go.mod
```

`pages/` is optional. A project with only `components/` compiles and runs without it — useful when gastro is embedded inside a larger module and used solely for component rendering.

## File Format

A `.gastro` file has two sections separated by `---` delimiters:

```gastro
---
Go frontmatter (runs on the server)
---
HTML template body (rendered with html/template)
```

- **Frontmatter**: Go code. No `package` declaration (the generator handles it). The `gastro` package is implicitly available.
- **Template body**: Standard Go `html/template` syntax.

## Pages

Pages are `.gastro` files in `pages/`. Each page becomes an HTTP route.

### Creating a page

Call `gastro.Context()` in the frontmatter to mark the file as a page and access the HTTP request:

```gastro
---
ctx := gastro.Context()

Title := "Hello"
Name := ctx.Query("name")
---
<h1>{{ .Title }}</h1>
<p>Hello, {{ .Name }}</p>
```

### Static pages

Pages that don't need request access can omit `gastro.Context()`:

```gastro
---
import Layout "components/layout.gastro"

Title := "About"
---
{{ wrap Layout (dict "Title" .Title) }}
    <h1>About</h1>
{{ end }}
```

### Variable visibility

Mirrors Go's export convention:

- **Uppercase** variables (`Title`, `Posts`) are exported to the template as `{{ .Title }}`, `{{ .Posts }}`
- **Lowercase** variables (`err`, `slug`) are private to the frontmatter

```gastro
---
ctx := gastro.Context()
posts, err := db.ListPublished()    // lowercase -> private
if err != nil {
    ctx.Error(500, "Failed to load posts")
    return
}

Posts := posts                       // Uppercase -> {{ .Posts }}
Title := "Blog"                      // Uppercase -> {{ .Title }}
---
<h1>{{ .Title }}</h1>
{{ range .Posts }}
<p>{{ .Title }}</p>
{{ end }}
```

### File-based routing

| File | Route |
|------|-------|
| `pages/index.gastro` | `GET /` |
| `pages/about/index.gastro` | `GET /about` |
| `pages/blog/index.gastro` | `GET /blog` |
| `pages/blog/[slug].gastro` | `GET /blog/{slug}` |
| `pages/[category]/[id].gastro` | `GET /{category}/{id}` |

- `index.gastro` maps to the directory root
- `[param]` becomes `{param}` in Go 1.22+ router patterns
- Only `GET` routes are generated. Register other methods in `main.go`.

### Context API

`gastro.Context()` returns a `*Context` with these methods:

| Method | Description |
|--------|-------------|
| `Request() *http.Request` | The underlying HTTP request |
| `Param(name string) string` | URL path parameter from `[param]` segments |
| `Query(name string) string` | Query string parameter (empty string if missing) |
| `Redirect(url string, code int)` | Send redirect. **Must call `return` after.** |
| `Error(code int, msg string)` | Send error response. **Must call `return` after.** |
| `Header(key, val string)` | Set a response header (before template renders) |

### Error handling

Two layers:

1. **Explicit**: `ctx.Error(code, msg)` + `return` for controlled error responses
2. **Panic recovery**: All handlers are wrapped in `defer gastro.Recover(w, r)` which catches panics and returns 500

## Components

Components are reusable `.gastro` files in `components/`. They accept typed props and can render children.

### Defining a component

Use `gastro.Props()` to declare props. Define a `Props` struct in the same frontmatter:

```gastro
---
type Props struct {
    Title  string
    Author string
}

p := gastro.Props()
Title := p.Title
Author := p.Author
---
<article>
    <h2>{{ .Title }}</h2>
    <p>By {{ .Author }}</p>
</article>
```

### Computed values

When you need derived values from multiple props, assign the whole struct first:

```gastro
---
import "fmt"

type Props struct {
    Label string
    X     int
}

p := gastro.Props()
Label := p.Label
CX := fmt.Sprintf("%d", p.X + 135)
---
<text x="{{ .CX }}">{{ .Label }}</text>
```

### Importing and using components

Import with the `.gastro` file extension to distinguish from Go imports:

```gastro
---
import (
    "myblog/db"

    Layout "components/layout.gastro"
    PostCard "components/post-card.gastro"
)

ctx := gastro.Context()
posts, _ := db.ListPublished()
Posts := posts
---
{{ wrap Layout (dict "Title" "Home") }}
    {{ range .Posts }}
    {{ PostCard (dict "Title" .Title "Slug" .Slug) }}
    {{ end }}
{{ end }}
```

### Leaf vs wrapper components

**Leaf** components (no children) use bare function calls:

```
{{ PostCard (dict "Title" .Title "Slug" .Slug) }}
```

**Wrapper** components (accept children) use `wrap` ... `{{ end }}`:

```
{{ wrap Layout (dict "Title" .Title) }}
    <h1>Hello</h1>
    <p>This becomes the children content.</p>
{{ end }}
```

### Children

Place `{{ .Children }}` where children should render. Only one `{{ .Children }}` per component:

```gastro
---
type Props struct {
    Title string
}

Title := gastro.Props().Title
---
<html>
<head><title>{{ .Title }}</title></head>
<body>
    <nav>...</nav>
    <main>{{ .Children }}</main>
    <footer>...</footer>
</body>
</html>
```

Children are rendered in the **parent's** data context, so they can reference the parent's template variables.

### Prop syntax

Props are passed via `dict` syntax:

| Syntax | Meaning | Example |
|--------|---------|---------|
| `.Expr` | Template expression from parent context | `"Title" .Title` |
| `"literal"` | String literal | `"Title" "About"` |
| `(.Val \| func "arg")` | Pipe expression | `"Date" (.CreatedAt \| timeFormat "Jan 2, 2006")` |

### Type coercion

Gastro coerces prop values to match struct field types:

| Target type | Accepted values |
|-------------|-----------------|
| `string` | Any value (via `fmt.Sprintf`) |
| `bool` | `bool`, `string` (`"true"`, `"false"`) |
| `int` | `int`, `int64`, `float64`, `float32`, `string` (parsed) |
| `float64` | `float64`, `float32`, `int`, `string` (parsed) |

## Template Functions

18 built-in functions available in all templates without registration:

**String**: `upper`, `lower`, `title`, `trim`, `join`, `split`, `contains`, `replace`

**Safety** (bypass html/template escaping -- use only with trusted content): `safeHTML`, `safeAttr`, `safeURL`, `safeCSS`, `safeJS`

**Utility**: `default`, `timeFormat`, `json`, `dict`, `list`

Functions that accept a piped value take it as the **last** parameter (Go template convention).

### Custom helpers

Register in `main.go`:

```go
routes := gastro.Routes(
    gastro.WithFuncs(template.FuncMap{
        "formatEUR": func(cents int) string {
            return fmt.Sprintf("%.2f EUR", float64(cents)/100)
        },
    }),
)
```

## SSE & Real-time

SSE endpoints are plain Go HTTP handlers registered alongside Gastro routes.

### Generic SSE

```go
func handleUpdates(w http.ResponseWriter, r *http.Request) {
    sse := gastro.NewSSE(w, r)

    for {
        select {
        case <-sse.Context().Done():
            return
        case msg := <-updates:
            sse.Send("update", msg)
        }
    }
}
```

### Datastar integration

```go
import "github.com/andrioid/gastro/pkg/gastro/datastar"

func handleIncrement(w http.ResponseWriter, r *http.Request) {
    html, err := gastro.Render.Counter(gastro.CounterProps{Count: 42})
    if err != nil {
        http.Error(w, err.Error(), 500)
        return
    }

    sse := datastar.NewSSE(w, r)
    sse.PatchElements(html)
}
```

### Type-safe Render API

The compiler generates a `Render` method for each component:

```go
html, err := gastro.Render.Counter(gastro.CounterProps{Count: 42})
html, err := gastro.Render.Layout(gastro.LayoutProps{Title: "Home"}, children...)
```

### Wiring in main.go

```go
mux := http.NewServeMux()
mux.HandleFunc("GET /api/increment", handleIncrement)
mux.Handle("/", gastro.Routes())
http.ListenAndServe(":4242", mux)
```

### Datastar page attributes

```gastro
---
Title := "Counter"
---
<div id="count">0</div>
<button data-on:click="@get('/api/increment')">+1</button>
```

## Raw Blocks

Use `{{ raw }}...{{ endraw }}` to display literal template syntax and HTML as visible text. The compiler escapes both template delimiters (`{{`/`}}`) and HTML special characters (`<`, `>`, `&`) so the content is rendered as plain text in the browser.

```gastro
<pre><code>
{{ raw }}
<h1>{{ .Greeting }}</h1>
<p>Hello {{ .Name }}, nice to see you.</p>
{{ endraw }}
</code></pre>
```

The author writes plain code inside `{{ raw }}...{{ endraw }}` and it displays exactly as written. The compiler handles all escaping:

- `{{` → `{{ "{{" }}` (template delimiter escaping)
- `}}` → `{{ "}}" }}` (template delimiter escaping)
- `<` → `&lt;` (HTML entity escaping)
- `>` → `&gt;` (HTML entity escaping)
- `&` → `&amp;` (HTML entity escaping)

Whitespace around `{{ raw }}` and `{{ endraw }}` markers is always trimmed, so raw blocks integrate cleanly into `<pre><code>` without extra blank lines.

For short inline mentions, use inline form: `<code>{{ raw }}{{ .Children }}{{ endraw }}</code>`.

Raw blocks cannot nest. The first `{{ endraw }}` always closes the block.

## Markdown

Use `{{ markdown "path/to/file.md" }}` to inline the rendered HTML of a markdown file into a `.gastro` template. The directive is **expanded at compile time** by `gastro generate`: the file is read, parsed with goldmark (GFM + footnotes), syntax-highlighted with chroma, and baked into the generated template. There is no runtime markdown parsing and no new runtime dependencies.

### Path rules

- `./foo.md` or `../shared/foo.md` — resolved relative to the `.gastro` file's directory. Paths may reach outside the project root (e.g. a shared `docs/` directory).
- `content/foo.md`, `pages/docs/foo.md` — any other path is resolved relative to the project root.
- Absolute paths are rejected.
- The file must have a `.md` extension.

### Usage

```gastro
---
import DocsLayout "components/docs-layout.gastro"

Title := "Getting Started"
---
{{ wrap DocsLayout (dict "Title" .Title) }}
    {{ markdown "./getting-started.md" }}
{{ end }}
```

The argument **must be a string literal** — it is evaluated at compile time, not runtime. Dynamic paths (`{{ markdown .Slug }}`) are not supported.

### Constraints

- Markdown files are **pure content**: no frontmatter, no template interpolation, no component embedding. If you need dynamic data, put it in the `.gastro` shell around the `markdown` call.
- Code fences (and inline `` `code` ``) are automatically protected — any `{{`/`}}` inside them is escaped so `html/template` won't re-parse them. You don't need `{{ raw }}` inside markdown.
- Syntax highlighting uses chroma's `github` theme with classed output. Users must include matching CSS (`gastro new` projects ship with `static/chroma.css`).

### Dev loop

The dev watcher tracks `.md` files anywhere in the project (skipping hidden directories, `node_modules`, `vendor`, `tmp`). Changes trigger a regenerate + reload, same as template edits to a `.gastro` file.

## Static Assets

- Place files in `static/`
- Served at `/static/` URL prefix
- Reference in templates as `/static/styles.css`
- In production: embedded into the binary via `go:embed`
- In dev mode (`GASTRO_DEV=1`): served from disk (changes are live)

## Development Workflow

### `gastro list` — discover components and pages

```sh
gastro list           # aligned table with Props signatures
gastro list --json    # JSON array — use from scripts and agents
```

JSON shape per entry: `{"kind":"component"|"page", "name":"Card", "path":"components/card.gastro", "props":[{"name":"Title","type":"string"}]}`.
`props` is always an array, never null.

### `gastro dev` (primary workflow)

```sh
gastro dev
```

The dev server:
- Watches all `.gastro` files for changes
- Regenerates Go code automatically
- Rebuilds and restarts the server
- **Template body changes**: hot-reloaded without restart
- **Frontmatter / Go code changes**: full rebuild + restart
- **CSS / static asset changes**: live (read from disk, no restart needed)
- Default port: 4242 (override with `PORT` env var)

#### Embedded-package projects

When the server process runs from a directory other than the gastro project root,
set `GASTRO_DEV_ROOT` so gastro can find `.gastro/templates/` and `static/`:

```sh
GASTRO_DEV=1 GASTRO_DEV_ROOT=/path/to/internal/web go run ./cmd/myapp
```

Without it, gastro falls back to cwd and crashes with "no such file or directory"
on the template read. See `docs/dev-mode.md` for a full `mise` task example.

### Production builds

```sh
# One-step build
gastro build
./app

# Or manually:
gastro generate
GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o dist/myapp .
```

### Manual code generation

Only needed outside the dev server (CI, deployment scripts, cross-compilation):

```sh
gastro generate
go build -o myapp .
```

## Rules

1. **Never edit files in `.gastro/`** -- they are generated and will be overwritten.
2. **Use `gastro dev` during development** -- it handles code generation, rebuilding, and restarting automatically.
3. **Always call `return` after `ctx.Redirect()` and `ctx.Error()`** -- otherwise the template will still render.
4. **`Props` struct must be defined in the frontmatter** of the component that calls `gastro.Props()`.
5. **Use `p := gastro.Props()` pattern** -- assign to a variable, then extract fields. Avoid `gastro.Props().Field` chains.
6. **Uppercase = exported, lowercase = private** -- this applies to both frontmatter variables and Props struct fields.
7. **Component imports use `.gastro` extension** -- this is how the compiler distinguishes them from Go package imports.
8. **Template expressions in code examples must be escaped** -- use `{{ raw }}...{{ endraw }}` blocks to output literal template syntax. For legacy/simple cases, `{{ "{{ .Var }}" }}` also works.
9. **Run tests with `-race`** -- always use `go test -race ./...` or `go build -race` to catch data races.
10. **Business logic belongs in Go packages** -- `.gastro` files are consumers, not providers. Keep models, services, and database code in normal `.go` files.
