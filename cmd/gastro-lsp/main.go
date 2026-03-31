package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/andrioid/gastro/internal/codegen"
	"github.com/andrioid/gastro/internal/lsp/proxy"
	"github.com/andrioid/gastro/internal/lsp/shadow"
	lsptemplate "github.com/andrioid/gastro/internal/lsp/template"
	"github.com/andrioid/gastro/internal/parser"
)

// gastro-lsp: a Language Server Protocol server for .gastro files.
// Communicates via JSON-RPC 2.0 over stdin/stdout.

func main() {
	log.SetOutput(os.Stderr)
	log.Println("gastro-lsp: starting")

	server := newServer()
	server.run()
}

type server struct {
	documents      map[string]string // URI -> content
	workspace      *shadow.Workspace
	gopls          *proxy.GoplsProxy
	projectDir     string
	goplsOpenFiles map[string]int // virtual URI -> version (tracks files opened in gopls)
}

func newServer() *server {
	return &server{
		documents:      make(map[string]string),
		goplsOpenFiles: make(map[string]int),
	}
}

func (s *server) run() {
	reader := bufio.NewReader(os.Stdin)

	for {
		msg, err := readMessage(reader)
		if err != nil {
			if err == io.EOF {
				return
			}
			log.Printf("read error: %v", err)
			return
		}

		response := s.handleMessage(msg)
		if response != nil {
			writeMessage(os.Stdout, response)
		}
	}
}

type jsonRPCMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  any             `json:"result,omitempty"`
}

func (s *server) handleMessage(msg *jsonRPCMessage) *jsonRPCMessage {
	switch msg.Method {
	case "initialize":
		return s.handleInitialize(msg)
	case "initialized":
		return nil
	case "textDocument/didOpen":
		s.handleDidOpen(msg)
		return nil
	case "textDocument/didChange":
		s.handleDidChange(msg)
		return nil
	case "textDocument/didClose":
		s.handleDidClose(msg)
		return nil
	case "textDocument/completion":
		return s.handleCompletion(msg)
	case "textDocument/hover":
		return s.handleHover(msg)
	case "textDocument/definition":
		return s.handleDefinition(msg)
	case "shutdown":
		s.shutdown()
		return &jsonRPCMessage{JSONRPC: "2.0", ID: msg.ID, Result: nil}
	case "exit":
		s.shutdown()
		os.Exit(0)
		return nil
	default:
		// If this is a request (has an ID), we must respond.
		// Notifications (no ID) can be silently ignored.
		if msg.ID != nil {
			return &jsonRPCMessage{JSONRPC: "2.0", ID: msg.ID, Result: nil}
		}
		return nil
	}
}

type initializeParams struct {
	RootURI  string `json:"rootUri"`
	RootPath string `json:"rootPath"`
}

func (s *server) handleInitialize(msg *jsonRPCMessage) *jsonRPCMessage {
	var params initializeParams
	json.Unmarshal(msg.Params, &params)

	// Determine project root
	s.projectDir = uriToPath(params.RootURI)
	if s.projectDir == "" {
		s.projectDir = params.RootPath
	}
	if s.projectDir == "" {
		s.projectDir, _ = os.Getwd()
	}
	log.Printf("project dir: %s", s.projectDir)

	// Set up shadow workspace and gopls proxy
	s.initGopls()

	return &jsonRPCMessage{
		JSONRPC: "2.0",
		ID:      msg.ID,
		Result: map[string]any{
			"capabilities": map[string]any{
				"textDocumentSync":   1, // Full sync
				"completionProvider": map[string]any{},
				"hoverProvider":      true,
				"definitionProvider": true,
			},
			"serverInfo": map[string]any{
				"name":    "gastro-lsp",
				"version": "0.1.0",
			},
		},
	}
}

