# Components

Components are reusable `.gastro` files in the `components/` directory. They accept typed props and can render children.

## Defining a Component

A component uses `gastro.Props()` to declare its props type. The `Props` struct defines what the component accepts:

```gastro
---
type Props struct {
    Title  string
    Author string
}

Title := gastro.Props().Title
Author := gastro.Props().Author
---
<article>
    <h2>{{ .Title }}</h2>
    <p>By {{ .Author }}</p>
</article>
```

`gastro.Props()` is a compile-time marker that tells the code generator this file is a component. The `Props` struct must be defined in the same frontmatter.

## Computed Values

When you need derived values from multiple props, assign the whole struct first:

```gastro
---
import "fmt"

type Props struct {
    Label string
    X     int
}

p := gastro.Props()
Label := p.Label
CX := fmt.Sprintf("%d", p.X + 135)
---
<text x="{{ .CX }}">{{ .Label }}</text>
```

## Importing & Using Components

Import components in the frontmatter with the `.gastro` file extension. The identifier is the local name used in the template:

```gastro
---
import (
    Layout "components/layout.gastro"
    PostCard "components/post-card.gastro"
)

ctx := gastro.Context()
---
{{ wrap Layout (dict "Title" "Home") }}
    {{ PostCard (dict "Title" "My Post" "Slug" "my-post") }}
{{ end }}
```

## Prop Syntax

Props are passed as attributes on the component tag:

```gastro
<!-- Template expression -->
{{ PostCard (dict "Title" .Title "Slug" .Slug) }}

<!-- String literal -->
{{ Layout (dict "Title" "About") }}

<!-- Pipe expression -->
{{ PostCard (dict "Date" (.CreatedAt | timeFormat "Jan 2, 2006")) }}
```

| Syntax | Meaning |
|--------|---------|
| `{.Expr}` | Go template expression, evaluated in parent's data context |
| `"literal"` | String literal |
| `{.Val \| func "arg"}` | Pipe expression |

## Type Coercion

Gastro automatically coerces prop values to match struct field types:

| Target Type | Accepted Values |
|-------------|-----------------|
| `string` | Any value (converted via `fmt.Sprintf`) |
| `bool` | `bool`, `string` ("true", "false") |
| `int` | `int`, `int64`, `float64`, `string` (parsed) |
| `float64` | `float64`, `float32`, `int`, `string` (parsed) |

## Children

Children let a component render content provided by its parent. Place `{{ .Children }}` where children should appear:

```gastro
---
type Props struct {
    Title string
}

Title := gastro.Props().Title
---
<html>
<head><title>{{ .Title }}</title></head>
<body>
    <nav>...</nav>
    <main>
        {{ .Children }}
    </main>
    <footer>...</footer>
</body>
</html>
```

The parent passes children by wrapping content in the component tags:

```gastro
{{ wrap Layout (dict "Title" "Home") }}
    <h1>Welcome</h1>
    <p>This becomes the children content.</p>
{{ end }}
```

Children are rendered in the **parent's** data context, so they can reference the parent's template data. Only one `{{ .Children }}` is supported per component.

## Calling Components from Go

Components can be rendered directly from Go code — useful for SSE handlers that
patch the DOM with fresh component markup, for tests, or for any handler that
produces HTML outside the page-routing flow.

For every component in `components/`, gastro generates a typed method on the
package-level `Render` value:

```go
import gastro "myapp/.gastro"

html, err := gastro.Render.Card(gastro.CardProps{
    Title: "Hello",
    Body:  "World",
})
if err != nil {
    // handle
}
```

Components with children take an optional `template.HTML` argument:

```go
html, err := gastro.Render.Layout(
    gastro.LayoutProps{Title: "Home"},
    template.HTML("<h1>Welcome</h1>"),
)
```

`Render` lives in the generated `.gastro/render.go`. Each method calls the same
underlying component function used by the template renderer, so frontmatter
logic (computed values, validation) runs identically whether a component is
invoked from a template or from Go.

### When to use Render vs Routes

| Goal | Use |
|------|-----|
| Mount file-based page routes on an HTTP server | `gastro.Routes()` |
| Render a single component to an HTML string | `gastro.Render.<Name>(...)` |

A typical SSE example combines both:

```go
mux := http.NewServeMux()
mux.HandleFunc("GET /api/increment", func(w http.ResponseWriter, r *http.Request) {
    n := count.Add(1)
    html, _ := gastro.Render.Counter(gastro.CounterProps{Count: int(n)})
    sse := datastar.NewSSE(w, r)
    sse.PatchElements(html)
})
mux.Handle("/", gastro.Routes())
```

See `examples/sse/` for the full version.
