package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"sync/atomic"

	gastro "gastro-website/.gastro"

	"github.com/andrioid/gastro/pkg/gastro/datastar"
)

var count atomic.Int64

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "4242"
	}

	mux := http.NewServeMux()

	// SSE endpoint for the live demo counter
	mux.HandleFunc("GET /api/increment", handleIncrement)

	// Gastro page routes (catch-all)
	mux.Handle("/", gastro.Routes())

	fmt.Printf("Listening on http://localhost:%s\n", port)
	log.Fatal(http.ListenAndServe(":"+port, mux))
}

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
