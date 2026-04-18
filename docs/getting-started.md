# Getting Started

Set up your first Gastro project in under a minute. You'll need Go 1.26+ installed.

## Install

Install the `gastro` CLI with [mise](https://mise.jdx.dev/) (recommended) or `go install`:

```bash
# With mise (recommended)
mise use github:andrioid/gastro@latest

# Or with go install
go install github.com/andrioid/gastro/cmd/gastro@latest
```

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
  pages/           Page templates (.gastro files)
  components/      Reusable components (.gastro files)
  static/          CSS, images, and other static assets
  main.go          Application entry point
  go.mod           Go module file
```

## Your First Page

The scaffolded `pages/index.gastro` shows the basic file format. The code between `---` delimiters is Go frontmatter that runs on the server. The HTML below is rendered with Go's `html/template`.

```go
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

```go
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

```go
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

## Build for Production

Build a single static binary for deployment:

```bash
gastro build
./app
```

The binary embeds all templates and static assets. See [Deployment](/docs/deployment) for Docker and other options.
