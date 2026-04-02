# Pages

Pages are `.gastro` files in the `pages/` directory that handle HTTP requests
and render HTML responses. Each page becomes a route in your application.

## File format

A page has two sections separated by `---` delimiters: Go frontmatter and an
HTML template body.

```gastro
---
ctx := gastro.Context()

Title := "Hello"
---
<h1>{{ .Title }}</h1>
```

The frontmatter runs as Go code when the page is requested. The template body
is rendered with Go's `html/template` engine.

## Marking a file as a page

Call `gastro.Context()` in the frontmatter. This is a compile-time marker that
tells the code generator to wire this file up as an HTTP handler. It returns a
`*Context` value you can use to access the request, set headers, redirect, etc.

```gastro
---
ctx := gastro.Context()
Name := ctx.Query("name")
---
<p>Hello, {{ .Name }}</p>
```

Pages that don't need request access can omit `gastro.Context()`. These are
static pages that only use component imports and exported variables:

```gastro
---
import Layout "components/layout.gastro"

Title := "About"
---
{{ wrap Layout (dict "Title" .Title) }}
    <h1>About Me</h1>
{{ end }}
```

## Data flow

Variables declared in the frontmatter are passed to the template based on their
first letter:

- **Uppercase** variables (e.g. `Title`, `Posts`) are exported to the template
  and accessible as `{{ .Title }}`, `{{ .Posts }}`, etc.
- **Lowercase** variables (e.g. `err`, `slug`) are private to the frontmatter
  and not available in the template.

A common pattern is to compute data with lowercase variables, then assign the
results to uppercase variables for the template:

```gastro
---
ctx := gastro.Context()
posts, err := db.ListPublished()
if err != nil {
    ctx.Error(500, "Failed to load posts")
    return
}

Posts := posts
Title := "Blog"
---
<h1>{{ .Title }}</h1>
{{ range .Posts }}
<p>{{ .Title }}</p>
{{ end }}
```

## Imports and component usage

Use Go `import` for both packages and components. Component imports are
distinguished by the `.gastro` file extension:

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
    {{ render PostCard (dict "Title" .Title "Slug" .Slug) }}
    {{ end }}
{{ end }}
```

See [components.md](components.md) for details on the component system.

## File-based routing

Page files map to HTTP routes automatically:

| File | Route |
|------|-------|
| `pages/index.gastro` | `GET /` |
| `pages/about/index.gastro` | `GET /about` |
| `pages/blog/index.gastro` | `GET /blog` |
| `pages/blog/[slug].gastro` | `GET /blog/{slug}` |
| `pages/blog/[slug]/comments.gastro` | `GET /blog/{slug}/comments` |
| `pages/[category]/[id].gastro` | `GET /{category}/{id}` |

Rules:

- Files named `index.gastro` map to the directory root.
- Square brackets denote dynamic segments: `[slug]` becomes `{slug}` in the
  Go 1.22+ `net/http` router pattern.
- Only `GET` routes are generated. For other HTTP methods, register handlers
  directly in your `main.go`.

## Context API

`gastro.Context()` returns a `*Context` with these methods:

### `Request() *http.Request`

Returns the underlying HTTP request.

```go
ctx := gastro.Context()
method := ctx.Request().Method
```

### `Param(name string) string`

Returns a URL path parameter from a dynamic route segment.

```gastro
---
ctx := gastro.Context()
slug := ctx.Param("slug")
---
<h1>{{ .Slug }}</h1>
```

For a route like `pages/blog/[slug].gastro`, requesting `/blog/hello-world`
returns `"hello-world"` from `ctx.Param("slug")`.

### `Query(name string) string`

Returns a query string parameter by name. Returns an empty string if not present.

```go
ctx := gastro.Context()
page := ctx.Query("page")
```

### `Redirect(url string, code int)`

Sends an HTTP redirect. You **must** call `return` after this, otherwise the
template will still be rendered.

```gastro
---
ctx := gastro.Context()

user := getUser(ctx.Request())
if user == nil {
    ctx.Redirect("/login", 302)
    return
}

Name := user.Name
---
<h1>Welcome, {{ .Name }}</h1>
```

Common status codes: `301` (permanent), `302` (temporary).

### `Error(code int, msg string)`

Sends a plain-text HTTP error response. You **must** call `return` after this.

```go
ctx := gastro.Context()
post, err := db.GetBySlug(slug)
if err != nil {
    ctx.Error(404, "Post not found")
    return
}
```

### `Header(key, val string)`

Sets a response header. Call this before the template renders.

```go
ctx := gastro.Context()
ctx.Header("Cache-Control", "public, max-age=3600")
```

## Error handling

There are two layers of error handling:

1. **Explicit errors** -- Use `ctx.Error(code, msg)` + `return` to send error
   responses from the frontmatter.
2. **Panic recovery** -- If a panic occurs during handler execution, Gastro
   catches it and returns a `500 Internal Server Error`. This is automatic for
   all pages.

For custom error pages, wrap `gastro.Routes()` with middleware in your `main.go`.

## Template functions

Pages have access to all built-in template functions. See the
[README](../README.md) for the full list, or register custom functions with
`gastro.WithFuncs()` in your `main.go`:

```go
routes := gastro.Routes(
    gastro.WithFuncs(template.FuncMap{
        "myHelper": func(s string) string { return strings.ToUpper(s) },
    }),
)
```

## Static assets

Place files in a `static/` directory alongside `pages/`. They are served at
`/static/`:

```
project/
  pages/
  components/
  static/
    styles.css
    logo.png
```

Reference them in templates as `/static/styles.css`.
