package main

import (
	"bufio"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"sse-example/app"
)

// TestLivePage opens the index page's live stream, runs the increment
// command, and expects the stream to morph the re-rendered page.
func TestLivePage(t *testing.T) {
	srv := httptest.NewServer(newHandler(app.New()))
	t.Cleanup(srv.Close)

	page, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(page.Body)
	page.Body.Close()
	for _, want := range []string{`<div id="app">`, `data-init="@get(&#39;/_gastro/live?path=&#39;`} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("page lacks %q:\n%s", want, body)
		}
	}

	stream, err := http.Get(srv.URL + "/_gastro/live?path=%2F")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stream.Body.Close() })
	// Each datastar-patch-elements event carries the root over several
	// "data: elements" lines; a blank line ends the event.
	patches := make(chan string, 4)
	go func() {
		defer close(patches)
		sc := bufio.NewScanner(stream.Body)
		var lines []string
		for sc.Scan() {
			if rest, ok := strings.CutPrefix(sc.Text(), "data: elements "); ok {
				lines = append(lines, rest)
			} else if sc.Text() == "" && lines != nil {
				patches <- strings.Join(lines, "\n")
				lines = nil
			}
		}
	}()
	next := func() string {
		t.Helper()
		select {
		case p, ok := <-patches:
			if !ok {
				t.Fatal("stream ended")
			}
			return p
		case <-time.After(5 * time.Second):
			t.Fatal("no patch within 5s")
		}
		return ""
	}

	if got := next(); !strings.Contains(got, `<div id="count">0</div>`) {
		t.Fatalf("initial patch: %q", got)
	}
	resp, err := http.Post(srv.URL+"/increment", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("increment: status %d", resp.StatusCode)
	}
	if got := next(); !strings.Contains(got, `<div id="count">1</div>`) {
		t.Fatalf("patch after increment: %q", got)
	}
}
