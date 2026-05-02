# Migrating to Track B (Page model v2)

This guide walks an existing gastro adopter through upgrading to the
Track B page model. It's written for projects that built against the
pre-Track-B shape (`gastro.Context()` marker, GET-only page routes,
mutating handlers in `main.go`), with extra notes for the SSE-heavy
mutation pattern that triggered the redesign in the first place.

If you've never written a `.gastro` page before, read
[`pages.md`](pages.md) and [`sse.md`](sse.md) directly — those docs
describe the model from scratch.

---

## TL;DR

1. Frontmatter has ambient `w *http.ResponseWriter` and `r *http.Request`.
   Drop the `ctx := gastro.Context()` line.
2. Pages now register for **every HTTP method**. Use `r.Method` to
   branch; `POST` handlers can move out of `main.go` and into the
   page that renders the corresponding `GET`.
3. `gastro.Context()` still works during a deprecation window (two
   minor releases) but emits a build warning. `gastro generate`,
   `gastro check`, and `gastro build` (strict) will fail until it's
   removed; `gastro dev` still serves.
4. Replace `ctx.Param`, `ctx.Query`, `ctx.Error`, `ctx.Redirect`,
   `ctx.Header` with the standard library equivalents. Mechanical.
5. Replace `gastro.From[T](ctx)` with `gastro.From[T](r.Context())`.
   The marker rewriter handles this for you, but the call site reads
   differently.
6. Any write to `w` (or `http.Redirect(w, r, …)`) must be followed
   by `return`. The codegen-side analyzer enforces this; under
   `--strict` it's an error.
7. `WithOverride` patterns lose their HTTP method prefix:
   `"GET /board"` becomes `"/board"`. Static-asset overrides keep
   `"GET /static/"`.
8. Component authoring is unchanged.

For the design rationale see [`docs/design.md` §21](design.md) and
the dated `DECISIONS.md` entry for 2026-05-02.

---

## Why this changes

Pre-Track-B, a page that needed both a GET render and a POST mutation
had to split logic across two files:

- `pages/board.gastro` — frontmatter ran on `GET /board`, computed
  filters, looked up deps, projected state, exposed uppercase locals
  to the template.
- `main.go` — defined `handleTaskPlace(w, r)` for `POST /tasks/{id}/place`,
  which had to re-implement filter parsing, re-look-up the deps,
  re-project the state, then emit an SSE patch with
  `gastro.Render.Column(...)`.

The two halves shared a URL convention and a component name, nothing
else. For SSE-heavy apps with many morphs per page, that duplication
became the dominant maintenance cost.

Track B's mechanic is simple. The page registers for every method.
Frontmatter gets ambient `(w, r)` and a body-tracking response writer.
After frontmatter completes:

- If the body has been written → the template render is **skipped**.
- Else → the template renders with the staged uppercase locals.

So a single `pages/board.gastro` can now branch on `r.Method`, write
an SSE patch on `POST`, and fall through to the template render on
`GET`. Filter parsing, dep lookup, and projection slicing live in
exactly one place.

---

## The new shape, side by side

### Before

```gastro
---
import Board "components/board.gastro"

ctx := gastro.Context()
deps := gastro.From[BoardDeps](ctx)
filter := boardFilterFromQuery(ctx.Request())

Columns := buildColumns(deps.State(), filter)
Filter := filter
---
{{ Board (dict "Columns" .Columns "Filter" .Filter) }}
```

```go
// main.go
mux.HandleFunc("POST /tasks/{id}/place", handleTaskPlace)

func handleTaskPlace(w http.ResponseWriter, r *http.Request) {
    deps := gastroRuntime.FromContext[BoardDeps](r.Context())
    filter := boardFilterFromQuery(r) // duplicate of frontmatter
    if err := deps.Service().Place(r); err != nil {
        http.Error(w, err.Error(), http.StatusConflict)
        return
    }
    cols := buildColumns(deps.State(), filter) // duplicate
    html, _ := gastro.Render.Column(gastro.ColumnProps{
        Status: r.PathValue("status"),
        Items:  cols[r.PathValue("status")],
    })
    sse := datastar.NewSSE(w, r)
    sse.PatchElements(html)
}
```

