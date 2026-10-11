package gastro

import (
	"fmt"
	"net/http"
)

// Route is one entry of a generated router's route table: an
// http.ServeMux pattern, the handler serving it, and a human-readable
// source ("page pages/index.gastro", `WithHandler("POST /tasks")`) used
// in conflict panics.
//
// Generated New() builds the table from the page routes (GET-only), the
// static-asset prefix, and WithHandler registrations; application code
// does not construct it.
type Route struct {
	Pattern string
	Source  string
	Handler http.Handler
}

// MergeRoutes applies WithHandler registrations to the generated routes.
// A handler whose pattern is identical to a generated route's replaces
// that route's handler (keeping its position); any other handler is
// appended. Two handlers replacing the same generated route panic,
// naming both.
func MergeRoutes(generated, handlers []Route) []Route {
	out := make([]Route, len(generated), len(generated)+len(handlers))
	copy(out, generated)
	replacedBy := make(map[int]string, len(handlers))
	for _, h := range handlers {
		i := indexOfPattern(out[:len(generated)], h.Pattern)
		if i < 0 {
			out = append(out, h)
			continue
		}
		if prev, dup := replacedBy[i]; dup {
			panic(fmt.Sprintf("gastro: %s and %s both replace %s (pattern %q)", prev, h.Source, generated[i].Source, h.Pattern))
		}
		replacedBy[i] = h.Source
		out[i] = Route{
			Pattern: h.Pattern,
			Source:  h.Source + " (replacing " + generated[i].Source + ")",
			Handler: h.Handler,
		}
	}
	return out
}

func indexOfPattern(routes []Route, pattern string) int {
	for i, r := range routes {
		if r.Pattern == pattern {
			return i
		}
	}
	return -1
}

// NewRouteMux registers routes on a fresh http.ServeMux in order. When
// ServeMux rejects a registration (an invalid pattern, or one that
// conflicts with an earlier route) NewRouteMux panics with a message
// naming the sources of both routes.
func NewRouteMux(routes []Route) *http.ServeMux {
	mux := http.NewServeMux()
	for i, r := range routes {
		err := tryHandle(mux, r.Pattern, r.Handler)
		if err == nil {
			continue
		}
		for _, prev := range routes[:i] {
			if patternsConflict(prev.Pattern, r.Pattern) {
				panic(fmt.Sprintf("gastro: route %q from %s conflicts with route %q from %s: %v",
					r.Pattern, r.Source, prev.Pattern, prev.Source, err))
			}
		}
		panic(fmt.Sprintf("gastro: route %q from %s: %v", r.Pattern, r.Source, err))
	}
	return mux
}

// tryHandle is mux.Handle with ServeMux's registration panic turned into
// an error.
func tryHandle(mux *http.ServeMux, pattern string, h http.Handler) (err error) {
	defer func() {
		if rec := recover(); rec != nil {
			err = fmt.Errorf("%v", rec)
		}
	}()
	mux.Handle(pattern, h)
	return nil
}

// patternsConflict reports whether registering a then b on one mux fails,
// i.e. whether ServeMux considers the two patterns conflicting.
func patternsConflict(a, b string) bool {
	mux := http.NewServeMux()
	noop := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	if tryHandle(mux, a, noop) != nil {
		return false
	}
	return tryHandle(mux, b, noop) != nil
}
