package datastar_test

import (
	"net/http"

	"github.com/andrioid/gastro/pkg/gastro/datastar"
)

// ExampleSSE_PatchElements shows how to push an HTML fragment to the
// browser over a Datastar SSE stream. The fragment must contain an
// element with a matching id attribute; Datastar morphs it into place
// without a full page reload.
//
// This is the typical library-mode pattern: a small endpoint that
// updates a single region of a page rendered by gastro.
func ExampleSSE_PatchElements() {
	handler := func(w http.ResponseWriter, r *http.Request) {
		sse := datastar.NewSSE(w, r)

		// Replace #counter with the new fragment.
		_ = sse.PatchElements(`<div id="counter">42</div>`)
	}

	http.HandleFunc("/api/counter", handler)
}

// ExampleSSE_PatchSignals shows how to update Datastar signals from
// the server. Any JSON-serialisable value works; nested maps and
// structs are merged into the client-side signal store.
func ExampleSSE_PatchSignals() {
	handler := func(w http.ResponseWriter, r *http.Request) {
		sse := datastar.NewSSE(w, r)

		_ = sse.PatchSignals(map[string]any{
			"user": map[string]any{
				"name":   "Ada",
				"online": true,
			},
		})
	}

	http.HandleFunc("/api/presence", handler)
}
