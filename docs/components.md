# Components

Components are reusable `.gastro` files in the `components/` directory. They
accept typed props, can render children via slots, and are invoked from pages
or other templates using HTML-like syntax.

## Defining a component

A component file uses `gastro.Props[T]()` to declare its props type:

```gastro
---
type Props struct {
    Title  string
    Author string
}

props := gastro.Props[Props]()
Title := props.Title
Author := props.Author
---
<article>
    <h2>{{ .Title }}</h2>
    <p>By {{ .Author }}</p>
</article>
```

`gastro.Props[T]()` is a compile-time marker (like `gastro.Context()` for
pages). It tells the code generator that this file is a component and what
props it accepts. The generic parameter `T` must be a struct type defined in
the same frontmatter.

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

`gastro.Props[T]()` returns a value of type `T`. Assign fields to uppercase
variables to make them available in the template:

```gastro
---
type Props struct {
    Title string
    Slug  string
}

props := gastro.Props[Props]()
Title := props.Title
Slug := props.Slug
---
<a href="/blog/{{ .Slug }}">{{ .Title }}</a>
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

Use the `use` declaration in the frontmatter to import a component:

```
use Layout "components/layout.gastro"
use PostCard "components/post-card.gastro"
```

The first word after `use` is the local name you'll use in the template. The
string is the path to the component file, relative to the project root.

### Invoking in templates

Components are invoked with HTML-like syntax. Self-closing for components
without children:

```html
<PostCard Title={.Title} Slug={.Slug} />
```

With opening and closing tags for components that accept children:

```html
<Layout Title={.Title}>
    <h1>Hello</h1>
    <p>This content goes into the slot.</p>
</Layout>
```

### Prop syntax

Props are passed as attributes on the component tag:

| Syntax | Meaning | Example |
|--------|---------|---------|
| `{.Expr}` | Go template expression, evaluated in the parent's data context | `Title={.Title}` |
| `"literal"` | String literal | `Title="About"` |
| `{.Val \| func "arg"}` | Pipe expression | `Date={.CreatedAt \| timeFormat "Jan 2, 2006"}` |

Expressions inside `{}` have access to the parent page's template data (the
uppercase variables from the parent's frontmatter).

## Slots

Slots let a component render content provided by its parent. Place `<slot />`
in the component template where children should appear:

```gastro
---
type Props struct {
    Title string
}

props := gastro.Props[Props]()
Title := props.Title
---
<html>
<head><title>{{ .Title }}</title></head>
<body>
    <nav>...</nav>
    <main>
        <slot />
    </main>
    <footer>...</footer>
</body>
</html>
```

The parent passes children by wrapping content in the component tags:

```html
<Layout Title="Home">
    <h1>Welcome</h1>
    <p>This replaces the slot.</p>
</Layout>
```

Children are rendered in the **parent's** data context, so they can reference
the parent's template data. The rendered HTML is then inserted where `<slot />`
appears in the component.

Only unnamed slots are supported. A component can have one `<slot />`.

## Complete example

### Component: `components/layout.gastro`

```gastro
---
type Props struct {
    Title string
}

props := gastro.Props[Props]()
Title := props.Title
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
        <slot />
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
props := gastro.Props[Props]()
Slug := props.Slug
Title := props.Title
Author := props.Author
Date := props.Date
---
<article class="post-card">
    <h2><a href="/blog/{{ .Slug }}">{{ .Title }}</a></h2>
    <p class="meta">By {{ .Author }} on {{ .Date }}</p>
</article>
```

### Page using both: `pages/index.gastro`

```gastro
---
import "myblog/db"

use Layout "components/layout.gastro"
use PostCard "components/post-card.gastro"

ctx := gastro.Context()
posts, err := db.ListPublished()
if err != nil {
    ctx.Error(500, "Failed to load posts")
    return
}

Posts := posts
Title := "Home"
---
<Layout Title={.Title}>
    <h1>Welcome to My Blog</h1>
    <section>
        {{ range .Posts }}
        <PostCard Slug={.Slug} Title={.Title} Author={.Author} Date={.CreatedAt | timeFormat "Jan 2, 2006"} />
        {{ end }}
    </section>
</Layout>
```
