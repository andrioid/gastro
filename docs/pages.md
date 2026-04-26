# Pages & Routing

Pages are `.gastro` files in the `pages/` directory. Each page becomes an HTTP route automatically.

## File Format

A page has two sections separated by `---` delimiters: Go frontmatter and an HTML template body.

```gastro
---
ctx := gastro.Context()

Title := "Hello"
---
<h1>{{ .Title }}</h1>
```

Call `gastro.Context()` in the frontmatter to mark the file as a page and get access to the HTTP request.

### Why the marker?

`gastro.Context()` is a compile-time signal, not a runtime call. The code
generator strips that line and replaces it with a real `ctx :=
gastroRuntime.NewContext(w, r)` at the top of the generated handler. The
name `ctx` is therefore implicitly declared inside the page body whenever
the marker is present.

The practical consequence: **referencing `ctx` without writing
`ctx := gastro.Context()` first is an error**. Because the marker is what
tells gastro to inject `ctx`, omitting it leaves `ctx` undefined and the
Go compiler will fail the build with `undefined: ctx`. The LSP detects
this and emits a warning before the build runs.

## Static Pages

Pages that don't need request access can omit `gastro.Context()`. These are static pages that only use component imports and exported variables:

```gastro
---
import Layout "components/layout.gastro"

Title := "About"
---
{{ wrap Layout (dict "Title" .Title) }}
    <h1>About Me</h1>
{{ end }}
```

## Data Flow

Variables follow Go's export convention:

- **Uppercase** variables (`Title`, `Posts`) are exported to the template
- **Lowercase** variables (`err`, `slug`) are private to the frontmatter

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

## Typed Dependencies

Pages frequently need access to runtime values that aren't part of the
request — a database handle, a service client, a snapshot of application
state. Inject these typed dependencies via `gastro.WithDeps` at router
construction time and retrieve them in the page frontmatter with
`gastro.From[T]`:

```go
// main.go
package main

import (
	"net/http"

	gastro "myapp/.gastro"
	"myapp/internal/board"
)

func main() {
	deps := board.BoardDeps{State: board.NewState(), Store: openStore()}
	router := gastro.New(
		gastro.WithDeps(deps),
	)
	http.ListenAndServe(":4242", router.Handler())
}
```

```gastro
---
import (
	"myapp/internal/board"
)

ctx := gastro.Context()
deps := gastro.From[board.BoardDeps](ctx)

state := deps.State()
Tasks := state.ByStatus(board.StatusTodo)
---
<h1>Board</h1>
{{ range .Tasks }}<p>{{ .Title }}</p>{{ end }}
```

Deps are keyed by their Go type. Each Go type can have at most one
instance per router; calling `WithDeps` twice with the same type panics
at startup. To register multiple dependency groups, use distinct types:

```go
router := gastro.New(
	gastro.WithDeps(BoardDeps{...}),
	gastro.WithDeps(AuthDeps{...}),
)
```

`gastro.From[T]` panics if no value of type `T` is registered. Use
`gastro.FromOK[T]` for a safe variant that returns `(T, false)` instead.

### When to override an auto-generated route

When a page's logic outgrows what frontmatter can express comfortably
(streaming responses, content negotiation, complex error handling),
replace the auto-generated handler with a Go handler via
`gastro.WithOverride`:

```go
router := gastro.New(
	gastro.WithOverride("GET /", board.NewHomeHandler(deps)),
)
```

The pattern must match an existing auto-route; `New` panics with the
list of valid patterns when it does not, so typos fail loudly.

## Imports

Use Go `import` for both packages and components. Component imports are distinguished by the `.gastro` file extension:

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

## File-Based Routing

Page files map to HTTP routes automatically:

| File | Route |
|------|-------|
| `pages/index.gastro` | `GET /` |
| `pages/about/index.gastro` | `GET /about` |
| `pages/blog/index.gastro` | `GET /blog` |
| `pages/blog/[slug].gastro` | `GET /blog/{slug}` |

Square brackets denote dynamic segments: `[slug]` becomes `{slug}` in Go 1.22+ router patterns. Only `GET` routes are generated.

## Dynamic Routes

Access URL parameters with `ctx.Param()`:

```gastro
---
import (
    "myblog/db"
    Layout "components/layout.gastro"
)

ctx := gastro.Context()
slug := ctx.Param("slug")

post, err := db.GetBySlug(slug)
if err != nil {
    ctx.Error(404, "Post not found")
    return
}

Post := post
Title := post.Title
---
{{ wrap Layout (dict "Title" .Title) }}
    <article>
        <h1>{{ .Post.Title }}</h1>
        <p class="meta">By {{ .Post.Author }}</p>
        <div>{{ .Post.Body | safeHTML }}</div>
    </article>
{{ end }}
```

## Context API

`gastro.Context()` returns a `*Context` with methods for request handling:

### Query Parameters

```gastro
---
ctx := gastro.Context()
Name := ctx.Query("name")
---
<p>Hello, {{ .Name }}</p>
```

### Redirects

Always call `return` after a redirect to prevent the template from rendering:

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

### Response Headers

```gastro
---
ctx := gastro.Context()
ctx.Header("Cache-Control", "public, max-age=3600")

Title := "Cached Page"
---
<h1>{{ .Title }}</h1>
```

## Error Handling

Two layers protect your application:

1. **Explicit errors** — use `ctx.Error(code, msg)` + `return` for controlled error responses
2. **Panic recovery** — all handlers are wrapped in `defer gastro.Recover(w, r)` which catches panics and returns a 500 error
