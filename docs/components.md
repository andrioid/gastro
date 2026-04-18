# Components

Components are reusable `.gastro` files in the `components/` directory. They accept typed props and can render children.

## Defining a Component

A component uses `gastro.Props()` to declare its props type. The `Props` struct defines what the component accepts:

```go
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

```go
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

```go
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

```go
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

```go
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

```go
{{ wrap Layout (dict "Title" "Home") }}
    <h1>Welcome</h1>
    <p>This becomes the children content.</p>
{{ end }}
```

Children are rendered in the **parent's** data context, so they can reference the parent's template data. Only one `{{ .Children }}` is supported per component.
