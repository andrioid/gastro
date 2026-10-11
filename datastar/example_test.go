package datastar_test

import (
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/http/httptest"
	"os"

	"github.com/andrioid/gastro/datastar"
)

// ExampleNewSSE shows the query side of a CQRS page: one long-lived stream
// that re-renders the whole region whenever state changes. Commands live in
// separate handlers and answer 204 No Content.
func ExampleNewSSE() {
	changes := make(chan int) // fed by your command handlers

	http.HandleFunc("GET /counter/stream", func(w http.ResponseWriter, r *http.Request) {
		sse, err := datastar.NewSSE(w, r, datastar.WithCompression())
		if err != nil {
			log.Printf("open stream: %v", err)
			return
		}
		for {
			select {
			case <-sse.Context().Done():
				return
			case n := <-changes:
				if err := sse.PatchElements(fmt.Sprintf(`<div id="counter">%d</div>`, n)); err != nil {
					log.Printf("patch: %v", err)
					return
				}
			}
		}
	})
}

// ExampleSSE_PatchElements prints the wire format of a single patch.
func ExampleSSE_PatchElements() {
	w := httptest.NewRecorder()
	sse, err := datastar.NewSSE(w, httptest.NewRequest(http.MethodGet, "/stream", nil))
	if err != nil {
		log.Fatal(err)
	}
	if err := sse.PatchElements(`<li>new</li>`, datastar.WithSelector("#list"), datastar.WithMode(datastar.ModeAppend)); err != nil {
		log.Fatal(err)
	}
	fmt.Print(w.Body.String())
	// Output:
	// event: datastar-patch-elements
	// data: selector #list
	// data: mode append
	// data: elements <li>new</li>
}

// ExampleSSE_MarshalAndPatchSignals updates client-side signals; the value
// is applied as a JSON merge patch.
func ExampleSSE_MarshalAndPatchSignals() {
	w := httptest.NewRecorder()
	sse, err := datastar.NewSSE(w, httptest.NewRequest(http.MethodGet, "/stream", nil))
	if err != nil {
		log.Fatal(err)
	}
	if err := sse.MarshalAndPatchSignals(map[string]any{"saving": false}); err != nil {
		log.Fatal(err)
	}
	fmt.Print(w.Body.String())
	// Output:
	// event: datastar-patch-signals
	// data: signals {"saving":false}
}

// ExampleClientHandler serves the embedded Datastar client and makes
// {{ datastarScript }} available to templates.
func ExampleClientHandler() {
	mux := http.NewServeMux()
	mux.Handle(datastar.ClientPattern, datastar.ClientHandler())

	// With gastro: gastro.New(gastro.WithFuncs(datastar.Funcs()), ...)
	script := datastar.Funcs()["datastarScript"].(func() template.HTML)()
	fmt.Fprintln(os.Stdout, script == template.HTML(`<script type="module" src="`+datastar.ClientPath+`"></script>`))
	// Output: true
}

// ExampleLivePages keeps every open page current: a command writes and
// calls Notify; each page's stream re-renders the page and morphs its live
// root. With gastro, register live.Funcs() with WithFuncs, mount
// live.Handler() with WithHandler, and set live.Pages = router.Handler().
func ExampleLivePages() {
	var changes datastar.Notifier
	live := &datastar.LivePages{Signal: &changes, Root: "app", Compression: true}

	layout := template.Must(template.New("layout").Funcs(live.Funcs()).Parse(
		`<body>{{ liveRoot .Body }}{{ liveScript }}</body>`))

	mux := http.NewServeMux()
	mux.Handle(live.Pattern(), live.Handler())
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		_ = layout.Execute(w, map[string]any{"Body": template.HTML(`<p>hello</p>`)})
	})
	mux.HandleFunc("POST /touch", func(w http.ResponseWriter, r *http.Request) {
		changes.Notify() // after the write
		w.WriteHeader(http.StatusNoContent)
	})
	live.Pages = mux

	fmt.Println(live.Pattern())
	// Output: GET /_gastro/live
}
