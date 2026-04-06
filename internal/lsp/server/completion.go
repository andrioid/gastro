package server

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/andrioid/gastro/internal/codegen"
	"github.com/andrioid/gastro/internal/lsp/proxy"
	lsptemplate "github.com/andrioid/gastro/internal/lsp/template"
	"github.com/andrioid/gastro/internal/parser"
)

// positionParams is used by completion, hover, and definition handlers.
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
			compInst := s.instanceForURI(params.TextDocument.URI)
			vf := s.findVirtualFileForURI(params.TextDocument.URI, compInst)
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

func (s *server) templateCompletions(uri, content string, pos proxy.Position, templateBodyLine int) []map[string]any {
	parsed, err := parser.Parse("virtual.gastro", content)
	if err != nil {
		return nil
	}

	// Convert cursor position to byte offset within the template body
	cursorOffset := cursorPosToBodyOffset(content, pos, templateBodyLine)

	// Only offer completions when the cursor is inside a {{ }} action.
	// The "{" trigger character fires on a single brace, but we should not
	// suggest template variables/functions in plain HTML or CSS contexts.
	if !isInsideAction(parsed.TemplateBody, cursorOffset) {
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
		if cursorOffset >= 0 {
			cursorScope = lsptemplate.CursorScope(tree, cursorOffset)
		}
	}

	var items []map[string]any

	// Check if cursor is inside a prop value expression ({...}) — if after |,
	// offer only pipe function completions. If after {., let the regular
	// variable completions handle it (they already work via fall-through).
	if propValCtx := lsptemplate.DetectPropValueContext(parsed.TemplateBody, cursorOffset); propValCtx != nil {
		if propValCtx.AfterPipe {
			for _, c := range lsptemplate.FuncMapCompletions() {
				items = append(items, map[string]any{
					"label":      c.Label,
					"kind":       3, // LSP Function
					"detail":     c.Detail,
					"insertText": c.InsertText,
				})
			}
			return items
		}
		// AfterPipe == false: cursor is after {. — fall through to regular
		// variable completions which already handle this correctly.
	}

	// Check if cursor is inside a component tag — if so, offer prop completions
	tagCtx := lsptemplate.DetectComponentTagContext(parsed.TemplateBody, cursorOffset, parsed.Uses, tree)
	if tagCtx != nil {
		// Resolve the component's Props fields
		compPath := ""
		for _, u := range parsed.Uses {
			if u.Name == tagCtx.ComponentName {
				compPath = u.Path
				break
			}
		}
		if compPath != "" {
			compInst := s.instanceForURI(uri)
			compRoot := s.projectDir
			if compInst != nil {
				compRoot = compInst.root
			}
			fields, err := lsptemplate.ResolveComponentProps(compRoot, compPath, s.documents)
			if err == nil && len(fields) > 0 {
				for _, c := range lsptemplate.ComponentPropCompletions(fields, tagCtx.ExistingProps) {
					items = append(items, map[string]any{
						"label":      c.Label,
						"kind":       5, // LSP Field
						"detail":     c.Detail,
						"insertText": c.InsertText,
					})
				}
				return items
			}
		}
	}

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

	// Imported component completions
	importedNames := make(map[string]bool, len(parsed.Uses))
	for _, c := range lsptemplate.ComponentCompletions(parsed.Uses) {
		importedNames[c.Label] = true
		items = append(items, map[string]any{
			"label":      c.Label,
			"kind":       7,
			"detail":     c.Detail,
			"insertText": c.InsertText,
		})
	}

	// Un-imported component completions (with auto-import edit).
	// Uses getComponents() which re-scans the components/ directory if the
	// cache is stale, so newly created files are picked up without restart.
	tcInst := s.instanceForURI(uri)
	var tcComponents []componentInfo
	if tcInst != nil {
		tcComponents = tcInst.getComponents()
	}
	for _, comp := range tcComponents {
		if importedNames[comp.Name] {
			continue
		}
		item := map[string]any{
			"label":      comp.Name,
			"kind":       7,
			"detail":     comp.Path + " (auto-import)",
			"insertText": comp.Name,
		}

		// Compute additional text edit to insert the import declaration
		importEdit := s.computeAutoImportEdit(content, comp)
		if importEdit != nil {
			item["additionalTextEdits"] = []map[string]any{importEdit}
		}

		items = append(items, item)
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

// computeAutoImportEdit calculates an LSP TextEdit that inserts a component
// import into the frontmatter. Returns nil if the insertion point can't be found.
func (s *server) computeAutoImportEdit(content string, comp componentInfo) map[string]any {
	lines := strings.Split(content, "\n")
	importLine := fmt.Sprintf("\timport %s %q", comp.Name, comp.Path)

	// Look for an existing grouped import block: import ( ... )
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == ")" {
			// Check if this closes a grouped import block by scanning backwards
			for j := i - 1; j >= 0; j-- {
				prevTrimmed := strings.TrimSpace(lines[j])
				if prevTrimmed == "import (" || strings.HasPrefix(prevTrimmed, "import (") {
					// Insert before the closing )
					return map[string]any{
						"range": map[string]any{
							"start": map[string]any{"line": i, "character": 0},
							"end":   map[string]any{"line": i, "character": 0},
						},
						"newText": importLine + "\n",
					}
				}
				// Stop scanning if we hit a non-import line that's not empty/comment
				if prevTrimmed != "" && !strings.HasPrefix(prevTrimmed, "//") && !strings.HasPrefix(prevTrimmed, "\"") && !strings.Contains(prevTrimmed, ".gastro\"") {
					break
				}
			}
		}
	}

	// No grouped import block found — insert a standalone import after the opening ---
	for i, line := range lines {
		if strings.TrimSpace(line) == "---" {
			return map[string]any{
				"range": map[string]any{
					"start": map[string]any{"line": i + 1, "character": 0},
					"end":   map[string]any{"line": i + 1, "character": 0},
				},
				"newText": fmt.Sprintf("import %s %q\n", comp.Name, comp.Path),
			}
		}
	}

	return nil
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
