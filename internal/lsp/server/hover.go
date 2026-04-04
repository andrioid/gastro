package server

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/andrioid/gastro/internal/lsp/proxy"
	lsptemplate "github.com/andrioid/gastro/internal/lsp/template"
	"github.com/andrioid/gastro/internal/parser"
)

// componentNameRegex matches PascalCase component names in {{ }} blocks.
// Matches bare calls ({{ Card ...) and wrap calls ({{ wrap Layout ...).
var componentNameRegex = regexp.MustCompile(`\{\{\s*(?:wrap\s+)?([A-Z][a-zA-Z0-9]*)`)

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
				hInst := s.instanceForURI(params.TextDocument.URI)
				vf := s.findVirtualFileForURI(params.TextDocument.URI, hInst)
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
// frontmatter variables, range/with element fields, template functions,
// and component tags.
func (s *server) templateHover(uri, content string, pos proxy.Position, parsed *parser.File) any {
	cursorOffset := cursorPosToBodyOffset(content, pos, parsed.TemplateBodyLine)
	if cursorOffset < 0 {
		return nil
	}

	// Check if cursor is on a component tag name before trying AST-based hover.
	// Component tags are raw text to the Go template parser, so NodeAtCursor
	// won't find them.
	if result := s.componentHover(uri, parsed, cursorOffset); result != nil {
		return result
	}

	tree, err := lsptemplate.ParseTemplateBody(parsed.TemplateBody, parsed.Uses)
	if err != nil {
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

// componentHover checks if the cursor is on a component name in a {{ }} block
// and returns hover information showing the component's Props struct fields.
func (s *server) componentHover(uri string, parsed *parser.File, cursorOffset int) any {
	body := parsed.TemplateBody
	for _, idx := range componentNameRegex.FindAllStringSubmatchIndex(body, -1) {
		nameStart, nameEnd := idx[2], idx[3]
		if cursorOffset < nameStart || cursorOffset > nameEnd {
			continue
		}

		compName := body[nameStart:nameEnd]

		// Find the use declaration for this component
		var usePath string
		for _, u := range parsed.Uses {
			if u.Name == compName {
				usePath = u.Path
				break
			}
		}
		if usePath == "" {
			continue
		}

		hoverInst := s.instanceForURI(uri)
		propsMap := s.resolveAllComponentProps(parsed.Uses, hoverInst)
		fields := propsMap[compName]

		bodyLineOffset := parsed.TemplateBodyLine - 1
		startLine, startChar := lsptemplate.OffsetToLineChar(body, nameStart)
		endLine, endChar := lsptemplate.OffsetToLineChar(body, nameEnd)

		var value string
		if len(fields) > 0 {
			var sb strings.Builder
			sb.WriteString("```go\ntype Props struct {\n")
			for _, f := range fields {
				sb.WriteString(fmt.Sprintf("    %s %s\n", f.Name, f.Type))
			}
			sb.WriteString("}\n```\n\n")
			sb.WriteString("Component: " + usePath)
			value = sb.String()
		} else {
			value = "Component: " + usePath + "\n\nNo Props struct defined."
		}

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

	return nil
}
