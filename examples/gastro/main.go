package main

import (
	"fmt"
	"log"
	"net/http"
	"os"

	gastro "gastro-website/.gastro"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "4242"
	}

	mux := http.NewServeMux()

	// Traditional form handler (Post/Redirect/Get)
	mux.HandleFunc("POST /guestbook", handleGuestbookPost)

	// Datastar SSE endpoints
	mux.HandleFunc("GET /api/ds/search", handleDsSearch)
	mux.HandleFunc("POST /api/ds/add", handleDsAdd)
	mux.HandleFunc("GET /api/ds/edit/{id}", handleDsEdit)
	mux.HandleFunc("POST /api/ds/save/{id}", handleDsSave)

	// HTMX endpoints (plain HTML fragments)
	mux.HandleFunc("GET /api/htmx/search", handleHtmxSearch)
	mux.HandleFunc("POST /api/htmx/add", handleHtmxAdd)
	mux.HandleFunc("GET /api/htmx/edit/{id}", handleHtmxEdit)
	mux.HandleFunc("POST /api/htmx/save/{id}", handleHtmxSave)

	// Gastro page routes (catch-all)
	mux.Handle("/", gastro.Routes())

	fmt.Printf("Listening on http://localhost:%s\n", port)
	log.Fatal(http.ListenAndServe(":"+port, mux))
}
