// Package main is the gastro CSRF example.
//
// What it demonstrates:
//
//   - WithRequestFuncs serving two helpers with DIFFERENT return
//     types from the same binder: csrfToken (string) and csrfField
//     (template.HTML). The binder is ≈3 lines because
//     internal/csrf/RequestFuncs already returns the FuncMap shape
//     gastro expects.
//   - Middleware that PRODUCES the request state (mints + verifies
//     the token cookie). The token persists across requests, unlike
//     the i18n example where state was derived purely from the
//     incoming request.
//   - Form + header submission paths verified by round-trip tests.
//   - The form's POST is a command: an ordinary handler registered
//     next to the GET page with WithHandler, behind the same
//     middleware.
//
// The architectural punchline is the same as the i18n example:
// ≈95% generic Go + ≈5 LOC of glue.
package main

import (
	"fmt"
	"html"
	"net/http"
	"os"

	"gastro-csrf-example/internal/csrf"

	gastro "gastro-csrf-example/.gastro"
)

func main() {
	router := gastro.New(
		gastro.WithHandler("POST /{$}", http.HandlerFunc(greet)),
		gastro.WithMiddleware("/{path...}", csrf.Middleware),
		gastro.WithRequestFuncs(csrf.RequestFuncs),
	)

	port := os.Getenv("PORT")
	if port == "" {
		port = "4242"
	}
	fmt.Printf("gastro-csrf: http://localhost:%s\n", port)
	if err := http.ListenAndServe(":"+port, router.Handler()); err != nil {
		fmt.Fprintf(os.Stderr, "server: %v\n", err)
		os.Exit(1)
	}
}

// greet handles the form submission. The middleware already verified the
// CSRF token, so any POST that reaches this point is a valid submission.
func greet(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, "Hello, %s! (CSRF-protected)", html.EscapeString(r.FormValue("name")))
}
