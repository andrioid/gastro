package datastar_test

import (
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/andrioid/gastro/datastar"
	sdk "github.com/starfederation/datastar-go/datastar"
)

// newStream returns a facade stream writing into a fresh recorder.
func newStream(t *testing.T) (*datastar.SSE, *httptest.ResponseRecorder) {
	t.Helper()
	w := httptest.NewRecorder()
	sse, err := datastar.NewSSE(w, httptest.NewRequest(http.MethodGet, "/stream", nil))
	if err != nil {
		t.Fatalf("NewSSE: %v", err)
	}
	return sse, w
}

// sdkOutput runs fn against the upstream SDK and returns the bytes it wrote.
func sdkOutput(t *testing.T, fn func(*sdk.ServerSentEventGenerator) error) string {
	t.Helper()
	w := httptest.NewRecorder()
	gen := sdk.NewSSE(w, httptest.NewRequest(http.MethodGet, "/stream", nil))
	if err := fn(gen); err != nil {
		t.Fatalf("sdk: %v", err)
	}
	return w.Body.String()
}

// TestEncoding_MatchesSDK asserts that, for every event type the facade
// exposes, the bytes on the wire are identical to what the upstream SDK
// produces for the same input.
func TestEncoding_MatchesSDK(t *testing.T) {
	type signals struct {
		Count int    `json:"count"`
		Name  string `json:"name"`
	}

	tests := []struct {
		name   string
		facade func(*datastar.SSE) error
		sdk    func(*sdk.ServerSentEventGenerator) error
	}{
		{
			name:   "patch elements default",
			facade: func(s *datastar.SSE) error { return s.PatchElements(`<div id="count">42</div>`) },
			sdk:    func(g *sdk.ServerSentEventGenerator) error { return g.PatchElements(`<div id="count">42</div>`) },
		},
		{
			name: "patch elements multiline with all options",
			facade: func(s *datastar.SSE) error {
				return s.PatchElements("<svg id=\"a\">\n<circle/>\n</svg>",
					datastar.WithSelector("#target"),
					datastar.WithMode(datastar.ModeInner),
					datastar.WithNamespace(datastar.NamespaceSVG),
					datastar.WithUseViewTransitions(true),
					datastar.WithViewTransitionSelector(".card"),
				)
			},
			sdk: func(g *sdk.ServerSentEventGenerator) error {
				return g.PatchElements("<svg id=\"a\">\n<circle/>\n</svg>",
					sdk.WithSelector("#target"),
					sdk.WithMode(sdk.ElementPatchModeInner),
					sdk.WithNamespace(sdk.NamespaceSVG),
					sdk.WithUseViewTransitions(true),
					sdk.WithViewTransitionSelector(".card"),
				)
			},
		},
		{
			name:   "remove element",
			facade: func(s *datastar.SSE) error { return s.RemoveElement("#toast-1") },
			sdk:    func(g *sdk.ServerSentEventGenerator) error { return g.RemoveElement("#toast-1") },
		},
		{
			name:   "patch signals",
			facade: func(s *datastar.SSE) error { return s.PatchSignals([]byte(`{"count":1}`)) },
			sdk:    func(g *sdk.ServerSentEventGenerator) error { return g.PatchSignals([]byte(`{"count":1}`)) },
		},
		{
			name: "patch signals only if missing",
			facade: func(s *datastar.SSE) error {
				return s.PatchSignals([]byte("{\n\"count\":1\n}"), datastar.WithOnlyIfMissing(true))
			},
			sdk: func(g *sdk.ServerSentEventGenerator) error {
				return g.PatchSignals([]byte("{\n\"count\":1\n}"), sdk.WithOnlyIfMissing(true))
			},
		},
		{
			name:   "marshal and patch signals",
			facade: func(s *datastar.SSE) error { return s.MarshalAndPatchSignals(signals{Count: 3, Name: "Ada"}) },
			sdk: func(g *sdk.ServerSentEventGenerator) error {
				return g.MarshalAndPatchSignals(signals{Count: 3, Name: "Ada"})
			},
		},
		{
			name:   "execute script",
			facade: func(s *datastar.SSE) error { return s.ExecuteScript(`alert("hi")`) },
			sdk:    func(g *sdk.ServerSentEventGenerator) error { return g.ExecuteScript(`alert("hi")`) },
		},
		{
			name: "execute script with options",
			facade: func(s *datastar.SSE) error {
				return s.ExecuteScript(`init()`,
					datastar.WithAutoRemove(false),
					datastar.WithAttributes(`type="module"`, `defer`),
				)
			},
			sdk: func(g *sdk.ServerSentEventGenerator) error {
				return g.ExecuteScript(`init()`,
					sdk.WithExecuteScriptAutoRemove(false),
					sdk.WithExecuteScriptAttributes(`type="module"`, `defer`),
				)
			},
		},
		{
			name:   "redirect",
			facade: func(s *datastar.SSE) error { return s.Redirect("/tasks/1") },
			sdk:    func(g *sdk.ServerSentEventGenerator) error { return g.Redirect("/tasks/1") },
		},
		{
			name:   "console log",
			facade: func(s *datastar.SSE) error { return s.ConsoleLog("saved") },
			sdk:    func(g *sdk.ServerSentEventGenerator) error { return g.ConsoleLog("saved") },
		},
		{
			name: "dispatch custom event",
			facade: func(s *datastar.SSE) error {
				return s.DispatchCustomEvent("task-placed", map[string]int{"id": 7},
					datastar.WithDispatchSelector("#board"),
					datastar.WithDispatchBubbles(false),
					datastar.WithDispatchCancelable(false),
					datastar.WithDispatchComposed(false),
				)
			},
			sdk: func(g *sdk.ServerSentEventGenerator) error {
				return g.DispatchCustomEvent("task-placed", map[string]int{"id": 7},
					sdk.WithDispatchCustomEventSelector("#board"),
					sdk.WithDispatchCustomEventBubbles(false),
					sdk.WithDispatchCustomEventCancelable(false),
					sdk.WithDispatchCustomEventComposed(false),
				)
			},
		},
		{
			name: "raw send with id and retry",
			facade: func(s *datastar.SSE) error {
				return s.Send(datastar.EventTypePatchElements, []string{"elements <p id=\"x\">y</p>"},
					datastar.WithEventID("42"),
					datastar.WithRetryDuration(3*time.Second),
				)
			},
			sdk: func(g *sdk.ServerSentEventGenerator) error {
				return g.Send(sdk.EventTypePatchElements, []string{"elements <p id=\"x\">y</p>"},
					sdk.WithSSEEventId("42"),
					sdk.WithSSERetryDuration(3*time.Second),
				)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sse, w := newStream(t)
			if err := tt.facade(sse); err != nil {
				t.Fatalf("facade: %v", err)
			}
			want := sdkOutput(t, tt.sdk)
			if got := w.Body.String(); got != want {
				t.Errorf("wire output differs from SDK:\ngot:  %q\nwant: %q", got, want)
			}
		})
	}
}

