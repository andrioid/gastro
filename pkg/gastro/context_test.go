package gastro_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/andrioid/gastro/pkg/gastro"
)

func TestContext_Request(t *testing.T) {
	r := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()

	ctx := gastro.NewContext(w, r)

	if ctx.Request() != r {
		t.Error("Request() should return the original request")
	}
}

func TestContext_Param(t *testing.T) {
	r := httptest.NewRequest("GET", "/blog/hello-world", nil)
	r.SetPathValue("slug", "hello-world")
	w := httptest.NewRecorder()

	ctx := gastro.NewContext(w, r)

	if got := ctx.Param("slug"); got != "hello-world" {
		t.Errorf("Param(\"slug\"): got %q, want %q", got, "hello-world")
	}
}

func TestContext_Query(t *testing.T) {
	r := httptest.NewRequest("GET", "/search?q=hello&page=2", nil)
	w := httptest.NewRecorder()

	ctx := gastro.NewContext(w, r)

	if got := ctx.Query("q"); got != "hello" {
		t.Errorf("Query(\"q\"): got %q, want %q", got, "hello")
	}
	if got := ctx.Query("page"); got != "2" {
		t.Errorf("Query(\"page\"): got %q, want %q", got, "2")
	}
	if got := ctx.Query("missing"); got != "" {
		t.Errorf("Query(\"missing\"): got %q, want empty", got)
	}
}

func TestContext_Redirect(t *testing.T) {
	r := httptest.NewRequest("GET", "/old", nil)
	w := httptest.NewRecorder()

	ctx := gastro.NewContext(w, r)
	ctx.Redirect("/new", http.StatusFound)

	if w.Code != http.StatusFound {
		t.Errorf("status code: got %d, want %d", w.Code, http.StatusFound)
	}
	if got := w.Header().Get("Location"); got != "/new" {
		t.Errorf("Location header: got %q, want %q", got, "/new")
	}
}

func TestContext_Error(t *testing.T) {
	r := httptest.NewRequest("GET", "/fail", nil)
	w := httptest.NewRecorder()

	ctx := gastro.NewContext(w, r)
	ctx.Error(http.StatusNotFound, "Post not found")

	if w.Code != http.StatusNotFound {
		t.Errorf("status code: got %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestContext_Header(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()

	ctx := gastro.NewContext(w, r)
	ctx.Header("X-Custom", "value")

	if got := w.Header().Get("X-Custom"); got != "value" {
		t.Errorf("Header: got %q, want %q", got, "value")
	}
}
