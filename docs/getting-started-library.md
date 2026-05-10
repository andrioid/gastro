# Getting Started — Library Mode

This guide is for adding gastro to an **existing Go project** — typically
an API service, internal tool, or background worker that needs to grow a
UI without becoming a new project. If you're starting fresh and want
gastro to scaffold the whole app for you, use the [framework getting
started](getting-started.md) instead.

The runtime is identical between modes. What differs is the bootstrap,
the directory layout, and the dev-loop command.

## When to choose this mode

Pick library mode when you want to **add a UI to your otherwise-headless
Go service**. Concrete cases this guide is written around:

- An **admin UI** sitting alongside an API-first product
- An **internal tool** or **dashboard** for ops / finance / support
- A **status page** rendered server-side from your existing data sources
- **Server-rendered marketing pages** that share infrastructure with your
  product API

In all of these the gastro pages live next to existing code (handlers,
DB layer, business logic, middleware) and the existing `main.go` keeps
ownership of the process lifecycle.

## Install the CLI as a project tool

Inside your existing module:

```sh
go get -tool github.com/andrioid/gastro/cmd/gastro
```

This pins gastro in your `go.mod`'s `tool` directive, so every
contributor gets the same version without a separate install. Invoke
the CLI as `go tool gastro <cmd>` (or `gastro <cmd>` if you also have
it on `PATH`).

