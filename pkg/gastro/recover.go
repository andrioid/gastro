package gastro

import (
	"log"
	"net/http"
)

// Recover is a deferred function for generated handlers that catches panics,
// logs them, and returns a 500 Internal Server Error.
func Recover(w http.ResponseWriter, r *http.Request) {
	if err := recover(); err != nil {
		log.Printf("gastro: panic in %s %s: %v", r.Method, r.URL.Path, err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}
