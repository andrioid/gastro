package server

import (
	"encoding/json"
	"fmt"
	"path/filepath"
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
		// //gastro:embed lines: gopls sees them as ordinary comments and
		// returns nil hover. Intercept first so users get a useful
		// resolved-path hover when the cursor sits on the directive.
		if hov := s.embedDirectiveHover(params.TextDocument.URI, content, parsed, params.Position); hov != nil {
			return &jsonRPCMessage{JSONRPC: "2.0", ID: msg.ID, Result: hov}
		}
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

	var rfNames []string
	if inst := s.instanceForURI(uri); inst != nil {
		rfNames = s.requestFuncs.Lookup(inst.root).Names()
	}
	tree, err := lsptemplate.ParseTemplateBodyWithRequestFuncs(parsed.TemplateBody, parsed.Uses, rfNames)
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
		typeStr, description = s.hoverFieldType(uri, target, scope)

	case "variable":
		// $.FieldName — always refers to root context. Chain segments
		// past the head go through the same chain-resolution path as
		// FieldNode chains since both originate from frontmatter
		// variables.
		if target.ChainIdx == 0 {
			types := s.queryVariableTypes(uri)
			if t, ok := types[target.Name]; ok {
				typeStr = t
			}
			description = "frontmatter variable (root context)"
		} else {
			typeStr, description = s.hoverChainSegment(uri, target.Chain, target.ChainIdx, "" /* root */)
		}

	case "function":
		// Request-aware helper (registered via WithRequestFuncs) takes
		// precedence over the built-in function table so the hover shows
		// the source location adopters actually edit. The discovery cache
		// recorded File/Line/Column when scanning main.go.
		if inst := s.instanceForURI(uri); inst != nil {
			entry := s.requestFuncs.Lookup(inst.root)
			if info, ok := entry.HelperAt(target.Name); ok {
				typeStr = target.Name
				rel := info.File
				if r, err := filepath.Rel(inst.root, info.File); err == nil {
					rel = r
				}
				description = fmt.Sprintf(
					"request-aware helper (WithRequestFuncs[%d]) · defined in %s:%d",
					info.BinderID, rel, info.Line,
				)
				break
			}
		}
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

// hoverFieldType returns (typeString, description) for a field-kind
// HoverTarget. Splits into three cases based on chain depth and
// surrounding scope:
//
//   - Top-level head segment (.Foo): look up the frontmatter export's type.
//   - Range/with head segment (.Foo inside {{ range .Items }}): the
//     element type's field.
//   - Chained sub-segment (.Foo.Bar / .Foo.Bar.Baz): walk the chain
//     through gopls via a probe expression. Both top-level and
//     range/with origins are supported.
//
// Phase 4.3: chained sub-segments used to fall through to no hover at
// all because NodeAtCursor only matched the head ident. Now that the
// HoverTarget carries the full chain plus an index, the resolver can
// walk to any depth.
func (s *server) hoverFieldType(uri string, target *lsptemplate.HoverTarget, scope lsptemplate.ScopeInfo) (typeStr, description string) {
	if target.ChainIdx == 0 {
		if scope.Depth == 0 {
			// Top-level: look up type from frontmatter exports
			types := s.queryVariableTypes(uri)
			if t, ok := types[target.Name]; ok {
				typeStr = t
			}
			return typeStr, "frontmatter variable"
		}
		if scope.RangeVar != "" {
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
				return typeStr, fmt.Sprintf("field on `%s`", elemType)
			}
			return typeStr, "range element field"
		}
		return "", ""
	}

	// ChainIdx > 0: chained sub-field. Compute the prefix chain
	// expression so gopls can resolve the parent's type.
	var prefixExpr string
	if scope.Depth == 0 {
		prefixExpr = strings.Join(target.Chain[:target.ChainIdx], ".")
	} else if scope.RangeVar != "" {
		// Mirror the synthesised expression used by walk.go's
		// resolveRangeScope: a range over Foo binds dot to Foo[0], a
		// with on Foo binds dot to Foo. Fields on dot then chain
		// onto that. We use the [0] form unconditionally here —
		// gopls reports identical field sets regardless of whether the
		// container is sliced or with'd because both reduce to the
		// element type.
		parts := append([]string{scope.RangeVar + "[0]"}, target.Chain[:target.ChainIdx]...)
		prefixExpr = strings.Join(parts, ".")
	}
	return s.hoverChainSegment(uri, target.Chain, target.ChainIdx, prefixExpr)
}

// hoverChainSegment resolves the type of chain[idx] given a Go
// expression that evaluates to chain[idx-1]'s value (the prefixExpr).
// When prefixExpr is empty, the chain's head is treated as a top-level
// frontmatter variable and the lookup goes through queryVariableTypes;
// this is the entry point used by `$.Foo.Bar` chains.
func (s *server) hoverChainSegment(uri string, chain []string, idx int, prefixExpr string) (typeStr, description string) {
	if idx == 0 {
		types := s.queryVariableTypes(uri)
		if t, ok := types[chain[0]]; ok {
			return t, "frontmatter variable"
		}
		return "", ""
	}
	if prefixExpr == "" {
		// Re-derive from chain head when caller didn't supply one
		// (the $.A.B.C path).
		prefixExpr = strings.Join(chain[:idx], ".")
	}
	// Cache key: the prefix expression itself. Two different chains
	// landing on the same prefix share fields, which is correct —
	// gopls returns the same completion set for the same Go expression.
	fields := s.resolveFieldsViaChain(uri, "chain:"+prefixExpr, prefixExpr)
	name := chain[idx]
	for _, f := range fields {
		if f.Name == name {
			return f.Type, fmt.Sprintf("field on chain `%s`", prefixExpr)
		}
	}
	return "", fmt.Sprintf("field on chain `%s`", prefixExpr)
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
