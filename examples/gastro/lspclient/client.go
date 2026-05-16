// Package lspclient is a small JSON-RPC-over-stdio client for the
// gastro LSP server. It exists so the gastro example website can drive
// a single shared `gastro lsp` subprocess at startup and serve hover +
// diagnostics requests against an embedded `.gastro` demo file.
//
// Scope: the bare minimum the homepage live-LSP demo needs.
//
//   - initialize / initialized handshake
//   - textDocument/didOpen and per-URI publishDiagnostics caching
//   - textDocument/hover (returns MarkupContent the caller can render)
//   - graceful Close that kills the subprocess
//
// Not implemented: didChange, didClose, completion, diagnostics push
// to a subscriber, request cancellation. The demo opens the embedded
// file once at app boot and never edits it.
//
// Concurrency
//
// A single dispatcher goroutine owns the stdout bufio.Reader, demuxes
// responses (by id) to per-request channels, and routes notifications
// (publishDiagnostics) into a per-URI cache. This matches the pattern
// used by cmd/gastro/lsp_integration_test.go — the comment there
// explains it exists because earlier per-call reader goroutines raced
// on the bufio.Reader's internal buffer (visible to `go test -race`).
//
// Stdin writes are serialised through writeMu. The LSP framing
// (`Content-Length: N\r\n\r\n` + body) MUST be emitted atomically, so
// the mutex wraps both header and body in a single critical section.
package lspclient

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
)

// -----------------------------------------------------------------------------
// LSP wire types (minimal subset used by gastro's server)
// -----------------------------------------------------------------------------

// Position is an LSP zero-indexed (line, character) pair.
type Position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

// Range is a half-open [Start, End) span in LSP coordinates.
type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

// Diagnostic mirrors the LSP Diagnostic struct, keeping only the
// fields the demo renderer needs.
type Diagnostic struct {
	Range    Range  `json:"range"`
	Severity int    `json:"severity"`
	Source   string `json:"source"`
	Message  string `json:"message"`
}

// MarkupContent is the LSP container for hover / signature help text.
// Kind is "markdown" or "plaintext"; the gastro LSP always returns
// "markdown" so the demo can pipe Value through md.Render directly.
type MarkupContent struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

// Hover is the parsed result of textDocument/hover.
//
// The LSP spec allows Contents to be a MarkupContent, a MarkedString,
// or an array thereof. The gastro server always returns a single
// MarkupContent, so we only decode that shape. If a future server
// version starts returning the legacy MarkedString form, the JSON
// unmarshal will fail loudly rather than silently produce empty hover
// text.
type Hover struct {
	Contents MarkupContent `json:"contents"`
	Range    *Range        `json:"range,omitempty"`
}

// -----------------------------------------------------------------------------
// Client
// -----------------------------------------------------------------------------

// Client owns one gastro LSP subprocess and serves concurrent hover +
// diagnostics queries against it. Construct via Start.
type Client struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	reader *bufio.Reader
	stderr io.ReadCloser

	// diagHook, if non-nil, is invoked from the dispatcher goroutine
	// every time a publishDiagnostics notification is received, with
	// the URI and the (already-decoded) diagnostics. Set via
	// SetDiagnosticsHook before any OpenFile call. Used by tests; the
	// production path uses WaitForDiagnostics / Diagnostics instead.
	diagHook func(uri string, diags []Diagnostic)

	writeMu sync.Mutex   // serialises framed writes to stdin
	nextID  atomic.Int64 // request id allocator

	mu      sync.Mutex
	pending map[int64]chan rpcResponse // id -> waiter for that response
	diags   map[string][]Diagnostic    // uri -> latest publishDiagnostics
	diagsCh map[string]chan struct{}   // uri -> closed on first publishDiagnostics
	dead    bool                       // dispatcher has exited; no more requests
	deadErr error

	done   chan struct{} // signals dispatcher to stop (Close path)
	exited chan struct{} // closed when dispatcher returns
}

type rpcResponse struct {
	Result json.RawMessage
	Error  *rpcError
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) toError() error {
	if e == nil {
		return nil
	}
	return fmt.Errorf("lsp error %d: %s", e.Code, e.Message)
}

