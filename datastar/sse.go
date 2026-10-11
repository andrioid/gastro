// Package datastar integrates the Datastar hypermedia framework
// (https://data-star.dev) with gastro.
//
// It is a thin facade over the official Go SDK
// (github.com/starfederation/datastar-go/datastar) and lives in its own
// module so the core gastro module stays free of third-party dependencies.
// On top of the SDK it adds:
//
//   - error returns where the SDK panics ([NewSSE] on a failed initial
//     flush);
//   - a guard against SSE field injection: carriage returns and line feeds
//     in user-controlled strings can never start a new SSE field or event;
//   - the version-matched Datastar browser client, embedded and served at a
//     content-hashed URL ([ClientHandler], [ClientPattern]) and inserted into
//     templates with {{ datastarScript }} ([Funcs]);
//   - live pages ([LivePages]): one stream per open page that re-renders the
//     page through its GET handler after every change signal ([Notifier])
//     and morphs the page's live root.
//
// The intended architecture is CQRS: each page opens one long-lived
// [SSE] stream (the query side) that pushes fat morphs of the page, while
// commands are plain POST/PUT/DELETE handlers that mutate state and answer
// 204 No Content. See docs/sse.md in the gastro repository.
package datastar

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	sdk "github.com/starfederation/datastar-go/datastar"
)

// SSE is a Datastar server-sent event stream. All methods are safe for
// concurrent use; events are written atomically and flushed immediately.
type SSE struct {
	gen *sdk.ServerSentEventGenerator
}

// SSEOption configures a stream created by [NewSSE].
type SSEOption = sdk.SSEOption

// WithContext replaces the request context as the stream's lifetime. Sends
// fail once it is done.
func WithContext(ctx context.Context) SSEOption {
	return sdk.WithContext(ctx)
}

// NewSSE upgrades w to a Datastar SSE stream: it sets the event-stream
// headers (plus Content-Encoding when [WithCompression] negotiates one) and
// flushes them to the client.
//
// Unlike the SDK, which panics, NewSSE returns an error when that initial
// flush fails — for example because w does not support flushing. In that
// case the returned stream is nil.
//
// Read request signals with [ReadSignals] before calling NewSSE: the stream
// takes over the response and the request body may no longer be readable.
func NewSSE(w http.ResponseWriter, r *http.Request, opts ...SSEOption) (*SSE, error) {
	fw := &flushGuard{ResponseWriter: w, setup: true}
	gen := sdk.NewSSE(fw, r, opts...)
	fw.setup = false
	if fw.setupErr != nil {
		return nil, fmt.Errorf("datastar: flush stream headers: %w", fw.setupErr)
	}
	return &SSE{gen: gen}, nil
}

// flushGuard wraps the response writer handed to the SDK so that a failed
// flush during sdk.NewSSE is recorded instead of panicking. After setup it
// reports flush errors normally, so Send surfaces them.
//
// setup and setupErr are only touched by NewSSE before the stream is
// returned, so they need no synchronisation.
type flushGuard struct {
	http.ResponseWriter
	setup    bool
	setupErr error
}

// FlushError is the hook http.ResponseController prefers over http.Flusher.
func (f *flushGuard) FlushError() error {
	err := http.NewResponseController(f.ResponseWriter).Flush()
	if f.setup && err != nil {
		f.setupErr = err
		return nil
	}
	return err
}

// Unwrap lets http.ResponseController reach the underlying writer for
// deadlines, hijacking and full-duplex support.
func (f *flushGuard) Unwrap() http.ResponseWriter { return f.ResponseWriter }

// Context returns the stream's lifetime context (the request context unless
// [WithContext] was used). Select on Done to stop pushing when the client
// goes away.
func (s *SSE) Context() context.Context { return s.gen.Context() }

// IsClosed reports whether the stream's context is done.
func (s *SSE) IsClosed() bool { return s.gen.IsClosed() }

// EventType is the SSE "event:" field.
type EventType = sdk.EventType

// Datastar's event types.
const (
	EventTypePatchElements = sdk.EventTypePatchElements
	EventTypePatchSignals  = sdk.EventTypePatchSignals
)

// SSEEventOption configures a raw event sent with [SSE.Send].
type SSEEventOption func(*eventConfig)

type eventConfig struct {
	id    string
	retry time.Duration
}

// WithEventID sets the SSE "id:" field, which the browser echoes back as
// Last-Event-ID when it reconnects. Line breaks are removed.
func WithEventID(id string) SSEEventOption {
	return func(c *eventConfig) { c.id = id }
}

// WithRetryDuration sets the SSE "retry:" field — how long the client waits
// before reconnecting. The protocol default (1s) is omitted from the wire.
func WithRetryDuration(d time.Duration) SSEEventOption {
	return func(c *eventConfig) { c.retry = d }
}

// Send writes a raw SSE event and flushes it, following the SDK's
// signature. Each data line becomes one "data:" field; a line containing
// CR or LF is split into several "data:" fields so that it cannot inject
// other SSE fields. Line breaks in the event type and id are removed.
func (s *SSE) Send(eventType EventType, dataLines []string, opts ...SSEEventOption) error {
	var cfg eventConfig
	for _, opt := range opts {
		opt(&cfg)
	}
	sendOpts := make([]sdk.SSEEventOption, 0, 2)
	if cfg.id != "" {
		sendOpts = append(sendOpts, sdk.WithSSEEventId(singleLine(cfg.id)))
	}
	if cfg.retry > 0 {
		sendOpts = append(sendOpts, sdk.WithSSERetryDuration(cfg.retry))
	}

	safe := make([]string, 0, len(dataLines))
	for _, line := range dataLines {
		if !strings.ContainsAny(line, "\r\n") {
			safe = append(safe, line)
			continue
		}
		safe = append(safe, strings.Split(normalizeNewlines(line), "\n")...)
	}
	return s.gen.Send(EventType(singleLine(string(eventType))), safe, sendOpts...)
}

// newlineNormalizer maps every SSE line terminator (CRLF, CR, LF) to LF.
// The SDK splits multi-line payloads on LF only; a bare CR would otherwise
// reach the wire inside a data field, where the browser treats it as a line
// break and parses what follows as a new field.
var newlineNormalizer = strings.NewReplacer("\r\n", "\n", "\r", "\n")

func normalizeNewlines(s string) string {
	if !strings.ContainsRune(s, '\r') {
		return s
	}
	return newlineNormalizer.Replace(s)
}

// lineBreakStripper removes CR and LF from values that must stay on a
// single SSE line (selectors, modes, ids, event types).
var lineBreakStripper = strings.NewReplacer("\r", "", "\n", "")

func singleLine(s string) string {
	if !strings.ContainsAny(s, "\r\n") {
		return s
	}
	return lineBreakStripper.Replace(s)
}
