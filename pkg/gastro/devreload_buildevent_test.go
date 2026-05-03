package gastro

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// These tests cover the Phase 4d additions to devreload.go: the
// build-error event type, the .gastro/.build-error signal-file
// polling, and the SSE wire format for both event kinds.

// TestDevReloader_BroadcastsBuildError: write .gastro/.build-error,
// verify subscribers receive a typed reloadEvent with the file's
// contents as Data.
func TestDevReloader_BroadcastsBuildError(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".gastro"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	d := NewDevReloaderInDir(dir)
	d.Start()
	t.Cleanup(d.Stop)

	ch, cancel := d.Subscribe()
	t.Cleanup(cancel)

	payload := "$ go build ./...\nmain.go:5:1: syntax error\nexit status 1"
	if err := os.WriteFile(filepath.Join(dir, ".gastro", ".build-error"),
		[]byte(payload), 0o644); err != nil {
		t.Fatalf("write signal: %v", err)
	}

	select {
	case ev := <-ch:
		if ev.Kind != "build-error" {
			t.Errorf("Kind = %q, want %q", ev.Kind, "build-error")
		}
		if ev.Data != payload {
			t.Errorf("Data = %q, want %q", ev.Data, payload)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no build-error event broadcast within timeout")
	}
}

// TestDevReloader_HandleSSE_BuildErrorWireFormat: drives HandleSSE end-
// to-end via a real HTTP server + client and asserts the SSE on-the-wire
// format for the new build-error event matches the spec
// (`event: build-error\ndata: <message>\n\n`). httptest.NewServer is
// preferred over httptest.ResponseRecorder here because the recorder's
// body buffer isn't safe for the concurrent read pattern this test
// would otherwise need.
func TestDevReloader_HandleSSE_BuildErrorWireFormat(t *testing.T) {
	d := NewDevReloader()
	t.Cleanup(d.Stop)

	srv := httptest.NewServer(http.HandlerFunc(d.HandleSSE))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	t.Cleanup(cancel)

	req, err := http.NewRequestWithContext(ctx, "GET", srv.URL, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("http.Do: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })

	// Wait for the SSE handshake to complete on the server side, then
	// fire the broadcast. A short sleep is the simplest reliable way
	// to ensure the subscription is registered — a stricter sync
	// would require exposing internal hooks.
	time.Sleep(100 * time.Millisecond)
	d.broadcastEvent(reloadEvent{Kind: "build-error", Data: "boom"})

	// Read until we see the expected event lines or the deadline
	// expires. SSE delivers "event: <kind>\ndata: <data>\n\n" per
	// event so we look for both substrings.
	deadline := time.Now().Add(2 * time.Second)
	var got strings.Builder
	buf := make([]byte, 256)
	for time.Now().Before(deadline) {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			got.Write(buf[:n])
			if strings.Contains(got.String(), "event: build-error") &&
				strings.Contains(got.String(), "data: boom") {
				return
			}
		}
		if err != nil {
			break
		}
	}
	t.Errorf("missing build-error wire format in body:\n%s", got.String())
}

// TestDevReloader_ReloadStillWorks is the regression test ensuring the
// existing `reload` event wire shape is unchanged (data: "reload"). Any
// in-the-wild client script versions parsing the data field should
// still see what they expect.
func TestDevReloader_ReloadStillWorks(t *testing.T) {
	d := NewDevReloader()
	t.Cleanup(d.Stop)

	ch, cancel := d.Subscribe()
	t.Cleanup(cancel)

	d.Broadcast()

	select {
	case ev := <-ch:
		if ev.Kind != "reload" {
			t.Errorf("Kind = %q, want %q", ev.Kind, "reload")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Broadcast did not deliver an event")
	}
}
