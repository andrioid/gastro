package server

import (
	"encoding/json"
	"log"
	"path/filepath"
	"strings"

	"github.com/andrioid/gastro/internal/format"
)

type formattingParams struct {
	TextDocument struct {
		URI string `json:"uri"`
	} `json:"textDocument"`
}

func (s *server) handleFormatting(msg *jsonRPCMessage) *jsonRPCMessage {
	var params formattingParams
	json.Unmarshal(msg.Params, &params)

	uri := canonicalizeURI(params.TextDocument.URI)

	s.dataMu.RLock()
	content, ok := s.documents[uri]
	s.dataMu.RUnlock()

	if !ok {
		return &jsonRPCMessage{JSONRPC: "2.0", ID: msg.ID, Result: []any{}}
	}

	filename := filepath.Base(uriToPath(uri))
	formatted, changed, err := format.FormatFile(filename, content)
	if err != nil {
		log.Printf("formatting %s: %v", uri, err)
		return &jsonRPCMessage{JSONRPC: "2.0", ID: msg.ID, Result: []any{}}
	}

	if !changed {
		return &jsonRPCMessage{JSONRPC: "2.0", ID: msg.ID, Result: []any{}}
	}

	// Single full-document replacement edit
	lastLine := strings.Count(content, "\n")
	lastLineLen := len(content) - strings.LastIndex(content, "\n") - 1
	if lastLineLen < 0 {
		lastLineLen = len(content)
	}

	edits := []map[string]any{
		{
			"range": map[string]any{
				"start": map[string]any{"line": 0, "character": 0},
				"end":   map[string]any{"line": lastLine, "character": lastLineLen},
			},
			"newText": formatted,
		},
	}

	return &jsonRPCMessage{JSONRPC: "2.0", ID: msg.ID, Result: edits}
}
