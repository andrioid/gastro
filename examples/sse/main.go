package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	gastro "sse-example/.gastro"
	"sse-example/app"

	"github.com/andrioid/gastro/datastar"
	gastroRuntime "github.com/andrioid/gastro/pkg/gastro"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "4242"
	}

	state := app.New()

	// The clock on the page is just page state too: tick the change signal
	// every second and every open page re-renders with the new time.
	go func() {
		for range time.Tick(time.Second) {
			state.Changes.Notify()
		}
	}()

	fmt.Printf("Listening on http://localhost:%s\n", port)
	log.Fatal(http.ListenAndServe(":"+port, newHandler(state)))
}

// newHandler wires the CQRS shape from docs/sse.md with live pages:
//   - pages/index.gastro is the GET view (query). Its layout wraps the page
//     in {{ liveRoot }} and opens the page's one stream with {{ liveScript }};
//   - the live stream (live.Pattern()) re-renders the page through the
//     router after every state change and morphs the live root;
//   - POST /increment is a command: it writes, signals, and answers 204.
//
// WithHandler routes share the router's deps (gastroRuntime.FromContext
// works inside them) and middleware. "/{path...}" is a catch-all that
// matches every page and WithHandler route.
func newHandler(state *app.State) http.Handler {
	live := &datastar.LivePages{
		Signal:      &state.Changes,
		Root:        "app",
		Compression: true,
	}
	router := gastro.New(
		gastro.WithDeps(state),
		// {{ datastarScript }} in the layout, and the embedded client it
		// points at.
		gastro.WithFuncs(datastar.Funcs()),
		gastro.WithHandler(datastar.ClientPattern, datastar.ClientHandler()),
		// {{ liveRoot }} and {{ liveScript }}, and the stream they open.
		gastro.WithFuncs(live.Funcs()),
		gastro.WithHandler(live.Pattern(), live.Handler()),
		gastro.WithHandler("POST /increment", http.HandlerFunc(increment)),
		gastro.WithMiddleware("/{path...}", logRequests),
	)
	// The stream re-renders pages through the router itself.
	h := router.Handler()
	live.Pages = h
	return h
}

// increment is a command: it writes and renders nothing. The state's change
// signal wakes every open page's stream, which re-renders the page.
func increment(w http.ResponseWriter, r *http.Request) {
	gastroRuntime.FromContext[*app.State](r.Context()).Increment(1)
	w.WriteHeader(http.StatusNoContent)
}

// logRequests is a tiny middleware that logs each request's method,
// path, and elapsed handling time. It illustrates the canonical shape
// of a WithMiddleware-compatible function: take an http.Handler, wrap
// it, return the wrapped handler.
func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s (%s)", r.Method, r.URL.Path, time.Since(start))
	})
}
