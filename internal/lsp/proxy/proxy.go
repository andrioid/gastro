package proxy

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/andrioid/gastro/internal/lsp/sourcemap"
)

// Position is an LSP position (0-indexed line and character).
type Position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

// MapPositionToVirtual maps a position from .gastro coordinates to virtual .go
// coordinates. LSP positions are 0-indexed; source map works with 1-indexed.
func MapPositionToVirtual(pos Position, sm *sourcemap.SourceMap) Position {
	gastroLine1 := pos.Line + 1
	virtualLine1 := sm.GastroToVirtual(gastroLine1)
	return Position{
		Line:      virtualLine1 - 1,
		Character: pos.Character,
	}
}

// MapPositionToGastro maps a position from virtual .go coordinates back to
// .gastro coordinates.
func MapPositionToGastro(pos Position, sm *sourcemap.SourceMap) Position {
	virtualLine1 := pos.Line + 1
	gastroLine1 := sm.VirtualToGastro(virtualLine1)
	return Position{
		Line:      gastroLine1 - 1,
		Character: pos.Character,
	}
}

// RewriteURIToVirtual replaces a gastro file URI with the virtual .go file path.
func RewriteURIToVirtual(gastroURI, virtualPath string) string {
	return "file://" + virtualPath
}

// RewriteURIToGastro replaces a virtual .go file URI with the original gastro URI.
func RewriteURIToGastro(virtualURI, gastroURI string) string {
	return gastroURI
}

// RemapCompletionPositions rewrites textEdit and additionalTextEdits ranges
// in a gopls completion response from virtual .go coordinates back to .gastro
// coordinates. Handles both CompletionList and CompletionItem[] formats.
func RemapCompletionPositions(raw json.RawMessage, sm *sourcemap.SourceMap) json.RawMessage {
	if len(raw) == 0 || string(raw) == "null" {
		return raw
	}

	var result any
	if err := json.Unmarshal(raw, &result); err != nil {
		return raw
	}

	var items []any
	switch v := result.(type) {
	case []any:
		items = v
	case map[string]any:
		if list, ok := v["items"].([]any); ok {
			items = list
		}
	}

	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if te, ok := m["textEdit"].(map[string]any); ok {
			remapRange(te, sm)
		}
		if ates, ok := m["additionalTextEdits"].([]any); ok {
			for _, ate := range ates {
				if ateMap, ok := ate.(map[string]any); ok {
					remapRange(ateMap, sm)
				}
			}
		}
	}

	remapped, err := json.Marshal(result)
	if err != nil {
		return raw
	}
	return remapped
}

// RemapHoverRange rewrites the range in a gopls hover response from virtual
// .go coordinates back to .gastro coordinates.
func RemapHoverRange(raw json.RawMessage, sm *sourcemap.SourceMap) json.RawMessage {
	if len(raw) == 0 || string(raw) == "null" {
		return raw
	}

	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		return raw
	}

	if r, ok := result["range"].(map[string]any); ok {
		remapPos(r, "start", sm)
		remapPos(r, "end", sm)
	}

	remapped, err := json.Marshal(result)
	if err != nil {
		return raw
	}
	return remapped
}

// URIChecker determines whether a URI belongs to the shadow workspace and
// returns the original .gastro URI and source map for remapping. Returns
// ("", nil) if the URI is not a virtual file.
type URIChecker func(virtualURI string) (gastroURI string, sm *sourcemap.SourceMap)

// RemapDefinitionResult rewrites URIs and positions in a gopls definition
// response from virtual .go coordinates back to .gastro coordinates.
// Handles Location, Location[], and LocationLink[] response formats.
// Non-virtual URIs (real .go files) are passed through unchanged.
func RemapDefinitionResult(raw json.RawMessage, check URIChecker) json.RawMessage {
	if len(raw) == 0 || string(raw) == "null" {
		return raw
	}

	var result any
	if err := json.Unmarshal(raw, &result); err != nil {
		return raw
	}

	switch v := result.(type) {
	case map[string]any:
		// Single Location: { uri, range }
		remapLocation(v, check)
	case []any:
		for _, item := range v {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if _, hasTargetURI := m["targetUri"]; hasTargetURI {
				// LocationLink format
				remapLocationLink(m, check)
			} else {
				// Location format
				remapLocation(m, check)
			}
		}
	}

	remapped, err := json.Marshal(result)
	if err != nil {
		return raw
	}
	return remapped
}