### After

```gastro
---
import (
    "net/http"

    Board "components/board.gastro"

    "github.com/andrioid/gastro/pkg/gastro/datastar"
)

deps := gastro.From[BoardDeps](r.Context())
filter := boardFilterFromQuery(r)

if r.Method == http.MethodPost {
    if err := deps.Service().Place(r); err != nil {
        http.Error(w, err.Error(), http.StatusConflict)
        return
    }
    cols := buildColumns(deps.State(), filter)
    html, err := gastro.Render.Column(ColumnProps{
        Status: r.PathValue("status"),
        Items:  cols[r.PathValue("status")],
    })
    if err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }
    sse := datastar.NewSSE(w, r)
    sse.PatchElements(html)
    return
}

// GET — fall through to template render
Columns := buildColumns(deps.State(), filter)
Filter := filter
---
{{ Board (dict "Columns" .Columns "Filter" .Filter) }}
```

```go
// main.go
// (the POST handler is gone; the page registers for both methods)
```

`pages/board.gastro` now serves both `GET /board` and `POST /board`.
For richer routing (`POST /tasks/{id}/place` ≠ `POST /board`), see the
**Distinct mutation paths** section below.

---

## Mechanical replacements

| Pre-Track-B                  | Track B                                                  |
|------------------------------|----------------------------------------------------------|
| `ctx := gastro.Context()`    | (delete; `w` and `r` are ambient)                        |
| `ctx.Param("slug")`          | `r.PathValue("slug")`                                    |
| `ctx.Query("q")`             | `r.URL.Query().Get("q")`                                 |
| `ctx.Error(404, "missing")`  | `http.Error(w, "missing", http.StatusNotFound)` (note arg order) |
| `ctx.Redirect("/x", 302)`    | `http.Redirect(w, r, "/x", http.StatusFound)`            |
| `ctx.Header("X-Y", "z")`     | `w.Header().Set("X-Y", "z")`                             |
| `ctx.Request()`              | `r`                                                      |
| `ctx.SSE()`                  | `datastar.NewSSE(w, r)` or `gastro.NewSSE(w, r)`         |
| `gastro.From[T](ctx)`        | `gastro.From[T](r.Context())`                            |
| `gastro.FromOK[T](ctx)`      | `gastro.FromOK[T](r.Context())`                          |

The pre-Track-B `From(*Context)` and `FromOK(*Context)` runtime
helpers are **removed**. Frontmatter still spells the call as
`gastro.From[T](r.Context())`; the marker rewriter aliases it to
`gastroRuntime.FromContext[T](...)` at codegen time. If you call
`gastro.From` from a side-mounted handler in `main.go`, switch to
`gastroRuntime.FromContext[T](r.Context())` directly.

---

## Route patterns lose their method prefix

| Pre-Track-B                              | Track B                              |
|------------------------------------------|--------------------------------------|
| `gastro.WithOverride("GET /", h)`        | `gastro.WithOverride("/", h)`        |
| `gastro.WithOverride("GET /blog/{slug}", h)` | `gastro.WithOverride("/blog/{slug}", h)` |
| `gastro.WithOverride("GET /static/", h)` | `gastro.WithOverride("GET /static/", h)` *(static keeps its prefix)* |

Page patterns are now method-less: the page handles every method, so
the override applies to every method too. If you only want to
override one method, do the dispatch inside your handler:

```go
func myHandler(w http.ResponseWriter, r *http.Request) {
    if r.Method != http.MethodGet {
        http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
        return
    }
    // …
}
```

`New()` panics with the list of valid patterns when the override
doesn't match — typos still fail loudly.

---

## The missing-return analyzer

Any frontmatter call that writes to `w` (or `http.Redirect(w, r, …)`,
or a method on `w`) must be followed by `return` in the same block,
or be the last statement of the synthetic function body. Otherwise
the codegen-side analyzer emits:

