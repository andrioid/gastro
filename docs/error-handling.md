# Error Handling

Gastro pages can fail in five distinct ways. Each has its own contract,
its own recovery path, and its own production knob. This page enumerates
them so production deployments can decide where to add visibility, retry
logic, or branded error pages.

> **Wave 4 / C4** (`plans/frictions-plan.md` §3 Wave 4) introduced the
> `WithErrorHandler` option for render errors. The other failure modes
> were already in place before that wave; this page is the first time
> they are written down together.

## The five failure modes

| # | Failure | When it fires | Default behaviour | User knob |
|---|---|---|---|---|
| 1 | **Parse error (production)** | `New()` startup; `.gastro` template fails to compile | `log.Fatal` — process exits | None: parse errors must be fixed in source |
| 2 | **Parse error (dev mode)** | Per-request; templates re-parsed on each call | Logs and serves an error page; dev-reload kicks in when source changes | `gastro check` in CI catches before deploy |
| 3 | **Render error** | `template.Execute` returns an error mid-stream | `DefaultErrorHandler`: log, write 500 if uncommitted | **`WithErrorHandler`** |
| 4 | **Frontmatter panic** | `panic()` inside frontmatter Go | `Recover`: log; write 500 if uncommitted | Wrap your own goroutines |
| 5 | **Missing dependency** | `gastro.From[T]` cannot find a registered dep | Panics; falls into mode 4 | **`gastro.FromOK[T]`** for graceful handling |

Each failure mode is covered in detail below.

---

## 1. Parse error (production)

A `.gastro` file's template body has a syntax error or references an
undefined component. In production, templates are parsed once at
`New()` time and stored in a registry; a parse failure is fatal.

```text
gastro: parsing template pageBlogSlug: template: pageBlogSlug:5: function "Card" not defined
```

**What gastro does:** `log.Fatalf`. The process exits before
`http.ListenAndServe` is reached.

**Why it is not user-tunable:** there is no useful runtime response to
"the template is broken". Every request would return 500. Failing
loudly at startup is the only sensible behaviour.

**Mitigation:** run `gastro check` in CI. It compiles every `.gastro`
file and exits non-zero on any error, so broken templates never reach
production.

```bash
# CI gate
gastro generate
gastro check
```

---

## 2. Parse error (dev mode)

Same failure shape, but `WithDevMode(true)` (or `GASTRO_DEV=1`)
re-parses templates on every request. This is the dev-loop
quality-of-life feature: edit a file, refresh the browser, see the
result without restarting the server.

When parse fails, the dev-mode handler serves an error page describing
the problem and the dev-reload SSE stream pushes a refresh once the
file is fixed.

**Production note:** `WithDevMode(false)` forces the parse-once
behaviour even when `GASTRO_DEV=1` is set. Use it in tests where you
want to assert against the production code path. See [`docs/pages.md`
§"Forcing dev or production mode"](pages.md).

---

## 3. Render error

Templates parse but fail at `Execute` time — typically because a
`FuncMap` function returned an error, a method on a typed value
panicked, or the underlying `io.Writer` failed (closed connection,
broken pipe).

The page handler routes the error through `__router.__gastro_handleError`,
which dispatches to either:

- **Your `WithErrorHandler`** if one is installed, *or*
- **`gastroRuntime.DefaultErrorHandler`** otherwise

### The default

`DefaultErrorHandler` mirrors the gating logic of `gastro.Recover`:

```go
func DefaultErrorHandler(w http.ResponseWriter, r *http.Request, err error) {
    log.Printf("gastro: page render failed for %s %s: %v", r.Method, r.URL.Path, err)

    if HeaderCommitted(w) || BodyWritten(w) {
        return
    }

    http.Error(w, "Internal Server Error", http.StatusInternalServerError)
}
```

Writing a 500 after the response has already started would interleave
with whatever bytes the template managed to flush, confusing the
client and producing visibly broken HTML. The default is conservative:
log always, write only when it is safe.

### Custom handler

```go
router := gastro.New(
    gastro.WithErrorHandler(func(w http.ResponseWriter, r *http.Request, err error) {
        // Report to your error tracker.
        sentry.CaptureException(err)

        // Optionally fall back to the default 500 page.
        gastroRuntime.DefaultErrorHandler(w, r, err)
    }),
)
```

The handler signature is `func(http.ResponseWriter, *http.Request, error)`,
exported as `gastro.PageErrorHandler` for users who want to declare
the type explicitly.

### Common patterns

**Render a templated error page** when the response is uncommitted:

```go
gastro.WithErrorHandler(func(w http.ResponseWriter, r *http.Request, err error) {
    if gastroRuntime.HeaderCommitted(w) || gastroRuntime.BodyWritten(w) {
        log.Printf("gastro: render failed mid-stream: %v", err)
        return
    }
    w.WriteHeader(http.StatusInternalServerError)
    gastro.Render.ErrorPage(ErrorPageProps{
        RequestID: middleware.GetReqID(r.Context()),
        Message:   "Something went wrong.",
    })
})
```

