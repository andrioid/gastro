package gastro

import (
	"log"
	"net/http"
)

// Recover is a deferred function for generated handlers that catches
// panics, logs them, and — only when the response is still uncommitted —
// writes a 500 Internal Server Error.
//
// When w is a gastro-owned page writer (Track B) and a body has already
// been written, Recover logs the panic but does not attempt to write the
// 500 page. Writing after the body would either no-op (closed connection)
// or interleave with whatever the panicking code emitted, both of which
// confuse clients. The pre-Track-B behaviour (always write 500) is
// preserved for plain http.ResponseWriter values.
func Recover(w http.ResponseWriter, r *http.Request) {
	err := recover()
	if err == nil {
		return
	}

	log.Printf("gastro: panic in %s %s: %v", r.Method, r.URL.Path, err)

	if g, ok := unwrapGastroWriter(w); ok && (g.bodyWritten || g.headerCommitted) {
		// Headers — or worse, body bytes — are already on the wire.
		// A second WriteHeader/Write would be ignored or interleave
		// with the partial response. Log only.
		return
	}

	http.Error(w, "Internal Server Error", http.StatusInternalServerError)
}
