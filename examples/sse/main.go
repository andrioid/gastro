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

	// The CQRS shape from docs/sse.md:
	//   - pages/index.gastro is the GET view (query);
	//   - GET /stream is the page's one long-lived SSE stream, re-rendering
	//     the counter on every change (and the clock every second);
	//   - POST /increment is a command: it writes and answers 204.
	//
	// WithHandler routes share the router's deps (gastroRuntime.FromContext
	// works inside them) and middleware. "/{path...}" is a catch-all that
	// matches every page and WithHandler route.
	var router *gastro.Router
	router = gastro.New(
		gastro.WithDeps(state),
		// {{ datastarScript }} in the layout, and the embedded client it
		// points at.
		gastro.WithFuncs(datastar.Funcs()),
		gastro.WithHandler(datastar.ClientPattern, datastar.ClientHandler()),
		gastro.WithHandler("GET /stream", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			stream(router, w, r)
		})),
		gastro.WithHandler("POST /increment", http.HandlerFunc(increment)),
		gastro.WithMiddleware("/{path...}", logRequests),
	)

	fmt.Printf("Listening on http://localhost:%s\n", port)
	log.Fatal(http.ListenAndServe(":"+port, router.Handler()))
}

// stream is the page's query side: it sends the full Counter on connect and
// after every change, and the clock every second, until the client leaves.
func stream(router *gastro.Router, w http.ResponseWriter, r *http.Request) {
	state := gastroRuntime.FromContext[*app.State](r.Context())

	sse, err := datastar.NewSSE(w, r, datastar.WithCompression())
	if err != nil {
		log.Printf("stream: open: %v", err)
		return
	}

	changes, unsubscribe := state.Subscribe()
	defer unsubscribe()

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	sendCounter := func() error {
		html, err := router.Render(gastro.Counter, gastro.CounterProps{Count: state.Count()})
		if err != nil {
			return err
		}
		return sse.PatchElements(string(html))
	}
	sendClock := func() error {
		return sse.PatchElements(fmt.Sprintf(`<div id="clock">%s</div>`, time.Now().Format("15:04:05")))
	}

	if err := sendCounter(); err != nil {
		log.Printf("stream: %v", err)
		return
	}
	if err := sendClock(); err != nil {
		return
	}
	for {
		select {
		case <-sse.Context().Done():
			return
		case <-changes:
			if err := sendCounter(); err != nil {
				return
			}
		case <-ticker.C:
			if err := sendClock(); err != nil {
				return
			}
		}
	}
}

// increment is a command: it writes, notifies subscribers through the state,
// and renders nothing. Every open page sees the change via its stream.
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