// remapLocation rewrites uri and range in a Location object.
func remapLocation(loc map[string]any, check URIChecker) {
	uri, _ := loc["uri"].(string)
	if uri == "" {
		return
	}

	gastroURI, sm := check(uri)
	if gastroURI == "" {
		return
	}

	loc["uri"] = gastroURI
	if r, ok := loc["range"].(map[string]any); ok {
		remapPos(r, "start", sm)
		remapPos(r, "end", sm)
	}
}

// remapLocationLink rewrites URIs and ranges in a LocationLink object.
func remapLocationLink(link map[string]any, check URIChecker) {
	targetURI, _ := link["targetUri"].(string)
	if targetURI == "" {
		return
	}

	gastroURI, sm := check(targetURI)
	if gastroURI == "" {
		return
	}

	link["targetUri"] = gastroURI
	if r, ok := link["targetRange"].(map[string]any); ok {
		remapPos(r, "start", sm)
		remapPos(r, "end", sm)
	}
	if r, ok := link["targetSelectionRange"].(map[string]any); ok {
		remapPos(r, "start", sm)
		remapPos(r, "end", sm)
	}
	if r, ok := link["originSelectionRange"].(map[string]any); ok {
		remapPos(r, "start", sm)
		remapPos(r, "end", sm)
	}
}

// remapRange rewrites start/end positions in a range object (used by textEdit,
// additionalTextEdits, etc.) from virtual to gastro coordinates.
func remapRange(edit map[string]any, sm *sourcemap.SourceMap) {
	if r, ok := edit["range"].(map[string]any); ok {
		remapPos(r, "start", sm)
		remapPos(r, "end", sm)
	}
}

// remapPos rewrites a single position (line + character) from virtual to
// gastro coordinates. JSON numbers arrive as float64.
func remapPos(rangeMap map[string]any, key string, sm *sourcemap.SourceMap) {
	pos, ok := rangeMap[key].(map[string]any)
	if !ok {
		return
	}
	line, _ := pos["line"].(float64)
	char, _ := pos["character"].(float64)
	mapped := MapPositionToGastro(Position{Line: int(line), Character: int(char)}, sm)
	pos["line"] = float64(mapped.Line)
	pos["character"] = float64(mapped.Character)
}

// Backoff implements exponential backoff for gopls restart.
type Backoff struct {
	initial time.Duration
	max     time.Duration
	current time.Duration
}

func NewBackoff(initial, max time.Duration) *Backoff {
	return &Backoff{initial: initial, max: max, current: initial}
}

func (b *Backoff) Next() time.Duration {
	d := b.current
	b.current *= 2
	if b.current > b.max {
		b.current = b.max
	}
	return d
}

func (b *Backoff) Reset() {
	b.current = b.initial
}

// GoplsProxy manages a gopls subprocess and forwards LSP requests to it.
type GoplsProxy struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	reader  *bufio.Reader
	mu      sync.Mutex
	nextID  atomic.Int64
	pending map[int64]chan json.RawMessage
	closed  bool
	backoff *Backoff

	// Callback for async notifications from gopls (e.g., publishDiagnostics)
	OnNotification func(method string, params json.RawMessage)
}

// jsonRPCMessage is a JSON-RPC 2.0 message.
type jsonRPCMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// NewGoplsProxy starts a gopls subprocess and initializes the LSP connection.
func NewGoplsProxy(workspaceDir string, onNotification func(string, json.RawMessage)) (*GoplsProxy, error) {
	goplsPath, err := exec.LookPath("gopls")
	if err != nil {
		return nil, fmt.Errorf("gopls not found in PATH. Install with: go install golang.org/x/tools/gopls@latest")
	}

	p := &GoplsProxy{
		pending:        make(map[int64]chan json.RawMessage),
		backoff:        NewBackoff(1*time.Second, 30*time.Second),
		OnNotification: onNotification,
	}

	if err := p.start(goplsPath, workspaceDir); err != nil {
		return nil, err
	}

	return p, nil
}

