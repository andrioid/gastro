package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	gastro "dashboard/.gastro"

	"dashboard/mock"

	"github.com/andrioid/gastro/pkg/gastro/datastar"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "4242"
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/dashboard", handleDashboard)
	mux.Handle("/", gastro.Routes())

	fmt.Printf("Listening on http://localhost:%s\n", port)
	log.Fatal(http.ListenAndServe(":"+port, mux))
}

func handleDashboard(w http.ResponseWriter, r *http.Request) {
	sse := datastar.NewSSE(w, r)

	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	send := func() error {
		d := mock.Generate()

		html, err := gastro.Render.Dashboard(gastro.DashboardProps{
			ActiveCalls: d.ActiveCalls,
			AvgWaitSecs: d.AvgWaitSecs,
			CallsToday:  d.CallsToday,
			QueueDepth:  d.QueueDepth,
			Agents:      d.Agents,
		})
		if err != nil {
			return err
		}

		return sse.PatchElements(html,
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
