package datastar_test

import (
	"bufio"
	"bytes"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/andybalholm/brotli"

	"github.com/andrioid/gastro/datastar"
)

// liveApp is a stand-in for a gastro router: one page whose layout wraps the
// page body in {{ liveRoot }} and starts the stream with {{ liveScript }}.
type liveApp struct {
	live     *datastar.LivePages
	notifier *datastar.Notifier
	server   *httptest.Server
	logs     *syncBuffer

	renders atomic.Int64
	status  atomic.Int64 // page status; 0 means 200
}

const liveLayout = `<!DOCTYPE html><html><body><h1>shell</h1>` +
	`{{ liveRoot .Body }}{{ liveScript }}</body></html>`

func newLiveApp(t *testing.T, configure func(*datastar.LivePages)) *liveApp {
	t.Helper()
	app := &liveApp{notifier: &datastar.Notifier{}, logs: &syncBuffer{}}
	app.live = &datastar.LivePages{
		Signal:   app.notifier,
		Root:     "app",
		ErrorLog: log.New(app.logs, "", 0),
	}
	if configure != nil {
		configure(app.live)
	}
	tmpl := template.Must(template.New("layout").Funcs(app.live.Funcs()).Parse(liveLayout))

	mux := http.NewServeMux()
	mux.Handle(app.live.Pattern(), app.live.Handler())
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		n := app.renders.Add(1)
		if s := app.status.Load(); s != 0 {
			http.Error(w, "page failed", int(s))
			return
		}
		cookie := ""
		if c, err := r.Cookie("session"); err == nil {
			cookie = c.Value
		}
		body := template.HTML(fmt.Sprintf(`<p>render=%d path=%s session=%s fragment=%t</p>`,
			n, template.HTMLEscapeString(r.URL.RequestURI()), template.HTMLEscapeString(cookie), datastar.IsFragment(r)))
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := tmpl.Execute(w, map[string]any{"Body": body}); err != nil {
			t.Errorf("execute: %v", err)
		}
	})
	app.live.Pages = mux
	app.server = httptest.NewServer(mux)
	t.Cleanup(app.server.Close)
	return app
}

// liveStream is one open live-page stream, parsed into Datastar events.
type liveStream struct {
	resp   *http.Response
	events chan sseEvent
}

type sseEvent struct {
	typ  string
	data []string
}

// elements returns the HTML of a datastar-patch-elements event.
func (e sseEvent) elements() string {
	var lines []string
	for _, d := range e.data {
		if rest, ok := strings.CutPrefix(d, "elements "); ok {
			lines = append(lines, rest)
		}
	}
	return strings.Join(lines, "\n")
}

func (a *liveApp) open(t *testing.T, page string, header http.Header) *liveStream {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, a.server.URL+"/_gastro/live?path="+url.QueryEscape(page), nil)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range header {
		req.Header[k] = v
	}
	resp, err := a.server.Client().Do(req)
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("open stream: status %d: %s", resp.StatusCode, body)
	}
	var body io.Reader = resp.Body
	if resp.Header.Get("Content-Encoding") == "br" {
		body = brotli.NewReader(resp.Body)
	}
	s := &liveStream{resp: resp, events: make(chan sseEvent, 16)}
	go func() {
		defer close(s.events)
		sc := bufio.NewScanner(body)
		sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
		var ev sseEvent
		for sc.Scan() {
			line := sc.Text()
			switch {
			case line == "":
				if ev.typ != "" {
					s.events <- ev
				}
				ev = sseEvent{}
			case strings.HasPrefix(line, "event: "):
				ev.typ = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				ev.data = append(ev.data, strings.TrimPrefix(line, "data: "))
			}
		}
	}()
	t.Cleanup(s.close)
	return s
}

func (s *liveStream) close() { s.resp.Body.Close() }

// next returns the next patch, failing the test after a timeout.
func (s *liveStream) next(t *testing.T) string {
	t.Helper()
	select {
	case ev, ok := <-s.events:
		if !ok {
			t.Fatal("stream ended")
		}
		if ev.typ != "datastar-patch-elements" {
			t.Fatalf("event %q, want datastar-patch-elements", ev.typ)
		}
		return ev.elements()
	case <-time.After(5 * time.Second):
		t.Fatal("no patch within 5s")
	}
	return ""
}

// none asserts that no event arrives for a while.
func (s *liveStream) none(t *testing.T) {
	t.Helper()
	select {
	case ev, ok := <-s.events:
		if ok {
			t.Fatalf("unexpected event %q: %q", ev.typ, ev.data)
		}
	case <-time.After(150 * time.Millisecond):
	}
}