```
gastro: pages/board.gastro:5: response was written but no return
follows; frontmatter execution will continue and any uppercase
variables computed after this point are dead code. Add `return`
to short-circuit. (at: http.Error(w, err.Error(), 500))
```

Severity follows the existing convention:

- `gastro dev`: warns; the dev server keeps rendering.
- `gastro generate`, `gastro check`, `gastro build` (strict): fails
  the command.

The most common bite is forgetting `return` after `http.Error`:

```gastro
if err := svc.Place(r); err != nil {
    http.Error(w, err.Error(), http.StatusConflict)
    return  // ← required; without it the template renders too
}
```

A documented false-positive class: helpers that take `w` as a
parameter but don't actually write (e.g. `extractRequestID(w, r) string`).
Mitigation is to remove the unused parameter.

---

## Distinct mutation paths

If your `pages/board.gastro` needs a sibling URL like
`POST /tasks/{id}/place` (i.e. a different path, not just a different
method), you have two options:

**Option A — keep mutation handlers in `main.go`.** Track B does not
deprecate `mux.HandleFunc("POST /tasks/{id}/place", h)`. The handler
can still call `gastro.Render.Column(...)` and emit SSE patches.
Use this when the mutation URL is structurally different from the
page URL.

**Option B — give the mutation its own `.gastro` file.** Create
`pages/tasks/[id]/place.gastro` whose frontmatter handles the POST
exclusively (returning 405 on GET). This collapses POST + render
helpers + dep lookup into one source file even when the URL doesn't
match the page's URL.

```gastro
---
import (
    "net/http"

    Board "components/board.gastro"

    "github.com/andrioid/gastro/pkg/gastro/datastar"
)

if r.Method != http.MethodPost {
    http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
    return
}

deps := gastro.From[BoardDeps](r.Context())
filter := boardFilterFromQuery(r)

if err := deps.Service().Place(r); err != nil {
    http.Error(w, err.Error(), http.StatusConflict)
    return
}

cols := buildColumns(deps.State(), filter)
html, err := gastro.Render.Column(ColumnProps{
    Status: r.PathValue("status"),
    Items:  cols[r.PathValue("status")],
})
if err != nil {
    http.Error(w, err.Error(), http.StatusInternalServerError)
    return
}

sse := datastar.NewSSE(w, r)
sse.PatchElements(html)
return
---
```

The frontmatter never falls through to the template render because
the SSE write commits the body. The template body can be empty (or a
short error message; it's never reached on the happy path).

---

## Typed dependencies

`gastro.WithDeps[T]` and `gastro.From[T]` continue to work; only the
call signature changes (now takes `context.Context` instead of
`*Context`).

```go
// main.go
router := gastro.New(
    gastro.WithDeps(BoardDeps{
        State:   stateFn,
        Service: svc,
    }),
)
```

```gastro
---
deps := gastro.From[BoardDeps](r.Context())
state := deps.State()
Tasks := state.ByStatus(StatusTodo)
---
```

A new constraint: if the dep type is defined in `package main`, the
generated handler can't import it (main is unreachable from generated
code). Move the type to a small types package — e.g. `myapp/app` —
that both `main.go` and the generated code can import. The migrated
`examples/sse/app/state.go` is a worked reference.

---

## SSE-heavy app patterns

If your app emits N morph events per page interaction (the git-pm
shape), Track B lets you co-locate every morph handler with the page
that renders the corresponding fragment. The morph URL still routes
through gastro; the morph body lives in a `.gastro` file alongside
the page that owns the component being patched.

```text
pages/
  board.gastro              # GET /board (renders the whole board)
  tasks/
    [id]/
      place.gastro          # POST /tasks/{id}/place (morph one column)
      delete.gastro         # DELETE /tasks/{id}    (morph the column)
      comments/
        [cid]/
          delete.gastro     # DELETE /tasks/{id}/comments/{cid}
```

Filter parsing, dep lookup, and projection slicing live in shared
helpers (a `boardFilterFromQuery` package, a `buildColumns` package).
Each `.gastro` page imports the helpers it needs. The duplication
across `pages/board.gastro` and the mutation handlers in `main.go`
goes away.

