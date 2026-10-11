package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	gastro "dashboard/.gastro"

	"dashboard/mock"

	"github.com/andrioid/gastro/datastar"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "4242"
	}

	// The page opens one stream (GET /api/dashboard), registered next to
	// the pages with WithHandler; it re-renders the Dashboard component through the same router.
	var router *gastro.Router
	router = gastro.New(
		// {{ datastarScript }} in the layout, and the embedded Datastar
		// client it points at.
		gastro.WithFuncs(datastar.Funcs()),
		gastro.WithHandler(datastar.ClientPattern, datastar.ClientHandler()),
		gastro.WithHandler("GET /api/dashboard", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			handleDashboard(router, w, r)
		})),
	)

	fmt.Printf("Listening on http://localhost:%s\n", port)
	log.Fatal(http.ListenAndServe(":"+port, router.Handler()))
}

func handleDashboard(router *gastro.Router, w http.ResponseWriter, r *http.Request) {
	sse, err := datastar.NewSSE(w, r)
	if err != nil {
		log.Printf("dashboard: open stream: %v", err)
		return
	}

	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	send := func() error {
		d := mock.Generate()

		html, err := router.Render(gastro.Dashboard, gastro.DashboardProps{
			ActiveCalls: d.ActiveCalls,
			AvgWaitSecs: d.AvgWaitSecs,
			CallsToday:  d.CallsToday,
			QueueDepth:  d.QueueDepth,
			Agents:      d.Agents,
		})
		if err != nil {
			return err
		}

		return sse.PatchElements(string(html),
			datastar.WithSelector("#dashboard"),
			datastar.WithMode(datastar.ModeInner),
		)
	}

	if err := send(); err != nil {
		return
	}

	for {
		select {
		case <-sse.Context().Done():
			return
		case <-ticker.C:
			if err := send(); err != nil {
				return
			}
		}
	}
}