func TestLivePages_MorphsRootAfterSignal(t *testing.T) {
	app := newLiveApp(t, nil)
	s := app.open(t, "/board?filter=open", nil)

	first := s.next(t)
	want := `<div id="app"><p>render=1 path=/board?filter=open session= fragment=true</p></div>`
	if first != want {
		t.Fatalf("initial patch:\ngot:  %q\nwant: %q", first, want)
	}

	app.notifier.Notify()
	second := s.next(t)
	if !strings.Contains(second, "render=2") || !strings.HasPrefix(second, `<div id="app">`) {
		t.Fatalf("patch after signal: %q", second)
	}
	if strings.Contains(second, "shell") {
		t.Errorf("patch contains markup outside the live root: %q", second)
	}
}

func TestLivePages_RejectsInvalidPaths(t *testing.T) {
	app := newLiveApp(t, nil)
	for _, path := range []string{
		"",
		"board",
		"http://evil.example/board",
		"//evil.example/board",
		`/\evil.example`,
		"/_gastro/live",
		"/_gastro/live?path=/",
		"/_gastro/./live",
	} {
		t.Run(path, func(t *testing.T) {
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodGet, "/_gastro/live?path="+url.QueryEscape(path), nil)
			app.live.Handler().ServeHTTP(w, r)
			if w.Code != http.StatusBadRequest {
				t.Errorf("status %d, want 400 (body %q)", w.Code, w.Body.String())
			}
			if app.renders.Load() != 0 {
				t.Errorf("an invalid path was rendered")
			}
		})
	}
}

func TestLivePages_ForwardsCookies(t *testing.T) {
	app := newLiveApp(t, nil)
	s := app.open(t, "/", http.Header{"Cookie": {"session=alice"}})
	if got := s.next(t); !strings.Contains(got, "session=alice") {
		t.Fatalf("render did not see the caller's cookie: %q", got)
	}
}

func TestLivePages_NonOKRenderKeepsLastGoodDOM(t *testing.T) {
	app := newLiveApp(t, nil)
	s := app.open(t, "/", nil)
	s.next(t)

	app.status.Store(http.StatusInternalServerError)
	app.notifier.Notify()
	s.none(t)
	if !strings.Contains(app.logs.String(), "status 500") {
		t.Errorf("failed render not logged; log: %q", app.logs.String())
	}

	app.status.Store(0)
	app.notifier.Notify()
	if got := s.next(t); !strings.Contains(got, "render=3") {
		t.Fatalf("patch after recovery: %q", got)
	}
}

func TestLivePages_PageWithoutLiveRootSendsNothing(t *testing.T) {
	app := newLiveApp(t, nil)
	app.live.Pages = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		app.renders.Add(1)
		io.WriteString(w, `<!DOCTYPE html><p>no root</p>`)
	})
	s := app.open(t, "/", nil)
	s.none(t)
	if !strings.Contains(app.logs.String(), "live root") {
		t.Errorf("missing root not logged; log: %q", app.logs.String())
	}
}

func TestLivePages_SharesRenderPerIdentity(t *testing.T) {
	app := newLiveApp(t, nil)
	alice := http.Header{"Cookie": {"session=alice"}}

	var streams []*liveStream
	for range 5 {
		s := app.open(t, "/board", alice)
		if got := s.next(t); !strings.Contains(got, "render=1 ") {
			t.Fatalf("joiner got %q, want the shared first render", got)
		}
		streams = append(streams, s)
	}
	if got := app.renders.Load(); got != 1 {
		t.Fatalf("renders for 5 streams of one identity = %d, want 1", got)
	}

	for change := 2; change <= 3; change++ {
		app.notifier.Notify()
		for _, s := range streams {
			if got := s.next(t); !strings.Contains(got, fmt.Sprintf("render=%d ", change)) {
				t.Fatalf("change %d: got %q", change, got)
			}
		}
		if got := app.renders.Load(); got != int64(change) {
			t.Fatalf("after change %d: renders = %d, want one per change", change, got)
		}
	}
}

