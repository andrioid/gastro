package datastar_test

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/andrioid/gastro/pkg/gastro/datastar"
)

func TestPatchElements_Simple(t *testing.T) {
	r := httptest.NewRequest("GET", "/api/update", nil)
	w := httptest.NewRecorder()

	sse := datastar.NewSSE(w, r)
	err := sse.PatchElements(`<div id="count">42</div>`)
	if err != nil {
		t.Fatalf("PatchElements: %v", err)
	}

	body := w.Body.String()
	want := "event: datastar-patch-elements\ndata: elements <div id=\"count\">42</div>\n\n"
	if body != want {
		t.Errorf("body:\ngot:  %q\nwant: %q", body, want)
	}
}

func TestPatchElements_WithSelector(t *testing.T) {
	r := httptest.NewRequest("GET", "/api/update", nil)
	w := httptest.NewRecorder()

	sse := datastar.NewSSE(w, r)
	err := sse.PatchElements(`<p>New content</p>`,
		datastar.WithSelector("#target"),
	)
	if err != nil {
		t.Fatalf("PatchElements: %v", err)
	}

	body := w.Body.String()
	if !strings.Contains(body, "data: selector #target\n") {
		t.Errorf("missing selector line in: %q", body)
	}
	if !strings.Contains(body, "data: elements <p>New content</p>\n") {
		t.Errorf("missing elements line in: %q", body)
	}
}

func TestPatchElements_WithMode(t *testing.T) {
	r := httptest.NewRequest("GET", "/api/update", nil)
	w := httptest.NewRecorder()

	sse := datastar.NewSSE(w, r)
	err := sse.PatchElements(`<li>Item</li>`,
		datastar.WithSelector("#list"),
		datastar.WithMode(datastar.ModeAppend),
	)
	if err != nil {
		t.Fatalf("PatchElements: %v", err)
	}

	body := w.Body.String()
	if !strings.Contains(body, "data: mode append\n") {
		t.Errorf("missing mode line in: %q", body)
	}
}

func TestPatchElements_OuterModeOmitted(t *testing.T) {
	r := httptest.NewRequest("GET", "/api/update", nil)
	w := httptest.NewRecorder()

	sse := datastar.NewSSE(w, r)
	sse.PatchElements(`<div id="x">y</div>`)

	body := w.Body.String()
	// "outer" is the default and should not appear as a data line
	if strings.Contains(body, "data: mode") {
		t.Errorf("outer mode should be omitted (it's the default): %q", body)
	}
}

func TestPatchElements_MultilineHTML(t *testing.T) {
	r := httptest.NewRequest("GET", "/api/update", nil)
	w := httptest.NewRecorder()

	sse := datastar.NewSSE(w, r)
	html := "<div id=\"card\">\n  <h1>Title</h1>\n  <p>Body</p>\n</div>"
	err := sse.PatchElements(html)
	if err != nil {
		t.Fatalf("PatchElements: %v", err)
	}

	body := w.Body.String()
	lines := strings.Split(body, "\n")

	// Count "data: elements" lines -- should be 4 (one per HTML line)
	elemCount := 0
	for _, line := range lines {
		if strings.HasPrefix(line, "data: elements ") {
			elemCount++
		}
	}
	if elemCount != 4 {
		t.Errorf("expected 4 elements data lines, got %d in: %q", elemCount, body)
	}
}

func TestPatchSignals(t *testing.T) {
	r := httptest.NewRequest("GET", "/api/update", nil)
	w := httptest.NewRecorder()

	sse := datastar.NewSSE(w, r)
	err := sse.PatchSignals(map[string]any{
		"count": 42,
	})
	if err != nil {
		t.Fatalf("PatchSignals: %v", err)
	}

	body := w.Body.String()
	if !strings.Contains(body, "event: datastar-patch-signals\n") {
		t.Errorf("missing event type in: %q", body)
	}
	if !strings.Contains(body, `data: signals {"count":42}`) {
		t.Errorf("missing signals data in: %q", body)
	}
}

func TestRemoveElement(t *testing.T) {
	r := httptest.NewRequest("GET", "/api/remove", nil)
	w := httptest.NewRecorder()

	sse := datastar.NewSSE(w, r)
	err := sse.RemoveElement("#toast-1")
	if err != nil {
		t.Fatalf("RemoveElement: %v", err)
	}

	body := w.Body.String()
	if !strings.Contains(body, "event: datastar-patch-elements\n") {
		t.Errorf("missing event type in: %q", body)
	}
	if !strings.Contains(body, "data: selector #toast-1\n") {
		t.Errorf("missing selector line in: %q", body)
	}
	if !strings.Contains(body, "data: mode remove\n") {
		t.Errorf("missing mode line in: %q", body)
	}
}

func TestSSE_Headers(t *testing.T) {
	r := httptest.NewRequest("GET", "/api/sse", nil)
	w := httptest.NewRecorder()

	datastar.NewSSE(w, r)

	if got := w.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Errorf("Content-Type: got %q, want %q", got, "text/event-stream")
	}
}
