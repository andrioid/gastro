# Template Helpers

Gastro provides 21 built-in template functions available in all templates without registration. You can also add custom helpers.

## String Functions

```go
{{ .Name | upper }}        // "ALICE"
{{ .Name | lower }}        // "alice"
{{ .Bio | trim }}          // trims whitespace
{{ .Tags | join ", " }}    // "go, web, ssr"
```

| Function | Description |
|----------|-------------|
| `upper` | Converts string to uppercase |
| `lower` | Converts string to lowercase |
| `trim` | Trims leading and trailing whitespace |
| `join` | Joins a slice of strings with a separator |
| `split` | Splits a string by separator |
| `contains` | Checks if a string contains a substring |
| `replace` | Replaces occurrences in a string |

## Safety Functions

These functions mark content as safe for specific contexts, bypassing `html/template`'s automatic escaping. Use them only with trusted content:

```go
// Render trusted HTML
{{ .Body | safeHTML }}

// Safe attribute values
<div class="{{ .Class | safeAttr }}">

// Safe URLs
<a href="{{ .URL | safeURL }}">

// Safe CSS
<div style="{{ .Style | safeCSS }}">

// Safe JS
<script>var x = {{ .Data | safeJS }}</script>
```

| Function | Marks safe for |
|----------|----------------|
| `safeHTML` | HTML content (renders without escaping) |
| `safeAttr` | HTML attribute values |
| `safeURL` | URL values in `href`/`src` attributes |
| `safeCSS` | CSS property values |
| `safeJS` | JavaScript values |

## Utility Functions

```go
// Default values
{{ .Name | default "Anonymous" }}

// Time formatting
{{ .CreatedAt | timeFormat "Jan 2, 2006" }}

// JSON output
{{ .Config | json }}

// Build maps and lists inline
{{ dict "key" "value" "other" 42 }}
{{ list "a" "b" "c" }}

// String operations
{{ split .Tags "," }}
{{ contains .Title "Go" }}
{{ replace .Text "old" "new" }}
```

| Function | Description |
|----------|-------------|
| `default` | Returns value, or fallback if empty/zero |
| `timeFormat` | Formats a `time.Time` using Go's layout syntax |
| `json` | JSON-encodes a value |
| `dict` | Creates a `map[string]any` from key-value pairs |
| `list` | Creates a `[]any` from arguments |

## Membership and Lookup

Templates often need to ask "is this thing in that thing?" — e.g. "is this
tab the active one?", "does this dict carry an optional field?". Without
helpers, authors end up declaring `activeSet := map[string]bool{...}` in
frontmatter; these helpers let templates ask the question directly.

```go
// Slice membership
{{ if has .Tag .ActiveTags }}<span class="active">{{ end }}

// Variadic form (no slice needed)
{{ if has .Status "open" "in_progress" }}⚠️{{ end }}

// Map key presence (works against any map)
{{ if hasKey "Avatar" .User }}<img src="{{ .User.Avatar }}">{{ end }}

// The active-set idiom: build a set once, query repeatedly.
{{ $active := set "home" "about" "contact" }}
{{ range .Tabs }}
  <a class="{{ if hasKey . $active }}active{{ end }}">{{ . }}</a>
{{ end }}
```

| Function | Description |
|----------|-------------|
| `has` | Reports whether `needle` appears in `haystack`. Accepts a slice/array or variadic arguments. Uses `reflect.DeepEqual`. |
| `hasKey` | Reports whether `key` is present in `m`. Works on any map (string-keyed, int-keyed, `map[any]bool`). Returns false for non-maps rather than panicking. |
| `set` | Builds a `map[any]bool` from the given items. Combine with `hasKey` for efficient repeated membership tests. Unhashable items (slices, maps, funcs) are skipped silently. |

## Custom Helpers

Register custom template functions in your `main.go` using `gastro.WithFuncs()`:

```go
router := gastro.New(
    gastro.WithFuncs(template.FuncMap{
        "formatEUR": func(cents int) string {
            return fmt.Sprintf("%.2f EUR", float64(cents)/100)
        },
        "slugify": func(s string) string {
            return strings.ToLower(strings.ReplaceAll(s, " ", "-"))
        },
    }),
)
http.ListenAndServe(":4242", router.Handler())
```

Custom functions are available in all pages and components, just like the built-in helpers.

## Request-aware Helpers (`WithRequestFuncs`)

`WithFuncs` registers helpers at template-parse time — their bodies are
fixed for the lifetime of the router. **Request-aware helpers** are
different: their bodies close over a `*http.Request` and can read
request state. That makes the same helper name (`t`, `csrfField`,
`cspNonce`, …) return different values on different requests.

Use `gastro.WithRequestFuncs(binder)` to register them:

```go
router := gastro.New(
    gastro.WithMiddleware("/", i18n.Middleware),
    gastro.WithRequestFuncs(func(r *http.Request) template.FuncMap {
        l := i18n.FromCtx(r.Context())
        return template.FuncMap{
            "t":  l.T,
            "tn": l.TN,
            "tc": l.TC,
        }
    }),
)
```

In a `.gastro` template:

```gastro
---
---
<h1>{{ t "Welcome" }}</h1>
<p>{{ tn "1 item" "%d items" .Count }}</p>
<button>{{ tc "button" "Open" }}</button>
```

