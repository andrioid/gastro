# SSE & Datastar

Gastro provides a lightweight SSE helper for streaming events from the
server to the browser, enabling real-time UI updates with
[Datastar](https://data-star.dev/) and [HTMX](https://htmx.org/).

There are two ways to wire SSE into a Gastro app:

1. **From a page** (Track B's headline pattern). The same `.gastro`
   file renders the initial HTML on `GET` and emits SSE patches on
   `POST` (or any non-GET) by branching on `r.Method`.
2. **From a side-mounted handler** registered on the router's mux.
   Useful for long-lived streams (live clocks, log tails) that don't
   share state with a particular page.

## SSE-from-page (the headline)

A single `pages/counter.gastro` handles both the initial render and
the click that increments the counter:

```gastro
---
import (
    "net/http"

    "myapp/app"

    Layout "components/layout.gastro"
    Counter "components/counter.gastro"

    "github.com/andrioid/gastro/pkg/gastro/datastar"
)

state := gastro.From[*app.State](r.Context())

if r.Method == http.MethodPost {
    n := state.Count.Add(1)

    html, err := gastro.Render.Counter(CounterProps{Count: int(n)})
    if err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }

    sse := datastar.NewSSE(w, r)
    sse.PatchElements(html)
    return
}

Title := "Counter"
Count := int(state.Count.Load())
---
{{ wrap Layout (dict "Title" .Title) }}
    {{ Counter (dict "Count" .Count) }}
    <button data-on:click="@post('/counter')">+1</button>
{{ end }}
```

What happens at runtime:

- **`GET /counter`** — `r.Method == "POST"` is false; the if-block is
  skipped. `Title` and `Count` are computed. The codegen-wrapped
  writer's body-written flag is still false, so the template renders.
- **`POST /counter`** — `r.Method == "POST"` is true. The Counter is
  rendered to a typed `template.HTML`, an SSE stream is opened, the
  patch event is emitted, and `return` exits the frontmatter.
  The body-written flag is now true, so the template render is
  **skipped**.

This pattern is exercised end-to-end in
[`examples/sse`](https://github.com/andrioid/gastro/tree/main/examples/sse).

### Required Imports

The frontmatter above pulls in the runtime alias and the `net/http`
package. `net/http` is auto-imported by the codegen so you don't
strictly need to declare it; `app` and the Datastar helper do.

### Mounting

`main.go` becomes:

```go
package main

import (
    "log"
    "net/http"

    gastro "myapp/.gastro"
    "myapp/app"
)

func main() {
    state := app.New()

    router := gastro.New(gastro.WithDeps(state))

    log.Fatal(http.ListenAndServe(":4242", router.Handler()))
}
```

There is no separate `mux.HandleFunc("POST /counter", …)` line. The
page is the handler.

## Side-mounted SSE handlers

Long-lived streams (clocks, log tails, monitoring feeds) often don't
share state with a particular page. Register them on the router's
mux directly:

```go
router := gastro.New(gastro.WithDeps(state))

mux := router.Mux()
mux.HandleFunc("GET /api/clock", handleClock)

http.ListenAndServe(":4242", router.Handler())
```

```go
import (
    gastroRuntime "github.com/andrioid/gastro/pkg/gastro"
)

func handleClock(w http.ResponseWriter, r *http.Request) {
    sse := gastroRuntime.NewSSE(w, r)

    ticker := time.NewTicker(1 * time.Second)
    defer ticker.Stop()

    for {
        select {
        case <-sse.Context().Done():
            return
        case <-ticker.C:
            now := time.Now().Format("15:04:05")
            sse.Send("datastar-patch-elements",
                "elements <div id=\"clock\">"+now+"</div>")
        }
    }
}
```

`router.Mux()` returns the underlying `*http.ServeMux` so you can
register additional routes alongside the auto-generated page handlers.
Handlers registered this way bypass the deps-attachment middleware,
so they cannot use `gastro.From[T]` — the dependency must be
captured in a closure or passed via another mechanism.

## Generic SSE helper

The core SSE helper in `pkg/gastro` is framework-agnostic and works
with any client that consumes `text/event-stream`:

```go
func handleUpdates(w http.ResponseWriter, r *http.Request) {
    sse := gastroRuntime.NewSSE(w, r)

    ticker := time.NewTicker(1 * time.Second)
    defer ticker.Stop()

    for {
        select {
        case <-sse.Context().Done():
            return
        case <-ticker.C:
            sse.Send("time", time.Now().Format("15:04:05"))
        }
    }
}
```

Methods available:

- **`Send(eventType, data ...string)`** — writes and flushes a single
  SSE event.
- **`IsClosed()`** — reports whether the client disconnected.
- **`Context()`** — returns the request context for `select` loops.

## Datastar Integration

The `pkg/gastro/datastar` subpackage formats events using Datastar's
SSE protocol. The page-side example above uses
`sse.PatchElements(html)`. The same helper is available from
side-mounted handlers:

```go
sse := datastar.NewSSE(w, r)

sse.PatchElements(html,
    datastar.WithSelector("#dashboard"),
    datastar.WithMode(datastar.ModeInner),
)

sse.PatchSignals(map[string]any{
    "count": 42, "loading": false,
})

sse.RemoveElement("#toast-1")
```

## Type-Safe Rendering

The compiler generates a `Render` API for calling Gastro components
with full type safety. From frontmatter:

```gastro
html, err := gastro.Render.Counter(CounterProps{Count: int(n)})
```

From `main.go` or any side-mounted handler:

```go
import gastro "myapp/.gastro"

html, err := gastro.Render.Counter(gastro.CounterProps{Count: 42})
```

| What        | Safety                                          |
|-------------|-------------------------------------------------|
| Method name | Compile-time — method exists or it doesn't     |
| Props fields | Compile-time — struct fields checked by Go    |
| Props types | Compile-time — Go type system                  |

Components with children carry a `Children template.HTML` field on their
Props struct (auto-added by codegen when the template references
`{{ .Children }}`):

```go
inner, _ := gastro.Render.Counter(gastro.CounterProps{Count: 42})
full, _  := gastro.Render.Layout(gastro.LayoutProps{
    Title:    "Dashboard",
    Children: template.HTML(inner),
})
```

## Design Notes

- **No external dependencies.** The SSE protocol is ~90 lines of Go.
- **Two layers.** Generic `pkg/gastro` works with any SSE client.
  `pkg/gastro/datastar` adds Datastar-specific formatting.
- **Track B body-tracking.** The wrapped page writer records when SSE
  events have committed a body so the template render is skipped on
  POST while still running on the corresponding GET. See
  [`docs/design.md`](design.md) §21 for the design rationale.

[Try the live Datastar demo →](/examples/guestbook-datastar)