func (s *server) initGopls() {
	var err error
	s.workspace, err = shadow.NewWorkspace(s.projectDir)
	if err != nil {
		log.Printf("warning: could not create shadow workspace: %v", err)
		return
	}

	s.gopls, err = proxy.NewGoplsProxy(s.workspace.Dir(), func(method string, params json.RawMessage) {
		s.handleGoplsNotification(method, params)
	})
	if err != nil {
		log.Printf("warning: could not start gopls: %v", err)
		s.workspace.Close()
		s.workspace = nil
		return
	}

	log.Println("gopls proxy initialized")
}

// handleGoplsNotification processes async notifications from gopls
// (e.g., publishDiagnostics) and forwards them to the editor with mapped positions.
func (s *server) handleGoplsNotification(method string, params json.RawMessage) {
	log.Printf("gopls notification: %s", method)
	if method != "textDocument/publishDiagnostics" {
		return
	}

	var diagParams struct {
		URI         string `json:"uri"`
		Diagnostics []struct {
			Range struct {
				Start proxy.Position `json:"start"`
				End   proxy.Position `json:"end"`
			} `json:"range"`
			Severity int    `json:"severity"`
			Message  string `json:"message"`
		} `json:"diagnostics"`
	}
	if err := json.Unmarshal(params, &diagParams); err != nil {
		return
	}

	// Find which .gastro file this virtual file corresponds to
	log.Printf("gopls diagnostic: uri=%s diags=%d", diagParams.URI, len(diagParams.Diagnostics))
	gastroURI := s.findGastroURIForVirtualURI(diagParams.URI)
	if gastroURI == "" {
		log.Printf("gopls diagnostic: no matching .gastro file for %s", diagParams.URI)
		return
	}

	vf := s.findVirtualFileForURI(gastroURI)
	if vf == nil {
		return
	}

	// Map diagnostic positions back to .gastro coordinates.
	// Must be non-nil so json.Marshal produces [] not null — VS Code crashes on null.
	mappedDiags := make([]map[string]any, 0)
	for _, d := range diagParams.Diagnostics {
		mappedStart := proxy.MapPositionToGastro(d.Range.Start, vf.SourceMap)
		mappedEnd := proxy.MapPositionToGastro(d.Range.End, vf.SourceMap)

		// Skip diagnostics outside the frontmatter region.
		// Negative lines are before the frontmatter; lines at or past
		// FrontmatterEndLine are on the closing --- or beyond (e.g.,
		// _ = VarName suppression lines added for template-exported vars).
		if mappedStart.Line < 0 || mappedEnd.Line < 0 {
			continue
		}
		if vf.FrontmatterEndLine > 0 && mappedStart.Line+1 >= vf.FrontmatterEndLine {
			continue
		}

		mappedDiags = append(mappedDiags, map[string]any{
			"range": map[string]any{
				"start": mappedStart,
				"end":   mappedEnd,
			},
			"severity": d.Severity,
			"message":  d.Message,
			"source":   "gopls",
		})
	}

	// Publish remapped diagnostics to the editor
	notification := &jsonRPCMessage{
		JSONRPC: "2.0",
		Method:  "textDocument/publishDiagnostics",
	}
	diagResult := map[string]any{
		"uri":         gastroURI,
		"diagnostics": mappedDiags,
	}
	notification.Params, _ = json.Marshal(diagResult)
	writeMessage(os.Stdout, notification)
}

type didOpenParams struct {
	TextDocument struct {
		URI        string `json:"uri"`
		LanguageID string `json:"languageId"`
		Version    int    `json:"version"`
		Text       string `json:"text"`
	} `json:"textDocument"`
}

func (s *server) handleDidOpen(msg *jsonRPCMessage) {
	var params didOpenParams
	json.Unmarshal(msg.Params, &params)

	uri := params.TextDocument.URI
	s.documents[uri] = params.TextDocument.Text
	log.Printf("opened: %s", uri)

	s.syncToGopls(uri, params.TextDocument.Text)
}

type didChangeParams struct {
	TextDocument struct {
		URI     string `json:"uri"`
		Version int    `json:"version"`
	} `json:"textDocument"`
	ContentChanges []struct {
		Text string `json:"text"`
	} `json:"contentChanges"`
}

