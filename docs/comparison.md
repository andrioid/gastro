# Comparison

Gastro is one of several approaches to building HTML with Go. This page compares Gastro with [Templ](https://templ.guide/), [gomponents](https://www.gomponents.com/), [htmgo](https://htmgo.dev/), and Go's standard `html/template` package. Each has real strengths — pick the one that fits your project.

## At a Glance

| | Gastro | Templ | gomponents | htmgo | html/template |
|---|--------|-------|------------|-------|---------------|
| **Syntax** | Go frontmatter + html/template markup in `.gastro` files | Custom DSL in `.templ` files, compiled to Go | Pure Go function calls with dot-import convention | Pure Go function calls, htmx-oriented | Go template directives in `.html` files |
| **Type Safety** | Compile-time (generated Go code, typed props) | Compile-time (generated Go code, typed params) | Compile-time (native Go types) | Compile-time (native Go types) | Runtime only (interface{} data) |
| **Routing** | File-based (automatic) | Manual (wire your own router) | Manual (wire your own router) | File-based (automatic) | Manual (wire your own router) |
| **Code Generation** | Yes (`gastro generate`) | Yes (`templ generate`) | No (pure Go) | Yes (route registration) | No |
| **Dev Server** | Built-in (`gastro dev`) with hot reload | Watch mode + external tools (e.g. Air) | External tools (e.g. Air) | Built-in with live reload | External tools |
| **Single Binary** | Yes (embedded assets) | Yes (templates compiled into Go) | Yes (no templates to embed) | Yes (embedded assets) | Yes (with go:embed) |
| **Interactivity** | SSE + Datastar (built-in) | BYO (commonly HTMX or Datastar) | BYO (commonly HTMX) | HTMX (built-in) | BYO |
| **IDE Support** | Go + HTML (standard tooling) | VSCode & GoLand plugins, LSP | Full Go tooling (native Go code) | Full Go tooling (native Go code) | Basic template highlighting |

## Syntax & Developer Experience

The biggest difference between these tools is how you write HTML. Here's the same component — a greeting card — in each approach.

### Gastro

A `.gastro` file with Go frontmatter and html/template markup. If you know HTML and Go, you can read it immediately:

```gastro
---
type Props struct {
    Name    string
    IsAdmin bool
}

p := gastro.Props()
Name := p.Name
IsAdmin := p.IsAdmin
---
<div class="greeting-card">
    <h2>Hello, {{ .Name }}</h2>
    {{ if .IsAdmin }}
        <span class="badge">Admin</span>
    {{ end }}
</div>
```

### Templ

A custom DSL in `.templ` files. Looks like Go with embedded HTML, compiled to Go code:

```go
templ GreetingCard(name string, isAdmin bool) {
    <div class="greeting-card">
        <h2>Hello, { name }</h2>
        if isAdmin {
            <span class="badge">Admin</span>
        }
    </div>
}
```

### gomponents

Pure Go functions that build an HTML node tree. No templates, no code generation:

```go
func GreetingCard(name string, isAdmin bool) g.Node {
    return Div(Class("greeting-card"),
        H2(g.Textf("Hello, %s", name)),
        g.If(isAdmin,
            Span(Class("badge"), g.Text("Admin")),
        ),
    )
}
```

### htmgo

Pure Go functions similar to gomponents, designed around htmx patterns:

```go
func GreetingCard(name string, isAdmin bool) *h.Element {
    return h.Div(
        h.Class("greeting-card"),
        h.H2(h.TextF("Hello, %s", name)),
        h.Iff(isAdmin, func() *h.Element {
            return h.Span(h.Class("badge"), h.Text("Admin"))
        }),
    )
}
```

### html/template

Go's standard library. Templates are parsed at runtime from strings or files:

```go
// Template file: greeting-card.html
<div class="greeting-card">
    <h2>Hello, {{.Name}}</h2>
    {{if .IsAdmin}}
        <span class="badge">Admin</span>
    {{end}}
</div>

// Go code to render:
type GreetingData struct {
    Name    string
    IsAdmin bool
}
tmpl.ExecuteTemplate(w, "greeting-card.html", GreetingData{
    Name: "Alice", IsAdmin: true,
})
```

## Type Safety

Type safety determines when you catch errors — at compile time or at runtime.

- **Gastro** generates Go code with typed `Props` structs. If you pass the wrong type to a component, the Go compiler catches it. Template expressions (`{{ .Name }}`) are still checked at parse time but not at compile time.
- **Templ** compiles `.templ` files to Go. Component parameters are regular Go function arguments — fully type-checked by the compiler. Expressions inside templates are also compiled to Go, giving end-to-end compile-time safety.
- **gomponents** and **htmgo** are native Go code. Everything is type-checked by the compiler. There are no templates at all — HTML structure is Go function calls.
- **html/template** passes data via `interface{}`. Misspelled field names, wrong types, and missing data are only caught at runtime.

## File-based Routing

File-based routing maps filesystem paths to URL routes automatically, reducing boilerplate.

- **Gastro** — files in `pages/` become routes automatically. `pages/blog/[slug].gastro` becomes `GET /blog/{slug}`. Dynamic parameters use bracket syntax. No manual route registration needed.
- **htmgo** — similar file-based routing. Pages in a designated directory are registered automatically. Supports dynamic segments.
- **Templ**, **gomponents**, and **html/template** — you wire routes manually using `http.ServeMux`, Chi, Echo, Gorilla, or any Go router. This is more flexible but requires more boilerplate.

## Build & Tooling

| | Code Generation | Dev Server | Hot Reload |
|---|-----------------|------------|------------|
| **Gastro** | `gastro generate` compiles `.gastro` files to Go | `gastro dev` watches files, rebuilds, restarts | Template changes hot-reload without restart; Go changes trigger rebuild |
| **Templ** | `templ generate` compiles `.templ` files to Go | No built-in dev server; commonly paired with Air | Proxy mode supports hot reload of templates |
| **gomponents** | None needed (pure Go) | No built-in dev server; use Air or similar | Full rebuild required for any change |
| **htmgo** | Route registration generated from file paths | Built-in dev server with live reload | Rebuilds Go, CSS, and routes on change |
| **html/template** | None | No built-in dev server | Templates can be re-parsed from disk without recompile |

## Deployment

All five approaches produce a single deployable binary. The differences are in how assets are embedded:

- **Gastro** — templates and static assets are embedded via `go:embed` at build time. One binary, zero runtime dependencies.
- **Templ** — templates are compiled directly into Go code. No template files to embed. Static assets still need `go:embed` or an external CDN.
- **gomponents** — HTML is generated by Go code, so there are no template files at all. Static assets need separate handling.
- **htmgo** — assets (including built CSS) are embedded into the binary. Built-in Tailwind CSS processing.
- **html/template** — templates can be embedded with `go:embed`. Everything else is manual.

## Learning Curve

| Approach | What You Need to Know | New Concepts |
|----------|----------------------|--------------|
| **Gastro** | Go + html/template syntax + HTML/CSS | `.gastro` file format, frontmatter conventions, ambient `(w, r)` and `gastro.Props()` |
| **Templ** | Go + the Templ DSL | Templ-specific syntax (looks like Go but is not Go), `@component` calls, `templ` blocks |
| **gomponents** | Go only | The `Node` interface and dot-import pattern. Otherwise it is standard Go. |
| **htmgo** | Go + HTMX attributes | htmgo's element builder API, HTMX concepts (hypermedia-driven), partial rendering |
| **html/template** | Go + template syntax | Template actions, pipelines, `FuncMap`, `define`/`block` semantics |

## Ecosystem & Maturity

| | Maturity | Community |
|---|----------|-----------|
| **Gastro** | Early stage, under active development | Small — new project |
| **Templ** | Established, widely adopted in the Go community | Large — active GitHub, Discord, conference talks |
| **gomponents** | Stable, maintained since 2020 | Medium — steady adoption, GopherCon talks |
| **htmgo** | Newer project, growing quickly | Growing — active Discord community |
| **html/template** | Part of the Go standard library since Go 1.0 | Universal — every Go developer knows it |

## When to Choose What

- **Gastro** — you want file-based routing, HTML-first syntax with Go's type safety, built-in SSE support, and a single CLI that handles the full dev-to-deploy workflow.
- **Templ** — you want the strongest compile-time type safety, mature tooling with IDE integration, and don't mind learning a custom DSL.
- **gomponents** — you want zero code generation, zero new syntax, and prefer building HTML entirely in Go functions.
- **htmgo** — you want a batteries-included framework with HTMX integration, file-based routing, and built-in Tailwind support.
- **html/template** — you want zero dependencies, are comfortable with runtime-only type checking, and prefer the standard library.
