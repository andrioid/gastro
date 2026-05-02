package gastro_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	gastro "github.com/andrioid/gastro/pkg/gastro"
)

func TestPatternMatchesAnyRoute(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		routes  []string
		want    bool
	}{
		{
			name:    "exact match",
			pattern: "/counter",
			routes:  []string{"/counter", "/about"},
			want:    true,
		},
		{
			name:    "exact no match",
			pattern: "/typo",
			routes:  []string{"/counter", "/about"},
			want:    false,
		},
		{
			name:    "param segment matches concrete route in synthesis",
			pattern: "/blog/{slug}",
			routes:  []string{"/blog/{slug}"},
			want:    true,
		},
		{
			name:    "trailing wildcard catches subtree",
			pattern: "/admin/{path...}",
			routes:  []string{"/admin/users", "/admin/settings"},
			want:    true,
		},
		{
			name:    "trailing wildcard at root catches everything",
			pattern: "/{path...}",
			routes:  []string{"/", "/counter", "/blog/{slug}"},
			want:    true,
		},
		{
			name:    "wildcard scoped to subtree does not match unrelated routes",
			pattern: "/admin/{path...}",
			routes:  []string{"/about", "/blog/{slug}"},
			want:    false,
		},
		{
			name:    "exact-match marker {$} normalised",
			pattern: "/{$}",
			routes:  []string{"/{$}"},
			want:    true,
		},
		{
			name:    "method-prefixed static pattern matches itself",
			pattern: "GET /static/",
			routes:  []string{"GET /static/"},
			want:    true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := gastro.PatternMatchesAnyRoute(tt.pattern, tt.routes)
			if got != tt.want {
				t.Errorf("PatternMatchesAnyRoute(%q, %v) = %v; want %v", tt.pattern, tt.routes, got, tt.want)
			}
		})
	}
}

func TestValidateMiddlewarePattern_ErrorWording(t *testing.T) {
	err := gastro.ValidateMiddlewarePattern("/typo", []string{"/counter"})
	if err == nil {
		t.Fatal("expected error for unknown pattern")
	}
	msg := err.Error()
	if !strings.Contains(msg, "/typo") || !strings.Contains(msg, "/counter") {
		t.Errorf("error message should name the bad pattern and the known routes; got %q", msg)
	}
	if !strings.Contains(msg, "WithMiddleware") {
		t.Errorf("error message should name the option; got %q", msg)
	}
}

func TestMiddlewareApplies(t *testing.T) {
	if !gastro.MiddlewareApplies("/admin/{path...}", "/admin/users") {
		t.Error("/admin/{path...} should apply to /admin/users")
	}
	if gastro.MiddlewareApplies("/admin/{path...}", "/about") {
		t.Error("/admin/{path...} should not apply to /about")
	}
	if !gastro.MiddlewareApplies("/{$}", "/{$}") {
		t.Error("/{$} should apply to itself")
	}
}

// TestMiddlewareFunc_TypeShape: smoke test that MiddlewareFunc composes
// the way users expect — a chain of two middlewares wraps the inner
// handler in registration order, observable via header writes.
func TestMiddlewareFunc_TypeShape(t *testing.T) {
	var order atomic.Int32
	var seen [3]int32

	outer := gastro.MiddlewareFunc(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			seen[0] = order.Add(1)
			next.ServeHTTP(w, r)
		})
	})
	inner := gastro.MiddlewareFunc(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			seen[1] = order.Add(1)
			next.ServeHTTP(w, r)
		})
	})

	final := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen[2] = order.Add(1)
	})

	wrapped := outer(inner(final))
	wrapped.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))

	if seen[0] != 1 || seen[1] != 2 || seen[2] != 3 {
		t.Errorf("expected outer(1) → inner(2) → final(3), got %v", seen)
	}
}