**Attach a request ID** to every error log line:

```go
gastro.WithErrorHandler(func(w http.ResponseWriter, r *http.Request, err error) {
    log.Printf("gastro: render failed [reqID=%s] %s %s: %v",
        middleware.GetReqID(r.Context()), r.Method, r.URL.Path, err)
    gastroRuntime.DefaultErrorHandler(w, r, err)
})
```

---

## 4. Frontmatter panic

Frontmatter is regular Go. Any `panic()` — yours, a stdlib panic, a
nil pointer dereference — is caught by `defer gastro.Recover(w, r)`
which the codegen-generated handler installs for every page.

```go
func Recover(w http.ResponseWriter, r *http.Request) {
    err := recover()
    if err == nil {
        return
    }

    log.Printf("gastro: panic in %s %s: %v", r.Method, r.URL.Path, err)

    if g, ok := unwrapGastroWriter(w); ok && (g.bodyWritten || g.headerCommitted) {
        return // partial response already on the wire — log only
    }

    http.Error(w, "Internal Server Error", http.StatusInternalServerError)
}
```

Same gating as render errors: log always, 500 only when uncommitted.

**Goroutines you spawn in frontmatter are *not* covered.** A panic on
a background goroutine crashes the process. Wrap your own:

```go
go func() {
    defer func() {
        if err := recover(); err != nil {
            log.Printf("background task panic: %v", err)
        }
    }()
    doWork()
}()
```

---

## 5. Missing dependency

`gastro.From[T](ctx)` panics when no value of type `T` is registered
via `WithDeps`. The panic falls through to `Recover` (mode 4) and
produces a 500.

**For required deps** — services your page cannot function without —
this is the right behaviour. The 500 is more useful than silent
fallback because it surfaces the wiring bug fast.

**For optional deps** — feature flags, A/B test buckets, anything the
page can degrade without — use `gastro.FromOK[T](ctx)`:

```gastro
---
import "github.com/myorg/myapp/featureflags"

flags, ok := gastro.FromOK[*featureflags.Set](r.Context())
if !ok {
    flags = featureflags.Empty() // graceful degradation
}

ShowBeta := flags.Enabled("beta-ui")
---
```

`FromOK` returns `(T, bool)` instead of panicking. The `ok` discriminator
lets the page render with sensible defaults instead of returning 500
when the dep is missing — useful for staged rollouts, multi-tenant
setups, and tests that exercise the page without a full DI graph.

---

## Failure-mode interaction matrix

| Triggered failure | Wrapped writer state | Visible response |
|---|---|---|
| Parse error (prod) | n/a | Process exits before serving |
| Parse error (dev) | Fresh | Dev error page |
| Render error, fresh writer | Uncommitted | 500 from `DefaultErrorHandler` (or your handler) |
| Render error, mid-stream | Body written | Partial HTML; error logged only |
| Frontmatter panic, fresh | Uncommitted | 500 from `Recover` |
| Frontmatter panic, mid-stream | Body written | Partial response; panic logged only |
| Missing required dep | Same as panic | Same as panic |
| Missing optional dep with `FromOK` | Fresh | Page renders with defaults; no error |

The principle behind every "log only" branch: **a partial response is
not yours to overwrite.** Once bytes are on the wire, the client has
already started parsing them. Appending a 500 page or a fresh status
line corrupts whatever was sent.

---

## Where to start

If you have not customised anything yet, here is a sensible production
baseline:

```go
router := gastro.New(
    gastro.WithDeps(/* your services */),
    gastro.WithErrorHandler(func(w http.ResponseWriter, r *http.Request, err error) {
        // 1. Always report.
        log.Printf("[render] %s %s: %v", r.Method, r.URL.Path, err)
        // sentry.CaptureException(err) // or your tracker

        // 2. Fall back to the default for the user-visible response.
        gastroRuntime.DefaultErrorHandler(w, r, err)
    }),
)
```

That single hook gives you full visibility into render failures
without changing how the response is shaped.

For frontmatter-panic visibility, wrap `Recover` is not currently
pluggable — log scraping or a panic tracker process is the workaround
until an adopter asks for `WithRecoverHandler`.

---

## See also

- [`docs/pages.md`](pages.md) — the page model and the body-tracking
  writer that gates the "log only" branches.
- [`docs/dev-mode.md`](dev-mode.md) — dev-mode parse errors and the
  reload SSE stream.
- `pkg/gastro/error_handler.go` — `PageErrorHandler` type and
  `DefaultErrorHandler` source.
- `pkg/gastro/recover.go` — the panic-recovery middleware.