func TestLivePages_SeparateRendersPerCookie(t *testing.T) {
	app := newLiveApp(t, nil)
	a1 := app.open(t, "/board", http.Header{"Cookie": {"session=alice"}})
	a2 := app.open(t, "/board", http.Header{"Cookie": {"session=alice"}})
	b := app.open(t, "/board", http.Header{"Cookie": {"session=bob"}})
	for _, s := range []*liveStream{a1, a2, b} {
		s.next(t)
	}
	if got := app.renders.Load(); got != 2 {
		t.Fatalf("initial renders = %d, want 2 (one per identity)", got)
	}

	app.notifier.Notify()
	if got := a1.next(t); !strings.Contains(got, "session=alice") {
		t.Errorf("alice got %q", got)
	}
	a2.next(t)
	if got := b.next(t); !strings.Contains(got, "session=bob") {
		t.Errorf("bob got %q", got)
	}
	if got := app.renders.Load(); got != 4 {
		t.Fatalf("renders after one change = %d, want 4", got)
	}
}

func TestLivePages_StopsRenderingWhenStreamsLeave(t *testing.T) {
	app := newLiveApp(t, nil)
	s := app.open(t, "/", nil)
	s.next(t)
	if got := app.notifier.Subscribers(); got != 1 {
		t.Fatalf("Subscribers with one open page = %d, want 1", got)
	}
	s.close()

	deadline := time.Now().Add(5 * time.Second)
	for app.notifier.Subscribers() != 0 {
		if time.Now().After(deadline) {
			t.Fatal("the page's subscription outlived its last stream")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestLivePages_Brotli(t *testing.T) {
	app := newLiveApp(t, func(l *datastar.LivePages) { l.Compression = true })
	s := app.open(t, "/", http.Header{"Accept-Encoding": {"gzip, deflate, br"}})
	if got := s.resp.Header.Get("Content-Encoding"); got != "br" {
		t.Fatalf("Content-Encoding = %q, want br", got)
	}
	if got := s.next(t); !strings.Contains(got, "render=1") {
		t.Fatalf("decoded patch: %q", got)
	}
	app.notifier.Notify()
	if got := s.next(t); !strings.Contains(got, "render=2") {
		t.Fatalf("decoded patch after signal: %q", got)
	}
}

func TestLivePages_UncompressedByDefault(t *testing.T) {
	app := newLiveApp(t, nil)
	s := app.open(t, "/", http.Header{"Accept-Encoding": {"br"}})
	if got := s.resp.Header.Get("Content-Encoding"); got != "" {
		t.Fatalf("Content-Encoding = %q without Compression", got)
	}
}

func TestLivePages_DebounceCoalescesBursts(t *testing.T) {
	app := newLiveApp(t, func(l *datastar.LivePages) { l.Debounce = 100 * time.Millisecond })
	s := app.open(t, "/", nil)
	s.next(t)

	for range 5 {
		app.notifier.Notify()
		time.Sleep(10 * time.Millisecond)
	}
	if got := s.next(t); !strings.Contains(got, "render=2") {
		t.Fatalf("patch after burst: %q", got)
	}
	s.none(t)
	if got := app.renders.Load(); got != 2 {
		t.Errorf("renders after a debounced burst = %d, want 2", got)
	}
}

func TestLivePages_Funcs(t *testing.T) {
	live := &datastar.LivePages{Root: "app", Path: "/live"}
	var out bytes.Buffer
	tmpl := template.Must(template.New("t").Funcs(live.Funcs()).Parse(`{{ liveRoot .Body }}|{{ liveScript }}`))
	if err := tmpl.Execute(&out, map[string]any{"Body": template.HTML(`<b>hi</b>`)}); err != nil {
		t.Fatal(err)
	}
	root, starter, _ := strings.Cut(out.String(), "|")
	if !strings.Contains(root, `<div id="app"><b>hi</b></div>`) {
		t.Errorf("liveRoot = %q", root)
	}
	if !strings.Contains(starter, `data-init="@get(&#39;/live?path=&#39; + encodeURIComponent(location.pathname + location.search))"`) {
		t.Errorf("liveScript = %q", starter)
	}
	if got := live.Pattern(); got != "GET /live" {
		t.Errorf("Pattern = %q", got)
	}
	if got := (&datastar.LivePages{}).Pattern(); got != "GET /_gastro/live" {
		t.Errorf("default Pattern = %q", got)
	}
}

func TestIsFragment(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	if datastar.IsFragment(r) {
		t.Error("plain request reported as fragment")
	}
	r.Header.Set(datastar.FragmentHeader, "app")
	if !datastar.IsFragment(r) {
		t.Error("live re-render not reported as fragment")
	}
}

// syncBuffer is a goroutine-safe log sink.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
