# Pages & Routing

Pages are `.gastro` files in the `pages/` directory. Each page becomes
a `GET` route automatically — pages are **views**. `http.ServeMux`
answers `HEAD` for free and every other method with
`405 Method Not Allowed` (plus an `Allow` header). Commands — form
posts, Datastar actions, anything that mutates state — are ordinary
`http.Handler`s registered next to the pages with
[`gastro.WithHandler`](#commands-withhandler).

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
import (
    "net/http"

    "myapp/auth"
)

user, ok := auth.User(r)
if !ok {
    http.Redirect(w, r, "/login", http.StatusSeeOther)
    return
}

Name := user.Name
---
<h1>Hello, {{ .Name }}</h1>
```

After your frontmatter completes, gastro inspects the wrapped writer.
If the frontmatter wrote a body — through `http.Error`, `http.Redirect`,
a Datastar SSE patch, an explicit `w.Write(…)`, or a `Hijack` —
**the template render is skipped**. Otherwise the template runs with
your uppercase locals (`Title`, `Tasks`, …) as the data.

So a page can still set a status code, redirect, add headers, or
answer its `GET` with JSON (a health check, a small API endpoint)
without a separate handler. Don't branch on `r.Method` in a page: only
`GET`/`HEAD` reach it. Mutations live in `WithHandler` commands — see
[`examples/sse`](https://github.com/andrioid/gastro/tree/main/examples/sse)
for a GET page plus a `POST /increment` command; the page is a
[live page](sse.md#live-pages), so its stream re-renders it after the
command.

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
`w` is followed by `return`. Forgetting the `return` produces a warning
(`pages/x.gastro:12:2: warning: …`) that fails the strict commands
(`gastro generate`, `gastro build`, `gastro check`) before the silent
"frontmatter continued past the write" footgun ever runs.

## Commands: `WithHandler`

A page renders the `GET`; the `POST` that the page's form submits is a
plain Go handler registered with `gastro.WithHandler`:

```gastro
---
Title := "Sign-up"
---
<h1>{{ .Title }}</h1>
<form method="POST" action="/signup">…</form>
```

```go
// main.go
router := gastro.New(
    gastro.WithHandler("POST /signup", http.HandlerFunc(signup)),
)

func signup(w http.ResponseWriter, r *http.Request) {
    if err := forms.HandleSignup(r); err != nil {
        http.Error(w, err.Error(), http.StatusBadRequest)
        return
    }
    http.Redirect(w, r, "/thanks", http.StatusSeeOther)
}
```

`pages/signup.gastro` registers `GET /signup`; the handler registers
`POST /signup`. Both share the router's deps and middleware.

A Datastar command answers with an SSE patch instead. It renders a
component through the router, so it needs the `*Router` — build it in
`main` and close over it:

```go
var router *gastro.Router
router = gastro.New(
    gastro.WithHandler("POST /signup", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        signup(router, w, r)
    })),
)

func signup(router *gastro.Router, w http.ResponseWriter, r *http.Request) {
    if err := forms.HandleSignup(r); err != nil {
        http.Error(w, err.Error(), http.StatusBadRequest)
        return
    }
    html, err := router.Render(gastro.Flash, gastro.FlashProps{Message: "Thanks!"})
    if err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }
    sse, err := datastar.NewSSE(w, r)
    if err != nil {
        return // the client can no longer be reached
    }
    sse.PatchElements(string(html))
}
```

with the page's form posting through Datastar:

```gastro
---
Title := "Sign-up"
---
<form data-on:submit="@post('/signup')">…</form>
<p id="msg"></p>
```

See [Adding and replacing routes](#adding-and-replacing-routes) for
the full `WithHandler` rules.

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

### Adding and replacing routes

`gastro.WithHandler(pattern, h)` registers any `http.Handler` under any
`http.ServeMux` pattern, with or without a method
(`"POST /tasks/{id}/place"`, `"/healthz"`):

- **Add.** A pattern no page uses adds a route.
- **Replace.** A pattern identical to a page's route (`"GET /board"`,
  `"GET /{$}"` for `pages/index.gastro`) or to the static-asset prefix
  (`"GET /static/"`) replaces that route's handler — useful when a
  page's logic outgrows frontmatter (streaming, intricate negotiation):

  ```go
  router := gastro.New(
      gastro.WithHandler("GET /{$}", board.NewHomeHandler(deps)),
  )
  ```

- **Conflict.** A registration `http.ServeMux` rejects — e.g.
  `WithHandler("/tasks/new", h)` next to `pages/tasks/[id].gastro`
  (`GET /tasks/{id}`) — panics in `New()` with a message naming both
  sources, so overlaps fail at startup rather than at request time.

Deps reach handlers through the request context:
`gastroRuntime.FromContext[T](r.Context())` (from
`github.com/andrioid/gastro/pkg/gastro`); `gastro.From[T]` is the
frontmatter-only spelling of the same call.

### Wrapping routes with middleware

Use `gastro.WithMiddleware(pattern, fn)` to wrap one or more routes —
page routes and `WithHandler` routes alike — with a
`func(http.Handler) http.Handler`. Patterns use Go's stdlib
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
- **`/{path...}`** is the canonical catch-all: every page and
  `WithHandler` route.

The middleware pattern must match at least one route (page or
`WithHandler`) at `New()` time; patterns that match nothing panic with
a descriptive error listing the known routes, so typos fail loudly.

**Composition.** Multiple `WithMiddleware` calls compose in registration
order — the first registered ends up outermost (runs first on the
request, last on the response). When a `WithHandler` replaces a page
route, **middleware wraps the replacement**: the handler replaces the
page handler, then matching middleware wraps it.

**Patterns are path-only.** A method-qualified route matches by its
path: `WithMiddleware("/signup", csrf)` wraps both the `GET /signup`
page and a `POST /signup` handler. Method scoping (e.g. "only run CSRF
on POST") lives inside the middleware itself:

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

Routes added directly on `router.Mux()` bypass `WithMiddleware` and
the conflict check; register them with `WithHandler` instead.

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

| File                          | Route                |
|-------------------------------|----------------------|
| `pages/index.gastro`          | `GET /{$}`           |
| `pages/about/index.gastro`    | `GET /about`         |
| `pages/blog/index.gastro`     | `GET /blog`          |
| `pages/blog/[slug].gastro`    | `GET /blog/{slug}`   |

Square brackets denote dynamic segments: `[slug]` becomes `{slug}` in
Go 1.22+ router patterns. Every page is a `GET` route; `HEAD` is served
automatically and other methods get `405` with `Allow: GET, HEAD`
unless a `WithHandler` route claims them. `gastro list` prints each
page's route.

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

## SSE

Datastar streams and commands are `WithHandler` routes: a
`GET /…/stream` handler holds the long-lived connection, and `POST`
commands mutate state. Either can render components with
`router.Render`. The page stays a plain `GET` view that opens the
stream from the template. See [SSE](sse.md).
