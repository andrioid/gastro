# Gastro

A file-based component framework for Go. `.gastro` files combine Go frontmatter
with `html/template` markup in a single file, compiled to type-safe Go code with
file-based routing.

Think: Astro's developer experience, Go's type safety, PHP's file-based routing.

**Status:** Early development. The core pipeline works end-to-end (parse, codegen,
route, build, serve). Editor support is in progress.

See [`ROADMAP.md`](ROADMAP.md) for deferred work and known
limitations, and [`DECISIONS.md`](DECISIONS.md) for the chronological
design log.


## Scope

### Goals

- Make Go web development fun again
- Idiomatic Go -- standard `html/template`, `net/http`, and `go:embed` under the hood. No new language to learn.
- Type-safety for pages, components and templates
- Opinionated project structure

### Non-Goals

- Astro's Island Architecture is cool, but outside of the scope of this project. We're focusing on server rendering.
- CSS/Style parsing/bundling from .gastro files
- JS parsing/bundling from .gastro files



## Quick Start

Gastro fits two project shapes. Pick the one that matches you:

### Framework mode — building a new site

For greenfield projects where gastro scaffolds the layout, owns the dev
server, and handles the build:

```sh
# install (pick one):
mise use github:andrioid/gastro@latest
# or: go install github.com/andrioid/gastro/cmd/gastro@latest

gastro new myapp && cd myapp
gastro dev
```

