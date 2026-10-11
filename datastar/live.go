package datastar

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"
)

// DefaultLivePath is the stream endpoint of [LivePages] when Path is empty.
const DefaultLivePath = "/_gastro/live"

// DefaultLiveRoot is the id of the live root when [LivePages].Root is empty.
const DefaultLiveRoot = "gastro-live"

// FragmentHeader is set on the internal requests [LivePages] makes to
// re-render a page; its value is the id of the live root. See [IsFragment].
const FragmentHeader = "Gastro-Live-Fragment"

// Markers around the live root in a rendered page. They are emitted by the
// liveRoot template helper and cut out by the stream; HTML comments inside
// template.HTML values survive html/template.
const (
	liveRootOpen  = "<!--gastro:live-root-->"
	liveRootClose = "<!--/gastro:live-root-->"
)

// forwardedHeaders are the caller headers an internal re-render sees. They
// are also the render's identity: streams whose values match share one
// render per change.
var forwardedHeaders = [...]string{"Cookie", "Authorization", "Accept-Language"}

// IsFragment reports whether r is a [LivePages] re-render, which only needs
// the live root. Pages and layouts can use it to skip work outside the root;
// the output does not have to change, because the stream cuts the root out
// of the full page.
//
// The header can be sent by any client, so use it only to skip rendering
// work, never for access decisions.
func IsFragment(r *http.Request) bool {
	return r.Header.Get(FragmentHeader) != ""
}

// LivePages keeps pages current over one long-lived Datastar stream per
// open page: after every change signal it re-renders the page through its
// normal GET handler and morphs the page's live root in place.
//
// Wire it next to the generated router. The router's handler only exists
// after New, so set Pages before serving:
//
//	notifier := &datastar.Notifier{} // Notify() after every write
//	live := &datastar.LivePages{Signal: notifier, Root: "app", Compression: true}
//	router := gastro.New(
//		gastro.WithFuncs(datastar.Funcs()),
//		gastro.WithFuncs(live.Funcs()),
//		gastro.WithHandler(datastar.ClientPattern, datastar.ClientHandler()),
//		gastro.WithHandler(live.Pattern(), live.Handler()),
//	)
//	live.Pages = router.Handler()
//
// The layout wraps the page content in the live root and starts the stream
// outside it:
//
//	<body>
//		{{ liveRoot .Children }}
//		{{ liveScript }}
//	</body>
//
// The stream endpoint takes GET Path?path=<page URL>. The page URL must be
// a same-origin absolute path other than the stream itself. On connect and
// after every signal the page is rendered as an internal GET through Pages,
// carrying the caller's Cookie, Authorization and Accept-Language headers
// and [FragmentHeader]; the live root is cut out of the response and sent
// as one PatchElements, which Datastar morphs into the element with the
// root's id. A render that does not answer 200, or has no live root, is
// logged and sends nothing, so the browser keeps its last good DOM.
//
// Streams with the same page URL, Host and forwarded headers share one
// render per change, whatever their number. Each stream compresses its own
// copy, so the compressor's window spans the stream's earlier patches.
//
// Configure the fields before the first request and do not change them
// afterwards. A LivePages must not be copied after first use.
type LivePages struct {
	// Pages renders pages; usually the generated router's Handler().
	Pages http.Handler
	// Signal wakes the streams after a change, e.g. a [Notifier].
	Signal Signal
	// Root is the id of the live root element ([DefaultLiveRoot] when
	// empty).
	Root string
	// Path is the stream endpoint ([DefaultLivePath] when empty).
	Path string
	// Compression compresses streams with Brotli (or zstd, gzip, deflate)
	// when the client accepts it; see [WithCompression].
	Compression bool
	// Debounce, when positive, waits this long after a signal before
	// re-rendering, so a burst of changes costs one render.
	Debounce time.Duration
	// ErrorLog receives failed renders; log.Default() when nil.
	ErrorLog *log.Logger

	mu     sync.Mutex
	groups map[liveKey]*liveGroup
}

func (l *LivePages) root() string {
	if l.Root == "" {
		return DefaultLiveRoot
	}
	return l.Root
}

func (l *LivePages) path() string {
	if l.Path == "" {
		return DefaultLivePath
	}
	return l.Path
}