func (s *server) handleDidChange(msg *jsonRPCMessage) {
	var params didChangeParams
	json.Unmarshal(msg.Params, &params)

	if len(params.ContentChanges) > 0 {
		uri := params.TextDocument.URI
		content := params.ContentChanges[0].Text
		s.documents[uri] = content
		s.syncToGopls(uri, content)
	}
}

type didCloseParams struct {
	TextDocument struct {
		URI string `json:"uri"`
	} `json:"textDocument"`
}

func (s *server) handleDidClose(msg *jsonRPCMessage) {
	var params didCloseParams
	json.Unmarshal(msg.Params, &params)
	delete(s.documents, params.TextDocument.URI)
}

// syncToGopls updates the virtual .go file in the shadow workspace and
// notifies gopls about the change. Sends didOpen on first sync for a file,
// didChange on subsequent syncs.
func (s *server) syncToGopls(gastroURI, content string) {
	if s.workspace == nil || s.gopls == nil {
		return
	}

	// Convert URI to relative path for the workspace
	gastroPath := uriToPath(gastroURI)
	relPath, err := filepath.Rel(s.projectDir, gastroPath)
	if err != nil {
		log.Printf("cannot compute relative path: %v", err)
		return
	}

	vf, err := s.workspace.UpdateFile(relPath, content)
	if err != nil {
		log.Printf("updating virtual file: %v", err)
		return
	}

	virtualPath := s.workspace.VirtualFilePath(relPath)
	virtualURI := "file://" + virtualPath
	log.Printf("syncToGopls: gastro=%s virtual=%s", relPath, virtualURI)

	version, alreadyOpen := s.goplsOpenFiles[virtualURI]
	if !alreadyOpen {
		// First time: send didOpen
		version = 1
		s.goplsOpenFiles[virtualURI] = version
		if err := s.gopls.Notify("textDocument/didOpen", map[string]any{
			"textDocument": map[string]any{
				"uri":        virtualURI,
				"languageId": "go",
				"version":    version,
				"text":       vf.GoSource,
			},
		}); err != nil {
			log.Printf("gopls didOpen error: %v", err)
		}
	} else {
		// Subsequent: send didChange with incremented version
		version++
		s.goplsOpenFiles[virtualURI] = version
		if err := s.gopls.Notify("textDocument/didChange", map[string]any{
			"textDocument": map[string]any{
				"uri":     virtualURI,
				"version": version,
			},
			"contentChanges": []map[string]any{
				{"text": vf.GoSource},
			},
		}); err != nil {
			log.Printf("gopls didChange error: %v", err)
		}
	}
}

type positionParams struct {
	TextDocument struct {
		URI string `json:"uri"`
	} `json:"textDocument"`
	Position proxy.Position `json:"position"`
}

func (s *server) handleCompletion(msg *jsonRPCMessage) *jsonRPCMessage {
	var params positionParams
	json.Unmarshal(msg.Params, &params)

	content, ok := s.documents[params.TextDocument.URI]
	if !ok {
		return &jsonRPCMessage{JSONRPC: "2.0", ID: msg.ID, Result: []any{}}
	}

	parsed, err := parser.Parse("virtual.gastro", content)
	if err != nil {
		return &jsonRPCMessage{JSONRPC: "2.0", ID: msg.ID, Result: []any{}}
	}

	cursorLine := params.Position.Line + 1 // 0-indexed -> 1-indexed

	// Frontmatter region: forward to gopls
	if cursorLine < parsed.TemplateBodyLine {
		result := s.forwardToGopls(params.TextDocument.URI, "textDocument/completion", params.Position)
		if result == nil {
			return &jsonRPCMessage{JSONRPC: "2.0", ID: msg.ID, Result: []any{}}
		}
		// Remap textEdit ranges from virtual .go coordinates to .gastro coordinates
		if raw, ok := result.(json.RawMessage); ok {
			vf := s.findVirtualFileForURI(params.TextDocument.URI)
			if vf != nil {
				result = proxy.RemapCompletionPositions(raw, vf.SourceMap)
			}
		}
		return &jsonRPCMessage{JSONRPC: "2.0", ID: msg.ID, Result: result}
	}

	// Template body region: our own completions
	items := s.templateCompletions(content)
	if items == nil {
		items = []map[string]any{}
	}
	return &jsonRPCMessage{JSONRPC: "2.0", ID: msg.ID, Result: items}
}

