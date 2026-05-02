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

	// Track B (plans/frictions-plan.md §4.10): the increment endpoint is
	// gone from main.go. Both the GET render and the POST patch live in
	// pages/index.gastro and branch on r.Method. The router registers
	// the page for every HTTP method so the same .gastro file handles
	// both. This is the headline mechanic — one source of truth per
	// route, no parallel API handler to keep in sync with the page.
	router := gastro.New(gastro.WithDeps(state))

	// The clock streams from a separate path because long-lived SSE
	// streams have a different shape than the request / response /
	// patch pattern of the index page. It still demonstrates that
	// page handlers (Track B) and side-mounted SSE handlers coexist.
	mux := router.Mux()
	mux.HandleFunc("GET /api/clock", handleClock)

	fmt.Printf("Listening on http://localhost:%s\n", port)
	log.Fatal(http.ListenAndServe(":"+port, router.Handler()))
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
