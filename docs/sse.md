# SSE & Datastar

Gastro provides a lightweight SSE helper for streaming events from the server to the browser, enabling real-time UI updates with [Datastar](https://data-star.dev/) and [HTMX](https://htmx.org/).

## How It Works

1. A Gastro page renders the initial HTML (as usual)
2. Client-side attributes open an SSE connection to an API endpoint
3. Your Go handler writes SSE events that patch the DOM

SSE endpoints are plain Go HTTP handlers — no compiler changes needed. Register them alongside Gastro routes in your `main.go`.

## Generic SSE

The core SSE helper in `pkg/gastro` is framework-agnostic. It works with any client that consumes `text/event-stream`:

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

Methods available on the SSE helper:

- **`Send(eventType, data ...string)`** — writes and flushes a single SSE event
- **`IsClosed()`** — reports whether the client disconnected
- **`Context()`** — returns the request context for `select` loops

## Datastar Integration

The `pkg/gastro/datastar` subpackage formats events using Datastar's SSE protocol:

```go
var count atomic.Int64

func handleIncrement(w http.ResponseWriter, r *http.Request) {
    n := count.Add(1)

    html, err := gastro.Render.Counter(
        gastro.CounterProps{Count: int(n)},
    )
    if err != nil {
        http.Error(w, err.Error(), 500)
        return
    }

    sse := datastar.NewSSE(w, r)
    sse.PatchElements(html)
}
```

### Datastar Page

Add Datastar attributes to your `.gastro` pages to trigger SSE connections:

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

### Patch Options

Datastar supports selectors, patch modes, and signal patching:

```go
sse.PatchElements(html,
    datastar.WithSelector("#dashboard"),
    datastar.WithMode(datastar.ModeInner),
)

sse.PatchSignals(map[string]any{
    "count": 42, "loading": false,
})

sse.RemoveElement("#toast-1")
```

## Wiring It Up

Create a top-level `http.ServeMux` and mount both API routes and Gastro page routes:

```go
func main() {
    mux := http.NewServeMux()

    // API/SSE endpoints first
    mux.HandleFunc("GET /api/increment", handleIncrement)
    mux.HandleFunc("GET /api/clock", handleClock)

    // Gastro page routes (catch-all)
    router := gastro.New()
    mux.Handle("/", router.Handler())

    http.ListenAndServe(":4242", mux)
}
```

`gastro.Routes()` (the legacy one-shot) still works and is equivalent to
`gastro.New().Handler()`; new code should prefer `New()` because it
returns a `*Router` value that exposes typed dependency injection
(`WithDeps`), per-route overrides (`WithOverride`), and direct mux
access (`Mux()`).

## Type-Safe Rendering

The compiler generates a `Render` API for calling Gastro components from SSE handlers with full type safety:

```go
// Each component gets a typed Render method
html, err := gastro.Render.Counter(
    gastro.CounterProps{Count: 42},
)

// Components that accept children
inner, _ := gastro.Render.Counter(
    gastro.CounterProps{Count: 42},
)
full, _ := gastro.Render.Layout(
    gastro.LayoutProps{Title: "Dashboard"},
    template.HTML(inner),
)
```

| What | Safety |
|------|--------|
| Method name | Compile-time — method exists or doesn't |
| Props fields | Compile-time — struct fields checked by Go |
| Props types | Compile-time — Go type system |

## Design Notes

- **No external dependencies.** The SSE protocol is ~90 lines of Go.
- **Two layers.** Generic `pkg/gastro` works with any SSE client. `pkg/gastro/datastar` adds Datastar-specific formatting.
- **Render wraps internal functions.** Each method calls the internal component function, preserving all frontmatter logic.

[Try the live Datastar demo →](/examples/guestbook-datastar)
