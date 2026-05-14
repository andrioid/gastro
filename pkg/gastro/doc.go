// Package gastro is the runtime for the gastro server-side UI framework.
//
// Gastro renders typed, file-based pages and components from .gastro
// source files (HTML + Go frontmatter) using Go's html/template under
// the hood. This package exposes the small set of runtime primitives
// that generated code and host applications rely on: request Context,
// typed dependency injection, Server-Sent Events, dev-time live
// reload, response writer helpers, and the template FuncMap.
//
// Most users never construct these types directly — they are wired up
// by the code that `gastro generate` emits under `.gastro/` inside
// your project. The exported API here is what host applications,
// middleware, and SSE handlers reach for.
//
// # Two ways to use gastro
//
// Gastro supports two integration styles with the same runtime:
//
//   - Framework mode — gastro owns main.go and the dev server.
//     Best when starting a new server-rendered app from scratch.
//     See https://github.com/andrioid/gastro/blob/main/docs/getting-started.md
//
//   - Library mode — you keep main.go and mount gastro into an
//     existing http.Handler tree. Best for adding server-rendered
//     UI (admin panels, dashboards, status pages, marketing pages)
//     to an existing Go service without taking a dependency on a new
//     process lifecycle. The dev loop is driven by `gastro watch`.
//     See https://github.com/andrioid/gastro/blob/main/docs/getting-started-library.md
//
// # Library-mode quick start
//
// Inside your existing module:
//
//	go get -tool github.com/andrioid/gastro/cmd/gastro
//	go tool gastro generate --project ./internal/web
//
// Then wire the generated package into your existing mux. The
// generated package (conventionally aliased `gastro`) carries the
// `New`, `WithDeps`, `WithMiddleware`, and `Handler` entry points;
// this runtime package supplies the per-request primitives those
// handlers use:
//
//	import (
//	    gastro "myapp/internal/web/.gastro" // generated
//	    runtime "github.com/andrioid/gastro/pkg/gastro"
//	)
//
//	func main() {
//	    db := openDB()
//	    r := gastro.New(
//	        gastro.WithDeps(db),
//	        gastro.WithMiddleware("/{path...}", logRequests),
//	    )
//
//	    mux := http.NewServeMux()
//	    mux.HandleFunc("GET /api/v1/users", listUsers)
//	    mux.Handle("/", r.Handler()) // gastro owns the UI subtree
//
//	    _ = runtime.IsDev() // runtime helpers stay available
//	    http.ListenAndServe(":8080", mux)
//	}
//
// Pages drop into ./internal/web/pages/ and components into
// ./internal/web/components/. Both compile to typed Go code that
// imports this package.
//
// # Key entry points
//
// The most commonly used types and functions for host code are:
//
//   - [Context] — request/response handle passed to page handlers.
//   - [NewSSE] / [SSE] — upgrade an http.ResponseWriter to a
//     Server-Sent Events stream. See also the [datastar] subpackage
//     for Datastar-formatted events.
//   - [FromContext], [FromContextOK] — read a typed dependency
//     attached via the generated package's WithDeps option.
//   - [NewDevReloader] — opt-in dev-mode browser auto-reload.
//   - [DefaultErrorHandler], [Recover] — error and panic handling.
//   - [DefaultFuncs] — the default template.FuncMap used by all
//     generated templates.
//
// # Examples
//
// See the Example functions on this page for runnable snippets, and
// the examples/ directory in the repository for full-app demos:
// https://github.com/andrioid/gastro/tree/main/examples
//
// [datastar]: https://pkg.go.dev/github.com/andrioid/gastro/pkg/gastro/datastar
package gastro