func (l *LivePages) logf(format string, args ...any) {
	logger := l.ErrorLog
	if logger == nil {
		logger = log.Default()
	}
	logger.Printf("datastar: live pages: "+format, args...)
}

// Pattern is the http.ServeMux pattern for [LivePages.Handler]:
// "GET " + Path.
func (l *LivePages) Pattern() string {
	return http.MethodGet + " " + l.path()
}

// Funcs returns the live-page template helpers:
//
//   - liveRoot wraps its argument (template.HTML, e.g. .Children) in the
//     live root, <div id="Root">…</div>. A page has exactly one.
//   - liveScript renders a hidden element whose data-init opens the page's
//     stream. Place it outside the live root, so a morph never restarts
//     the stream. The page also needs the Datastar client
//     ({{ datastarScript }} from [Funcs]).
//
// Register them with gastro.WithFuncs(live.Funcs()).
func (l *LivePages) Funcs() template.FuncMap {
	open := liveRootOpen + `<div id="` + html.EscapeString(l.root()) + `">`
	closing := `</div>` + liveRootClose
	init := `@get('` + template.JSEscapeString(l.path()) + `?path=' + encodeURIComponent(location.pathname + location.search))`
	starter := template.HTML(`<div hidden data-init="` + html.EscapeString(init) + `"></div>`)
	return template.FuncMap{
		"liveRoot": func(content template.HTML) template.HTML {
			return template.HTML(open) + content + template.HTML(closing)
		},
		"liveScript": func() template.HTML { return starter },
	}
}

// Handler returns the stream endpoint. Mount it at [LivePages.Pattern].
func (l *LivePages) Handler() http.Handler {
	return http.HandlerFunc(l.serve)
}

func (l *LivePages) serve(w http.ResponseWriter, r *http.Request) {
	target, err := l.target(r.URL.Query().Get("path"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if l.Pages == nil || l.Signal == nil {
		l.logf("Pages and Signal must be set before serving")
		http.Error(w, "live pages are not configured", http.StatusInternalServerError)
		return
	}

	var opts []SSEOption
	if l.Compression {
		opts = append(opts, WithCompression())
	}
	sse, err := NewSSE(w, r, opts...)
	if err != nil {
		l.logf("%s: %v", target, err)
		return
	}

	key := liveKey{target: target, host: r.Host}
	for i, h := range forwardedHeaders {
		key.headers[i] = strings.Join(r.Header.Values(h), ", ")
	}
	m := &liveMember{frames: make(chan string, 1)}
	g := l.join(key, r.RemoteAddr, m)
	defer l.leave(g, m)

	ctx := sse.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case frame := <-m.frames:
			if err := sse.PatchElements(frame); err != nil {
				return // client gone
			}
		}
	}
}

// target validates the page a stream follows: a same-origin absolute path
// that is not the stream itself. It returns the path with its query.
func (l *LivePages) target(raw string) (string, error) {
	if raw == "" {
		return "", errors.New("path is required")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid path: %w", err)
	}
	if u.Scheme != "" || u.Host != "" || u.Opaque != "" ||
		!strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") ||
		strings.ContainsRune(u.Path, '\\') {
		return "", errors.New("path must be a same-origin absolute path")
	}
	if path.Clean(u.Path) == path.Clean(l.path()) {
		return "", errors.New("path must be a page, not the live stream")
	}
	return u.RequestURI(), nil
}

// liveKey identifies streams that render identically: same page, same Host
// and the same forwarded headers (in forwardedHeaders order).
type liveKey struct {
	target  string
	host    string
	headers [len(forwardedHeaders)]string
}

// liveGroup renders one page for every stream with the same key.
type liveGroup struct {
	key        liveKey
	remoteAddr string
	cancel     context.CancelFunc

	mu      sync.Mutex
	members map[*liveMember]struct{}
	last    string // last good frame, sent to streams joining later
	hasLast bool
}

// liveMember is one open stream. frames holds at most the newest frame.
type liveMember struct {
	frames chan string
}

// deliver replaces any pending frame with frame. Callers hold the group's
// lock, so deliver is the only sender.
func (m *liveMember) deliver(frame string) {
	select {
	case m.frames <- frame:
		return
	default:
	}
	select {
	case <-m.frames:
	default:
	}
	m.frames <- frame
}

