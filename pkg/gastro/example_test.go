package gastro_test

import (
	"fmt"
	"net/http"
	"time"

	"github.com/andrioid/gastro/pkg/gastro"
)

// DB is a placeholder type for the package-level Example.
type DB struct{ DSN string }

// Example demonstrates the smallest useful library-mode integration:
// mounting a gastro-generated UI subtree into an existing http.ServeMux
// that already serves a JSON API.
//
// The `web` import path refers to the package emitted by
// `go tool gastro generate --project ./internal/web` — it carries the
// New / WithDeps / WithMiddleware / Handler entry points generated for
// your project. The runtime types live in this package.
func Example() {
	// In real code, the import would be:
	//
	//   import web "myapp/internal/web/.gastro"
	//
	//   r := web.New(
	//       web.WithDeps(&DB{DSN: "postgres://..."}),
	//   )
	//
	//   mux := http.NewServeMux()
	//   mux.HandleFunc("GET /api/v1/users", listUsers)
	//   mux.Handle("/", r.Handler())
	//
	//   http.ListenAndServe(":8080", mux)

	fmt.Println("see https://github.com/andrioid/gastro/blob/main/docs/getting-started-library.md")
	// Output: see https://github.com/andrioid/gastro/blob/main/docs/getting-started-library.md
}

// ExampleNewSSE shows how to stream Server-Sent Events from a plain
// http.HandlerFunc. SSE handlers compose with any router and do not
// require gastro's page system, which makes them a common library-mode
// touchpoint: an existing service can grow a live dashboard endpoint
// without adopting the full framework.
func ExampleNewSSE() {
	handler := func(w http.ResponseWriter, r *http.Request) {
		sse := gastro.NewSSE(w, r)

		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-sse.Context().Done():
				return
			case t := <-ticker.C:
				if err := sse.Send("tick", t.Format(time.RFC3339)); err != nil {
					return
				}
			}
		}
	}

	http.HandleFunc("/api/clock", handler)
}

// ExampleFromContext shows how a page handler retrieves a typed
// dependency that was registered on the router via the generated
// package's WithDeps option.
//
// The runtime panics with a descriptive error if no value of the
// requested type was registered; use [FromContextOK] when a missing
// dependency should be recoverable.
func ExampleFromContext() {
	type Database struct{ Name string }

	// Inside a page handler or SSE handler:
	handler := func(w http.ResponseWriter, r *http.Request) {
		db := gastro.FromContext[*Database](r.Context())
		fmt.Fprintf(w, "using db %s", db.Name)
	}

	_ = handler
}

// ExampleIsDev shows how host code can branch on dev-mode behaviour.
// IsDev reads the GASTRO_DEV environment variable, which `gastro dev`
// and `gastro watch` set automatically while you iterate.
func ExampleIsDev() {
	if gastro.IsDev() {
		// e.g. mount the dev reloader, serve files from disk, etc.
		_ = gastro.NewDevReloader()
	}
}
