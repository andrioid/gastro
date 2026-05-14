// Package main is the gastro i18n example.
//
// What it demonstrates:
//
//   - WithRequestFuncs: binder closes over an i18n.Localizer pulled from
//     the request context. Three helpers (t, tn, tc) cover the
//     simple/plural/contextual gettext patterns.
//   - WithMiddleware: locale-detection middleware attaches the right
//     Localizer to every incoming request based on path / cookie /
//     Accept-Language.
//   - WithFuncs: a request-agnostic langPath helper that lang-switcher
//     components use to rewrite a URL with a different locale prefix.
//     This is the boring "static helper" tier — request-aware is
//     overkill for pure string manipulation.
//
// What it does NOT demonstrate:
//
//   - CLDR plural rules. The bundled internal/i18n package uses the
//     trivial n==1 → singular split. Real apps should use gotext.
//   - PO extraction tooling. Use xgettext directly.
//   - SEO-perfect [lang]/ route mirroring. The recipe runs every page
//     at every locale via a single set of files (locale picked at
//     request time); apps that care about per-locale URLs should
//     write pages/[lang]/index.gastro files.
//
// The point of the example is the architectural statement:
// **WithRequestFuncs is the contract; everything else is plain Go.**
// internal/i18n/ is ~250 LOC of plain Go; the gastro-specific wiring
// in this main.go is ~15 LOC.
package main

import (
	"embed"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"strings"

	"gastro-i18n-example/internal/i18n"

	gastro "gastro-i18n-example/.gastro"
)

//go:embed i18n/*.po
var poFS embed.FS

func main() {
	cat, err := i18n.Load(poFS, "i18n", []string{"en", "da", "de"}, "en")
	if err != nil {
		fmt.Fprintf(os.Stderr, "i18n: %v\n", err)
		os.Exit(1)
	}

	router := gastro.New(
		// Locale detection runs for every page — sets r.Context() so
		// i18n.FromCtx returns the right Localizer.
		gastro.WithMiddleware("/{path...}", cat.Middleware),

		// Request-aware helpers: the binder runs once per request,
		// pulls the Localizer out of the context, and returns method
		// values closed over it. Templates use {{ t "..." }} etc.
		//
		// This is the architectural punchline: ≈15 LOC of glue, ≈250
		// LOC of plain Go in internal/i18n/ that knows nothing about
		// gastro.
		gastro.WithRequestFuncs(func(r *http.Request) template.FuncMap {
			l := i18n.FromCtx(r.Context())
			return template.FuncMap{
				"t":  l.T,
				"tn": l.TN,
				"tc": l.TC,
			}
		}),

		// langPath is request-agnostic — it just rewrites a URL's
		// locale prefix and never reads request state. Registered as
		// a static WithFuncs helper rather than a request-aware
		// helper. The lang-switcher component uses it to render
		// "switch to Danish" links.
		gastro.WithFuncs(template.FuncMap{
			"langPath": func(locale, path string) string {
				return rewriteLangPath(locale, path)
			},
			"locales": func() []string { return cat.Locales() },
		}),
	)

	port := os.Getenv("PORT")
	if port == "" {
		port = "4242"
	}
	fmt.Printf("gastro-i18n: http://localhost:%s\n", port)
	if err := http.ListenAndServe(":"+port, router.Handler()); err != nil {
		fmt.Fprintf(os.Stderr, "server: %v\n", err)
		os.Exit(1)
	}
}

// rewriteLangPath returns the supplied URL path with its locale prefix
// replaced by locale. Live behaviour:
//
//	rewriteLangPath("da", "/")        == "/da/"
//	rewriteLangPath("de", "/about")   == "/de/about"
//	rewriteLangPath("de", "/da/about") == "/de/about"
//
// The function lives in main.go (not in pkg/i18n) because it's pure UI
// concerns: which locale prefix should the lang-switcher links point at.
// Backend code never needs it.
func rewriteLangPath(locale, path string) string {
	if path == "" {
		path = "/"
	}
	rest := strings.TrimPrefix(path, "/")
	if i := strings.Index(rest, "/"); i >= 0 {
		first := rest[:i]
		if len(first) >= 2 && len(first) <= 5 {
			// Strip the existing locale prefix before re-applying.
			rest = rest[i+1:]
		}
	} else if len(rest) >= 2 && len(rest) <= 5 {
		// Path is just "/xx" — drop it, leaving the bare locale prefix.
		rest = ""
	}
	if rest == "" {
		return "/" + locale + "/"
	}
	return "/" + locale + "/" + rest
}