// Start spawns cmd, performs the initialize/initialized handshake
// against rootURI, and returns a ready Client. cmd is expected to be
// fully configured (binary path, args, working dir, env). Start takes
// ownership of cmd's pipes; the caller MUST NOT touch Stdin/Stdout/
// Stderr after this call.
//
// If the handshake fails, Start kills the subprocess before returning
// the error.
func Start(ctx context.Context, cmd *exec.Cmd, rootURI string) (*Client, error) {
	return startWithStderr(ctx, cmd, rootURI, nil)
}

// StartWithStderr is like Start but tees the subprocess's stderr to
// stderrSink. Used by tests; production code uses plain Start.
func StartWithStderr(ctx context.Context, cmd *exec.Cmd, rootURI string, stderrSink io.Writer) (*Client, error) {
	return startWithStderr(ctx, cmd, rootURI, stderrSink)
}

func startWithStderr(ctx context.Context, cmd *exec.Cmd, rootURI string, stderrSink io.Writer) (*Client, error) {
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting lsp: %w", err)
	}

	c := &Client{
		cmd:     cmd,
		stdin:   stdin,
		reader:  bufio.NewReader(stdout),
		stderr:  stderr.(io.ReadCloser),
		pending: make(map[int64]chan rpcResponse),
		diags:   make(map[string][]Diagnostic),
		diagsCh: make(map[string]chan struct{}),
		done:    make(chan struct{}),
		exited:  make(chan struct{}),
	}

	// Drain stderr in the background so a chatty server (or one logging
	// to stderr) can't fill its pipe and deadlock on a write. Tests
	// (or future production paths) capture it by calling StartWithStderr
	// with a non-nil sink.
	sink := io.Writer(io.Discard)
	if stderrSink != nil {
		sink = stderrSink
	}
	go func() {
		io.Copy(sink, c.stderr)
	}()

	go c.dispatch()

	if err := c.handshake(ctx, rootURI); err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}

func (c *Client) handshake(ctx context.Context, rootURI string) error {
	// LSP 3.17 initialize. Capabilities map is intentionally empty: the
	// gastro server doesn't gate features on client capability flags,
	// and the demo only needs hover + publishDiagnostics (both default
	// server capabilities). If the server ever starts honouring client
	// capability flags, this is the place to declare them.
	params := map[string]any{
		"processId":    nil,
		"rootUri":      rootURI,
		"capabilities": map[string]any{},
	}
	if _, err := c.request(ctx, "initialize", params); err != nil {
		return fmt.Errorf("initialize: %w", err)
	}
	if err := c.notify("initialized", map[string]any{}); err != nil {
		return fmt.Errorf("initialized: %w", err)
	}
	return nil
}

// OpenFile sends a textDocument/didOpen for uri with the given source
// text. Returns immediately — use WaitForDiagnostics to block until
// the server has analysed the file. languageID is the LSP language
// identifier ("gastro" for .gastro files).
//
// uri must be a file:// URI; the LSP server treats other schemes as
// virtual documents and won't run gopls against them.
func (c *Client) OpenFile(uri, languageID, text string) error {
	c.mu.Lock()
	if _, exists := c.diagsCh[uri]; !exists {
		c.diagsCh[uri] = make(chan struct{})
	}
	c.mu.Unlock()

	return c.notify("textDocument/didOpen", map[string]any{
		"textDocument": map[string]any{
			"uri":        uri,
			"languageId": languageID,
			"version":    1,
			"text":       text,
		},
	})
}

// WaitForDiagnostics blocks until the server publishes at least one
// publishDiagnostics notification for uri, then returns the cached
// diagnostics. If ctx fires first, returns ctx.Err() and the
// diagnostics cache stays empty for uri (the next call will wait
// again).
//
// OpenFile MUST have been called for uri before this; otherwise the
// returned channel never fires.
func (c *Client) WaitForDiagnostics(ctx context.Context, uri string) ([]Diagnostic, error) {
	c.mu.Lock()
	ch, ok := c.diagsCh[uri]
	c.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("lspclient: WaitForDiagnostics called before OpenFile for %s", uri)
	}

	select {
	case <-ch:
		return c.Diagnostics(uri), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.exited:
		return nil, c.exitErr()
	}
}

