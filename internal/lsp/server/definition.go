package server

import (
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/andrioid/gastro/internal/lsp/proxy"
	"github.com/andrioid/gastro/internal/parser"
)

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
		// Frontmatter: forward to gopls and remap the result
		result := s.forwardToGopls(params.TextDocument.URI, "textDocument/definition", params.Position)
		if result != nil {
			if raw, ok := result.(json.RawMessage); ok {
				result = json.RawMessage(proxy.RemapDefinitionResult(raw, s.virtualURIChecker(params.TextDocument.URI)))
			}
			return &jsonRPCMessage{JSONRPC: "2.0", ID: msg.ID, Result: result}
		}
		return &jsonRPCMessage{JSONRPC: "2.0", ID: msg.ID, Result: nil}
	}

	// Template body: check for component tag go-to-definition
	if loc := s.componentDefinition(params.TextDocument.URI, parsed, params.Position); loc != nil {
		return &jsonRPCMessage{JSONRPC: "2.0", ID: msg.ID, Result: loc}
	}

	return &jsonRPCMessage{JSONRPC: "2.0", ID: msg.ID, Result: nil}
}

// componentDefinition returns an LSP Location for the component file when
// the cursor is on a component tag name in the template body.
func (s *server) componentDefinition(gastroURI string, parsed *parser.File, pos proxy.Position) any {
	body := parsed.TemplateBody
	bodyStartLine := parsed.TemplateBodyLine - 1 // 0-indexed
	if pos.Line < bodyStartLine {
		return nil
	}

	// Calculate byte offset within template body
	lines := strings.Split(body, "\n")
	relLine := pos.Line - bodyStartLine
	offset := 0
	for i := 0; i < relLine && i < len(lines); i++ {
		offset += len(lines[i]) + 1
	}
	offset += pos.Character
	if offset < 0 || offset > len(body) {
		return nil
	}

	for _, idx := range componentNameRegex.FindAllStringSubmatchIndex(body, -1) {
		nameStart, nameEnd := idx[2], idx[3]
		if offset < nameStart || offset > nameEnd {
			continue
		}

		compName := body[nameStart:nameEnd]
		for _, u := range parsed.Uses {
			if u.Name == compName {
				defInst := s.instanceForURI(gastroURI)
				root := s.projectDir
				if defInst != nil {
					root = defInst.root
				}
				absPath := filepath.Join(root, u.Path)
				return map[string]any{
					"uri": "file://" + absPath,
					"range": map[string]any{
						"start": map[string]any{"line": 0, "character": 0},
						"end":   map[string]any{"line": 0, "character": 0},
					},
				}
			}
		}
	}

	return nil
}
