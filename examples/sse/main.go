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

	// pages/index.gastro is the GET view. The increment command is an
	// ordinary handler registered next to it with WithHandler: it shares
	// the router's deps (gastroRuntime.FromContext[*app.State] works inside it) and
	// middleware, and renders the Counter component through the router.
	//
	// WithMiddleware composes route middleware. The pattern uses Go's
	// stdlib syntax — "/{path...}" is a catch-all that matches every page
	// and WithHandler route. Method branching, if any, lives inside the
	// middleware itself.
	var router *gastro.Router
	router = gastro.New(
		gastro.WithDeps(state),
		gastro.WithHandler("POST /increment", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			increment(router, w, r)
		})),
		// The clock streams from its own path because long-lived SSE
		// streams have a different shape than the request / response /
		// patch pattern of the increment command.
		gastro.WithHandler("GET /api/clock", http.HandlerFunc(handleClock)),
		gastro.WithMiddleware("/{path...}", logRequests),
	)

	fmt.Printf("Listening on http://localhost:%s\n", port)
	log.Fatal(http.ListenAndServe(":"+port, router.Handler()))
}

// increment bumps the counter and patches the Counter component into the
// page over SSE.
func increment(router *gastro.Router, w http.ResponseWriter, r *http.Request) {
	state := gastroRuntime.FromContext[*app.State](r.Context())
	n := state.Count.Add(1)

	html, err := router.Render(gastro.Counter, gastro.CounterProps{Count: int(n)})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	sse, err := datastar.NewSSE(w, r)
	if err != nil {
		log.Printf("counter: open stream: %v", err)
		return
	}
	if err := sse.PatchElements(string(html)); err != nil {
		log.Printf("counter: patch: %v", err)
	}
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

// handleClock streams the current time every second.
// Demonstrates a long-lived SSE connection using the generic SSE helper.
func handleClock(w http.ResponseWriter, r *http.Request) {
	sse := gastroRuntime.NewSSE(w, r)

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	// Send the current time immediately, then every tick.
	sendTime := func() error {
		now := time.Now().Format("15:04:05")
		html := fmt.Sprintf(`<div id="clock">%s</div>`, now)
		return sse.Send("datastar-patch-elements", "elements "+html)
	}

	sendTime()

	for {
		select {
		case <-sse.Context().Done():
			return
		case <-ticker.C:
			if err := sendTime(); err != nil {
				return
			}
		}
	}
}
