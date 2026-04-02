# Components

Components are reusable `.gastro` files in the `components/` directory. They
accept typed props, can render children via slots, and are invoked from pages
or other templates using Go template actions.

## Defining a component

A component file uses `gastro.Props()` to declare its props type:

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

`gastro.Props()` is a compile-time marker (like `gastro.Context()` for
pages). It tells the code generator that this file is a component and what
props it accepts. The `Props` struct type must be defined in the same
frontmatter.

## Props

### Defining the struct

Props are defined as a Go struct in the frontmatter. Field names must start
with an uppercase letter (they are exported Go fields):

```go
type Props struct {
    Title  string
    Body   string
    Count  int
    Active bool
}
```

### Accessing props

`gastro.Props()` returns a value of the `Props` struct type. Access fields
directly and assign to uppercase variables to make them available in the
template:

```gastro
---
type Props struct {
    Title string
    Slug  string
}

Title := gastro.Props().Title
Slug := gastro.Props().Slug
---
<a href="/blog/{{ .Slug }}">{{ .Title }}</a>
```

If you need computed values from multiple props, assign the whole struct
first:

```gastro
---
type Props struct {
    Label string
    X     int
}

p := gastro.Props()
Label := p.Label
CX := fmt.Sprintf("%d", p.X + 135)
---
```

### Type coercion

When props are passed from templates, Gastro automatically coerces values to
match the struct field types:

| Target type | Accepted values |
|-------------|-----------------|
| `string` | Any value (converted via `fmt.Sprintf`) |
| `bool` | `bool`, `string` (`"true"`, `"false"`) |
| `int` | `int`, `int64`, `float64`, `float32`, `string` (parsed) |
| `float64` | `float64`, `float32`, `int`, `string` (parsed) |

If the types match directly, no coercion is needed.

## Using components

### Importing

Use the `import` declaration in the frontmatter to import a component:

```
import Layout "components/layout.gastro"
import PostCard "components/post-card.gastro"
```

Or grouped together with Go imports:

```
import (
    "myblog/db"

    Layout "components/layout.gastro"
    PostCard "components/post-card.gastro"
)
```

Component imports are distinguished from Go imports by the `.gastro` file
extension. The identifier before the path is the local name you'll use in
the template. The string is the path to the component file, relative to the
project root.

### Invoking in templates

Components are invoked with Go template actions. Leaf components (no children)
use bare function calls:

```
{{ PostCard (dict "Title" .Title "Slug" .Slug) }}
```

Use `wrap` for components that accept children, closed by `{{ end }}`:

```
{{ wrap Layout (dict "Title" .Title) }}
    <h1>Hello</h1>
    <p>This content goes into the slot.</p>
{{ end }}
```

### Prop syntax

Props are passed using `dict` syntax inside the template action:

| Syntax | Meaning | Example |
|--------|---------|---------|
| `.Expr` | Go template expression, evaluated in the parent's data context | `"Title" .Title` |
| `"literal"` | String literal | `"Title" "About"` |
| `(.Val \| func "arg")` | Pipe expression | `"Date" (.CreatedAt \| timeFormat "Jan 2, 2006")` |

Expressions have access to the parent page's template data (the
uppercase variables from the parent's frontmatter).

## Slots

Slots let a component render content provided by its parent. Place `{{ .Children }}`
in the component template where children should appear:

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

The parent passes children by using `wrap` with the component:

```
{{ wrap Layout (dict "Title" "Home") }}
    <h1>Welcome</h1>
    <p>This replaces the slot.</p>
{{ end }}
```

Children are rendered in the **parent's** data context, so they can reference
the parent's template data. The rendered HTML is then inserted where `{{ .Children }}`
appears in the component.

Only unnamed slots are supported. A component can have one `{{ .Children }}`.

## Complete example

### Component: `components/layout.gastro`

```gastro
---
type Props struct {
    Title string
}

Title := gastro.Props().Title
---
<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <title>{{ .Title }} - My Blog</title>
    <link rel="stylesheet" href="/static/styles.css">
</head>
<body>
    <nav>
        <a href="/">Home</a>
        <a href="/about">About</a>
        <a href="/blog">Blog</a>
    </nav>
    <main>
        {{ .Children }}
    </main>
    <footer><p>Built with Gastro</p></footer>
</body>
</html>
```

### Component: `components/post-card.gastro`

```gastro
---
type Props struct {
    Slug   string
    Title  string
    Author string
    Date   string
}

p := gastro.Props()
Slug := p.Slug
Title := p.Title
Author := p.Author
Date := p.Date
---
<article class="post-card">
    <h2><a href="/blog/{{ .Slug }}">{{ .Title }}</a></h2>
    <p class="meta">By {{ .Author }} on {{ .Date }}</p>
</article>
```

### Page using both: `pages/index.gastro`

```gastro
---
import (
    "myblog/db"

    Layout "components/layout.gastro"
    PostCard "components/post-card.gastro"
)

ctx := gastro.Context()
posts, err := db.ListPublished()
if err != nil {
    ctx.Error(500, "Failed to load posts")
    return
}

Posts := posts
Title := "Home"
---
{{ wrap Layout (dict "Title" .Title) }}
    <h1>Welcome to My Blog</h1>
    <section>
        {{ range .Posts }}
        {{ PostCard (dict "Slug" .Slug "Title" .Title "Author" .Author "Date" (.CreatedAt | timeFormat "Jan 2, 2006")) }}
        {{ end }}
    </section>
{{ end }}
```
