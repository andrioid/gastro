package main

import (
	"fmt"
	"net/http"
	"os"

	gastro "myblog/.gastro"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "4242"
	}

	routes := gastro.Routes()

	fmt.Printf("Listening on http://localhost:%s\n", port)
	http.ListenAndServe(":"+port, routes)
}
