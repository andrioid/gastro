# examples/csp — `WithRequestFuncs` with helper-to-middleware coordination

Demonstrates Content-Security-Policy nonces for inline scripts. The
binder helper (`cspNonce`) and the middleware (`csp.Middleware`) must
agree on the same per-request value — if they diverge, the browser
refuses to execute inline scripts. The example is the proof that
Gastro's `WithRequestFuncs` path is genuinely per-request: the
rendered `<script nonce="X">` always matches the
`Content-Security-Policy: nonce-X` response header for the same
request.

≈60 LOC of generic Go in `internal/csp/`, ≈10 LOC of `main.go`
glue.

## Run it

```sh
cd examples/csp
go run .                  # http://localhost:4242
```

Open the page, open DevTools → Network → click the document, and
verify:

- The `Content-Security-Policy` header contains `nonce-XXXX`.
- The inline `<script nonce="XXXX">` in the HTML has the *same*
  `XXXX`.
- The script ran (the status text turned green).

Reload a few times. The nonce changes every request.

## How it works

### `internal/csp/csp.go`

The middleware:

1. Mints a fresh nonce per request via `crypto/rand`.
2. Writes the `Content-Security-Policy` header advertising
   `'nonce-XXXX'`.
3. Attaches the same nonce to the request context.

The template helper (`cspNonce`) reads the nonce out of the context
and returns it as a plain string.

### `main.go`

```go
gastro.New(
    gastro.WithMiddleware("/{path...}", csp.Middleware),
    gastro.WithRequestFuncs(func(r *http.Request) template.FuncMap {
        fm := template.FuncMap{}
        for k, v := range csp.RequestFuncs(r) {
            fm[k] = v
        }
        return fm
    }),
)
```

### Template

```gastro
<script nonce="{{ cspNonce }}">
    /* This runs because the nonce matches what the server advertised. */
</script>
```

## The coordination story

CSP is a strong example of why `WithRequestFuncs` exists rather than
`WithFuncs`: the helper's return value must vary per request *and*
must agree with another piece of per-request state (the header). A
static `WithFuncs` couldn't express this — at parse time there's no
nonce yet. Computing the nonce in frontmatter would work, but then
every page would have to do its own crypto + context.WithValue +
helper plumbing, which is exactly the duplication `WithRequestFuncs`
exists to eliminate.

## Tests

`internal/csp/csp_test.go` covers:

- A nonce is minted fresh per request (no repeats across requests).
- The `Content-Security-Policy` header advertises the same nonce that
  the request context carries.
- `NonceFromCtx` is probe-safe (empty context → empty string).
- `RequestFuncs`'s helper returns the right nonce for the given
  request.

Run with `go test -race ./internal/csp/`.

## See also

- `docs/helpers.md` — the `WithRequestFuncs` reference
- `examples/i18n/` — request-aware translation helpers
- `examples/csrf/` — token-based middleware with mixed return types
