# Pages & Routing

Pages are `.gastro` files in the `pages/` directory. Each page becomes
an HTTP route automatically. The page handler receives every HTTP
method for that path; the frontmatter branches on `r.Method` when the
page handles more than just `GET`.

## File Format

A page has two sections separated by `---` delimiters: Go frontmatter
and an HTML template body.

```gastro
---
Title := "Hello"
---
<h1>{{ .Title }}</h1>
```

## The page model

The generated handler injects two ambient identifiers into the
frontmatter:

- `r *http.Request` — the request, complete with URL, headers, body,
  and `Context()`.
- `w http.ResponseWriter` — the response writer, wrapped by gastro to
  track whether you have written a body.

You use them like any other Go variable:

```gastro
---
import "net/http"

if r.Method == http.MethodPost {
    if err := handlePost(r); err != nil {
        http.Error(w, err.Error(), http.StatusBadRequest)
        return
    }
    http.Redirect(w, r, "/thanks", http.StatusSeeOther)
    return
}

Title := "Sign-up"
---
<h1>{{ .Title }}</h1>
<form method="POST">…</form>
```

After your frontmatter completes, gastro inspects the wrapped writer.
If the frontmatter wrote a body — through `http.Error`, `http.Redirect`,
a Datastar SSE patch, an explicit `w.Write(…)`, or a `Hijack` —
**the template render is skipped**. Otherwise the template runs with
your uppercase locals (`Title`, `Tasks`, …) as the data.

