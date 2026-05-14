# examples/csrf — `WithRequestFuncs` with mixed return types

A double-submit-cookie CSRF protection example, end to end. Exercises:

- A `WithRequestFuncs` binder that returns **two helpers with different
  return types** from the same call: `csrfToken` (string) and
  `csrfField` (`template.HTML`).
- Middleware that **produces** request state (mints + verifies a token)
  rather than just reading it. The token persists across requests via a
  `SameSite=Lax` cookie.
- Form submission and AJAX header submission paths, both verified.

≈100 LOC of generic Go in `internal/csrf/` and ≈5 LOC of `gastro`-facing
glue in `main.go`. The point: **`WithRequestFuncs` is the contract;
everything else is plain Go.**

## Run it

```sh
cd examples/csrf
go run .                  # http://localhost:4242
```

Submit the form on `/` — it works. Try POSTing with a wrong token:

```sh
# Get the cookie + token first.
curl -c jar.txt http://localhost:4242/
# This succeeds (token matches):
TOKEN=$(awk '/gastro_csrf/{print $7}' jar.txt)
curl -b jar.txt -X POST -d "name=Andri&_csrf=$TOKEN" http://localhost:4242/

# This fails with 403:
curl -b jar.txt -X POST -d "name=Andri&_csrf=wrong" http://localhost:4242/
```

## How it works

### `internal/csrf/csrf.go` — domain code

A self-contained CSRF library:

- `Middleware(next)` — mints a token cookie on first request, verifies
  the submitted token on POST / PUT / PATCH / DELETE.
- `TokenFromCtx(ctx)` — pulls the per-request token out of the context.
- `RequestFuncs(r)` — the `template.FuncMap` shape that
  `gastro.WithRequestFuncs` expects. Built once per request; helpers
  close over the token so they need no extra arguments at template time.

Nothing in here references Gastro. The package is a stdlib HTTP
middleware + a context accessor + a method that returns a `FuncMap`.

### `main.go` — gastro wiring

```go
gastro.New(
    gastro.WithMiddleware("/{path...}", csrf.Middleware),
    gastro.WithRequestFuncs(csrf.RequestFuncs),
)
```

Two lines. The middleware attaches the token to the context; the binder
exposes it to templates as `csrfToken` and `csrfField`.

### Templates

```gastro
<form method="POST">
    {{ csrfField }}
    <input type="text" name="name">
    <button>Submit</button>
</form>
```

`csrfField` expands to `<input type="hidden" name="_csrf" value="...">`.

AJAX clients can read the token via a `<meta>` tag:

```gastro
<meta name="csrf-token" content="{{ csrfToken }}">
```

Then in JavaScript:

```js
fetch("/api/x", {
    method: "POST",
    headers: {
        "X-CSRF-Token": document.querySelector('meta[name=csrf-token]').content,
        "Content-Type": "application/json",
    },
    body: JSON.stringify({ foo: "bar" }),
});
```

## Swapping in gorilla/csrf

If you want a battle-tested implementation, [gorilla/csrf](https://github.com/gorilla/csrf)
wraps to the same shape:

```go
import "github.com/gorilla/csrf"

protect := csrf.Protect(csrfKey, csrf.Secure(false))

router := gastro.New(
    gastro.WithMiddleware("/{path...}", protect),
    gastro.WithRequestFuncs(func(r *http.Request) template.FuncMap {
        return template.FuncMap{
            "csrfToken": func() string { return csrf.Token(r) },
            "csrfField": func() template.HTML { return csrf.TemplateField(r) },
        }
    }),
)
```

The `WithRequestFuncs` shape is unchanged; only the middleware
implementation differs.

## Tests

`internal/csrf/csrf_test.go` covers:

- Safe-method requests issue (or reuse) the cookie.
- POST without a token → 403.
- POST with matching form token → 200.
- POST with matching `X-CSRF-Token` header → 200.
- POST with a mismatched token → 403.
- `RequestFuncs` is probe-safe (empty context → empty helpers).
- `RequestFuncs` carries the real token when the context has one.

Run with `go test -race ./internal/csrf/`.

## See also

- `docs/helpers.md` — the `WithRequestFuncs` reference
- `examples/i18n/` — same pattern, three helpers, ≈3 lines of glue
- `examples/csp/` — same pattern, helper-to-helper coordination via context