func (s *server) handleHover(msg *jsonRPCMessage) *jsonRPCMessage {
	var params positionParams
	json.Unmarshal(msg.Params, &params)

	content, ok := s.documents[params.TextDocument.URI]
	if !ok {
		return &jsonRPCMessage{JSONRPC: "2.0", ID: msg.ID, Result: nil}
	}

	parsed, err := parser.Parse("virtual.gastro", content)
	if err != nil {
		return &jsonRPCMessage{JSONRPC: "2.0", ID: msg.ID, Result: nil}
	}

	cursorLine := params.Position.Line + 1
	if cursorLine < parsed.TemplateBodyLine {
		result := s.forwardToGopls(params.TextDocument.URI, "textDocument/hover", params.Position)
		if result != nil {
			// Remap range from virtual .go coordinates to .gastro coordinates
			if raw, ok := result.(json.RawMessage); ok {
				vf := s.findVirtualFileForURI(params.TextDocument.URI)
				if vf != nil {
					result = proxy.RemapHoverRange(raw, vf.SourceMap)
				}
			}
			return &jsonRPCMessage{JSONRPC: "2.0", ID: msg.ID, Result: result}
		}
	}

	return &jsonRPCMessage{JSONRPC: "2.0", ID: msg.ID, Result: nil}
}

func (s *server) handleDefinition(msg *jsonRPCMessage) *jsonRPCMessage {
	var params positionParams
	json.Unmarshal(msg.Params, &params)

	content, ok := s.documents[params.TextDocument.URI]
	if !ok {
		return &jsonRPCMessage{JSONRPC: "2.0", ID: msg.ID, Result: nil}
	}

	parsed, err := parser.Parse("virtual.gastro", content)
	if err != nil {
		return &jsonRPCMessage{JSONRPC: "2.0", ID: msg.ID, Result: nil}
	}

	cursorLine := params.Position.Line + 1
	if cursorLine < parsed.TemplateBodyLine {
		result := s.forwardToGopls(params.TextDocument.URI, "textDocument/definition", params.Position)
		if result != nil {
			return &jsonRPCMessage{JSONRPC: "2.0", ID: msg.ID, Result: result}
		}
	}

	return &jsonRPCMessage{JSONRPC: "2.0", ID: msg.ID, Result: nil}
}

// forwardToGopls sends a request to gopls with mapped positions and returns
// the result with positions mapped back.
func (s *server) forwardToGopls(gastroURI, method string, pos proxy.Position) any {
	if s.gopls == nil || s.workspace == nil {
		return nil
	}

	gastroPath := uriToPath(gastroURI)
	relPath, err := filepath.Rel(s.projectDir, gastroPath)
	if err != nil {
		return nil
	}

	vf := s.workspace.GetFile(relPath)
	if vf == nil {
		return nil
	}

	// Map position to virtual file coordinates
	virtualPos := proxy.MapPositionToVirtual(pos, vf.SourceMap)
	log.Printf("forwardToGopls: %s gastro pos=%+v -> virtual pos=%+v", method, pos, virtualPos)
	virtualPath := s.workspace.VirtualFilePath(relPath)
	virtualURI := "file://" + virtualPath

	goplsParams := map[string]any{
		"textDocument": map[string]any{
			"uri": virtualURI,
		},
		"position": virtualPos,
	}

	result, err := s.gopls.Request(method, goplsParams)
	if err != nil {
		log.Printf("gopls %s error: %v", method, err)
		return nil
	}

	// For completion results, the positions don't need mapping back
	// since they're insertion positions relative to the cursor.
	// For hover/definition, we'd need to map range positions back.
	// Return as-is for now — completions are the primary use case.
	return json.RawMessage(result)
}

