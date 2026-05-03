package gastro

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const signalPollInterval = 200 * time.Millisecond

// DevReloader is the package-level live-reload broadcaster used in dev mode.
// The generated Routes() function calls Start() and wires HandleSSE when
// GASTRO_DEV=1. The CLI writes .gastro/.reload after each regeneration to
// signal connected browsers to reload, and .gastro/.build-error after a
// failed build so the browser can console.warn the user.
var DevReloader = NewDevReloader()

// reloadEvent is the typed payload broadcast to subscribers. Kind is the
// SSE event name ("reload" or "build-error"); Data is the SSE data field.
// For reload events Data is empty/ignored; for build-error events it
// carries the failure message (failing command + stderr).
type reloadEvent struct {
	Kind string
	Data string
}

// DevReloader manages live-reload SSE connections in dev mode.
type devReloader struct {
	mu       sync.Mutex
	clients  map[chan reloadEvent]struct{}
	once     sync.Once
	stopOnce sync.Once
	done     chan struct{}
	dir      string // project root where .gastro/.reload is written
}

// NewDevReloader creates a devReloader that watches for .gastro/.reload in the
// current working directory.
func NewDevReloader() *devReloader {
	return &devReloader{
		clients: make(map[chan reloadEvent]struct{}),
		done:    make(chan struct{}),
		dir:     ".",
	}
}

// NewDevReloaderInDir creates a devReloader that watches for .gastro/.reload in
// the given directory. Useful for testing with isolated temp directories.
func NewDevReloaderInDir(dir string) *devReloader {
	return &devReloader{
		clients: make(map[chan reloadEvent]struct{}),
		done:    make(chan struct{}),
		dir:     dir,
	}
}

// Start begins watching for the .gastro/.reload signal file.
// Safe to call multiple times; the goroutine starts only once.
func (d *devReloader) Start() {
	d.once.Do(func() {
		go d.watchSignal()
	})
}

// Stop terminates the signal watcher goroutine. Safe to call concurrently
// and multiple times. After calling Stop the devReloader cannot be restarted.
func (d *devReloader) Stop() {
	d.stopOnce.Do(func() { close(d.done) })
}

func (d *devReloader) signalPath() string {
	return filepath.Join(d.dir, ".gastro", ".reload")
}

func (d *devReloader) buildErrorPath() string {
	return filepath.Join(d.dir, ".gastro", ".build-error")
}

// watchSignal polls for the .gastro/.reload and .gastro/.build-error
// signal files. When found, it reads (build-error only) and deletes the
// file, then broadcasts the appropriate event to all clients.
//
// .reload is checked first so a successful rebuild that lands in the
// same poll tick as a stale .build-error file still wins — the reload
// loads the new page, which by definition implicitly clears any prior
// banner state on the client.
func (d *devReloader) watchSignal() {
	ticker := time.NewTicker(signalPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-d.done:
			return
		case <-ticker.C:
			if err := os.Remove(d.signalPath()); err == nil {
				d.broadcastEvent(reloadEvent{Kind: "reload"})
				// A successful reload makes any pending build-error
				// stale; remove the file without broadcasting so the
				// next tick doesn't replay an old failure.
				_ = os.Remove(d.buildErrorPath())
				continue
			}
			// Read-then-delete the build-error payload. Reading first
			// means we don't lose the message if Remove succeeds but
			// the file gets recreated by a racing CLI write — worst
			// case we re-broadcast an already-shown message, which the
			// client console handler is idempotent against.
			if data, err := os.ReadFile(d.buildErrorPath()); err == nil {
				_ = os.Remove(d.buildErrorPath())
				d.broadcastEvent(reloadEvent{Kind: "build-error", Data: string(data)})
			}
		}
	}
}

// Subscribe returns a channel that receives an event on each broadcast,
// and a cancel function to unsubscribe. The channel is buffered (cap 4)
// because build-error events can closely follow reload events; a strict
// cap-1 channel would drop the build-error if a reload was already
// queued.
func (d *devReloader) Subscribe() (<-chan reloadEvent, func()) {
	ch := make(chan reloadEvent, 4)
	d.mu.Lock()
	d.clients[ch] = struct{}{}
	d.mu.Unlock()

	return ch, func() {
		d.mu.Lock()
		delete(d.clients, ch)
		d.mu.Unlock()
	}
}

// Broadcast notifies all connected SSE clients to reload. Backwards
// compat for callers (and tests) that only care about reload events.
func (d *devReloader) Broadcast() {
	d.broadcastEvent(reloadEvent{Kind: "reload"})
}

func (d *devReloader) broadcastEvent(ev reloadEvent) {
	d.mu.Lock()
	defer d.mu.Unlock()

	for ch := range d.clients {
		// Non-blocking send: if the channel is full the client will be
		// notified on the next drain; we never block the watcher.
		select {
		case ch <- ev:
		default:
		}
	}
}