Open [http://localhost:4242](http://localhost:4242) and edit
`pages/index.gastro`. Full guide: [Getting Started — Framework Mode](docs/getting-started.md).

### Library mode — adding gastro to an existing Go project

For existing Go services growing a UI — admin tools, dashboards, status
pages, server-rendered marketing alongside an API:

```sh
cd your-project
go get -tool github.com/andrioid/gastro/cmd/gastro
mkdir -p internal/web/{pages,components}

# Smallest integration: render one component into an existing handler.
# OR wire gastro.New(...) into your main.go for the full pattern.

gastro watch --run 'go run ./cmd/myapp'
```

Full guide: [Getting Started — Library Mode](docs/getting-started-library.md).

The **runtime is identical between modes**. What differs is the
bootstrap (`gastro new` vs hand-wiring), the layout (scaffold vs your
existing tree), and the dev-loop command (`gastro dev` vs
`gastro watch`).

### Prerequisites

- Go 1.26+
- One of: [mise](https://mise.jdx.dev/), `go install`, or `go get -tool`

### All install methods

Three equivalent ways to get the gastro CLI. Framework mode usually goes
with the global install; library mode usually goes with `go get -tool`
(version-pinned in your `go.mod`). All three work for either mode.

```sh
# Option A: mise -- global, easy upgrades
mise use github:andrioid/gastro@latest

# Option B: go install -- global, no extra tooling
go install github.com/andrioid/gastro/cmd/gastro@latest

# Option C: go tool -- per-project version pin in go.mod, no global install
go get -tool github.com/andrioid/gastro/cmd/gastro
```

With Option C the CLI is invoked as `go tool gastro <cmd>` instead of
`gastro <cmd>`, and the version is pinned in your project's `go.mod` —
convenient for CI/CD where you'd rather not install a global binary.
The scaffold (`gastro new`) adds the `tool` directive to the generated
`go.mod` and a `//go:generate go tool gastro generate` directive to
`main.go`, so `go tool gastro …` and `go generate ./...` work out of
the box.

### Explore Your Project

```sh
# List all components and pages with their Props signatures
gastro list

# Machine-readable output for scripts and agents
gastro list --json
```

### Build for Production

```sh
gastro build       # or: go tool gastro build
./app
```

## How It Works

### File Format

A `.gastro` file has two sections separated by `---`:

- **Frontmatter** (between the delimiters): Go code that runs on the server.
  Uppercase variables are exported to the template. Lowercase variables are
  private.
- **Template body** (after the second delimiter): Standard Go `html/template`
  syntax.

### Variable Visibility

Mirrors Go's own export convention:

```gastro
---
slug := r.PathValue("slug")   // lowercase -> private
err := doSomething()           // lowercase -> private

Title := "Hello"               // Uppercase -> {{ .Title }}
Items := fetchItems()          // Uppercase -> {{ .Items }}
---
<h1>{{ .Title }}</h1>
```

Frontmatter has ambient `w http.ResponseWriter` and `r *http.Request`.
The page handler runs for every HTTP method; branch on `r.Method` to
handle non-GET requests in the same file.

### File-Based Routing

| File                       | Route          |
|----------------------------|----------------|
| `pages/index.gastro`       | `/`            |
| `pages/about/index.gastro` | `/about`       |
| `pages/blog/[slug].gastro` | `/blog/{slug}` |

Patterns are method-less; the page handles every method (GET, POST,
etc.) and branches on `r.Method`.

### Static Assets

Files in `static/` are served at the `/static/` URL prefix. Reference them
in templates as `/static/styles.css`, `/static/images/logo.png`, etc.

### Built-in Template Helpers

Available in all templates without registration:

`upper`, `lower`, `trim`, `join`, `split`, `contains`, `replace`,
`default`, `safeHTML`, `safeAttr`, `safeURL`, `safeCSS`, `safeJS`, `dict`,
`list`, `json`, `timeFormat`

Register custom helpers in `main.go`:

```go
router := gastro.New(
    gastro.WithFuncs(template.FuncMap{
        "formatEUR": func(cents int) string {
            return fmt.Sprintf("%.2f EUR", float64(cents)/100)
        },
    }),
)
http.ListenAndServe(":4242", router.Handler())
```

`gastro.New(opts...)` returns a `*Router` whose `Handler()` you mount on
your HTTP server. Other options include `WithDeps[T]` for typed dependency
injection into pages, `WithOverride(pattern, handler)` for replacing an
auto-generated route with a Go handler, `WithMiddleware(pattern, fn)` for
wrapping routes (subtree wildcards via `"/admin/{path...}"`),
`WithRequestFuncs(binder)` for **request-aware template helpers** (i18n
translators, CSRF tokens, CSP nonces — see [Helpers → Request-aware
helpers](docs/helpers.md#request-aware-helpers-withrequestfuncs)), and
`WithErrorHandler(fn)` for custom render-error responses (logging, error
tracking, branded 500 pages). See [Pages & Routing](docs/pages.md) for
the full API and [Error Handling](docs/error-handling.md) for the
failure-mode catalogue.

> The legacy `gastro.Routes(opts...) http.Handler` one-shot is retained as
> a deprecated shim around `gastro.New(opts...).Handler()`. Prefer `New()`
> in new code.

### Deployment

`gastro build` produces a single binary. Deploy by copying it to the server.
Templates and static assets (`static/`) are baked into the binary via
`//go:embed`, so the deployed binary is self-contained — no
`templates/` or `static/` directories need to ship alongside it. In
dev mode (`GASTRO_DEV=1`, set automatically by `gastro dev`) both are
read from disk so edits hot-reload.

```sh
gastro generate
GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o dist/myapp .
scp dist/myapp server:/opt/myapp
```

## Editor Support

Editor intelligence requires two binaries in your PATH:

- **`gastro`** -- includes the Gastro language server (`gastro lsp` subcommand)
- **`gopls`** -- the Go language server (for frontmatter Go intelligence)

```sh
go install github.com/andrioid/gastro/cmd/gastro@latest
go install golang.org/x/tools/gopls@latest
```

If `gopls` is not in PATH, `gastro lsp` will still provide template body
completions (variables, components, functions) but frontmatter Go intelligence
(completions, hover, diagnostics) will not be available.

### VS Code

The extension is in `editors/vscode/`. To install locally:

```sh
cd editors/vscode
npm install   # required -- installs vscode-languageclient dependency
# Symlink into VS Code extensions
ln -s "$(pwd)" ~/.vscode/extensions/gastro-vscode
```

Then reload VS Code.

### Neovim

Copy or symlink `editors/neovim/gastro.lua` to
`~/.config/nvim/after/plugin/gastro.lua`. The LSP starts automatically for
`.gastro` files. Requires `nvim-treesitter` for syntax highlighting.

### Zed

A Zed extension is in `editors/zed/`. It auto-downloads `gastro` from
GitHub releases on first use. To install as a dev extension:

1. Open Zed's command palette and run "zed: install dev extension"
2. Select the `editors/zed/` directory

## Example

See `examples/blog/` for a complete working blog with file-based routing,
dynamic pages, template helpers, and static assets.

## Documentation

- [Getting Started — Framework Mode](docs/getting-started.md) -- New gastro projects
- [Getting Started — Library Mode](docs/getting-started-library.md) -- Add gastro to an existing Go project
- [Pages](docs/pages.md) -- Page authoring guide and API reference
- [Components](docs/components.md) -- Component authoring guide and API reference
- [Markdown](docs/markdown.md) -- Embedding markdown via `//gastro:embed` and rendering with goldmark
- [Dev Mode](docs/dev-mode.md) -- `gastro dev`, `gastro watch`, env vars, composed setups
- [Error Handling](docs/error-handling.md) -- Failure modes and `WithErrorHandler`
- [Design](docs/design.md) -- Complete design document with all decisions
- [Architecture](docs/architecture.md) -- Code architecture and package guide
- [Contributing](docs/contributing.md) -- How to contribute

## License

MIT
