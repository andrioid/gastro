package gastro

import (
	"bufio"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

// flusherRecorder wraps httptest.ResponseRecorder to expose http.Flusher
// (which the bare recorder doesn't always advertise via type assertion).
type flusherRecorder struct {
	*httptest.ResponseRecorder
	flushed int
}

func (f *flusherRecorder) Flush() { f.flushed++ }

// minimalWriter is a bare http.ResponseWriter implementation that does NOT
// satisfy http.Flusher — useful for testing capability dispatch since
// httptest.ResponseRecorder always implements Flush().
type minimalWriter struct {
	header http.Header
	body   []byte
	code   int
}

func newMinimalWriter() *minimalWriter { return &minimalWriter{header: http.Header{}} }

func (m *minimalWriter) Header() http.Header        { return m.header }
func (m *minimalWriter) WriteHeader(code int)       { m.code = code }
func (m *minimalWriter) Write(p []byte) (int, error) { m.body = append(m.body, p...); return len(p), nil }

// hijackerOnlyWriter implements only ResponseWriter + Hijacker (no Flusher).
type hijackerOnlyWriter struct {
	*minimalWriter
}

func (h *hijackerOnlyWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return nil, nil, nil
}

// flushHijackRecorder implements both.
type flushHijackRecorder struct {
	*httptest.ResponseRecorder
	flushed int
}

func (fh *flushHijackRecorder) Flush() { fh.flushed++ }
func (fh *flushHijackRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return nil, nil, nil
}

func TestNewPageWriter_TracksHeaderAndBody(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	w := NewPageWriter(rec)

	if BodyWritten(w) {
		t.Fatal("BodyWritten true before any write")
	}
	if HeaderCommitted(w) {
		t.Fatal("HeaderCommitted true before any write")
	}

	w.WriteHeader(http.StatusCreated)
	if !HeaderCommitted(w) {
		t.Error("HeaderCommitted false after WriteHeader")
	}
	if BodyWritten(w) {
		t.Error("BodyWritten true after WriteHeader alone (Q3a: tracked separately)")
	}

	if _, err := w.Write([]byte("hello")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if !BodyWritten(w) {
		t.Error("BodyWritten false after Write")
	}
	if rec.Code != http.StatusCreated {
		t.Errorf("status = %d, want %d (custom status preserved)", rec.Code, http.StatusCreated)
	}
	if rec.Body.String() != "hello" {
		t.Errorf("body = %q, want %q", rec.Body.String(), "hello")
	}
}

func TestNewPageWriter_HeaderOnlyDoesNotMarkBodyWritten(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	w := NewPageWriter(rec)

	w.Header().Set("X-Custom", "value")
	if BodyWritten(w) {
		t.Error("Header().Set marked body as written")
	}
	if HeaderCommitted(w) {
		t.Error("Header().Set marked headers as committed")
	}
	if rec.Header().Get("X-Custom") != "value" {
		t.Error("header not propagated to underlying writer")
	}
}

func TestNewPageWriter_DropsSecondWriteHeader(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	w := NewPageWriter(rec)

	w.WriteHeader(http.StatusTeapot)
	w.WriteHeader(http.StatusInternalServerError) // should be ignored

	if rec.Code != http.StatusTeapot {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusTeapot)
	}
}

func TestNewPageWriter_PreservesFlusher(t *testing.T) {
	t.Parallel()

	rec := &flusherRecorder{ResponseRecorder: httptest.NewRecorder()}
	w := NewPageWriter(rec)

	flusher, ok := w.(http.Flusher)
	if !ok {
		t.Fatal("wrapper does not implement http.Flusher when underlying does")
	}
	if _, ok := w.(http.Hijacker); ok {
		t.Error("wrapper falsely implements http.Hijacker")
	}

	flusher.Flush()
	if rec.flushed != 1 {
		t.Errorf("underlying Flush called %d times, want 1", rec.flushed)
	}
	if !HeaderCommitted(w) {
		t.Error("Flush did not mark headers as committed")
	}
	if BodyWritten(w) {
		t.Error("Flush incorrectly marked body as written")
	}
}

func TestNewPageWriter_PreservesHijacker(t *testing.T) {
	t.Parallel()

	rec := &hijackerOnlyWriter{minimalWriter: newMinimalWriter()}
	w := NewPageWriter(rec)

	if _, ok := w.(http.Flusher); ok {
		t.Error("wrapper falsely implements http.Flusher")
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		t.Fatal("wrapper does not implement http.Hijacker when underlying does")
	}
	if _, _, err := hj.Hijack(); err != nil {
		t.Errorf("Hijack returned error: %v", err)
	}
	if !BodyWritten(w) {
		t.Error("Hijack did not mark body as written (template must not render)")
	}
}

func TestNewPageWriter_PreservesFlusherAndHijacker(t *testing.T) {
	t.Parallel()

	rec := &flushHijackRecorder{ResponseRecorder: httptest.NewRecorder()}
	w := NewPageWriter(rec)

	if _, ok := w.(http.Flusher); !ok {
		t.Error("wrapper does not implement http.Flusher")
	}
	if _, ok := w.(http.Hijacker); !ok {
		t.Error("wrapper does not implement http.Hijacker")
	}
}

func TestNewPageWriter_IsIdempotent(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	w1 := NewPageWriter(rec)
	w2 := NewPageWriter(w1)
	if w1 != w2 {
		t.Error("NewPageWriter on a gastro-owned writer should return it unchanged")
	}
}

func TestBodyWritten_NonGastroWriterReturnsFalse(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	if BodyWritten(rec) {
		t.Error("BodyWritten on plain ResponseRecorder should return false")
	}
}

func TestNewPageWriter_PusherIsNotPreserved(t *testing.T) {
	t.Parallel()

	// Even if the underlying writer were to implement http.Pusher (no
	// stdlib test recorder does), Track B's design (Q3b, plan §4.7)
	// deliberately omits Pusher support. The wrapper must NOT report
	// itself as a Pusher.
	rec := httptest.NewRecorder()
	w := NewPageWriter(rec)
	if _, ok := w.(http.Pusher); ok {
		t.Error("wrapper falsely implements http.Pusher; Track B Q3b: Pusher unsupported")
	}
}