The other install methods (`mise`, `go install`) work fine too — see
the [framework getting started](getting-started.md#install) for
tradeoffs. `go tool` is the recommended default for library mode
because it keeps the CLI version under version control.

## Smallest possible integration: one component

The smallest useful integration is rendering a single component from an
existing `http.HandlerFunc`. About 20 lines of setup.

Lay out a `web/` (or `internal/web/`, your choice) directory with the
gastro tree inside:

```text
internal/web/
  components/
    welcome.gastro
  pages/                ← optional in library mode
```

Write the component:

```gastro
---
type Props struct {
    Name string
}

p := gastro.Props()
Name := p.Name
---
<section>
    <h2>Hello, {{ .Name }}!</h2>
    <p>Rendered by a gastro component inside an existing Go service.</p>
</section>
```

Generate the Go code (creates `internal/web/.gastro/`):

```sh
go tool gastro generate --project ./internal/web
# or, with GASTRO_PROJECT set: just `go tool gastro generate`
```

Wire one component into your existing handler:

```go
package main

import (
    "net/http"

    web "myapp/internal/web/.gastro"  // generated package
    "github.com/andrioid/gastro/pkg/gastro"
)

func main() {
    // Construct a router so the component package's typed Render API works.
    // We don't mount its Handler() — we just want access to Render.Welcome.
    r := web.New()

    http.HandleFunc("/welcome", func(w http.ResponseWriter, req *http.Request) {
        w.Header().Set("Content-Type", "text/html; charset=utf-8")
        html, err := r.Render().Welcome(web.WelcomeProps{Name: "World"})
        if err != nil {
            http.Error(w, err.Error(), http.StatusInternalServerError)
            return
        }
        w.Write([]byte(html))
    })

    http.ListenAndServe(":8080", nil)
}
```

That's it. You now have a typed component you can compose anywhere a Go
`http.Handler` runs. Components are pure: they don't own routing,
middleware, or response writing — your existing service still controls
all of that.

> **Note:** The `web.WelcomeProps` and `r.Render().Welcome(...)`
> signatures are auto-generated from your `.gastro` file's `type Props
> struct{...}` declaration. Run `gastro list` (or `go tool gastro list`)
> any time to see the up-to-date signatures across your project.

## The full pattern: `gastro.New(...)` in `main.go`

When you want gastro to own a subtree of routes (file-based pages with
auto-generated routing), wire its `Router.Handler()` into your existing
mux. The runtime API is identical to framework mode; what differs is
that you mount it under a path prefix or alongside your own routes
rather than letting it own `:4242`.

```go
package main

import (
    "log"
    "net/http"

    web "myapp/internal/web/.gastro"
    "github.com/andrioid/gastro/pkg/gastro"
)

func main() {
    // Your existing dependency wiring.
    db := openDB()
    cache := openCache()

    // Construct the gastro router. Same API as framework mode.
    gastroRouter := web.New(
        gastro.WithDeps(db),
        gastro.WithDeps(cache),
        gastro.WithMiddleware("/{path...}", requestLogger),
    )

    // Compose with your existing routes.
    mux := http.NewServeMux()
    mux.HandleFunc("GET /api/v1/users", listUsers)
    mux.HandleFunc("POST /api/v1/users", createUser)
    mux.HandleFunc("/healthz", healthCheck)

    // Mount gastro at the prefix of your choice. Use "/" to let it own
    // every otherwise-unmatched route, or a prefix like "/admin/" to
    // scope it to a subtree.
    mux.Handle("/", gastroRouter.Handler())

    log.Fatal(http.ListenAndServe(":8080", mux))
}
```

Pages in `internal/web/pages/` are now auto-routed. `pages/admin/index.gastro`
serves `/admin/`; `pages/admin/users/[id].gastro` serves `/admin/users/{id}`.
Frontmatter has the same ambient `w http.ResponseWriter` and `r *http.Request`,
the same `gastro.From[T](ctx)` for typed deps, and the same template syntax
as framework mode. See [Pages & Routing](pages.md) for the full reference.

## The dev loop: `gastro watch`

`gastro watch` is the library-mode counterpart to `gastro dev`. You
supply your own build and run commands; gastro handles the file
watching, smart classification, browser reload, and process supervision.

The minimal form:

```sh
gastro watch --run 'go run ./cmd/myapp'
```

This watches `.gastro` files **and** `*.go` files under the project
root, regenerates on `.gastro` changes, restarts the binary on `*.go`
changes, and signals the browser to reload after every successful
rebuild. The `--run` command is started once and re-spawned across
restarts.

For faster iteration, split build and run so `go build`'s incremental
compile cache does the heavy lifting:

```sh
gastro watch \
  --build 'go build -o tmp/app ./cmd/myapp' \
  --run 'tmp/app'
```

`--build` is repeatable — chain a CSS pipeline or anything else before
the Go build:

```sh
gastro watch \
  --build 'tailwindcss -i in.css -o internal/web/static/styles.css' \
  --build 'go build -o tmp/app ./cmd/myapp' \
  --run 'tmp/app'
```

Run `gastro watch --help` for the full flag list (`--exclude`,
`--debounce`, `--quiet`, `--project`).

### Build-failure resilience

When a build fails after a code change (typo in a `.go` file, a broken
template), the **previously-running binary stays alive** so you can keep
clicking through your app while you fix the error. The failure surfaces
in two places:

- The **terminal** prints the failing command's stderr, prefixed with
  `gastro: build failed; previous version still serving`.
- The **browser console** logs a `console.warn` with the same message
  via the gastro live-reload SSE channel, so the failure is visible
  even if you've tabbed away from the terminal. (A visible banner UI
  is a planned follow-up; v1 ships the transport.)

When the next build succeeds, the running binary is replaced and the
browser receives a normal reload event, which clears any prior warning
state on the page.

## Production build

Library-mode projects build the same way they did before gastro joined
the picture:

```sh
go generate ./...     # runs the //go:generate directive in main.go
go build -o myapp ./cmd/myapp
```

There's no special gastro build step in production. The generated
`.gastro/` package is plain Go source compiled into your binary.
Embed (`//go:embed`) of templates and static assets is handled inside
the generated package; production startup uses the embedded copies, dev
mode reads them from disk so edits show up without rebuilding.

If your build pipeline runs in CI without internet access, commit the
`.gastro/` tree (it's gitignored by default but you can opt in) and run
`gastro check` as a CI gate — it byte-compares the committed tree
against fresh codegen output and fails if they diverge.

## Mental model: what `gastro watch` is and isn't

We built `gastro watch` so you don't need to install `air`, `wgo`, or
`watchexec` just to get a hot-reload loop for a gastro-in-Go-project
setup. It's a small, focused, gastro-aware version of the slice of
`air` that gastro projects actually need.

It is **not** a general-purpose Go file watcher. The watch surface is
hardcoded: `.gastro` files, `*.md` (and other files) referenced via
`//gastro:embed` directives, `static/**`, and `*.go` files anywhere under the
project root (minus `.gastro/`, `vendor/`, `node_modules/`, `.git/`,
`tmp/`, plus any `--exclude` paths). It doesn't watch arbitrary file
extensions. This is intentional: covering the cases gastro projects
actually have lets us keep the configuration surface tiny.

If you need to compose gastro generation with a different runner
(`watchexec`, `entr`, a Makefile-based system), see the [composition
recipe in dev-mode.md](dev-mode.md#composing-with-other-runners) — it
uses just `gastro generate` and the `.gastro/.reload` signal file, no
process supervision. That's the right tool for "I already have a runner
I love and want gastro to plug into it."

## Environment variables

The `GASTRO_PROJECT` env var tells the gastro CLI and LSP where the
gastro project root lives. Set it once and the CLI works from anywhere
in your repo without `cd`. See [dev-mode.md](dev-mode.md#gastro_project)
for the full reference.

For library-mode projects whose binary launches from a directory other
than the gastro project root, also set `GASTRO_DEV_ROOT` so the runtime
can find `.gastro/templates/` and `static/` at request time. See
[dev-mode.md](dev-mode.md#gastro_dev_root).

## What to read next

- [Pages & Routing](pages.md) — the full page authoring reference.
  Identical between framework and library modes.
- [Components](components.md) — component authoring + the typed Render
  API your library-mode handlers use.
- [Dev Mode](dev-mode.md) — `gastro watch` advanced usage,
  `GASTRO_PROJECT` / `GASTRO_DEV_ROOT`, and the watcher recipe for
  composed setups.
- [Error Handling](error-handling.md) — failure modes and the
  `WithErrorHandler` API.
