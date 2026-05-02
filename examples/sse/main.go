package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	gastro "sse-example/.gastro"
	"sse-example/app"

	gastroRuntime "github.com/andrioid/gastro/pkg/gastro"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "4242"
	}

	state := app.New()

	// Track B (docs/history/frictions-plan.md §4.10): the increment endpoint is
	// gone from main.go. Both the GET render and the POST patch live in
	// pages/index.gastro and branch on r.Method. The router registers
	// the page for every HTTP method so the same .gastro file handles
	// both. This is the headline mechanic — one source of truth per
	// route, no parallel API handler to keep in sync with the page.
	//
	// Wave 4 / C2 (docs/history/frictions-plan.md §3 Wave 4): WithMiddleware
	// composes route middleware. The pattern uses Go's stdlib syntax —
	// "/{path...}" is a catch-all that matches every page route. Method
	// branching, if any, lives inside the middleware itself.
	router := gastro.New(
		gastro.WithDeps(state),
		gastro.WithMiddleware("/{path...}", logRequests),
	)

	// The clock streams from a separate path because long-lived SSE
	// streams have a different shape than the request / response /
	// patch pattern of the index page. It still demonstrates that
	// page handlers (Track B) and side-mounted SSE handlers coexist.
	mux := router.Mux()
	mux.HandleFunc("GET /api/clock", handleClock)

	fmt.Printf("Listening on http://localhost:%s\n", port)
	log.Fatal(http.ListenAndServe(":"+port, router.Handler()))
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
