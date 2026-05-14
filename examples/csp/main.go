// Package main is the gastro CSP-nonce example.
//
// The architectural axis it exercises: **helper-to-middleware
// coordination**. The CSP middleware generates a fresh nonce per
// request, writes the Content-Security-Policy response header
// advertising it, AND attaches the same nonce to the request context
// for the cspNonce template helper. The inline <script nonce="...">
// tag the template emits must agree with the header for the browser
// to allow the script to run.
//
// If gastro's per-request FuncMap path got this wrong — invoking the
// binder once at New() and caching the result, say — the nonce in
// the rendered HTML would diverge from the header on every subsequent
// request and inline scripts would silently break. That this works
// is the proof that WithRequestFuncs is genuinely per-request.
package main

import (
	"fmt"
	"html/template"
	"net/http"
	"os"

	"gastro-csp-example/internal/csp"

	gastro "gastro-csp-example/.gastro"
)

func main() {
	router := gastro.New(
		gastro.WithMiddleware("/{path...}", csp.Middleware),
		gastro.WithRequestFuncs(func(r *http.Request) template.FuncMap {
			fm := template.FuncMap{}
			for k, v := range csp.RequestFuncs(r) {
				fm[k] = v
			}
			return fm
		}),
	)

	port := os.Getenv("PORT")
	if port == "" {
		port = "4242"
	}
	fmt.Printf("gastro-csp: http://localhost:%s\n", port)
	if err := http.ListenAndServe(":"+port, router.Handler()); err != nil {
		fmt.Fprintf(os.Stderr, "server: %v\n", err)
		os.Exit(1)
	}
}
