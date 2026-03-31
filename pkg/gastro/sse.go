package gastro

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
)

// SSE streams Server-Sent Events into an http.ResponseWriter.
// Each event is flushed to the client immediately.
// All methods are safe for concurrent use.
type SSE struct {
	ctx context.Context
	rc  *http.ResponseController
	w   http.ResponseWriter
	mu  sync.Mutex
}

// NewSSE upgrades an http.ResponseWriter to an SSE stream.
// It sets the required headers and flushes them to the client.
// The connection stays open until the request context is cancelled
// or the handler returns.
func NewSSE(w http.ResponseWriter, r *http.Request) *SSE {
	rc := http.NewResponseController(w)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	if r.ProtoMajor == 1 {
		w.Header().Set("Connection", "keep-alive")
	}

	// Flush headers so the client sees text/event-stream immediately.
	rc.Flush()

	return &SSE{
		ctx: r.Context(),
		rc:  rc,
		w:   w,
	}
}

// Send writes a single SSE event and flushes it to the client.
// The eventType maps to the SSE "event:" field.
// Each element in data becomes a "data:" line in the event.
func (s *SSE) Send(eventType string, data ...string) error {
	if err := s.ctx.Err(); err != nil {
		return fmt.Errorf("sse: connection closed: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	var b strings.Builder

	b.WriteString("event: ")
	b.WriteString(eventType)
	b.WriteByte('\n')

	for _, line := range data {
		b.WriteString("data: ")
		b.WriteString(line)
		b.WriteByte('\n')
	}

	// Blank line terminates the event per the SSE spec.
	b.WriteByte('\n')

	if _, err := fmt.Fprint(s.w, b.String()); err != nil {
		return fmt.Errorf("sse: write failed: %w", err)
	}

	if err := s.rc.Flush(); err != nil {
		return fmt.Errorf("sse: flush failed: %w", err)
	}

	return nil
}

// IsClosed reports whether the client has disconnected.
func (s *SSE) IsClosed() bool {
	return s.ctx.Err() != nil
}

// Context returns the request context, useful for select loops
// that wait for client disconnection.
func (s *SSE) Context() context.Context {
	return s.ctx
}
