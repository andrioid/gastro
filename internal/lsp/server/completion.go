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

// LSP CompletionItemKind values
// https://microsoft.github.io/language-server-protocol/specifications/lsp/3.17/specification/#completionItemKind
const (
	completionKindFunction = 3
	completionKindField    = 5
	completionKindVariable = 6
	completionKindClass    = 7 // used for components
	completionKindFile     = 17
	completionKindFolder   = 19
)

// LSP InsertTextFormat values
// https://microsoft.github.io/language-server-protocol/specifications/lsp/3.17/specification/#insertTextFormat
const (
	insertTextFormatPlainText = 1
	insertTextFormatSnippet   = 2
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
	params.TextDocument.URI = canonicalizeURI(params.TextDocument.URI)

	content, ok := s.documents[params.TextDocument.URI]
	if !ok {
		return &jsonRPCMessage{JSONRPC: "2.0", ID: msg.ID, Result: []any{}}
	}

	parsed, err := parser.Parse("virtual.gastro", content)
	if err != nil {
		return &jsonRPCMessage{JSONRPC: "2.0", ID: msg.ID, Result: []any{}}
	}

	cursorLine := params.Position.Line + 1 // 0-indexed -> 1-indexed

	// Frontmatter region: intercept //gastro:embed path completions
	// before forwarding to gopls (gopls treats those as ordinary
	// comments and offers nothing useful).
	if cursorLine < parsed.TemplateBodyLine {
		if items := s.embedDirectiveCompletion(params.TextDocument.URI, content, params.Position); items != nil {
			return &jsonRPCMessage{JSONRPC: "2.0", ID: msg.ID, Result: items}
		}
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

	// Discover WithRequestFuncs binder names so the parse + completion
	// paths see request-aware helpers. Lookup is cached and cheap.
	rfInstParse := s.instanceForURI(uri)
	var rfParseNames []string
	if rfInstParse != nil {
		rfParseNames = s.requestFuncs.Lookup(rfInstParse.root).Names()
	}

	// Determine cursor scope by parsing the template body into an AST
	// and checking if the cursor is inside a range/with block.
	cursorScope := lsptemplate.ScopeInfo{}
	tree, parseErr := lsptemplate.ParseTemplateBodyWithRequestFuncs(parsed.TemplateBody, parsed.Uses, rfParseNames)
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
			// Pipe position — only runtime functions make sense here, not
			// compile-time directives (wrap/raw/endraw). Disable
			// snippet mode so we don't insert argument skeletons where they
			// wouldn't parse.
			for _, c := range lsptemplate.FuncMapCompletionsWithRequestFuncs(false, rfParseNames) {
				items = append(items, map[string]any{
					"label":      c.Label,
					"kind":       completionKindFunction,
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
				for _, c := range lsptemplate.ComponentPropCompletions(fields, tagCtx.ExistingProps, s.snippetSupport) {
					item := map[string]any{
						"label":      c.Label,
						"kind":       completionKindField,
						"detail":     c.Detail,
						"insertText": c.InsertText,
					}
					if c.IsSnippet {
						item["insertTextFormat"] = insertTextFormatSnippet
					}
					items = append(items, item)
				}
				return items
			}
		}
	}

	// Phase 4.5: chained-field completions. When the cursor is sitting
	// after a chain like `.Foo.|` or `.Foo.Bar.|`, the user wants the
	// fields of the prior segment's type — not another top-level
	// frontmatter variable. Detect that shape from the character
	// stream rather than the AST because the trailing dot makes the
	// template syntactically incomplete during this exact edit.
	chainPrefix := detectChainPrefix(content, pos.Line, dotChar)
	switch {
	case len(chainPrefix) > 0:
		items = append(items, s.chainedFieldCompletions(uri, chainPrefix, cursorScope, pos, dotChar)...)
	case cursorScope.Depth > 0 && cursorScope.RangeVar != "":
		// Inside a range/with block — offer field completions for the element type
		fieldItems := s.scopedFieldCompletions(uri, cursorScope.RangeVar, pos, dotChar)
		items = append(items, fieldItems...)
	default:
		// Top-level — offer frontmatter variable completions
		for _, c := range lsptemplate.VariableCompletions(info) {
			item := map[string]any{
				"label":  c.Label,
				"kind":   completionKindVariable,
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
	compInst := s.instanceForURI(uri)
	importedNames := make(map[string]bool, len(parsed.Uses))
	for _, c := range lsptemplate.ComponentCompletions(parsed.Uses) {
		importedNames[c.Label] = true
		item := map[string]any{
			"label":  c.Label,
			"kind":   completionKindClass,
			"detail": c.Detail,
		}
		if s.snippetSupport {
			// Look up the component's path from use declarations to resolve Props
			compPath := ""
			for _, u := range parsed.Uses {
				if u.Name == c.Label {
					compPath = u.Path
					break
				}
			}
			fields := s.cachedComponentProps(compInst, compPath)
			item["insertText"] = lsptemplate.BuildComponentSnippet(c.Label, fields)
			item["insertTextFormat"] = insertTextFormatSnippet
		} else {
			item["insertText"] = c.InsertText
		}
		items = append(items, item)
	}

	// Un-imported component completions (with auto-import edit).
	// Uses getComponents() which re-scans the components/ directory if the
	// cache is stale, so newly created files are picked up without restart.
	var tcComponents []componentInfo
	if compInst != nil {
		tcComponents = compInst.getComponents()
	}
	for _, comp := range tcComponents {
		if importedNames[comp.Name] {
			continue
		}
		item := map[string]any{
			"label":  comp.Name,
			"kind":   completionKindClass,
			"detail": comp.Path + " (auto-import)",
		}
		if s.snippetSupport {
			fields := s.cachedComponentProps(compInst, comp.Path)
			item["insertText"] = lsptemplate.BuildComponentSnippet(comp.Name, fields)
			item["insertTextFormat"] = insertTextFormatSnippet
		} else {
			item["insertText"] = comp.Name
		}

		// Compute additional text edit to insert the import declaration
		importEdit := s.computeAutoImportEdit(content, comp)
		if importEdit != nil {
			item["additionalTextEdits"] = []map[string]any{importEdit}
		}

		items = append(items, item)
	}

	// WithRequestFuncs binder helpers (request-aware) discovered by
	// scanning the project's main.go. Reuses the names captured above
	// for parse-stub feeding so we only walk main.go once per request.
	for _, c := range lsptemplate.FuncMapCompletionsWithRequestFuncs(s.snippetSupport, rfParseNames) {
		item := map[string]any{
			"label":      c.Label,
			"kind":       completionKindFunction,
			"detail":     c.Detail,
			"insertText": c.InsertText,
		}
		if c.IsSnippet {
			item["insertTextFormat"] = insertTextFormatSnippet
		}
		items = append(items, item)
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

// cachedComponentProps returns Props struct fields for a component, checking
// the project instance's cache first to avoid disk I/O on every completion.
func (s *server) cachedComponentProps(inst *projectInstance, compPath string) []codegen.StructField {
	if inst == nil {
		return nil
	}
	if cached, ok := inst.getComponentPropsCacheEntry(compPath); ok {
		if cached.HasValue() {
			return cached.Value()
		}
		return nil
	}
	fields, err := lsptemplate.ResolveComponentProps(inst.root, compPath, s.documents)
	if err != nil {
		return nil
	}
	if fields != nil {
		inst.setComponentPropsCacheEntry(compPath, cacheEntry[[]codegen.StructField]{value: fields})
	} else {
		inst.setComponentPropsCacheEntry(compPath, cacheEntry[[]codegen.StructField]{negative: true})
	}
	return fields
}

// chainedFieldCompletions probes gopls for the fields of the type at
// the end of a chain prefix and returns them as completion items.
// Triggered when the cursor sits after a dot that follows another
// dot-chain (e.g. `.Agent.|` or `.Foo.Bar.|`).
//
// Inside a range/with scope the chain is rooted at the synthesised
// element expression (RangeVar[0]), matching what walk.go does in
// resolveRangeScope. At top-level the chain is rooted at the head
// frontmatter variable.
//
// Falls back to no completions (caller silently appends nothing) if
// gopls is unavailable, the chain doesn't resolve, or the type has no
// exported fields. The user still gets component / function
// suggestions from the surrounding code path so the completion list
// is never blank in practice.
func (s *server) chainedFieldCompletions(uri string, chainPrefix []string, scope lsptemplate.ScopeInfo, pos proxy.Position, dotChar int) []map[string]any {
	// Build the Go expression that evaluates to the type whose fields
	// we want to suggest. Mirrors hover/definition's prefix builder.
	var prefixExpr string
	if scope.Depth > 0 && scope.RangeVar != "" {
		parts := append([]string{scope.RangeVar + "[0]"}, chainPrefix...)
		prefixExpr = strings.Join(parts, ".")
	} else {
		prefixExpr = strings.Join(chainPrefix, ".")
	}

	inst := s.instanceForURI(uri)
	if inst == nil || inst.gopls == nil {
		return nil
	}
	// resolveFieldsViaChain handles the probe injection / restoration.
	// Use the chain expression as the cache key so two completions on
	// the same chain reuse work.
	entries := s.resolveFieldsViaChain(uri, "chain:"+prefixExpr, prefixExpr)
	if len(entries) == 0 {
		return nil
	}

	items := make([]map[string]any, 0, len(entries))
	for _, fi := range entries {
		item := map[string]any{
			"label":      "." + fi.Name,
			"kind":       completionKindField,
			"detail":     fi.Type,
			"filterText": "." + fi.Name,
		}
		if dotChar >= 0 {
			item["textEdit"] = map[string]any{
				"range": map[string]any{
					"start": map[string]any{"line": pos.Line, "character": dotChar},
					"end":   map[string]any{"line": pos.Line, "character": pos.Character},
				},
				"newText": "." + fi.Name,
			}
		} else {
			item["insertText"] = "." + fi.Name
		}
		items = append(items, item)
	}
	return items
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
			"kind":       completionKindField,
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