func (s *server) templateCompletions(content string) []map[string]any {
	parsed, err := parser.Parse("virtual.gastro", content)
	if err != nil {
		return nil
	}

	info, err := codegen.AnalyzeFrontmatter(parsed.Frontmatter)
	if err != nil {
		return nil
	}

	var items []map[string]any

	for _, c := range lsptemplate.VariableCompletions(info) {
		items = append(items, map[string]any{
			"label":      c.Label,
			"kind":       6,
			"detail":     c.Detail,
			"insertText": c.InsertText,
		})
	}

	for _, c := range lsptemplate.ComponentCompletions(parsed.Uses) {
		items = append(items, map[string]any{
			"label":      c.Label,
			"kind":       7,
			"detail":     c.Detail,
			"insertText": c.InsertText,
		})
	}

	for _, c := range lsptemplate.FuncMapCompletions() {
		items = append(items, map[string]any{
			"label":      c.Label,
			"kind":       3,
			"detail":     c.Detail,
			"insertText": c.InsertText,
		})
	}

	return items
}

func (s *server) shutdown() {
	if s.gopls != nil {
		s.gopls.Close()
	}
	if s.workspace != nil {
		s.workspace.Close()
	}
}

// findGastroURIForVirtualURI looks up which .gastro file corresponds to a
// virtual .go file URI from the shadow workspace.
func (s *server) findGastroURIForVirtualURI(virtualURI string) string {
	virtualPath := uriToPath(virtualURI)
	if s.workspace == nil {
		return ""
	}

	// Check each tracked document
	for gastroURI := range s.documents {
		gastroPath := uriToPath(gastroURI)
		relPath, err := filepath.Rel(s.projectDir, gastroPath)
		if err != nil {
			continue
		}
		if s.workspace.VirtualFilePath(relPath) == virtualPath {
			return gastroURI
		}
	}
	return ""
}

func (s *server) findVirtualFileForURI(gastroURI string) *shadow.VirtualFile {
	if s.workspace == nil {
		return nil
	}
	gastroPath := uriToPath(gastroURI)
	relPath, err := filepath.Rel(s.projectDir, gastroPath)
	if err != nil {
		return nil
	}
	return s.workspace.GetFile(relPath)
}

// uriToPath converts a file:// URI to a local filesystem path.
func uriToPath(uri string) string {
	parsed, err := url.Parse(uri)
	if err != nil {
		return strings.TrimPrefix(uri, "file://")
	}
	return parsed.Path
}

// LSP message framing

func readMessage(reader *bufio.Reader) (*jsonRPCMessage, error) {
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
		return nil, fmt.Errorf("missing Content-Length header")
	}

	body := make([]byte, contentLength)
	_, err := io.ReadFull(reader, body)
	if err != nil {
		return nil, err
	}

	var msg jsonRPCMessage
	if err := json.Unmarshal(body, &msg); err != nil {
		return nil, err
	}

	return &msg, nil
}

func writeMessage(w io.Writer, msg *jsonRPCMessage) {
	// JSON-RPC 2.0 requires responses (messages with an ID) to always have
	// a "result" or "error" field. Since we use omitempty on Result to avoid
	// including it in notifications, we need to manually ensure responses
	// with nil Result serialize "result": null.
	if msg.ID != nil && msg.Result == nil && msg.Method == "" {
		// This is a response with no result — serialize with explicit null
		body, _ := json.Marshal(struct {
			JSONRPC string `json:"jsonrpc"`
			ID      any    `json:"id"`
			Result  any    `json:"result"`
		}{msg.JSONRPC, msg.ID, nil})
		header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(body))
		w.Write([]byte(header))
		w.Write(body)
		return
	}

	body, _ := json.Marshal(msg)
	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(body))
	w.Write([]byte(header))
	w.Write(body)
}