func (p *GoplsProxy) start(goplsPath, workspaceDir string) error {
	p.cmd = exec.Command(goplsPath, "serve")
	p.cmd.Dir = workspaceDir
	// Capture gopls stderr so its error messages appear in gastro-lsp logs
	p.cmd.Stderr = log.Writer()

	var err error
	p.stdin, err = p.cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("gopls stdin pipe: %w", err)
	}

	stdout, err := p.cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("gopls stdout pipe: %w", err)
	}
	p.reader = bufio.NewReader(stdout)

	if err := p.cmd.Start(); err != nil {
		return fmt.Errorf("starting gopls: %w", err)
	}

	// Read responses in a goroutine
	go p.readLoop()

	// Send initialize
	initParams := map[string]any{
		"processId": nil,
		"rootUri":   "file://" + workspaceDir,
		"capabilities": map[string]any{
			"textDocument": map[string]any{
				"completion": map[string]any{
					"completionItem": map[string]any{
						"snippetSupport": false,
					},
				},
			},
		},
	}

	_, err = p.Request("initialize", initParams)
	if err != nil {
		return fmt.Errorf("gopls initialize: %w", err)
	}

	// Send initialized notification
	p.Notify("initialized", map[string]any{})
	p.backoff.Reset()

	return nil
}

// Request sends a request to gopls and waits for the response.
func (p *GoplsProxy) Request(method string, params any) (json.RawMessage, error) {
	id := p.nextID.Add(1)

	paramsJSON, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}

	ch := make(chan json.RawMessage, 1)
	p.mu.Lock()
	p.pending[id] = ch
	p.mu.Unlock()

	msg := jsonRPCMessage{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  paramsJSON,
	}

	if err := p.writeMessage(&msg); err != nil {
		p.mu.Lock()
		delete(p.pending, id)
		p.mu.Unlock()
		return nil, err
	}

	select {
	case result := <-ch:
		return result, nil
	case <-time.After(30 * time.Second):
		p.mu.Lock()
		delete(p.pending, id)
		p.mu.Unlock()
		return nil, fmt.Errorf("gopls request timed out: %s", method)
	}
}

// Notify sends a notification to gopls (no response expected).
func (p *GoplsProxy) Notify(method string, params any) error {
	paramsJSON, err := json.Marshal(params)
	if err != nil {
		return err
	}

	msg := jsonRPCMessage{
		JSONRPC: "2.0",
		Method:  method,
		Params:  paramsJSON,
	}

	return p.writeMessage(&msg)
}

// Close shuts down the gopls subprocess. Safe to call while requests are
// in flight — subsequent writeMessage calls will return an error.
func (p *GoplsProxy) Close() {
	p.mu.Lock()
	p.closed = true
	p.mu.Unlock()

	if p.stdin != nil {
		p.stdin.Close()
	}
	if p.cmd != nil && p.cmd.Process != nil {
		p.cmd.Process.Kill()
		p.cmd.Wait()
	}
}

func (p *GoplsProxy) readLoop() {
	for {
		msg, err := readLSPMessage(p.reader)
		if err != nil {
			if err != io.EOF {
				log.Printf("gopls read error: %v", err)
			}
			return
		}

		// Response to a request we sent
		if msg.ID != nil {
			if msg.Error != nil {
				log.Printf("gopls error (id=%v): [%d] %s", msg.ID, msg.Error.Code, msg.Error.Message)
			}

			var id int64
			switch v := msg.ID.(type) {
			case float64:
				id = int64(v)
			case json.Number:
				id, _ = v.Int64()
			}

			p.mu.Lock()
			ch, ok := p.pending[id]
			if ok {
				delete(p.pending, id)
			}
			p.mu.Unlock()

			if ok {
				ch <- msg.Result
			}
			continue
		}

		// Notification from gopls
		if msg.Method != "" && p.OnNotification != nil {
			p.OnNotification(msg.Method, msg.Params)
		}
	}
}

func (p *GoplsProxy) writeMessage(msg *jsonRPCMessage) error {
	body, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(body))

	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return fmt.Errorf("proxy is closed")
	}

	if _, err := p.stdin.Write([]byte(header)); err != nil {
		return err
	}
	if _, err := p.stdin.Write(body); err != nil {
		return err
	}
	return nil
}

func readLSPMessage(reader *bufio.Reader) (*jsonRPCMessage, error) {
	contentLength := 0
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			break
		}
		if strings.HasPrefix(line, "Content-Length:") {
			lenStr := strings.TrimSpace(strings.TrimPrefix(line, "Content-Length:"))
			contentLength, _ = strconv.Atoi(lenStr)
		}
	}

	if contentLength == 0 {
		return nil, fmt.Errorf("missing Content-Length")
	}

	body := make([]byte, contentLength)
	if _, err := io.ReadFull(reader, body); err != nil {
		return nil, err
	}

	var msg jsonRPCMessage
	if err := json.Unmarshal(body, &msg); err != nil {
		return nil, err
	}

	return &msg, nil
}
