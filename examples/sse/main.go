package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"sync/atomic"
	"time"

	gastro "sse-example/.gastro"

	gastroRuntime "github.com/andrioid/gastro/pkg/gastro"
	"github.com/andrioid/gastro/pkg/gastro/datastar"
)

var count atomic.Int64

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "4242"
	}

	// Create a top-level mux and mount gastro routes alongside API handlers.
	mux := http.NewServeMux()

	// SSE endpoint: increments a counter and patches the DOM.
	// Triggered by Datastar's @get('/api/increment') on the button.
	mux.HandleFunc("GET /api/increment", handleIncrement)

	// SSE endpoint: streams a live clock every second.
	// Triggered by Datastar's @get('/api/clock') on data-init.
	mux.HandleFunc("GET /api/clock", handleClock)

	// Mount gastro page routes last (catch-all).
	mux.Handle("/", gastro.Routes())

	fmt.Printf("Listening on http://localhost:%s\n", port)
	log.Fatal(http.ListenAndServe(":"+port, mux))
}

// handleIncrement sends a single SSE event that patches the counter element.
// Uses gastro.Render for type-safe component rendering + Datastar for SSE.
func handleIncrement(w http.ResponseWriter, r *http.Request) {
	n := count.Add(1)

	html, err := gastro.Render.Counter(gastro.CounterProps{Count: int(n)})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	sse := datastar.NewSSE(w, r)
	sse.PatchElements(html)
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
