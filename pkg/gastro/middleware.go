package gastro

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
)

// MiddlewareFunc is the contract for HTTP middleware passed to
// WithMiddleware. Same shape as the chi/gorilla/stdlib convention:
// take an http.Handler, return an http.Handler that wraps it.
//
// Wave 4 / C2 (docs/history/frictions-plan.md §3 Wave 4).
type MiddlewareFunc func(http.Handler) http.Handler

// PatternMatchesAnyRoute reports whether middleware pattern matches at
// least one of knownRoutes when both are interpreted as http.ServeMux
// patterns.
//
// Used by codegen at New() time to validate WithMiddleware patterns:
// the pattern must match at least one auto-route, otherwise it is
// almost certainly a typo and we fail fast (same posture as
// WithOverride). Returning false from this function triggers the
// "pattern does not match any auto-route" panic.
//
// Mechanism. Register pattern in a throwaway *http.ServeMux with a
// sentinel handler, then for each known route construct a synthetic
// request whose URL exercises the route's exact path. If the throwaway
// mux dispatches *any* of those requests to the sentinel, the patterns
// overlap. Reusing http.ServeMux's pattern semantics this way means
// any future change to Go's pattern matching is automatically
// inherited — no parallel matcher to keep in sync.
//
// Limitation. http.ServeMux requires a concrete URL path to match
// against, so for a route pattern like "/blog/{slug}" we synthesise a
// path by replacing {name} segments with literal sentinel strings and
// {name...} suffixes with a single segment. Routes that use
// {$} (exact-match) are tested with the path stripped of the {$}.
func PatternMatchesAnyRoute(pattern string, knownRoutes []string) bool {
	mux := http.NewServeMux()

	// Defensive: an invalid pattern would panic here. Caller should
	// validate syntax separately if it cares to distinguish "bad
	// pattern" from "no match"; for the codegen path the panic is
	// fine because it bubbles up through New() like any other config
	// error.
	mux.HandleFunc(pattern, func(http.ResponseWriter, *http.Request) {})

	for _, route := range knownRoutes {
		path := synthesizePath(route)
		req := httptest.NewRequest(http.MethodGet, path, nil)
		_, matched := mux.Handler(req)
		if matched == pattern {
			return true
		}
	}
	return false
}

// synthesizePath turns a ServeMux pattern into a concrete URL path that
// can be fed to httptest.NewRequest. Pattern segments like {slug} become
// "x", and {slug...} suffixes become a single segment "x". {$}
// (exact-match marker) is dropped because it is not a path component.
//
// The result does not need to be a valid real URL — only something
// http.ServeMux's matcher will accept and dispatch deterministically.
func synthesizePath(pattern string) string {
	// "GET /static/" → "/static/" for the path-shape extraction step.
	// The throwaway mux registration will see method-scoped patterns as
	// such; we only synthesise the URL path here.
	if i := strings.Index(pattern, " "); i >= 0 {
		pattern = pattern[i+1:]
	}

	// Strip the {$} exact-match marker.
	pattern = strings.TrimSuffix(pattern, "{$}")
	if pattern == "" {
		return "/"
	}

	segments := strings.Split(pattern, "/")
	for i, seg := range segments {
		if !strings.HasPrefix(seg, "{") || !strings.HasSuffix(seg, "}") {
			continue
		}
		// {name...} → "x" (single concrete segment satisfies the
		// trailing-wildcard match)
		// {name} → "x"
		segments[i] = "x"
	}

	out := strings.Join(segments, "/")
	if out == "" {
		return "/"
	}
	return out
}

// ValidateMiddlewarePattern returns nil if pattern matches at least one
// of knownRoutes, or a descriptive error suitable for a panic message
// if it does not. Codegen calls this from the generated New() so
// pattern typos surface at startup with the same wording as
// WithOverride's typo panic.
func ValidateMiddlewarePattern(pattern string, knownRoutes []string) error {
	if PatternMatchesAnyRoute(pattern, knownRoutes) {
		return nil
	}
	return fmt.Errorf(
		"gastro: WithMiddleware: pattern %q does not match any auto-route. known: %v",
		pattern, knownRoutes,
	)
}

// MiddlewareApplies reports whether the middleware pattern should wrap
// the handler for the given route pattern. Used by codegen in the mux
// build loop, once per (middleware, route) pair.
//
// Same probe mechanism as PatternMatchesAnyRoute, scoped to a single
// route. Kept as a separate function so the codegen build loop can call
// it inline per route without rebuilding the synthetic request loop.
func MiddlewareApplies(middlewarePattern, routePattern string) bool {
	return PatternMatchesAnyRoute(middlewarePattern, []string{routePattern})
}
