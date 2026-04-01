package gastro_test

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/andrioid/gastro/pkg/gastro"
)

func TestNewSSE_SetsHeaders(t *testing.T) {
	r := httptest.NewRequest("GET", "/events", nil)
	w := httptest.NewRecorder()

	gastro.NewSSE(w, r)

	tests := []struct {
		header string
		want   string
	}{
		{"Content-Type", "text/event-stream"},
		{"Cache-Control", "no-cache"},
	}

	for _, tt := range tests {
		if got := w.Header().Get(tt.header); got != tt.want {
			t.Errorf("header %s: got %q, want %q", tt.header, got, tt.want)
		}
	}
}

func TestNewSSE_SetsConnectionKeepAliveForHTTP1(t *testing.T) {
	r := httptest.NewRequest("GET", "/events", nil)
	r.ProtoMajor = 1
	w := httptest.NewRecorder()

	gastro.NewSSE(w, r)

	if got := w.Header().Get("Connection"); got != "keep-alive" {
		t.Errorf("Connection header: got %q, want %q", got, "keep-alive")
	}
}

func TestSSE_Send_SingleDataLine(t *testing.T) {
	r := httptest.NewRequest("GET", "/events", nil)
	w := httptest.NewRecorder()

	sse := gastro.NewSSE(w, r)
	if err := sse.Send("message", "hello world"); err != nil {
		t.Fatalf("Send: unexpected error: %v", err)
	}

	body := w.Body.String()
	want := "event: message\ndata: hello world\n\n"
	if body != want {
		t.Errorf("body:\ngot:  %q\nwant: %q", body, want)
	}
}

func TestSSE_Send_MultipleDataLines(t *testing.T) {
	r := httptest.NewRequest("GET", "/events", nil)
	w := httptest.NewRecorder()

	sse := gastro.NewSSE(w, r)
	if err := sse.Send("update", "line one", "line two", "line three"); err != nil {
		t.Fatalf("Send: unexpected error: %v", err)
	}

	body := w.Body.String()
	want := "event: update\ndata: line one\ndata: line two\ndata: line three\n\n"
	if body != want {
		t.Errorf("body:\ngot:  %q\nwant: %q", body, want)
	}
}

func TestSSE_Send_MultipleEvents(t *testing.T) {
	r := httptest.NewRequest("GET", "/events", nil)
	w := httptest.NewRecorder()

	sse := gastro.NewSSE(w, r)
	sse.Send("first", "data1")
	sse.Send("second", "data2")

	body := w.Body.String()

	if !strings.Contains(body, "event: first\ndata: data1\n\n") {
		t.Errorf("missing first event in body: %q", body)
	}
	if !strings.Contains(body, "event: second\ndata: data2\n\n") {
		t.Errorf("missing second event in body: %q", body)
	}
}

func TestSSE_Send_NoDataLines(t *testing.T) {
	r := httptest.NewRequest("GET", "/events", nil)
	w := httptest.NewRecorder()

	sse := gastro.NewSSE(w, r)
	if err := sse.Send("ping"); err != nil {
		t.Fatalf("Send: unexpected error: %v", err)
	}

	body := w.Body.String()
	want := "event: ping\n\n"
	if body != want {
		t.Errorf("body:\ngot:  %q\nwant: %q", body, want)
	}
}

func TestSSE_Send_DatastarPatchElements(t *testing.T) {
	r := httptest.NewRequest("GET", "/events", nil)
	w := httptest.NewRecorder()

	sse := gastro.NewSSE(w, r)
	err := sse.Send("datastar-patch-elements",
		`elements <div id="count">42</div>`,
	)
	if err != nil {
		t.Fatalf("Send: unexpected error: %v", err)
	}

	body := w.Body.String()
	want := "event: datastar-patch-elements\ndata: elements <div id=\"count\">42</div>\n\n"
	if body != want {
		t.Errorf("body:\ngot:  %q\nwant: %q", body, want)
	}
}

func TestSSE_IsClosed_ActiveConnection(t *testing.T) {
	r := httptest.NewRequest("GET", "/events", nil)
	w := httptest.NewRecorder()

	sse := gastro.NewSSE(w, r)

	if sse.IsClosed() {
		t.Error("IsClosed: expected false for active connection")
	}
}

func TestSSE_Context_ReturnsRequestContext(t *testing.T) {
	r := httptest.NewRequest("GET", "/events", nil)
	w := httptest.NewRecorder()

	sse := gastro.NewSSE(w, r)

	if sse.Context() != r.Context() {
		t.Error("Context: should return the request context")
	}
}

func TestContext_SSE(t *testing.T) {
	r := httptest.NewRequest("GET", "/events", nil)
	w := httptest.NewRecorder()

	ctx := gastro.NewContext(w, r)
	sse := ctx.SSE()

	if err := sse.Send("test", "hello"); err != nil {
		t.Fatalf("Send via Context.SSE(): unexpected error: %v", err)
	}

	body := w.Body.String()
	if !strings.Contains(body, "event: test\n") {
		t.Errorf("expected SSE event in body: %q", body)
	}
}

func TestSSE_Send_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	r := httptest.NewRequest("GET", "/events", nil)
	r = r.WithContext(ctx)
	w := httptest.NewRecorder()

	sse := gastro.NewSSE(w, r)

	// Send should work before cancellation.
	if err := sse.Send("ok", "first"); err != nil {
		t.Fatalf("first Send: unexpected error: %v", err)
	}

	cancel()

	// Send after cancellation should return an error.
	err := sse.Send("fail", "second")
	if err == nil {
		t.Fatal("Send after cancel: expected error, got nil")
	}

	if !sse.IsClosed() {
		t.Error("IsClosed: expected true after cancel")
	}
}

func TestNewSSE_NoConnectionHeaderForHTTP2(t *testing.T) {
	r := httptest.NewRequest("GET", "/events", nil)
	r.ProtoMajor = 2
	w := httptest.NewRecorder()

	gastro.NewSSE(w, r)

	if got := w.Header().Get("Connection"); got != "" {
		t.Errorf("Connection header should be empty for HTTP/2, got %q", got)
	}
}

func TestSSE_ConcurrentSend(t *testing.T) {
	r := httptest.NewRequest("GET", "/events", nil)
	w := httptest.NewRecorder()

	sse := gastro.NewSSE(w, r)

	const goroutines = 20
	const eventsPerGoroutine = 10
	var wg sync.WaitGroup

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < eventsPerGoroutine; j++ {
				event := fmt.Sprintf("ev-%d-%d", id, j)
				sse.Send(event, "data")
			}
		}(i)
	}

	wg.Wait()

	body := w.Body.String()
	total := goroutines * eventsPerGoroutine
	count := strings.Count(body, "event: ev-")
	if count != total {
		t.Errorf("expected %d events, got %d", total, count)
	}
}