What Track B doesn't do: it doesn't introduce an `action` keyword,
typed `datastar.Patch` return values, or compile-time checking of
selector ↔ component fragment matching. Those are the unfilled half
of friction `C1` in the original report and remain held per
`plans/frictions-plan.md` rule 2 ("no new language to learn"). The
underlying motivation — mutation handlers in `.gastro` files — is
addressed by Track B.

---

## Migration order

For a project of git-pm's size (multiple pages, ~30 mutation
handlers), the recommended order:

1. **Upgrade gastro and regenerate** — your existing `gastro.Context()`
   pages will keep working with deprecation warnings. Verify the dev
   server boots.
2. **Drop the `GET ` prefix from any `WithOverride` patterns** —
   mechanical, can be done before any `.gastro` edits.
3. **Migrate one page** — pick the simplest one; replace
   `ctx := gastro.Context()` and the four ctx accessors. Verify
   `gastro check` and tests pass. Use this as the template for the rest.
4. **Migrate the remaining pages mechanically** — the table above
   covers ~95% of edits. Pages with no `ctx` calls (just a marker)
   need the marker line removed and nothing else.
5. **Co-locate one mutation handler** — pick a small POST handler from
   `main.go` that emits an SSE patch, move it into a sibling
   `.gastro` file (e.g. `pages/tasks/[id]/place.gastro`), delete the
   `mux.HandleFunc` line. Verify the morph still works in the browser.
6. **Decide which mutations to co-locate and which to leave in `main.go`**.
   There's no rule; pick the boundary that matches your team's mental
   model. The trade-off is: co-located handlers share helpers and
   deps with the page; standalone handlers in `main.go` are easier to
   reuse across multiple pages.
7. **Address strict-mode warnings**. `gastro check` will surface any
   missing `return` after a write to `w`. Fix them; they were latent
   bugs.
8. **Drop the `gastro.Context()` calls**. By now your pages should
   have no `ctx.Param`, `ctx.Query`, etc. references; the marker
   itself is the last thing to remove.

After step 8 your project should pass `gastro check` with zero
warnings.

---

## Checklist

- [ ] `gastro` upgraded to a version that includes Track B
- [ ] All `gastro.Context()` lines removed
- [ ] All `ctx.Param/Query/Error/Redirect/Header` calls replaced
- [ ] All `gastro.From[T](ctx)` calls now pass `r.Context()`
- [ ] All `WithOverride` page patterns lose their method prefix
- [ ] All `mux.HandleFunc("METHOD /path", h)` mutation handlers
      audited; co-located ones moved into `.gastro` files
- [ ] All writes to `w` followed by `return` (or last in function body)
- [ ] Dep types live in a non-`main` package if used from frontmatter
- [ ] `gastro check` clean
- [ ] Tests green with `-race`
- [ ] Browser smoke-test: GET render works; POST/PATCH/DELETE morphs work

---

## When you hit something the guide doesn't cover

- **Build error: `undefined: ctx`** — you missed a `ctx.X` reference
  during the mechanical sweep. The marker no longer auto-injects `ctx`.
- **Build error: `unknown gastro runtime symbol "X"`** — frontmatter
  references `gastro.X` for a name outside the allowlist (Props,
  Context, From, FromOK, FromContext, FromContextOK, NewSSE, Render).
  If you need one of the runtime helpers, import the runtime package
  and call it through that alias instead:
  ```gastro
  import gastroRuntime "github.com/andrioid/gastro/pkg/gastro"
  // …
  _ = gastroRuntime.AttachDeps
  ```
- **Strict-mode error: `response was written but no return follows`** —
  add `return` after the write site named in the message.
- **Two `"net/http"` imports** in the generated output — your version
  predates the import-dedup fix; upgrade to the latest patch release.
- **Frontmatter calls a runtime symbol from `package main`** —
  generated code can't import `main`. Extract the type to a sibling
  package both can import.

If a friction isn't covered here and isn't in `frictions.md`, file an
issue against gastro with a minimal reproducer. Track B is the
biggest pre-1.0 churn since the handler-instance refactor; corner
cases are likely.