// HandleSSE is an http.HandlerFunc for the GET /__gastro/reload endpoint.
// It establishes an SSE stream and sends events whenever the dev CLI
// signals a change. Two event kinds are emitted:
//
//   - reload — page should reload (existing behaviour, wire format unchanged)
//   - build-error — last build failed; the data field carries the failure
//     message. The client script logs it via console.warn; a visible
//     banner UI is deferred (see plan §10).
//
// A periodic heartbeat detects stale connections (e.g. browser crash,
// network drop) so server-side goroutines don't linger.
func (d *devReloader) HandleSSE(w http.ResponseWriter, r *http.Request) {
	sse := NewSSE(w, r)
	ch, cancel := d.Subscribe()
	defer cancel()

	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case ev := <-ch:
			data := ev.Data
			if ev.Kind == "reload" && data == "" {
				// Preserve the historical wire shape (data "reload")
				// for any client script versions still in the wild.
				data = "reload"
			}
			if err := sse.Send(ev.Kind, data); err != nil {
				return
			}
		case <-heartbeat.C:
			if err := sse.Send("heartbeat", ""); err != nil {
				return
			}
		}
	}
}

// Middleware wraps an http.Handler and injects the live-reload <script> into
// HTML responses. Non-HTML responses (SSE, JSON, etc.) are passed through
// without buffering.
func (d *devReloader) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		iw := &injectWriter{ResponseWriter: w}
		next.ServeHTTP(iw, r)
		iw.flush()
	})
}

// reloadScript is inlined into every HTML page response in dev mode.
// On receiving a "reload" SSE event the page reloads immediately. On
// reconnect after a disconnect (e.g. server restart) the dc flag
// triggers a reload so the page picks up the rebuilt binary. On a
// "build-error" event the failure message is logged to the browser
// console via console.warn — a visible banner UI is a deferred
// follow-up; v1 just exposes the transport so any developer with
// DevTools open sees the failure.
//
// The beforeunload listener closes the EventSource so the browser
// tears down the TCP connection immediately on navigation, preventing
// HTTP/1.1 connection pool exhaustion.
const reloadScript = `<script>(function(){` +
	`var e=new EventSource("/__gastro/reload"),dc=false;` +
	`e.onerror=function(){dc=true};` +
	`e.addEventListener("reload",function(){location.reload()});` +
	`e.addEventListener("build-error",function(ev){` +
	`console.warn("gastro: build failed — previous version still serving\n"+ev.data)` +
	`});` +
	`e.onopen=function(){if(dc)location.reload()};` +
	`window.addEventListener("beforeunload",function(){e.close()})` +
	`})()</script>`

// injectWriter buffers HTML responses so the live-reload script can be
// injected before </body>. Once the Content-Type is known to be non-HTML
// it switches to pass-through mode so SSE and other streaming responses
// are unaffected.
//
// injectWriter is not safe for concurrent use. This matches the standard
// library's http.ResponseWriter contract where handlers are expected to
// call WriteHeader and Write sequentially from a single goroutine.
type injectWriter struct {
	http.ResponseWriter

	buf    bytes.Buffer
	status int

	// decided is true once we know whether this response is HTML or not.
	decided bool
	// html is true when we are buffering an HTML response.
	html bool
}

// Unwrap lets http.NewResponseController reach the underlying writer,
// which is required for SSE flushing to work when a gastro page calls ctx.SSE().
func (w *injectWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func (w *injectWriter) WriteHeader(code int) {
	w.status = code
	if !w.decided {
		w.decide()
	}
	if !w.html {
		w.ResponseWriter.WriteHeader(code)
	}
}

func (w *injectWriter) Write(b []byte) (int, error) {
	if !w.decided {
		// No WriteHeader call yet — sniff from the body if no Content-Type set.
		if w.ResponseWriter.Header().Get("Content-Type") == "" {
			ct := http.DetectContentType(b)
			w.ResponseWriter.Header().Set("Content-Type", ct)
		}
		w.decide()

		if !w.html && w.status != 0 {
			w.ResponseWriter.WriteHeader(w.status)
		}
	}

	if w.html {
		return w.buf.Write(b)
	}
	return w.ResponseWriter.Write(b)
}

// decide inspects the Content-Type header that has been set so far.
func (w *injectWriter) decide() {
	w.decided = true
	ct := w.ResponseWriter.Header().Get("Content-Type")
	w.html = strings.Contains(ct, "text/html")
}

// flush writes the buffered HTML body (with the injected script) to the real
// ResponseWriter. It is a no-op for pass-through (non-HTML) responses.
func (w *injectWriter) flush() {
	if !w.html {
		return
	}

	body := w.buf.Bytes()
	script := []byte(reloadScript)

	// Inject before </body> if present, otherwise append to the end.
	if i := bytes.LastIndex(body, []byte("</body>")); i >= 0 {
		injected := make([]byte, 0, len(body)+len(script))
		injected = append(injected, body[:i]...)
		injected = append(injected, script...)
		injected = append(injected, body[i:]...)
		body = injected
	} else {
		body = append(body, script...)
	}

	// Remove Content-Length: it was set before injection and is now stale.
	w.ResponseWriter.Header().Del("Content-Length")

	if w.status != 0 {
		w.ResponseWriter.WriteHeader(w.status)
	}
	w.ResponseWriter.Write(body) //nolint:errcheck
}