// join adds m to the group for key, starting the group if it is new. A
// group that has rendered already hands m its last frame at once.
func (l *LivePages) join(key liveKey, remoteAddr string, m *liveMember) *liveGroup {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.groups == nil {
		l.groups = make(map[liveKey]*liveGroup)
	}
	g := l.groups[key]
	if g == nil {
		ctx, cancel := context.WithCancel(context.Background())
		g = &liveGroup{key: key, remoteAddr: remoteAddr, cancel: cancel, members: make(map[*liveMember]struct{})}
		l.groups[key] = g
		go l.run(ctx, g)
	}
	g.mu.Lock()
	g.members[m] = struct{}{}
	if g.hasLast {
		m.deliver(g.last)
	}
	g.mu.Unlock()
	return g
}

// leave removes m; the last stream to leave stops the group.
func (l *LivePages) leave(g *liveGroup, m *liveMember) {
	l.mu.Lock()
	defer l.mu.Unlock()
	g.mu.Lock()
	delete(g.members, m)
	empty := len(g.members) == 0
	g.mu.Unlock()
	if empty {
		delete(l.groups, g.key)
		g.cancel()
	}
}

// run renders the group's page now and after every signal until ctx ends.
// It subscribes before the first render, so a change landing during a
// render triggers the next one.
func (l *LivePages) run(ctx context.Context, g *liveGroup) {
	changes, cancel := l.Signal.Subscribe()
	defer cancel()
	for {
		frame, err := l.render(ctx, g)
		switch {
		case err == nil:
			g.publish(frame)
		case ctx.Err() == nil:
			l.logf("%s: %v", g.key.target, err)
		}
		select {
		case <-ctx.Done():
			return
		case <-changes:
		}
		if l.Debounce > 0 {
			t := time.NewTimer(l.Debounce)
			select {
			case <-ctx.Done():
				t.Stop()
				return
			case <-t.C:
			}
			// The render below sees every change signalled so far.
			select {
			case <-changes:
			default:
			}
		}
	}
}

func (g *liveGroup) publish(frame string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.last, g.hasLast = frame, true
	for m := range g.members {
		m.deliver(frame)
	}
}

// render re-renders the group's page through Pages and returns its live
// root.
func (l *LivePages) render(ctx context.Context, g *liveGroup) (frame string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.key.target, nil)
	if err != nil {
		return "", err
	}
	req.RequestURI = g.key.target
	req.Host = g.key.host
	req.RemoteAddr = g.remoteAddr
	for i, h := range forwardedHeaders {
		if v := g.key.headers[i]; v != "" {
			req.Header.Set(h, v)
		}
	}
	req.Header.Set(FragmentHeader, l.root())

	rec := &liveRecorder{header: http.Header{}}
	defer func() {
		// The render runs on the group's goroutine, outside net/http's
		// per-request recovery; a panicking page must not take the
		// process down.
		if p := recover(); p != nil {
			err = fmt.Errorf("render panicked: %v", p)
		}
	}()
	l.Pages.ServeHTTP(rec, req)

	if rec.status != 0 && rec.status != http.StatusOK {
		return "", fmt.Errorf("render: status %d: %s", rec.status, bytes.TrimSpace(rec.body.Bytes()))
	}
	return cutLiveRoot(rec.body.Bytes())
}

// cutLiveRoot returns the live root of a rendered page.
func cutLiveRoot(page []byte) (string, error) {
	start := bytes.Index(page, []byte(liveRootOpen))
	if start < 0 {
		return "", errors.New("render has no live root (wrap the page content in {{ liveRoot … }})")
	}
	if bytes.Count(page, []byte(liveRootOpen)) > 1 {
		return "", errors.New("render has more than one live root")
	}
	start += len(liveRootOpen)
	end := bytes.LastIndex(page, []byte(liveRootClose))
	if end < start {
		return "", errors.New("render has an unterminated live root")
	}
	return string(page[start:end]), nil
}

// liveRecorder buffers one internal page render.
type liveRecorder struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (r *liveRecorder) Header() http.Header { return r.header }

func (r *liveRecorder) WriteHeader(status int) {
	if r.status == 0 {
		r.status = status
	}
}

func (r *liveRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.body.Write(b)
}
