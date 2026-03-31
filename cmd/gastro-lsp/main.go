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
	"sync"

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
	goplsOpenFiles map[string]int                    // virtual URI -> version (tracks files opened in gopls)
	writeMu        sync.Mutex                        // protects stdout writes from concurrent goroutines
	goplsDiags     map[string][]map[string]any       // URI -> gopls diagnostics (frontmatter)
	templateDiags  map[string][]map[string]any       // URI -> template diagnostics (body)
	typeCache      map[string]map[string]string      // URI -> varName -> type string
	fieldCache     map[string]map[string][]fieldInfo // URI -> varName -> fields
}

func newServer() *server {
	return &server{
		documents:      make(map[string]string),
		goplsOpenFiles: make(map[string]int),
		goplsDiags:     make(map[string][]map[string]any),
		templateDiags:  make(map[string][]map[string]any),
		typeCache:      make(map[string]map[string]string),
		fieldCache:     make(map[string]map[string][]fieldInfo),
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
			s.writeToClient(response)
		}
	}
}

// writeToClient serializes a JSON-RPC message to stdout.
// Safe for concurrent use from the main loop and gopls notification goroutine.
func (s *server) writeToClient(msg *jsonRPCMessage) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	writeMessage(os.Stdout, msg)
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
				"textDocumentSync": 1, // Full sync
				"completionProvider": map[string]any{
					"triggerCharacters": []string{"."},
				},
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

	// Cache gopls diagnostics and publish merged set
	s.goplsDiags[gastroURI] = mappedDiags
	s.publishMergedDiagnostics(gastroURI)
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
	s.runTemplateDiagnostics(uri, params.TextDocument.Text)
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
		delete(s.typeCache, uri) // invalidate caches on change
		delete(s.fieldCache, uri)
		s.syncToGopls(uri, content)
		s.runTemplateDiagnostics(uri, content)
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
	uri := params.TextDocument.URI
	delete(s.documents, uri)
	delete(s.goplsDiags, uri)
	delete(s.templateDiags, uri)
	delete(s.typeCache, uri)
	delete(s.fieldCache, uri)
}

// publishMergedDiagnostics combines gopls (frontmatter) and template (body)
// diagnostics for a URI into a single publishDiagnostics notification.
// Each call replaces all diagnostics for the URI in the editor.
func (s *server) publishMergedDiagnostics(uri string) {
	// Must be non-nil so json.Marshal produces [] not null — VS Code crashes on null.
	merged := make([]map[string]any, 0)
	merged = append(merged, s.goplsDiags[uri]...)
	merged = append(merged, s.templateDiags[uri]...)

	notification := &jsonRPCMessage{
		JSONRPC: "2.0",
		Method:  "textDocument/publishDiagnostics",
	}
	diagResult := map[string]any{
		"uri":         uri,
		"diagnostics": merged,
	}
	notification.Params, _ = json.Marshal(diagResult)
	s.writeToClient(notification)
}

// runTemplateDiagnostics parses the document, runs template-body diagnostics
// (unknown variables, invalid syntax, unknown components), caches the results,
// and publishes the merged diagnostic set.
func (s *server) runTemplateDiagnostics(uri, content string) {
	parsed, err := parser.Parse("virtual.gastro", content)
	if err != nil {
		return
	}

	info, err := codegen.AnalyzeFrontmatter(parsed.Frontmatter)
	if err != nil {
		return
	}

	templateDiags := lsptemplate.Diagnose(parsed.TemplateBody, info, parsed.Uses)

	// Convert to LSP diagnostic format, offsetting positions by the template body start line.
	// TemplateBodyLine is 1-indexed; LSP positions are 0-indexed.
	bodyLineOffset := parsed.TemplateBodyLine - 1
	lspDiags := make([]map[string]any, 0, len(templateDiags))
	for _, d := range templateDiags {
		lspDiags = append(lspDiags, map[string]any{
			"range": map[string]any{
				"start": map[string]any{"line": d.StartLine + bodyLineOffset, "character": d.StartChar},
				"end":   map[string]any{"line": d.EndLine + bodyLineOffset, "character": d.EndChar},
			},
			"severity": 1, // Error
			"message":  d.Message,
			"source":   "gastro",
		})
	}

	s.templateDiags[uri] = lspDiags
	s.publishMergedDiagnostics(uri)
}