// eventTerminator ends every event on the wire. The SDK (and therefore the
// facade) writes an extra empty line after the blank line the SSE spec
// requires; browsers ignore it.
const eventTerminator = "\n\n\n"

// assertSingleEvent fails unless body is exactly one SSE event whose only
// fields are a single "event:" line followed by "data:" lines.
func assertSingleEvent(t *testing.T, body string) {
	t.Helper()
	if !strings.HasSuffix(body, eventTerminator) {
		t.Fatalf("event not terminated by a blank line: %q", body)
	}
	if strings.ContainsRune(body, '\r') {
		t.Fatalf("carriage return reached the wire: %q", body)
	}
	lines := strings.Split(strings.TrimSuffix(body, eventTerminator), "\n")
	if !strings.HasPrefix(lines[0], "event: ") {
		t.Fatalf("first line is not the event field: %q", body)
	}
	for _, line := range lines[1:] {
		switch {
		case strings.HasPrefix(line, "data: "):
		case line == "":
			t.Fatalf("blank line splits the event in two: %q", body)
		default:
			t.Fatalf("injected SSE field %q in: %q", line, body)
		}
	}
}

func TestCRLFInjection_IsNeutralised(t *testing.T) {
	const evil = "x\r\nevent: evil\r\rid: 1\r\n\r\ndata: pwned"

	tests := []struct {
		name string
		emit func(*datastar.SSE) error
	}{
		{"elements body", func(s *datastar.SSE) error { return s.PatchElements("<p>" + evil + "</p>") }},
		{"selector", func(s *datastar.SSE) error { return s.PatchElements("<p></p>", datastar.WithSelector(evil)) }},
		{"view transition selector", func(s *datastar.SSE) error {
			return s.PatchElements("<p></p>", datastar.WithUseViewTransitions(true), datastar.WithViewTransitionSelector(evil))
		}},
		{"mode", func(s *datastar.SSE) error {
			return s.PatchElements("<p></p>", datastar.WithMode(datastar.PatchMode(evil)))
		}},
		{"namespace", func(s *datastar.SSE) error {
			return s.PatchElements("<p></p>", datastar.WithNamespace(datastar.Namespace(evil)))
		}},
		{"remove selector", func(s *datastar.SSE) error { return s.RemoveElement(evil) }},
		{"signals", func(s *datastar.SSE) error { return s.PatchSignals([]byte(`{"a":1}` + evil)) }},
		{"script", func(s *datastar.SSE) error { return s.ExecuteScript(evil) }},
		{"script attributes", func(s *datastar.SSE) error {
			return s.ExecuteScript("go()", datastar.WithAttributes(evil))
		}},
		{"redirect", func(s *datastar.SSE) error { return s.Redirect(evil) }},
		{"console log", func(s *datastar.SSE) error { return s.ConsoleLog(evil) }},
		{"dispatch", func(s *datastar.SSE) error {
			return s.DispatchCustomEvent(evil, evil, datastar.WithDispatchSelector(evil))
		}},
		{"raw data lines", func(s *datastar.SSE) error {
			return s.Send(datastar.EventTypePatchElements, []string{"elements " + evil})
		}},
		{"raw event type", func(s *datastar.SSE) error {
			return s.Send(datastar.EventType("datastar-patch-elements"+evil), []string{"elements <p></p>"})
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sse, w := newStream(t)
			if err := tt.emit(sse); err != nil {
				t.Fatalf("emit: %v", err)
			}
			assertSingleEvent(t, w.Body.String())
		})
	}
}

