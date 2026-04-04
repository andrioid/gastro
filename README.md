# Gastro

A file-based component framework for Go. `.gastro` files combine Go frontmatter
with `html/template` markup in a single file, compiled to type-safe Go code with
file-based routing.

Think: Astro's developer experience, Go's type safety, PHP's file-based routing.

**Status:** Early development. The core pipeline works end-to-end (parse, codegen,
route, build, serve). Editor support is in progress.


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

### Prerequisites

- Go 1.26+
- [mise](https://mise.jdx.dev/) (optional, for managed tooling)

```sh
# If using mise, install tools
mise install

# Build the gastro CLI
go build -o gastro ./cmd/gastro/
```

### Create a Project

```
myapp/
  pages/
    index.gastro
  static/
    styles.css
  main.go
  go.mod
```

**pages/index.gastro:**

```gastro
---
import "time"

Title := "Hello, Gastro"
Year := time.Now().Year()
---
<!DOCTYPE html>
<html>
<head><title>{{ .Title }}</title></head>
<body>
    <h1>{{ .Title }}</h1>
    <p>Copyright {{ .Year }}</p>
</body>
</html>
```

**main.go:**

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
    fmt.Printf("Listening on http://localhost:%s\n", port)
    http.ListenAndServe(":"+port, gastro.Routes())
}
```

### Build and Run

```sh
# Generate Go code from .gastro files
gastro generate

# Build the binary
go build -o myapp .

# Run
./myapp
```

Or in one step:

```sh
gastro build
./app
```

### Development Mode

```sh
gastro dev
# Watches for changes, rebuilds, restarts server on :4242
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
ctx := gastro.Context()       // lowercase -> private
err := doSomething()          // lowercase -> private

Title := "Hello"              // Uppercase -> {{ .Title }}
Items := fetchItems()         // Uppercase -> {{ .Items }}
---
<h1>{{ .Title }}</h1>
```

### File-Based Routing

| File                       | Route             |
|----------------------------|-------------------|
| `pages/index.gastro`       | `GET /`           |
| `pages/about/index.gastro` | `GET /about`      |
| `pages/blog/[slug].gastro` | `GET /blog/{slug}` |

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
gastro.Routes(
    gastro.WithFuncs(template.FuncMap{
        "formatEUR": func(cents int) string {
            return fmt.Sprintf("%.2f EUR", float64(cents)/100)
        },
    }),
)
```

### Deployment

`gastro build` produces a single binary. Deploy by copying it to the server.

**Note:** Embedding of templates and static assets via `//go:embed` is designed
but not yet wired into the compiler. For now, templates are compiled into the
Go source at generation time. Static assets in `static/` are served from disk
at the `/static/` URL prefix.

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

- [Design](docs/design.md) -- Complete design document with all decisions
- [Architecture](docs/architecture.md) -- Code architecture and package guide
- [Contributing](docs/contributing.md) -- How to contribute
- [Pages](docs/pages.md) -- Page authoring guide and API reference
- [Components](docs/components.md) -- Component authoring guide and API reference

## License

MIT