// queryVariableTypes queries gopls for the types of exported frontmatter
// variables by sending textDocument/hover requests on the `_ = VarName`
// suppression lines in the virtual file. Returns a cached map of varName to
// type string (e.g. "[]db.Post", "string"). Results are cached per URI and
// invalidated on document changes.
func (s *server) queryVariableTypes(gastroURI string) map[string]string {
	if cached, ok := s.typeCache[gastroURI]; ok {
		return cached
	}

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

	virtualPath := s.workspace.VirtualFilePath(relPath)
	virtualURI := "file://" + virtualPath

	types := make(map[string]string)

	// Find `_ = VarName` lines in the virtual source and hover on VarName
	for lineIdx, line := range strings.Split(vf.GoSource, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "_ = ") {
			continue
		}
		varName := strings.TrimPrefix(trimmed, "_ = ")
		if varName == "" {
			continue
		}

		// Position the cursor on the variable name (after "_ = ")
		charOffset := strings.Index(line, "_ = ") + 4
		hoverParams := map[string]any{
			"textDocument": map[string]any{"uri": virtualURI},
			"position":     map[string]any{"line": lineIdx, "character": charOffset},
		}

		result, err := s.gopls.Request("textDocument/hover", hoverParams)
		if err != nil {
			log.Printf("gopls hover error for %s: %v", varName, err)
			continue
		}

		typeStr := parseTypeFromHover(result)
		if typeStr != "" {
			types[varName] = typeStr
			log.Printf("type for %s: %s", varName, typeStr)
		}
	}

	s.typeCache[gastroURI] = types
	return types
}

// parseTypeFromHover extracts the type string from a gopls hover response.
// gopls returns hover contents as markdown with the type on the first code line,
// typically formatted like: ```go\nvar VarName TypeName\n```
// or just the type expression in a code block.
func parseTypeFromHover(raw json.RawMessage) string {
	var hover struct {
		Contents struct {
			Kind  string `json:"kind"`
			Value string `json:"value"`
		} `json:"contents"`
	}
	if err := json.Unmarshal(raw, &hover); err != nil {
		return ""
	}

	value := hover.Contents.Value
	if value == "" {
		return ""
	}

	// gopls hover format: ```go\nvar name type\n``` or ```go\nname type\n```
	// Extract lines from the code block
	for _, line := range strings.Split(value, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line == "```go" || line == "```" {
			continue
		}
		// "var Posts []db.Post" → extract type after the name
		if strings.HasPrefix(line, "var ") {
			parts := strings.Fields(line)
			if len(parts) >= 3 {
				return strings.Join(parts[2:], " ")
			}
		}
		// "Posts []db.Post" → extract type after the name
		parts := strings.Fields(line)
		if len(parts) >= 2 {
			return strings.Join(parts[1:], " ")
		}
	}

	return ""
}

// elementTypeFromContainer extracts the element type from a container type string.
// "[]db.Post" → "db.Post", "[]*db.Post" → "*db.Post",
// "map[string]db.Post" → "db.Post". Returns "" for non-container types.
func elementTypeFromContainer(typeStr string) string {
	// Slice/array: []T or [N]T
	if strings.HasPrefix(typeStr, "[]") {
		return typeStr[2:]
	}
	// Fixed-size array: [N]T
	if strings.HasPrefix(typeStr, "[") {
		idx := strings.Index(typeStr, "]")
		if idx >= 0 && idx+1 < len(typeStr) {
			return typeStr[idx+1:]
		}
	}
	// Map: map[K]V
	if strings.HasPrefix(typeStr, "map[") {
		idx := strings.Index(typeStr, "]")
		if idx >= 0 && idx+1 < len(typeStr) {
			return typeStr[idx+1:]
		}
	}
	return ""
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
	items := s.templateCompletions(params.TextDocument.URI, content, params.Position, parsed.TemplateBodyLine)
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

	// Template body region: provide hover for variables and functions
	hoverResult := s.templateHover(params.TextDocument.URI, content, params.Position, parsed)
	return &jsonRPCMessage{JSONRPC: "2.0", ID: msg.ID, Result: hoverResult}
}