The binder runs once per request. The closures it returns capture `r`,
so `{{ t "Welcome" }}` resolves against the request's locale, CSRF
cookie, CSP nonce, or whatever else your middleware attached to the
request context.

### When to use it

| Pattern | Library | Helpers registered |
|---|---|---|
| Internationalisation (gettext-style) | `gotext`, `go-i18n`, hand-rolled | `t`, `tn`, `tc` |
| CSRF protection | `gorilla/csrf`, custom | `csrfToken`, `csrfField` |
| CSP nonces | custom (a few lines of `crypto/rand`) | `cspNonce` |
| Named-route reversal | custom | `routePath` |
| Asset hashing | custom | `asset` |
| Feature flags | flag library of choice | `flag` |

The common thread: anything that needs to read **per-request state**
at template time, where pre-computing in frontmatter would be
repetitive across many pages.

### Rendering from handlers and SSE

When you call `gastro.Render.X(props)` from a Go handler, the static
FuncMap is used — binders are *not* invoked, so request-aware helpers
resolve to placeholders (typically the empty string). To bind a render
call to a specific request, use `Render.With(r)`:

```go
func handleUpdate(w http.ResponseWriter, r *http.Request) {
    html, _ := gastro.Render.With(r).Card(gastro.CardProps{Title: "Hello"})
    datastar.NewSSE(w, r).PatchElements(html)
}
```

The returned `*renderAPI` is reusable within a single request — store
it in a local and render multiple components from it. It is **not**
goroutine-safe and must not be retained beyond the request.

### Multiple binders compose

You can register `WithRequestFuncs` multiple times — e.g. one for i18n,
one for CSRF — and the helper sets are merged. The only constraint is
that helper names must be unique across the union of:

- Gastro built-ins (`upper`, `lower`, `dict`, … — see top of this page)
- `WithFuncs` registrations
- All `WithRequestFuncs` binders

A collision panics at `gastro.New()` with both sources named:

```
gastro: helper name "t" registered twice
  - WithFuncs
  - WithRequestFuncs[1]
```

This is intentional — silent shadowing of a built-in or another binder
would make template behaviour depend on registration order, which is
brittle and hard to debug.

### The binder contract

A `WithRequestFuncs` binder is a `func(*http.Request) template.FuncMap`.
It MUST:

- Return a **stable key set** — the *names* returned must not depend on
  request state. (Closure *bodies* may read request state freely; that's
  the whole point.) Gastro probes each binder once at `New()` with a
  synthetic request to discover its key set for collision detection.
- Not panic during top-level execution when fed a probe request whose
  context carries no adopter-installed values. In particular, your
  `FromCtx`-style accessors must tolerate a missing locale / cookie /
  nonce and return safe zero defaults. The probe never invokes the
  *closures* inside the FuncMap — only the map's keys are read — but
  top-level statements in the binder body do run.

A binder SHOULD:

- Be cheap. It runs on every request.
- Return a **literal `template.FuncMap{...}`** so the Gastro LSP can
  extract helper names via static analysis and surface them in
  completion / hover / go-to-definition. Dynamically constructed maps
  (`m := make(template.FuncMap); m["t"] = …; return m`) work at runtime
  but degrade the editor experience for those helpers.

### Runtime panic recovery

If a binder or any helper it returned panics during request handling,
Gastro recovers the panic, logs it with the panicking binder's
registration index, and dispatches to your `WithErrorHandler` (default:
`500 Internal Server Error`). One bad binder cannot crash the server.

### Components, slots, and wrap blocks

Request-aware helpers propagate through every layer of a page render:

- Page templates (`pages/foo.gastro`).
- Components invoked from a page via `{{ Component . }}` or
  `{{ wrap Component (dict ...) }}`.
- Slot content rendered inside a wrap block.
- Components rendered programmatically via
  `gastro.Render.With(r).Component(props)`.

In every case, helpers like `{{ t "…" }}`, `{{ csrfField }}`, and
`{{ cspNonce }}` resolve against the right per-request state. You don't
need to translate strings in the page's frontmatter and pass them as
props — the helper just works inside the component template body.

Under the hood, each request Clones the page's parsed template (or, in
dev mode, re-parses it) and applies the per-request FuncMap to the
clone. Bare component invocations are then dispatched through closures
that thread the request all the way down the component tree. Cost is
proportional to nesting depth; on an Apple M3 a typical component
template clones in ~1.2 µs, so a 5-deep tree adds ~6 µs per request —
well below the per-request budget of any real handler. See the
`BenchmarkNestedClone` suite in `internal/compiler/` for the per-depth
roll-up.

### Editor support

The Gastro LSP discovers `WithRequestFuncs` binder helpers by AST-
parsing your project's `main.go`. As long as the binder returns a
literal `template.FuncMap{…}` (either inline or via a one-hop named
function reference in the same file), helper names show up in:

- Template completion (`{{ t<TAB>` suggests `t` with detail
  *"request-aware helper"*).
- Template parse — no spurious *"function not defined"* diagnostic.

Binders that build their FuncMap dynamically degrade gracefully: the
helpers still work at runtime, they just don't appear in completion.

### Worked example

A fuller end-to-end example app — with locale detection middleware, PO
file loading, plural rules, and an `Accept-Language` switcher — lives in
`examples/i18n/` (added in the follow-up PR alongside this feature).
