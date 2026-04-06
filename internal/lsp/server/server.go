package server

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/andrioid/gastro/internal/codegen"
	"github.com/andrioid/gastro/internal/lsp/proxy"
	"github.com/andrioid/gastro/internal/lsp/shadow"
	"github.com/andrioid/gastro/internal/lsp/sourcemap"
	lsptemplate "github.com/andrioid/gastro/internal/lsp/template"
)

// componentInfo represents a discovered component in the components/ directory.
type componentInfo struct {
	Name string // PascalCase name derived from filename (e.g., "PostCard")
	Path string // relative path (e.g., "components/post-card.gastro")
}

// projectInstance groups a shadow workspace, gopls proxy, and component index
// for a single project root (directory containing go.mod).
type projectInstance struct {
	root                string
	workspace           *shadow.Workspace
	gopls               *proxy.GoplsProxy
	goplsError          error // non-nil if gopls failed to start
	componentsMu        sync.RWMutex
	components          []componentInfo
	componentsScannedAt time.Time
	componentPropsCache map[string][]codegen.StructField // componentPath -> Props struct fields
	goplsOpenFiles      map[string]int                   // virtualURI -> version
}

// fieldInfo represents a field discovered from gopls completions.
type fieldInfo struct {
	Label  string
	Detail string
}

type server struct {
	version              string
	documents            map[string]string                              // URI -> content
	projectDir           string                                         // global root from editor (fallback)
	instances            map[string]*projectInstance                    // projectRoot -> instance
	writeMu              sync.Mutex                                     // protects stdout writes from concurrent goroutines
	dataMu               sync.RWMutex                                   // protects diagnostic and document maps from concurrent access
	goplsDiags           map[string][]map[string]any                    // URI -> gopls diagnostics (frontmatter)
	templateDiags        map[string][]map[string]any                    // URI -> template diagnostics (body)
	typeCache            map[string]map[string]string                   // URI -> varName -> type string
	fieldCache           map[string]map[string][]fieldInfo              // URI -> varName -> fields
	typeFieldCache       map[string]map[string][]lsptemplate.FieldEntry // URI -> typeName -> resolved fields
	notifiedGoplsMissing sync.Once                                      // ensures gopls-unavailable notification is sent only once
}

func newServer(version string) *server {
	return &server{
		version:        version,
		documents:      make(map[string]string),
		instances:      make(map[string]*projectInstance),
		goplsDiags:     make(map[string][]map[string]any),
		templateDiags:  make(map[string][]map[string]any),
		typeCache:      make(map[string]map[string]string),
		fieldCache:     make(map[string]map[string][]fieldInfo),
		typeFieldCache: make(map[string]map[string][]lsptemplate.FieldEntry),
	}
}

// Run starts the LSP server and processes messages from stdin.
// This is the only exported function in the server package.
func Run(version string) {
	log.SetOutput(os.Stderr)
	log.Println("gastro lsp: starting")

	server := newServer(version)
	server.run()
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

// notifyGoplsUnavailable sends a custom notification to the editor so it can
// prompt the user to install gopls. Only sent once per server lifetime.
func (s *server) notifyGoplsUnavailable(goplsErr error) {
	s.notifiedGoplsMissing.Do(func() {
		params, _ := json.Marshal(map[string]string{
			"message": goplsErr.Error(),
		})
		s.writeToClient(&jsonRPCMessage{
			JSONRPC: "2.0",
			Method:  "gastro/goplsNotAvailable",
			Params:  params,
		})
	})
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

func (s *server) shutdown() {
	for _, inst := range s.instances {
		if inst.gopls != nil {
			inst.gopls.Close()
		}
		if inst.workspace != nil {
			inst.workspace.Close()
		}
	}
}

// findGastroURIForVirtualURI looks up which .gastro file corresponds to a
// virtual .go file URI from the shadow workspace.
func (s *server) findGastroURIForVirtualURI(virtualURI string, inst *projectInstance) string {
	virtualPath := uriToPath(virtualURI)
	if inst == nil || inst.workspace == nil {
		return ""
	}

	// Check each tracked document that belongs to this instance
	for gastroURI := range s.documents {
		gastroPath := uriToPath(gastroURI)
		relPath, err := filepath.Rel(inst.root, gastroPath)
		if err != nil || strings.HasPrefix(relPath, "..") {
			continue
		}
		if inst.workspace.VirtualFilePath(relPath) == virtualPath {
			return gastroURI
		}
	}
	return ""
}

func (s *server) findVirtualFileForURI(gastroURI string, inst *projectInstance) *shadow.VirtualFile {
	if inst == nil || inst.workspace == nil {
		return nil
	}
	gastroPath := uriToPath(gastroURI)
	relPath, err := filepath.Rel(inst.root, gastroPath)
	if err != nil {
		return nil
	}
	return inst.workspace.GetFile(relPath)
}

// virtualURIChecker returns a proxy.URIChecker that resolves virtual file URIs
// in the shadow workspace back to their .gastro source URIs.
func (s *server) virtualURIChecker(requestingGastroURI string) proxy.URIChecker {
	return func(virtualURI string) (string, *sourcemap.SourceMap) {
		virtualPath := uriToPath(virtualURI)

		s.dataMu.RLock()
		defer s.dataMu.RUnlock()

		for _, inst := range s.instances {
			gastroRelPath := inst.workspace.FindGastroFileForVirtualPath(virtualPath)
			if gastroRelPath == "" {
				continue
			}
			vf := inst.workspace.GetFile(gastroRelPath)
			if vf == nil {
				continue
			}
			gastroAbsPath := filepath.Join(inst.root, gastroRelPath)
			return "file://" + gastroAbsPath, vf.SourceMap
		}

		return "", nil
	}
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