func TestCRLFInjection_EventIDCannotInjectFields(t *testing.T) {
	sse, w := newStream(t)
	if err := sse.Send(datastar.EventTypePatchSignals, []string{"signals {}"},
		datastar.WithEventID("1\r\nevent: evil\n\ndata: x")); err != nil {
		t.Fatalf("Send: %v", err)
	}
	want := "event: datastar-patch-signals\nid: 1event: evildata: x\ndata: signals {}" + eventTerminator
	if got := w.Body.String(); got != want {
		t.Errorf("event id not kept on a single line:\ngot:  %q\nwant: %q", got, want)
	}
}

func TestPatchElements_CRLFBodyKeepsLines(t *testing.T) {
	sse, w := newStream(t)
	if err := sse.PatchElements("<ul id=\"l\">\r\n<li>a</li>\r<li>b</li>\n</ul>"); err != nil {
		t.Fatalf("PatchElements: %v", err)
	}
	want := "event: datastar-patch-elements\n" +
		"data: elements <ul id=\"l\">\n" +
		"data: elements <li>a</li>\n" +
		"data: elements <li>b</li>\n" +
		"data: elements </ul>" + eventTerminator
	if got := w.Body.String(); got != want {
		t.Errorf("body:\ngot:  %q\nwant: %q", got, want)
	}
}

func TestNewSSE_SetsStreamHeaders(t *testing.T) {
	_, w := newStream(t)
	if got := w.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", got)
	}
	if got := w.Header().Get("Cache-Control"); got != "no-cache" {
		t.Errorf("Cache-Control = %q, want no-cache", got)
	}
	if !w.Flushed {
		t.Error("headers were not flushed on stream creation")
	}
}

// noFlushWriter is an http.ResponseWriter that cannot flush.
type noFlushWriter struct {
	header http.Header
	body   strings.Builder
}

func (w *noFlushWriter) Header() http.Header         { return w.header }
func (w *noFlushWriter) Write(b []byte) (int, error) { return w.body.Write(b) }
func (w *noFlushWriter) WriteHeader(int)             {}

func TestNewSSE_ReturnsErrorWhenFlushFails(t *testing.T) {
	w := &noFlushWriter{header: http.Header{}}
	sse, err := datastar.NewSSE(w, httptest.NewRequest(http.MethodGet, "/stream", nil))
	if err == nil {
		t.Fatal("NewSSE succeeded on a writer that cannot flush")
	}
	if !errors.Is(err, http.ErrNotSupported) {
		t.Errorf("error does not wrap the flush failure: %v", err)
	}
	if sse != nil {
		t.Errorf("NewSSE returned a stream alongside the error")
	}
}

// failingFlushWriter flushes fine during setup and fails afterwards.
type failingFlushWriter struct {
	*httptest.ResponseRecorder
	calls int
}

var errFlush = errors.New("connection reset")

func (w *failingFlushWriter) FlushError() error {
	w.calls++
	if w.calls > 1 {
		return errFlush
	}
	w.ResponseRecorder.Flush()
	return nil
}

func TestSend_ReportsFlushFailuresAfterSetup(t *testing.T) {
	w := &failingFlushWriter{ResponseRecorder: httptest.NewRecorder()}
	sse, err := datastar.NewSSE(w, httptest.NewRequest(http.MethodGet, "/stream", nil))
	if err != nil {
		t.Fatalf("NewSSE: %v", err)
	}
	if err := sse.PatchElements(`<p id="x"></p>`); !errors.Is(err, errFlush) {
		t.Errorf("PatchElements error = %v, want %v", err, errFlush)
	}
}

