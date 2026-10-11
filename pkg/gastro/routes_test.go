package gastro

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func textHandler(body string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, body)
	})
}

func serve(t *testing.T, h http.Handler, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

func pageRoutes() []Route {
	return []Route{
		{Pattern: "GET /tasks/{id}", Source: "page pages/tasks/[id].gastro", Handler: textHandler("task page")},
		{Pattern: "GET /tasks/new", Source: "page pages/tasks/new.gastro", Handler: textHandler("new page")},
		{Pattern: "GET /x", Source: "page pages/x.gastro", Handler: textHandler("x page")},
	}
}

func TestMergeRoutes_IdenticalPatternReplacesPage(t *testing.T) {
	routes := MergeRoutes(pageRoutes(), []Route{
		{Pattern: "GET /x", Source: `WithHandler("GET /x")`, Handler: textHandler("x handler")},
	})
	if len(routes) != 3 {
		t.Fatalf("len(routes) = %d, want 3 (override, not add)", len(routes))
	}
	mux := NewRouteMux(routes)
	if got := serve(t, mux, http.MethodGet, "/x").Body.String(); got != "x handler" {
		t.Errorf("GET /x body = %q, want override", got)
	}
}

func TestMergeRoutes_MethodQualifiedCommandsCoexistWithPages(t *testing.T) {
	routes := MergeRoutes(pageRoutes(), []Route{
		{Pattern: "POST /tasks/{id}/place", Source: `WithHandler("POST /tasks/{id}/place")`, Handler: textHandler("placed")},
		{Pattern: "POST /tasks/{id}", Source: `WithHandler("POST /tasks/{id}")`, Handler: textHandler("updated")},
	})
	mux := NewRouteMux(routes)
	cases := []struct{ method, path, want string }{
		{http.MethodGet, "/tasks/7", "task page"},
		{http.MethodGet, "/tasks/new", "new page"},
		{http.MethodPost, "/tasks/7/place", "placed"},
		{http.MethodPost, "/tasks/7", "updated"},
	}
	for _, c := range cases {
		if got := serve(t, mux, c.method, c.path).Body.String(); got != c.want {
			t.Errorf("%s %s = %q, want %q", c.method, c.path, got, c.want)
		}
	}
}

func TestNewRouteMux_GetOnlyPages(t *testing.T) {
	mux := NewRouteMux(pageRoutes())
	rec := serve(t, mux, http.MethodPost, "/x")
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /x status = %d, want 405", rec.Code)
	}
	if allow := rec.Header().Get("Allow"); allow != "GET, HEAD" {
		t.Errorf("Allow = %q, want %q", allow, "GET, HEAD")
	}
	if rec := serve(t, mux, http.MethodHead, "/x"); rec.Code != http.StatusOK {
		t.Errorf("HEAD /x status = %d, want 200", rec.Code)
	}
}

func expectPanic(t *testing.T, fn func(), wants ...string) {
	t.Helper()
	defer func() {
		t.Helper()
		v := recover()
		if v == nil {
			t.Fatal("expected panic")
		}
		msg, ok := v.(string)
		if !ok {
			t.Fatalf("panic value %T, want string: %v", v, v)
		}
		for _, w := range wants {
			if !strings.Contains(msg, w) {
				t.Errorf("panic %q missing %q", msg, w)
			}
		}
	}()
	fn()
}

func TestNewRouteMux_ConflictNamesBothSources(t *testing.T) {
	routes := MergeRoutes(pageRoutes(), []Route{
		{Pattern: "GET /{section}/new", Source: `WithHandler("GET /{section}/new")`, Handler: textHandler("h")},
	})
	expectPanic(t, func() { NewRouteMux(routes) },
		`WithHandler("GET /{section}/new")`, "page pages/tasks/[id].gastro")
}

func TestMergeRoutes_TwoOverridesOfOnePagePanic(t *testing.T) {
	expectPanic(t, func() {
		MergeRoutes(pageRoutes(), []Route{
			{Pattern: "GET /x", Source: `WithHandler("GET /x") #1`, Handler: textHandler("a")},
			{Pattern: "GET /x", Source: `WithHandler("GET /x") #2`, Handler: textHandler("b")},
		})
	}, `WithHandler("GET /x") #1`, `WithHandler("GET /x") #2`)
}
