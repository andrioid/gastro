# Server-Sent Events (SSE)

Gastro provides a lightweight SSE helper for streaming events from the server
to the browser. This enables real-time UI updates with libraries like
[Datastar](https://data-star.dev/) and [HTMX](https://htmx.org/).

## How it works

1. A gastro page renders the initial HTML (as usual)
2. The HTML includes client-side attributes that open an SSE connection to an
   API endpoint (e.g. Datastar's `data-on:click="@get('/api/endpoint')"`)
3. Your Go handler writes SSE events that patch the DOM

The SSE endpoints are plain Go HTTP handlers -- no compiler changes needed.
Register them alongside gastro routes in your `main.go`.

## Generic SSE (`pkg/gastro`)

The core SSE helper is framework-agnostic. It works with any client that
consumes `text/event-stream`.

### `NewSSE(w, r) *SSE`

Upgrades an `http.ResponseWriter` to an SSE stream. Sets `Content-Type`,
`Cache-Control`, and `Connection` headers automatically.

### `Send(eventType string, data ...string) error`

Writes a single SSE event and flushes it to the client. Each `data` argument
becomes a `data:` line in the event.

### `IsClosed() bool`

Reports whether the client has disconnected.

### `Context() context.Context`

Returns the request context, useful for `select` loops that wait for
disconnection.

### Example: generic SSE

```go
func handleUpdates(w http.ResponseWriter, r *http.Request) {
    sse := gastro.NewSSE(w, r)

    ticker := time.NewTicker(1 * time.Second)
    defer ticker.Stop()

    for {
        select {
        case <-sse.Context().Done():
            return
        case <-ticker.C:
            now := time.Now().Format("15:04:05")
            sse.Send("time", now)
        }
    }
}
```

The SSE is also available via the page context:

```go
ctx := gastro.Context()
sse := ctx.SSE()
sse.Send("ping", "hello")
```

## Datastar subpackage (`pkg/gastro/datastar`)

The `datastar` subpackage provides convenience methods that format events
using Datastar's SSE protocol. It wraps the generic SSE helper.

### `NewSSE(w, r) *SSE`

Same as the generic version, but returns a Datastar-specific `*SSE` with
additional methods.

### `PatchElements(html string, opts ...PatchOption) error`

Sends a `datastar-patch-elements` event. The HTML should contain elements
with `id` attributes for DOM morphing.

```go
sse := datastar.NewSSE(w, r)
sse.PatchElements(`<div id="count">42</div>`)
```

Options:

- `WithSelector(sel)` -- target a specific CSS selector
- `WithMode(mode)` -- patch mode: `ModeOuter` (default), `ModeInner`,
  `ModeAppend`, `ModePrepend`, `ModeBefore`, `ModeAfter`, `ModeReplace`,
  `ModeRemove`

```go
sse.PatchElements(`<li>New item</li>`,
    datastar.WithSelector("#list"),
    datastar.WithMode(datastar.ModeAppend),
)
```

### `PatchSignals(signals any) error`

Sends a `datastar-patch-signals` event. The signals value is JSON-encoded.

```go
sse.PatchSignals(map[string]any{"count": 42, "loading": false})
```

### `RemoveElement(selector string) error`

Removes an element from the DOM by CSS selector.

```go
sse.RemoveElement("#toast-1")
```

## Wiring it up

SSE endpoints are standard Go HTTP handlers. Create a top-level `http.ServeMux`
and mount both your API routes and gastro page routes:

```go
func main() {
    mux := http.NewServeMux()

    // API/SSE endpoints first (more specific patterns win)
    mux.HandleFunc("GET /api/increment", handleIncrement)
    mux.HandleFunc("GET /api/clock", handleClock)

    // Gastro page routes (catch-all)
    mux.Handle("/", gastro.Routes())

    http.ListenAndServe(":4242", mux)
}
```

## Datastar page example

A gastro page with Datastar attributes:

```gastro
---
import Layout "components/layout.gastro"
Title := "Counter"
---
{{ wrap Layout (dict "Title" .Title) }}
    <div id="count">0</div>
    <button data-on:click="@get('/api/increment')">+1</button>
{{ end }}
```

The layout includes the Datastar script:

```gastro
---
type Props struct { Title string }
Title := gastro.Props().Title
---
<!DOCTYPE html>
<html>
<head>
    <title>{{ .Title }}</title>
    <script type="module" src="https://cdn.jsdelivr.net/gh/starfederation/datastar@v1/bundles/datastar.js"></script>
</head>
<body>{{ .Children }}</body>
</html>
```

The increment handler:

```go
var count atomic.Int64

func handleIncrement(w http.ResponseWriter, r *http.Request) {
    n := count.Add(1)
    sse := datastar.NewSSE(w, r)
    sse.PatchElements(fmt.Sprintf(`<div id="count">%d</div>`, n))
}
```

See the full working example in `examples/sse/`.

## Rendering components for SSE (`gastro.Render`)

The compiler generates a `Render` API that lets you render gastro components
from SSE handlers with full type safety. Instead of hand-writing HTML strings,
reuse the same component templates used in your pages.

### Type-safe rendering

Each component with a `Props` struct gets a typed `Render` method:

```go
html, err := gastro.Render.Counter(gastro.CounterProps{Count: 42})
```

Components without Props (that fetch their own data) get a zero-argument method:

```go
html, err := gastro.Render.OrdersTable()
```

Components with slots accept optional children:

```go
inner, _ := gastro.Render.Counter(gastro.CounterProps{Count: 42})
full, _ := gastro.Render.Layout(
    gastro.LayoutProps{Title: "Dashboard"},
    template.HTML(inner),
)
```

### How it works

The compiler generates `render.go` in `.gastro/` with:

- `var Render = &renderAPI{}` -- a struct with one method per component
- `type CounterProps = componentCounterProps` -- exported type aliases

Each `Render` method converts the typed props to a map and calls the internal
component function, which runs the full frontmatter logic (data fetching,
derived values, etc.) and renders the template.

### Example: SSE handler with Render

```go
func handleIncrement(w http.ResponseWriter, r *http.Request) {
    n := count.Add(1)

    html, err := gastro.Render.Counter(gastro.CounterProps{Count: int(n)})
    if err != nil {
        http.Error(w, err.Error(), 500)
        return
    }

    sse := datastar.NewSSE(w, r)
    sse.PatchElements(html)
}
```

### Compile-time safety

| What             | Safety       |
|------------------|-------------|
| Method name      | Compile-time -- method exists or doesn't |
| Props fields     | Compile-time -- struct fields checked by Go |
| Props types      | Compile-time -- Go type system |

Typos in component names, field names, or field types are all caught at compile
time.

## Design notes

- **No external dependencies.** The SSE protocol is ~90 lines of Go. We
  intentionally avoid the Datastar Go SDK to keep gastro dependency-free.
- **SSE endpoints are plain Go handlers.** The gastro compiler generates page
  routes; API routes are registered manually in `main.go`.
- **Two layers.** The generic `pkg/gastro` SSE works with any SSE client.
  The `pkg/gastro/datastar` subpackage adds Datastar-specific formatting.
- **Render wraps internal functions.** Each `Render` method calls the internal
  component function, preserving all frontmatter logic (data fetching, derived
  values, etc.).
