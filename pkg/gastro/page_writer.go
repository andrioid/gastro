package gastro

import (
	"bufio"
	"net"
	"net/http"
)

// gastroWriter wraps an http.ResponseWriter so the codegen-generated page
// handler can detect whether frontmatter has produced a response body.
//
// Track B (Page model v2) uses the body-written flag to decide whether to
// run the template after frontmatter completes: if frontmatter wrote a body
// (e.g. an SSE patch, a redirect, an error), the template render is
// skipped. If frontmatter only set headers or called WriteHeader, the
// template still runs and any custom status is preserved.
//
// The type is unexported on purpose — it appears in stack traces as
// gastro.gastroWriter, signalling that the writer is owned by gastro and
// is not meant for direct user construction. Construction goes through
// NewPageWriter; body-write inspection goes through BodyWritten.
//
// Capability preservation. http.ResponseWriter is conventionally extended
// via type assertion (e.g. datastar.NewSSE asserts http.Flusher). A naive
// wrapper that always implements Flusher would lie when the underlying
// writer doesn't. The four-combination pattern below dispatches at
// construction time:
//
//	underlying writer        wrapper concrete type
//	base                  →   *gastroWriter
//	+ Flusher             →   *flushWriter
//	+ Hijacker            →   *hijackWriter
//	+ Flusher + Hijacker  →   *flushHijackWriter
//
// Type assertions on the returned http.ResponseWriter therefore succeed
// for exactly the interfaces the underlying writer supports.
//
// http.Pusher is intentionally not preserved. Pusher is the Go API for
// HTTP/2 server push, which all major browsers have removed support for
// (Chrome 106+, Firefox shortly after). The modern replacement, HTTP 103
// Early Hints, works through plain WriteHeader and needs no wrapper. If
// Pusher revives, adding it later is a 10-minute change.
type gastroWriter struct {
	inner           http.ResponseWriter
	headerCommitted bool // WriteHeader called
	bodyWritten     bool // Write called with non-empty payload
}

// Header satisfies http.ResponseWriter. Setting a header never commits the
// response.
func (g *gastroWriter) Header() http.Header { return g.inner.Header() }

// WriteHeader satisfies http.ResponseWriter. The first call commits the
// response status; subsequent calls are silently dropped to avoid the
// stdlib's "superfluous response.WriteHeader call" log spam in handlers
// that delegate writes to user-supplied helpers.
func (g *gastroWriter) WriteHeader(code int) {
	if g.headerCommitted {
		return
	}
	g.headerCommitted = true
	g.inner.WriteHeader(code)
}

// Write satisfies http.ResponseWriter. Writing any bytes (even zero) marks
// the body as written; a zero-length write still implies the handler is
// driving the body and the template render should be skipped. This matches
// stdlib semantics where the first Write commits headers and emits the
// response.
func (g *gastroWriter) Write(p []byte) (int, error) {
	if !g.headerCommitted {
		g.headerCommitted = true
	}
	g.bodyWritten = true
	return g.inner.Write(p)
}

// flushWriter adds http.Flusher when the underlying writer supports it.
type flushWriter struct{ *gastroWriter }

func (f *flushWriter) Flush() {
	// A flush implies headers are on the wire; record it so a subsequent
	// template render isn't redirected by a stale headerCommitted == false.
	f.gastroWriter.headerCommitted = true
	f.gastroWriter.inner.(http.Flusher).Flush()
}

// hijackWriter adds http.Hijacker when the underlying writer supports it.
type hijackWriter struct{ *gastroWriter }

func (h *hijackWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	// Hijacking takes over the connection entirely; the page template must
	// not run afterwards.
	h.gastroWriter.bodyWritten = true
	h.gastroWriter.headerCommitted = true
	return h.gastroWriter.inner.(http.Hijacker).Hijack()
}

// flushHijackWriter adds both http.Flusher and http.Hijacker.
type flushHijackWriter struct{ *gastroWriter }

func (fh *flushHijackWriter) Flush() {
	fh.gastroWriter.headerCommitted = true
	fh.gastroWriter.inner.(http.Flusher).Flush()
}

func (fh *flushHijackWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	fh.gastroWriter.bodyWritten = true
	fh.gastroWriter.headerCommitted = true
	return fh.gastroWriter.inner.(http.Hijacker).Hijack()
}

// NewPageWriter wraps w so generated page handlers can detect whether
// frontmatter wrote a body. Returns the wrapper as http.ResponseWriter; the
// concrete type is one of *gastroWriter, *flushWriter, *hijackWriter, or
// *flushHijackWriter, chosen by inspecting w for the corresponding
// optional interfaces.
//
// If w is already a *gastroWriter (or a wrapper around one), it is
// returned unchanged. This makes the wrapper idempotent so middleware that
// wraps the page writer cannot accidentally hide the body-tracking flag.
func NewPageWriter(w http.ResponseWriter) http.ResponseWriter {
	if _, ok := unwrapGastroWriter(w); ok {
		return w
	}

	g := &gastroWriter{inner: w}
	_, hasFlush := w.(http.Flusher)
	_, hasHijack := w.(http.Hijacker)

	switch {
	case hasFlush && hasHijack:
		return &flushHijackWriter{gastroWriter: g}
	case hasFlush:
		return &flushWriter{gastroWriter: g}
	case hasHijack:
		return &hijackWriter{gastroWriter: g}
	default:
		return g
	}
}

// BodyWritten reports whether w (or the *gastroWriter it wraps) has had a
// body write committed. The generated page handler calls this after
// frontmatter completes to decide whether to skip the template render.
//
// Returns false for any writer that is not a gastro-owned wrapper; callers
// outside generated code should not need this function.
func BodyWritten(w http.ResponseWriter) bool {
	if g, ok := unwrapGastroWriter(w); ok {
		return g.bodyWritten
	}
	return false
}

// HeaderCommitted reports whether WriteHeader has been called on the
// underlying gastro-owned wrapper. Used by tests; not currently consulted
// by generated code.
func HeaderCommitted(w http.ResponseWriter) bool {
	if g, ok := unwrapGastroWriter(w); ok {
		return g.headerCommitted
	}
	return false
}

// unwrapGastroWriter peels back the four-combination wrapper types to find
// the underlying *gastroWriter. Returns (nil, false) if w isn't gastro-owned.
func unwrapGastroWriter(w http.ResponseWriter) (*gastroWriter, bool) {
	switch v := w.(type) {
	case *gastroWriter:
		return v, true
	case *flushWriter:
		return v.gastroWriter, true
	case *hijackWriter:
		return v.gastroWriter, true
	case *flushHijackWriter:
		return v.gastroWriter, true
	}
	return nil, false
}