// Diagnostics returns the last publishDiagnostics payload for uri, or
// nil if none has arrived yet. The returned slice is a snapshot;
// callers may keep it without holding any client lock.
func (c *Client) Diagnostics(uri string) []Diagnostic {
	c.mu.Lock()
	defer c.mu.Unlock()
	src := c.diags[uri]
	if len(src) == 0 {
		return nil
	}
	out := make([]Diagnostic, len(src))
	copy(out, src)
	return out
}

// Hover sends textDocument/hover and returns the parsed result. A nil
// Hover with a nil error means the server returned a JSON null result
// (no hover available at that position) — this is the common case for
// whitespace, comments, and keywords.
func (c *Client) Hover(ctx context.Context, uri string, line, character int) (*Hover, error) {
	raw, err := c.request(ctx, "textDocument/hover", map[string]any{
		"textDocument": map[string]any{"uri": uri},
		"position":     map[string]any{"line": line, "character": character},
	})
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var h Hover
	if err := json.Unmarshal(raw, &h); err != nil {
		return nil, fmt.Errorf("decode hover: %w (payload: %s)", err, raw)
	}
	return &h, nil
}

// SetDiagnosticsHook installs a callback invoked from the dispatcher
// goroutine every time a publishDiagnostics arrives. Intended for
// tests; production code reads via WaitForDiagnostics / Diagnostics.
// MUST be called before any OpenFile to be useful.
func (c *Client) SetDiagnosticsHook(fn func(uri string, diags []Diagnostic)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.diagHook = fn
}

// Close shuts the LSP subprocess down. Safe to call multiple times.
// Returns the exec.Wait error (if any) from the subprocess, after the
// dispatcher goroutine has fully exited.
func (c *Client) Close() error {
	c.mu.Lock()
	if c.dead && c.deadErr == nil {
		// Dispatcher already exited cleanly; nothing to do beyond Wait.
		c.mu.Unlock()
	} else {
		c.mu.Unlock()
	}

	// Best-effort polite shutdown. We don't send the LSP "shutdown" /
	// "exit" pair: the server kills itself when stdin EOFs, and the
	// demo doesn't need a state-preserving handoff. Killing on Close
	// is also the test pattern in cmd/gastro/lsp_integration_test.go.
	select {
	case <-c.done:
		// Already closing.
	default:
		close(c.done)
	}
	_ = c.stdin.Close()
	if c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
	waitErr := c.cmd.Wait()
	<-c.exited
	return waitErr
}

// -----------------------------------------------------------------------------
// internals
// -----------------------------------------------------------------------------

func (c *Client) request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := c.nextID.Add(1)

	ch := make(chan rpcResponse, 1)
	c.mu.Lock()
	if c.dead {
		err := c.deadErr
		c.mu.Unlock()
		if err == nil {
			err = errors.New("lspclient: client closed")
		}
		return nil, err
	}
	c.pending[id] = ch
	c.mu.Unlock()

	if err := c.writeFrame(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	}); err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, fmt.Errorf("write %s: %w", method, err)
	}

	select {
	case resp := <-ch:
		if resp.Error != nil {
			return nil, resp.Error.toError()
		}
		return resp.Result, nil
	case <-ctx.Done():
		// Don't free the pending slot — the dispatcher will deliver a
		// late response into a closed channel otherwise. Mark it
		// abandoned and let the dispatcher drop it on receipt.
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, ctx.Err()
	case <-c.exited:
		return nil, c.exitErr()
	}
}

func (c *Client) notify(method string, params any) error {
	c.mu.Lock()
	if c.dead {
		err := c.deadErr
		c.mu.Unlock()
		if err == nil {
			err = errors.New("lspclient: client closed")
		}
		return err
	}
	c.mu.Unlock()

	return c.writeFrame(map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	})
}

func (c *Client) writeFrame(msg map[string]any) error {
	body, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(body))

	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if _, err := io.WriteString(c.stdin, header); err != nil {
		return err
	}
	if _, err := c.stdin.Write(body); err != nil {
		return err
	}
	return nil
}

// dispatch owns c.reader. It runs until the reader returns an error
// (typically io.EOF when the subprocess exits or Close kills it) and
// then marks the client dead so subsequent requests fail fast.
func (c *Client) dispatch() {
	defer close(c.exited)
	for {
		msg, err := readFrame(c.reader)
		if err != nil {
			c.markDead(err)
			return
		}
		c.handle(msg)
	}
}

