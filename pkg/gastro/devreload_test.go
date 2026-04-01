package gastro_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/andrioid/gastro/pkg/gastro"
)

// --- Middleware: HTML injection ---

func TestDevMiddleware_InjectsScriptBeforeBody(t *testing.T) {
	html := `<html><body><h1>Hello</h1></body></html>`
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(html))
	})

	r := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()

	d := gastro.NewDevReloader()
	d.Middleware(handler).ServeHTTP(w, r)

	body := w.Body.String()
	if !strings.Contains(body, "<script>") {
		t.Errorf("expected <script> tag in response, got: %s", body)
	}
	scriptIdx := strings.Index(body, "<script>")
	bodyIdx := strings.Index(body, "</body>")
	if scriptIdx > bodyIdx {
		t.Errorf("script must be injected before </body>; script at %d, </body> at %d", scriptIdx, bodyIdx)
	}
}

func TestDevMiddleware_InjectsAtEndWhenNoBodyTag(t *testing.T) {
	fragment := `<div>fragment only</div>`
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(fragment))
	})

	r := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()

	d := gastro.NewDevReloader()
	d.Middleware(handler).ServeHTTP(w, r)

	body := w.Body.String()
	if !strings.Contains(body, "<script>") {
		t.Errorf("expected <script> tag appended to fragment, got: %s", body)
	}
	if !strings.HasSuffix(strings.TrimSpace(body), "</script>") {
		t.Errorf("script should be at end of fragment, got: %s", body)
	}
}

func TestDevMiddleware_PassthroughSSE(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Write([]byte("event: ping\n\n"))
	})

	r := httptest.NewRequest("GET", "/__gastro/reload", nil)
	w := httptest.NewRecorder()

	d := gastro.NewDevReloader()
	d.Middleware(handler).ServeHTTP(w, r)

	body := w.Body.String()
	if strings.Contains(body, "<script>") {
		t.Errorf("SSE response must not have script injected, got: %s", body)
	}
	if body != "event: ping\n\n" {
		t.Errorf("SSE body should be unchanged, got: %q", body)
	}
}

func TestDevMiddleware_PassthroughJSON(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`))
	})

	r := httptest.NewRequest("GET", "/api/data", nil)
	w := httptest.NewRecorder()

	d := gastro.NewDevReloader()
	d.Middleware(handler).ServeHTTP(w, r)

	body := w.Body.String()
	if strings.Contains(body, "<script>") {
		t.Errorf("JSON response must not have script injected, got: %s", body)
	}
}

func TestDevMiddleware_RemovesContentLength(t *testing.T) {
	html := `<html><body>hello</body></html>`
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Content-Length", "30")
		w.Write([]byte(html))
	})

	r := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()

	d := gastro.NewDevReloader()
	d.Middleware(handler).ServeHTTP(w, r)

	if cl := w.Header().Get("Content-Length"); cl != "" {
		t.Errorf("Content-Length must be removed after injection, got: %s", cl)
	}
}

func TestDevMiddleware_MultipleWrites(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<html><body>"))
		w.Write([]byte("<h1>Hello</h1>"))
		w.Write([]byte("</body></html>"))
	})

	r := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()

	d := gastro.NewDevReloader()
	d.Middleware(handler).ServeHTTP(w, r)

	body := w.Body.String()
	if !strings.Contains(body, "<script>") {
		t.Errorf("expected script injection with multiple writes, got: %s", body)
	}
	scriptIdx := strings.Index(body, "<script>")
	bodyIdx := strings.Index(body, "</body>")
	if scriptIdx > bodyIdx {
		t.Errorf("script must be injected before </body>; script at %d, </body> at %d", scriptIdx, bodyIdx)
	}
	if !strings.Contains(body, "<h1>Hello</h1>") {
		t.Errorf("original content must be preserved, got: %s", body)
	}
}

// --- HandleSSE: sends reload event on Broadcast ---

func TestDevReloader_HandleSSE_SendsReloadEvent(t *testing.T) {
	d := gastro.NewDevReloader()

	ctx, cancel := context.WithCancel(context.Background())
	r := httptest.NewRequest("GET", "/__gastro/reload", nil).WithContext(ctx)
	w := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		defer close(done)
		d.HandleSSE(w, r)
	}()

	// Give the handler time to subscribe.
	time.Sleep(20 * time.Millisecond)
	d.Broadcast()

	// Wait for the event to be written, then cancel the context to stop
	// the handler goroutine before reading the body (avoids data race on
	// the httptest.ResponseRecorder).
	time.Sleep(20 * time.Millisecond)
	cancel()
	<-done

	body := w.Body.String()
	if !strings.Contains(body, "event: reload") {
		t.Errorf("expected reload SSE event, got: %q", body)
	}
}

// --- Signal file watcher ---

func TestDevReloader_WatchSignal_BroadcastsOnFile(t *testing.T) {
	dir := t.TempDir()
	gastroDir := filepath.Join(dir, ".gastro")
	if err := os.MkdirAll(gastroDir, 0o755); err != nil {
		t.Fatalf("mkdir .gastro: %v", err)
	}

	d := gastro.NewDevReloaderInDir(dir)
	d.Start()
	defer d.Stop()

	ch, cancel := d.Subscribe()
	defer cancel()

	signal := filepath.Join(gastroDir, ".reload")
	if err := os.WriteFile(signal, []byte(""), 0o644); err != nil {
		t.Fatalf("write signal: %v", err)
	}

	select {
	case <-ch:
		// Broadcast received.
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for reload broadcast after signal file write")
	}

	// Signal file should be deleted after detection.
	if _, err := os.Stat(signal); err == nil {
		t.Error("signal file should have been deleted after broadcast")
	}
}
