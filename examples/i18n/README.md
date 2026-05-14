# examples/i18n — `WithRequestFuncs` in action

A complete internationalised app built on Gastro's `WithRequestFuncs`
hook. Three locales (`en`, `da`, `de`), gettext-style `.po` catalogues,
request-aware `{{ t "..." }}` / `{{ tn ... }}` / `{{ tc ... }}` helpers,
and a lang-switcher component — all in ~150 LOC of plain Go (the i18n
library) plus ≈15 LOC of `main.go` glue.

The architectural point: **`WithRequestFuncs` is the contract; everything
else is library code adopters bring themselves.** This example uses a
tiny hand-rolled PO loader to stay self-contained. Real apps should
swap in [`gotext`](https://pkg.go.dev/github.com/leonelquinteros/gotext)
or [`go-i18n`](https://pkg.go.dev/github.com/nicksnyder/go-i18n/v2)
without changing the Gastro-facing wiring.

## Run it

```sh
cd examples/i18n
go run .                  # http://localhost:4242
```

Try:

```sh
curl -H "Accept-Language: de" http://localhost:4242/
curl -H "Accept-Language: da" http://localhost:4242/about
curl --cookie "gastro_locale=de" http://localhost:4242/
curl http://localhost:4242/da/                  # path-prefix
```

Dev mode with hot-reload on `.po` changes:

```sh
gastro dev --watch "i18n/*.po"
```

The `--watch` flag is what makes PO edits trigger a restart in dev mode
— without it, the embedded catalogue is baked into the binary at build
time and dev edits don't propagate. See `docs/dev-mode.md` for the
flag's full contract.

## How it works

### Locale detection middleware

`internal/i18n/i18n.go` exposes `Catalog.Middleware`, which is
registered as a `gastro.WithMiddleware("/{path...}", cat.Middleware)`.
On every request it picks a locale using the priority:

1. URL path prefix (`/da/...`, `/de/...`)
2. `gastro_locale` cookie
3. `Accept-Language` header
4. The catalogue's fallback locale (`en` here)

The selected `*Localizer` is attached to `r.Context()` under an
unexported key. `i18n.FromCtx(ctx)` is the accessor.

### `WithRequestFuncs` binder

`main.go` registers a binder that pulls the per-request Localizer and
returns method values bound to it:

```go
gastro.WithRequestFuncs(func(r *http.Request) template.FuncMap {
    l := i18n.FromCtx(r.Context())
    return template.FuncMap{
        "t":  l.T,
        "tn": l.TN,
        "tc": l.TC,
    }
}),
```

That's it. Templates use `{{ t "Welcome" }}`, `{{ tc "button" "Open" }}`,
`{{ printf (tn "%d item" "%d items" .Count) .Count }}` — the closures
capture `l` and resolve against the right locale because the binder
runs once per request.

### `WithFuncs` for static helpers

The lang-switcher needs to rewrite `/about` → `/de/about` (and so on)
for every locale. That's pure string manipulation; no request state
required. It lives as a static `gastro.WithFuncs` registration:

```go
gastro.WithFuncs(template.FuncMap{
    "langPath": rewriteLangPath,    // pure function
    "locales":  func() []string { return cat.Locales() },
}),
```

`WithRequestFuncs` is for state that varies per request.
`WithFuncs` is for state that doesn't. Use the right tier.

## File layout

```
examples/i18n/
├── main.go                        ≈100 LOC — gastro wiring
├── internal/i18n/
│   ├── i18n.go                    ≈250 LOC — PO loader, Localizer, Middleware
│   └── i18n_test.go               ≈180 LOC — round-trip tests
├── i18n/
│   ├── en.po
│   ├── da.po
│   └── de.po
├── pages/
│   ├── index.gastro               uses {{ t }} and {{ tn }}
│   └── about.gastro
└── components/
    ├── layout.gastro              uses {{ t "Home" }} inside the layout
    └── lang-switch.gastro
```

The internal/i18n package is **95% generic Go**. It has no reference to
Gastro at all — it's a `context.Context` accessor (`FromCtx`), a stdlib
HTTP middleware (`Middleware`), and three methods on a struct (`T`,
`TN`, `TC`). The Gastro-specific glue is the ≈15-line `WithRequestFuncs`
call in `main.go`.

## Translation workflow

The recommended flow uses `xgettext` (part of GNU gettext) to extract
strings from your Go sources and templates:

```sh
# Extract all {{ t "..." }} from templates and gastro source. xgettext
# doesn't natively parse .gastro files, but they're text — point it at
# the source tree and let it scan.
xgettext \
    --keyword=t:1 \
    --keyword=tn:1,2 \
    --keyword=tc:1c,2 \
    --from-code=UTF-8 \
    --output=i18n/messages.pot \
    pages/*.gastro components/*.gastro

# Update an existing translation file with new/changed strings.
msgmerge --update i18n/da.po i18n/messages.pot
```

After editing translations and running `gastro dev --watch "i18n/*.po"`,
the dev server restarts and the new translations appear on the next
request.

## Plural rules

The bundled `internal/i18n` package uses a trivial split: `n==1` →
singular, anything else → plural. This is correct for English, German,
Danish, and a handful of others, but wrong for Russian, Arabic, Polish,
and most non-Indo-European languages.

For CLDR-correct plural rules, swap in `gotext` (the most mature
Go gettext library):

```go
import "github.com/leonelquinteros/gotext"

func makeLocalizer(locale string, raw []byte) *gotext.Po {
    po := gotext.NewPo()
    po.Parse(raw)
    return po
}
```

The `WithRequestFuncs` binder shape stays the same; only the Localizer
type changes. Gastro doesn't care which library you use.

## Swapping in a different i18n library

The interface that matters is the one between `main.go` and the
Localizer:

```go
type Localizer interface {
    T(msgid string) string
    TN(singular, plural string, n int) string
    TC(ctx, msgid string) string
}
```

Any library can be wrapped to that shape. The middleware and `FromCtx`
accessor are equally library-agnostic — they only know about
`context.Value` and `*http.Request`. Replace them as needed.

This is the whole architectural argument for `WithRequestFuncs` over a
`pkg/gastro/i18n/` helper package: **adopters keep ownership of their
i18n stack**, and Gastro doesn't lock anyone in.

## See also

- `docs/helpers.md` — the `WithRequestFuncs` reference
- `docs/dev-mode.md` — the `--watch GLOB` flag
- `examples/csrf/` — same pattern, different domain (in this PR)
- `examples/csp/` — and another (in this PR)