func (c *Client) handle(msg map[string]any) {
	if idRaw, ok := msg["id"]; ok && idRaw != nil {
		// Response (or server-initiated request we don't service).
		// LSP ids may arrive as float64 from json.Unmarshal even
		// though we sent them as int64; coerce.
		var id int64
		switch v := idRaw.(type) {
		case float64:
			id = int64(v)
		case int64:
			id = v
		default:
			return
		}

		// Server requests (id + method, no result/error in the
		// payload) — the gastro server doesn't currently send any,
		// so we drop them. If we add support, route by method here.
		if _, isRequest := msg["method"]; isRequest {
			return
		}

		c.mu.Lock()
		ch, ok := c.pending[id]
		if ok {
			delete(c.pending, id)
		}
		c.mu.Unlock()
		if !ok {
			// Caller already abandoned this request (context cancel).
			return
		}

		resp := rpcResponse{}
		if errRaw, ok := msg["error"]; ok && errRaw != nil {
			b, _ := json.Marshal(errRaw)
			var rpcErr rpcError
			_ = json.Unmarshal(b, &rpcErr)
			resp.Error = &rpcErr
		} else if resultRaw, ok := msg["result"]; ok {
			b, _ := json.Marshal(resultRaw)
			resp.Result = b
		}
		ch <- resp
		return
	}

	// Notification.
	method, _ := msg["method"].(string)
	switch method {
	case "textDocument/publishDiagnostics":
		c.handleDiagnostics(msg)
	default:
		// Server log/showMessage/etc. ignored for now.
	}
}

func (c *Client) handleDiagnostics(msg map[string]any) {
	paramsRaw, _ := msg["params"]
	b, err := json.Marshal(paramsRaw)
	if err != nil {
		return
	}
	var params struct {
		URI         string       `json:"uri"`
		Diagnostics []Diagnostic `json:"diagnostics"`
	}
	if err := json.Unmarshal(b, &params); err != nil {
		return
	}
	if c.diagHook != nil {
		c.diagHook(params.URI, params.Diagnostics)
	}

	c.mu.Lock()
	c.diags[params.URI] = params.Diagnostics
	ch, ok := c.diagsCh[params.URI]
	if !ok {
		// Server pushed diagnostics for a URI we never opened (e.g.
		// gopls publishing for a sibling file). Record them and
		// create a pre-closed channel so a later WaitForDiagnostics
		// returns immediately.
		closed := make(chan struct{})
		close(closed)
		c.diagsCh[params.URI] = closed
		c.mu.Unlock()
		return
	}
	c.mu.Unlock()

	// Close exactly once. select on a closed channel is the cheap
	// "is it closed?" check.
	select {
	case <-ch:
		// already closed
	default:
		close(ch)
	}
}

func (c *Client) markDead(err error) {
	c.mu.Lock()
	if c.dead {
		c.mu.Unlock()
		return
	}
	c.dead = true
	c.deadErr = err
	pending := c.pending
	c.pending = nil
	c.mu.Unlock()

	// Fan out the error to every waiter so request() returns instead
	// of blocking forever.
	for _, ch := range pending {
		select {
		case ch <- rpcResponse{Error: &rpcError{Code: -32000, Message: err.Error()}}:
		default:
		}
	}
}

func (c *Client) exitErr() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.deadErr != nil {
		return fmt.Errorf("lsp subprocess exited: %w", c.deadErr)
	}
	return errors.New("lsp subprocess exited")
}

// readFrame reads one `Content-Length`-framed JSON-RPC message from r.
func readFrame(r *bufio.Reader) (map[string]any, error) {
	contentLength := 0
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			break
		}
		if strings.HasPrefix(line, "Content-Length:") {
			_, _ = fmt.Sscanf(strings.TrimPrefix(line, "Content-Length:"), "%d", &contentLength)
		}
	}
	if contentLength <= 0 {
		return nil, fmt.Errorf("invalid Content-Length: %d", contentLength)
	}
	body := make([]byte, contentLength)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, err
	}
	var msg map[string]any
	if err := json.Unmarshal(body, &msg); err != nil {
		return nil, fmt.Errorf("decode body: %w (body: %s)", err, body)
	}
	return msg, nil
}