This is the headline mechanic: one source of truth per route, no
parallel API handler in `main.go` to keep in sync with the page. See
[`examples/sse`](https://github.com/andrioid/gastro/tree/main/examples/sse)
for a worked GET-renders / POST-patches counter.

### Why no marker?

Earlier gastro versions required `ctx := gastro.Context()` at the top
of every page that used the request. The marker is gone — directory
placement is the signal, and `(w, r)` are always available. The marker
still works during a deprecation window but emits a build warning.
See [DECISIONS.md](../DECISIONS.md) for the timeline.

## Static Pages

Pages that don't read the request can ignore `(w, r)` entirely:

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

- **Uppercase** variables (`Title`, `Posts`) are exported to the template.
- **Lowercase** variables (`err`, `slug`) are private to the frontmatter.

## Frontmatter scope: package-level vs per-request

Go distinguishes two scopes inside a frontmatter block, and gastro
respects both:

- **Package scope (init-once):** top-level `var`, `const`, `type`, and
  `func` declarations. They run **once at process startup**, the same
  way they would in any Go file. Use these for expensive setup (a
  compiled regex, an env-var read, a helper function shared across
  every render).
- **Request scope (per-render):** `:=` short variable declarations and
  any other statements. They run **on every request**.

```gastro
---
import (
    "os"
    "regexp"
)

// Package scope — these run once at server startup.
var slugRE = regexp.MustCompile(`^[a-z0-9-]+$`)
const MaxItems = 50
var siteName = os.Getenv("SITE_NAME")

func isValidSlug(s string) bool {
    return slugRE.MatchString(s)
}

// Request scope — these run on every render.
slug := r.URL.Query().Get("q")
Valid := isValidSlug(slug)
Title := siteName + ": " + slug
---
<h1 data-valid="{{ .Valid }}">{{ .Title }}</h1>
```

The rule is identical to Go's: declarations that *look* like "once at
startup" (`var`, `const`, `func`, `type`) **are** once at startup;
everything else runs each request.

### Per-request references in package-scope decls

A hoisted decl that captures `r`, `w`, `gastro.Props()`,
`gastro.Context()`, `gastro.Children()`, or `gastro.From[T]` is a
build error — those values only exist inside the per-request handler.
The error includes a migration hint pointing at `:=`:

```
pages/index.gastro:12: var "Title" cannot be hoisted to package scope
because it references per-request state (r.URL.Path).

Hoisted decls run once at process init; per-request state is only
available inside the handler. Use `:=` so it runs each request:

    Title := r.URL.Path
```

### Foot-gun: package init can slow startup

`var X = expensive()` at frontmatter top level slows **boot**, not
request handling. If your binary takes long to start, profile the
package init (e.g. with `GODEBUG=inittrace=1`); a heavy regex compile
or synchronous network call masquerading as a `var` will show up
there.

### Embedding static content

For markdown, JSON config, or any other text/binary content shipped
with the repo, use the `//gastro:embed PATH` directive in frontmatter
to bake bytes into a `string` or `[]byte` var at codegen time. See
[Markdown](markdown.md) for the directive contract and the canonical
render-once-at-init pattern.

```gastro
---
import (
    "net/http"

    "myblog/db"
)

posts, err := db.ListPublished()
if err != nil {
    http.Error(w, "Failed to load posts", http.StatusInternalServerError)
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

The `http.Error(w, …)` write commits the response; the analyser at
`gastro generate` and `gastro check` time enforces that every write to
`w` is followed by `return`. Forgetting the `return` produces a build
warning (or, under `--strict`, an error) before the silent
"frontmatter continued past the write" footgun ever runs.

## Method-aware handlers

Branch on `r.Method` when one path serves multiple methods:

```gastro
---
import (
    "net/http"

    "myapp/forms"

    "github.com/andrioid/gastro/pkg/gastro/datastar"
)

if r.Method == http.MethodPost {
    if err := forms.HandleSignup(r); err != nil {
        http.Error(w, err.Error(), http.StatusBadRequest)
        return
    }

    sse := datastar.NewSSE(w, r)
    sse.PatchElements(`<p id="msg">Thanks!</p>`)
    return
}

Title := "Sign-up"
---
<form data-on:submit="@post('/signup')">…</form>
<p id="msg"></p>
```

The page is registered for every HTTP method. The POST branch writes
SSE events and returns; the GET fall-through renders the template.

## Typed Dependencies

Pages frequently need access to runtime values that aren't part of the
request — a database handle, a service client, a snapshot of
application state. Inject these typed dependencies via
`gastro.WithDeps` at router construction time and retrieve them in
page frontmatter with `gastro.From[T]`:

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

deps := gastro.From[board.BoardDeps](r.Context())

state := deps.State()
Tasks := state.ByStatus(board.StatusTodo)
---
<h1>Board</h1>
{{ range .Tasks }}<p>{{ .Title }}</p>{{ end }}
```

`gastro.From[T]` reaches into the request's context, where the router
attached the dep map. Use `gastro.FromOK[T]` for the safe variant that
returns `(T, false)` instead of panicking.

Deps are keyed by their Go type. Each Go type can have at most one
instance per router; calling `WithDeps` twice with the same type
panics at startup. To register multiple dependency groups, use
distinct types:

```go
router := gastro.New(
    gastro.WithDeps(BoardDeps{...}),
    gastro.WithDeps(AuthDeps{...}),
)
```

### When to override an auto-generated route

When a page's logic outgrows what frontmatter can express comfortably
(streaming responses, complex middleware, intricate negotiation),
replace the auto-generated handler with a Go handler via
`gastro.WithOverride`:

```go
router := gastro.New(
    gastro.WithOverride("/", board.NewHomeHandler(deps)),
)
```

The pattern must match an existing auto-route; `New` panics with the
list of valid patterns when it does not, so typos fail loudly. Page
patterns are method-less ("/", "/blog/{slug}") because the page
handles every method.

### Wrapping routes with middleware

Use `gastro.WithMiddleware(pattern, fn)` to wrap one or more auto-routes
with a `func(http.Handler) http.Handler`. Patterns use Go's stdlib
`http.ServeMux` syntax:

```go
router := gastro.New(
    gastro.WithMiddleware("/admin/{path...}", auth.Require),  // subtree
    gastro.WithMiddleware("/counter", csrf.Require),          // exact
    gastro.WithMiddleware("/{path...}", logRequests),         // catch-all
)
```

- **`{slug}`** matches a single path segment (`/blog/{slug}` matches
  `/blog/my-post`).
- **`{path...}`** is a trailing wildcard — matches one or more remaining
  segments. `/admin/{path...}` matches `/admin/users`, `/admin/users/42`,
  etc., but not `/admin` itself.
- **`/{path...}`** is the canonical catch-all: every page route.

The middleware pattern must match at least one known auto-route at
`New()` time; patterns that match nothing panic with a descriptive
error, same typo-safety as `WithOverride`.

**Composition.** Multiple `WithMiddleware` calls compose in registration
order — the first registered ends up outermost (runs first on the
request, last on the response). When `WithOverride` and `WithMiddleware`
target the same route, **middleware wraps the override**: the override
replaces the page handler, then matching middleware wraps that
replacement.

**Patterns are path-only.** Method scoping (e.g. "only run CSRF on POST")
lives inside the middleware itself:

```go
func csrfRequire(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if r.Method == http.MethodPost && !validToken(r) {
            http.Error(w, "forbidden", http.StatusForbidden)
            return
        }
        next.ServeHTTP(w, r)
    })
}
```

This matches `WithOverride`'s path-only contract and avoids the
asymmetry of one option accepting `"POST /counter"` while the other
rejects it.

For middleware that needs to run for routes gastro doesn't auto-generate
(e.g. a `mux.HandleFunc` you wired through `router.Mux()`), wrap it
yourself at the `mux.HandleFunc` call site — `WithMiddleware`'s typo
guard intentionally rejects unknown patterns.

### Forcing dev or production mode

Gastro detects `GASTRO_DEV=1` at startup; the `gastro dev` command sets
it automatically. When the env var is insufficient, use `WithDevMode`:

```go
router := gastro.New(
    gastro.WithDevMode(true),  // force dev mode
)
```

`WithDevMode(true)` forces dev mode (template reload + browser auto-reload
SSE endpoint) regardless of `GASTRO_DEV`. `WithDevMode(false)` forces
production mode regardless of `GASTRO_DEV`. When `WithDevMode` is not
called, the default behaviour (checking `GASTRO_DEV`) applies.

Use cases:

- **Library mode.** A larger Go application embeds gastro for component
  rendering and doesn't want its dev-vs-prod story tangled up with a
  framework-owned env var. The host app sets `WithDevMode` from its own
  config flag.
- **Tests.** A test that constructs a Router shouldn't depend on whether
  `GASTRO_DEV` happens to be set in the surrounding shell.
- **Production debug.** A short-lived production deploy with live-reload
  enabled to investigate a template bug, without setting an env var on
  the host.

## Imports

Use Go `import` for both packages and components. Component imports
are distinguished by the `.gastro` file extension:

```gastro
---
import (
    "net/http"

    "myblog/db"

    Layout "components/layout.gastro"
    PostCard "components/post-card.gastro"
)

posts, err := db.ListPublished()
if err != nil {
    http.Error(w, "load failed", http.StatusInternalServerError)
    return
}
Posts := posts
---
{{ wrap Layout (dict "Title" "Home") }}
    {{ range .Posts }}
    {{ PostCard (dict "Title" .Title "Slug" .Slug) }}
    {{ end }}
{{ end }}
```

`net/http`, `log`, `html/template`, and `bytes` are imported by the
codegen template by default; you don't need to re-import them, but
declaring them yourself does not produce a duplicate.

## File-Based Routing

Page files map to HTTP routes automatically:

| File                          | Route            |
|-------------------------------|------------------|
| `pages/index.gastro`          | `/`              |
| `pages/about/index.gastro`    | `/about`         |
| `pages/blog/index.gastro`     | `/blog`          |
| `pages/blog/[slug].gastro`    | `/blog/{slug}`   |

Square brackets denote dynamic segments: `[slug]` becomes `{slug}` in
Go 1.22+ router patterns. Patterns are method-less; the page handler
receives every method.

## Dynamic Routes

Access URL parameters with the standard library's `r.PathValue`:

```gastro
---
import (
    "net/http"

    "myblog/db"
    Layout "components/layout.gastro"
)

slug := r.PathValue("slug")

post, err := db.GetBySlug(slug)
if err != nil {
    http.Error(w, "Post not found", http.StatusNotFound)
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

Query parameters are read the standard way:

```gastro
---
q := r.URL.Query().Get("filter")
Filter := q
---
<p>Filtering by: {{ .Filter }}</p>
```

## Error Handling

Four layers protect your application:

1. **Explicit errors** — `http.Error(w, msg, code)` followed by
   `return` for controlled error responses. The analyser ensures the
   `return` is present.
2. **Status without body** — `w.WriteHeader(http.StatusCreated)`
   commits the status but not the body. The template still renders
   afterwards, with the custom status preserved.
3. **Panic recovery** — all handlers are wrapped in
   `defer gastro.Recover(w, r)` which catches panics. If the panic
   happens after the body was committed, the recover logs only;
   otherwise it writes a 500 page.
4. **Render error handler** — when `template.Execute` fails mid-render,
   the page handler dispatches through `WithErrorHandler` (if installed)
   or `gastroRuntime.DefaultErrorHandler` (the safe fallback: log + 500
   when uncommitted). Use `gastro.WithErrorHandler(fn)` at `New()` time
   to install a custom handler — commonly used to attach request IDs,
   report to an error tracker, or render a templated error page.

For the full failure-mode catalogue (parse errors, render errors,
frontmatter panics, missing deps) and recipes for each, see
[`docs/error-handling.md`](error-handling.md).

## SSE-from-page

Page frontmatter can open an SSE stream directly:

```gastro
---
import (
    "net/http"

    "github.com/andrioid/gastro/pkg/gastro/datastar"
)

if r.Method == http.MethodPost {
    sse := datastar.NewSSE(w, r)
    sse.PatchElements(html)
    return
}
---
… template body for the GET render …
```

`datastar.NewSSE(w, r)` writes the headers and flushes; the wrapped
writer records the body write. Track B's body-tracking ensures the
template render is skipped after the SSE patches go out. See
[SSE](sse.md) for more.
