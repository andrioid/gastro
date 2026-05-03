# Getting Started — Framework Mode

Set up your first Gastro project in under a minute. You'll need Go 1.26+ installed.

This page is for **building a new Gastro site from scratch**. If you're adding
gastro to an existing Go project (an API service growing an admin UI, an
internal tool, a status page, etc.), see [Getting Started — Library
Mode](getting-started-library.md) instead. The runtime is identical between
modes; what differs is the bootstrap and the dev-loop command.

## Install

The `gastro` CLI acts as a dev server, code generator and language server
(LSP). There are three equivalent ways to install it. Pick one:

```bash
# Option A: go install -- global, no extra tooling
go install github.com/andrioid/gastro/cmd/gastro@latest

# Option B: mise -- global, easy upgrades, version pinned per-tool
mise use -g github:andrioid/gastro@latest

# Option C: go tool -- per-project version pin in go.mod, no global install
# Run inside an existing Go module. `gastro new` (below) sets this up for you.
go get -tool github.com/andrioid/gastro/cmd/gastro
```

With Option C the CLI is invoked as `go tool gastro <cmd>` instead of
`gastro <cmd>`. The version is pinned alongside your other module
dependencies, which is convenient for CI/CD and for teams that want every
contributor on the same gastro version without a separate install step.

### Project-local CLI via `go tool`

When you run `gastro new`, the generated `go.mod` includes:

```
tool github.com/andrioid/gastro/cmd/gastro
```

and the generated `main.go` includes:

```go
//go:generate go tool gastro generate
```

So you can use `go tool gastro <cmd>` and `go generate ./...` immediately
after scaffolding, without installing the CLI globally. If you also have
`gastro` on `PATH`, both styles work and stay equivalent (the `tool`
directive just gives you a project-pinned version).

To add the directive to an existing project, run
`go get -tool github.com/andrioid/gastro/cmd/gastro` inside the module.

To add Gastro support in your IDE, you can install one of our extensions.

- [Gastro for Visual Studio Code](https://marketplace.visualstudio.com/items?itemName=andrioid.gastro-vscode)

You can also configure the language-server manually by setting up your IDE (or coding-agent) to use `gastro lsp` as the LSP.

## Create a Project

Scaffold a new project and start the dev server:

```bash
gastro new myapp
cd myapp
gastro dev
```

Open [http://localhost:4242](http://localhost:4242) in your browser. Edit `pages/index.gastro` and watch it reload automatically.

## Project Structure

`gastro new` creates this layout:

```text
myapp/
  pages/           Pages (.gastro files)
  components/      Reusable components (.gastro files)
  static/          Assets, such as images and css
  main.go          Application entry point
  go.mod           Go module file
```

Your project is still a Go project. Gastro generates routes and templates inside of `.gastro/`. The gastro folders are special, but otherwise you can organize your project as you see fit.

> **Note:** `pages/` is optional for component-only projects (e.g. when gastro is embedded inside a larger module and you use it solely for its component rendering and static asset serving).

## Your First Page

The scaffolded `pages/index.gastro` shows the basic file format. The code between `---` delimiters is Go frontmatter that runs on the server. The HTML below is rendered with Go's `html/template`.

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

Uppercase variables like `Title` and `Year` are automatically exported to the template as `{{ .Title }}` and `{{ .Year }}`. Lowercase variables stay private.

## Your First Component

Components are reusable `.gastro` files in the `components/` directory. Create `components/greeting.gastro`:

```gastro
---
type Props struct {
    Name string
}

Name := gastro.Props().Name
---
<section>
    <h2>Hello, {{ .Name }}!</h2>
    <p>This is a Gastro component with typed props.</p>
</section>
```

Components use `gastro.Props()` to declare typed props. The `Props` struct defines what the component accepts.

Now import and use it in `pages/index.gastro`:

```gastro
---
import (
    Greeting "components/greeting.gastro"
)

Title := "Welcome to Gastro"
---
<!DOCTYPE html>
<html>
<head><title>{{ .Title }}</title></head>
<body>
    <h1>{{ .Title }}</h1>
    {{ Greeting (dict "Name" "World") }}
</body>
</html>
```

Props are passed with `dict`. The dev server picks up the new component automatically — no restart needed.

## Explore Your Project

`gastro list` prints all components and pages with their Props signatures — useful for orientation in an unfamiliar project:

```sh
gastro list
# [component]  Card   (Title string, Body string)  components/card.gastro
# [page]       Index                                pages/index.gastro

gastro list --json   # machine-readable output for scripts and agents
```

## Build for Production

Build a single static binary for deployment:

```bash
gastro build       # or: go tool gastro build
./app
```

The binary embeds all templates and static assets. See [Deployment](/docs/deployment) for Docker and other options.