// templateHover returns hover information for template body elements:
// frontmatter variables, range/with element fields, and template functions.
func (s *server) templateHover(uri, content string, pos proxy.Position, parsed *parser.File) any {
	tree, err := lsptemplate.ParseTemplateBody(parsed.TemplateBody, parsed.Uses)
	if err != nil {
		return nil
	}

	cursorOffset := cursorPosToBodyOffset(content, pos, parsed.TemplateBodyLine)
	if cursorOffset < 0 {
		return nil
	}

	target := lsptemplate.NodeAtCursor(tree, cursorOffset)
	if target == nil {
		return nil
	}

	scope := lsptemplate.CursorScope(tree, cursorOffset)

	var typeStr, description string

	switch target.Kind {
	case "field":
		if scope.Depth == 0 {
			// Top-level: look up type from frontmatter exports
			types := s.queryVariableTypes(uri)
			if t, ok := types[target.Name]; ok {
				typeStr = t
			}
			description = "frontmatter variable"
		} else if scope.RangeVar != "" {
			// Inside range/with: look up field type from element type
			fields := s.getCachedFields(uri, scope.RangeVar)
			for _, f := range fields {
				if f.Label == target.Name {
					typeStr = f.Detail
					break
				}
			}
			types := s.queryVariableTypes(uri)
			if containerType, ok := types[scope.RangeVar]; ok {
				elemType := elementTypeFromContainer(containerType)
				if elemType == "" {
					elemType = containerType
				}
				description = fmt.Sprintf("field on `%s`", elemType)
			} else {
				description = "range element field"
			}
		}

	case "variable":
		// $.FieldName — always refers to root context
		types := s.queryVariableTypes(uri)
		if t, ok := types[target.Name]; ok {
			typeStr = t
		}
		description = "frontmatter variable (root context)"

	case "function":
		sigs := lsptemplate.FuncSignatures()
		if sig, ok := sigs[target.Name]; ok {
			typeStr = sig
		}
		description = "template function"
	}

	if typeStr == "" && description == "" {
		return nil
	}

	// Build the hover markdown
	var value string
	if typeStr != "" {
		value = "```go\n" + typeStr + "\n```\n\n" + description
	} else {
		value = description
	}

	// Convert target positions from template body offsets to absolute .gastro positions
	bodyLineOffset := parsed.TemplateBodyLine - 1
	startLine, startChar := lsptemplate.OffsetToLineChar(parsed.TemplateBody, target.Pos)
	endLine, endChar := lsptemplate.OffsetToLineChar(parsed.TemplateBody, target.EndPos)

	return map[string]any{
		"contents": map[string]any{
			"kind":  "markdown",
			"value": value,
		},
		"range": map[string]any{
			"start": map[string]any{"line": startLine + bodyLineOffset, "character": startChar},
			"end":   map[string]any{"line": endLine + bodyLineOffset, "character": endChar},
		},
	}
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

// findDotStart scans backward from the cursor on the current line to find
// the position of the '.' that starts a variable reference (e.g. in "{{ .T").
// Returns the character offset of the dot, or -1 if no dot is found.
func findDotStart(content string, line, character int) int {
	lines := strings.Split(content, "\n")
	if line < 0 || line >= len(lines) {
		return -1
	}
	lineText := lines[line]
	// Scan backward from cursor position to find a '.'
	for i := character - 1; i >= 0; i-- {
		ch := lineText[i]
		if ch == '.' {
			return i
		}
		// Stop if we hit a character that can't be part of a variable reference
		if !((ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9')) {
			return -1
		}
	}
	return -1
}

func (s *server) templateCompletions(uri, content string, pos proxy.Position, templateBodyLine int) []map[string]any {
	parsed, err := parser.Parse("virtual.gastro", content)
	if err != nil {
		return nil
	}

	info, err := codegen.AnalyzeFrontmatter(parsed.Frontmatter)
	if err != nil {
		return nil
	}

	// Find the dot position so variable completions can use textEdit
	// to replace from the dot through the cursor, avoiding double-dot insertion.
	dotChar := findDotStart(content, pos.Line, pos.Character)

	// Determine cursor scope by parsing the template body into an AST
	// and checking if the cursor is inside a range/with block.
	cursorScope := lsptemplate.ScopeInfo{}
	tree, parseErr := lsptemplate.ParseTemplateBody(parsed.TemplateBody, parsed.Uses)
	if parseErr == nil && tree != nil {
		// Convert cursor position to byte offset within the template body
		cursorOffset := cursorPosToBodyOffset(content, pos, templateBodyLine)
		if cursorOffset >= 0 {
			cursorScope = lsptemplate.CursorScope(tree, cursorOffset)
		}
	}

	var items []map[string]any

	if cursorScope.Depth > 0 && cursorScope.RangeVar != "" {
		// Inside a range/with block — offer field completions for the element type
		fieldItems := s.scopedFieldCompletions(uri, cursorScope.RangeVar, pos, dotChar)
		items = append(items, fieldItems...)
	} else {
		// Top-level — offer frontmatter variable completions
		for _, c := range lsptemplate.VariableCompletions(info) {
			item := map[string]any{
				"label":  c.Label,
				"kind":   6,
				"detail": c.Detail,
			}
			if c.FilterText != "" {
				item["filterText"] = c.FilterText
			}
			if dotChar >= 0 {
				item["textEdit"] = map[string]any{
					"range": map[string]any{
						"start": map[string]any{"line": pos.Line, "character": dotChar},
						"end":   map[string]any{"line": pos.Line, "character": pos.Character},
					},
					"newText": c.InsertText,
				}
			} else {
				item["insertText"] = c.InsertText
			}
			items = append(items, item)
		}
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

// cursorPosToBodyOffset converts a cursor position (line/character in the .gastro file)
// to a byte offset within the template body string.
func cursorPosToBodyOffset(content string, pos proxy.Position, templateBodyLine int) int {
	// templateBodyLine is 1-indexed; pos.Line is 0-indexed
	bodyStartLine := templateBodyLine - 1
	if pos.Line < bodyStartLine {
		return -1
	}

	lines := strings.Split(content, "\n")
	offset := 0
	for i := bodyStartLine; i < pos.Line && i < len(lines); i++ {
		offset += len(lines[i]) + 1 // +1 for newline
	}
	offset += pos.Character
	return offset
}

// scopedFieldCompletions queries gopls for the fields of a variable's element
// type and returns them as completion items. Used when the cursor is inside a
// range/with block.
func (s *server) scopedFieldCompletions(uri, rangeVar string, pos proxy.Position, dotChar int) []map[string]any {
	types := s.queryVariableTypes(uri)
	if types == nil {
		return nil
	}

	typeStr, ok := types[rangeVar]
	if !ok {
		return nil
	}

	// For range, we need the element type (e.g. []db.Post → db.Post)
	elemType := elementTypeFromContainer(typeStr)
	if elemType == "" {
		// Not a container — for `with`, the type itself is the scope type
		elemType = typeStr
	}

	// Strip pointer prefix for field query
	queryType := strings.TrimPrefix(elemType, "*")

	// Query gopls for field completions on this type
	fieldItems := s.queryFieldsFromGopls(uri, rangeVar, queryType)
	if fieldItems == nil {
		return nil
	}

	var items []map[string]any
	for _, fi := range fieldItems {
		item := map[string]any{
			"label":      "." + fi.Label,
			"kind":       5, // Field
			"detail":     fi.Detail,
			"filterText": "." + fi.Label,
		}
		if dotChar >= 0 {
			item["textEdit"] = map[string]any{
				"range": map[string]any{
					"start": map[string]any{"line": pos.Line, "character": dotChar},
					"end":   map[string]any{"line": pos.Line, "character": pos.Character},
				},
				"newText": "." + fi.Label,
			}
		} else {
			item["insertText"] = "." + fi.Label
		}
		items = append(items, item)
	}

	return items
}

// getCachedFields returns the field list for a variable, using the cache or
// querying gopls on a cache miss.
func (s *server) getCachedFields(uri, varName string) []fieldInfo {
	if perURI, ok := s.fieldCache[uri]; ok {
		if fields, ok := perURI[varName]; ok {
			return fields
		}
	}

	types := s.queryVariableTypes(uri)
	if types == nil {
		return nil
	}
	typeStr, ok := types[varName]
	if !ok {
		return nil
	}

	elemType := elementTypeFromContainer(typeStr)
	if elemType == "" {
		elemType = typeStr
	}
	queryType := strings.TrimPrefix(elemType, "*")

	fields := s.queryFieldsFromGopls(uri, varName, queryType)
	if fields == nil {
		return nil
	}

	if s.fieldCache[uri] == nil {
		s.fieldCache[uri] = make(map[string][]fieldInfo)
	}
	s.fieldCache[uri][varName] = fields
	return fields
}

// fieldInfo represents a field discovered from gopls completions.
type fieldInfo struct {
	Label  string
	Detail string
}

// queryFieldsFromGopls queries gopls for fields of the given type by
// temporarily injecting a probe line into the virtual file.
func (s *server) queryFieldsFromGopls(gastroURI, varName, typeName string) []fieldInfo {
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

	virtualPath := s.workspace.VirtualFilePath(relPath)
	virtualURI := "file://" + virtualPath

	// Find the `_ = VarName` line and inject a probe line after it.
	// The probe accesses an element and triggers completion on its fields.
	goLines := strings.Split(vf.GoSource, "\n")
	probeLine := -1
	probeText := fmt.Sprintf("\t_ = %s[0].", varName)

	for i, line := range goLines {
		if strings.TrimSpace(line) == "_ = "+varName {
			probeLine = i + 1
			break
		}
	}

	if probeLine < 0 {
		return nil
	}

	// Inject probe line
	newLines := make([]string, 0, len(goLines)+1)
	newLines = append(newLines, goLines[:probeLine]...)
	newLines = append(newLines, probeText)
	newLines = append(newLines, goLines[probeLine:]...)
	probeSource := strings.Join(newLines, "\n")

	// Write the modified virtual file
	if err := os.WriteFile(virtualPath, []byte(probeSource), 0o644); err != nil {
		return nil
	}

	// Sync the change to gopls
	version := s.goplsOpenFiles[virtualURI] + 1
	s.goplsOpenFiles[virtualURI] = version
	s.gopls.Notify("textDocument/didChange", map[string]any{
		"textDocument": map[string]any{
			"uri":     virtualURI,
			"version": version,
		},
		"contentChanges": []map[string]any{
			{"text": probeSource},
		},
	})

	// Request completions at the dot position on the probe line
	completionParams := map[string]any{
		"textDocument": map[string]any{"uri": virtualURI},
		"position":     map[string]any{"line": probeLine, "character": len(probeText)},
	}

	result, err := s.gopls.Request("textDocument/completion", completionParams)
	if err != nil {
		log.Printf("gopls completion for fields error: %v", err)
		s.restoreVirtualFile(virtualPath, vf, virtualURI)
		return nil
	}

	// Parse the completion response
	fields := parseFieldCompletions(result)

	// Restore the original virtual file
	s.restoreVirtualFile(virtualPath, vf, virtualURI)

	return fields
}

// restoreVirtualFile writes back the original virtual file content and syncs to gopls.
func (s *server) restoreVirtualFile(virtualPath string, vf *shadow.VirtualFile, virtualURI string) {
	os.WriteFile(virtualPath, []byte(vf.GoSource), 0o644)
	version := s.goplsOpenFiles[virtualURI] + 1
	s.goplsOpenFiles[virtualURI] = version
	s.gopls.Notify("textDocument/didChange", map[string]any{
		"textDocument": map[string]any{
			"uri":     virtualURI,
			"version": version,
		},
		"contentChanges": []map[string]any{
			{"text": vf.GoSource},
		},
	})
}

// parseFieldCompletions extracts field names and types from a gopls
// completion response.
func parseFieldCompletions(raw json.RawMessage) []fieldInfo {
	// gopls returns either {items: [...]} or [...] directly
	var response struct {
		Items []struct {
			Label  string `json:"label"`
			Detail string `json:"detail"`
			Kind   int    `json:"kind"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		// Try as plain array
		var items []struct {
			Label  string `json:"label"`
			Detail string `json:"detail"`
			Kind   int    `json:"kind"`
		}
		if err := json.Unmarshal(raw, &items); err != nil {
			return nil
		}
		for _, item := range items {
			response.Items = append(response.Items, item)
		}
	}

	var fields []fieldInfo
	for _, item := range response.Items {
		// Kind 5 = Field, Kind 2 = Method — include both
		if item.Kind == 5 || item.Kind == 2 {
			fields = append(fields, fieldInfo{
				Label:  item.Label,
				Detail: item.Detail,
			})
		}
	}

	return fields
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