func TestSend_FailsAfterClientDisconnects(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/stream", nil)
	ctx, cancel := context.WithCancel(r.Context())
	sse, err := datastar.NewSSE(httptest.NewRecorder(), r.WithContext(ctx))
	if err != nil {
		t.Fatalf("NewSSE: %v", err)
	}
	cancel()
	if !sse.IsClosed() {
		t.Error("IsClosed = false after the request context ended")
	}
	if err := sse.PatchElements(`<p id="x"></p>`); !errors.Is(err, context.Canceled) {
		t.Errorf("PatchElements error = %v, want context.Canceled", err)
	}
}

func TestNewSSE_Compression(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/stream", nil)
	r.Header.Set("Accept-Encoding", "gzip")
	w := httptest.NewRecorder()

	sse, err := datastar.NewSSE(w, r, datastar.WithCompression(datastar.WithGzip()))
	if err != nil {
		t.Fatalf("NewSSE: %v", err)
	}
	if err := sse.PatchElements(`<div id="count">42</div>`); err != nil {
		t.Fatalf("PatchElements: %v", err)
	}

	if got := w.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", got)
	}
	zr, err := gzip.NewReader(w.Body)
	if err != nil {
		t.Fatalf("gzip.NewReader: %v", err)
	}
	// The stream is still open, so the gzip trailer is missing; everything
	// flushed so far must nevertheless be readable.
	plain, err := io.ReadAll(zr)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("decompress: %v", err)
	}
	want := "event: datastar-patch-elements\ndata: elements <div id=\"count\">42</div>" + eventTerminator
	if string(plain) != want {
		t.Errorf("decompressed body:\ngot:  %q\nwant: %q", plain, want)
	}
}

func TestNewSSE_CompressionSkippedWhenClientRefuses(t *testing.T) {
	w := httptest.NewRecorder()
	sse, err := datastar.NewSSE(w, httptest.NewRequest(http.MethodGet, "/stream", nil),
		datastar.WithCompression(datastar.WithBrotli(datastar.WithBrotliLevel(5)), datastar.WithGzip(datastar.WithGzipLevel(1))))
	if err != nil {
		t.Fatalf("NewSSE: %v", err)
	}
	if err := sse.PatchSignals([]byte(`{"a":1}`)); err != nil {
		t.Fatalf("PatchSignals: %v", err)
	}
	if got := w.Header().Get("Content-Encoding"); got != "" {
		t.Errorf("Content-Encoding = %q without Accept-Encoding", got)
	}
	if !strings.HasPrefix(w.Body.String(), "event: datastar-patch-signals\n") {
		t.Errorf("body not plain text: %q", w.Body.String())
	}
}

func TestReadSignals(t *testing.T) {
	var got struct {
		Count int `json:"count"`
	}

	post := httptest.NewRequest(http.MethodPost, "/cmd", strings.NewReader(`{"count":5}`))
	if err := datastar.ReadSignals(post, &got); err != nil {
		t.Fatalf("ReadSignals POST: %v", err)
	}
	if got.Count != 5 {
		t.Errorf("POST count = %d, want 5", got.Count)
	}

	get := httptest.NewRequest(http.MethodGet, `/stream?datastar=%7B%22count%22%3A9%7D`, nil)
	if err := datastar.ReadSignals(get, &got); err != nil {
		t.Fatalf("ReadSignals GET: %v", err)
	}
	if got.Count != 9 {
		t.Errorf("GET count = %d, want 9", got.Count)
	}

	bad := httptest.NewRequest(http.MethodPost, "/cmd", strings.NewReader(`{`))
	if err := datastar.ReadSignals(bad, &got); err == nil {
		t.Error("ReadSignals accepted malformed JSON")
	}
}

func TestSSE_ConcurrentSendsDoNotInterleave(t *testing.T) {
	sse, w := newStream(t)
	const n = 50
	errs := make(chan error, n)
	for i := range n {
		go func() {
			errs <- sse.PatchElements(strings.Repeat("<p>line</p>\n", i%5+1))
		}()
	}
	for range n {
		if err := <-errs; err != nil {
			t.Fatalf("PatchElements: %v", err)
		}
	}
	events := strings.Split(strings.TrimSuffix(w.Body.String(), eventTerminator), eventTerminator)
	if len(events) != n {
		t.Fatalf("got %d events, want %d", len(events), n)
	}
	for _, ev := range events {
		assertSingleEvent(t, ev+eventTerminator)
	}
}
