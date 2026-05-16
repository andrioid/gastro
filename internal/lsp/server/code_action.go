package server

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/andrioid/gastro/internal/codegen"
	"github.com/andrioid/gastro/internal/lsp/proxy"
	"github.com/andrioid/gastro/internal/parser"
)

// codeActionParams is the subset of LSP CodeActionParams we care about.
// The full spec carries a few more fields (triggerKind, partial result
// tokens, work-done progress) that we don't act on; ignoring them is
// safe because the protocol marks them optional.
type codeActionParams struct {
	TextDocument struct {
		URI string `json:"uri"`
	} `json:"textDocument"`
	Range   proxy.Range `json:"range"`
	Context struct {
		// Diagnostics is the editor's current diagnostic set
		// intersecting Range. We don't trust it for fix detection
		// (see embedCodeActions: we re-validate from source), but we
		// do attach matching entries to the action's `diagnostics`
		// field so editors know which squiggle each fix resolves.
		Diagnostics []map[string]any `json:"diagnostics"`
		Only        []string         `json:"only,omitempty"`
	} `json:"context"`
}

// handleCodeAction is the dispatch point for textDocument/codeAction.
// Returns a possibly-empty array of CodeAction objects. Never returns
// null — VS Code (in particular) treats null as a protocol error.
//
// Strategy:
//   - Build our own quick-fixes for embed-directive diagnostics by
//     re-validating the document (cheap; no I/O for var-type errors).
//   - Forward the same request to gopls and merge its actions in so
//     features like "organize imports" keep working in frontmatter.
//
// Phase 1 returns an empty array; Phase 2 wires embedCodeActions and
// Phase 3 wires the gopls forwarder.
func (s *server) handleCodeAction(msg *jsonRPCMessage) *jsonRPCMessage {
	var params codeActionParams
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		// Malformed request — return empty so the client doesn't see
		// a hard error. Editors recover by retrying on the next edit.
		return &jsonRPCMessage{JSONRPC: "2.0", ID: msg.ID, Result: []map[string]any{}}
	}
	params.TextDocument.URI = canonicalizeURI(params.TextDocument.URI)

	// If the client filtered to a specific Only set, honour it. Our
	// only category is "quickfix"; if that's not in the filter,
	// short-circuit. (LSP convention: an empty/nil Only means
	// "everything goes".)
	if len(params.Context.Only) > 0 && !sliceContainsString(params.Context.Only, "quickfix") {
		return &jsonRPCMessage{JSONRPC: "2.0", ID: msg.ID, Result: []map[string]any{}}
	}

	actions := s.embedCodeActions(params)
	actions = append(actions, s.forwardCodeActionToGopls(params)...)
	if actions == nil {
		actions = []map[string]any{}
	}
	return &jsonRPCMessage{JSONRPC: "2.0", ID: msg.ID, Result: actions}
}

// embedCodeActions builds quick-fix code actions for embed-directive
// diagnostics intersecting the request range. Today only BadVarType
// produces actions; the rest of the diagnostic kinds keep their
// descriptive error text without an automated fix.
//
// Re-validation is intentional: the alternative (stuffing fix metadata
// into Diagnostic.data and decoding it on the way back) adds JSON
// round-trip friction for a check that runs in microseconds and reads
// no files for var-type errors.
func (s *server) embedCodeActions(params codeActionParams) []map[string]any {
	s.dataMu.RLock()
	content, ok := s.documents[params.TextDocument.URI]
	s.dataMu.RUnlock()
	if !ok {
		return nil
	}

	parsed, err := parser.Parse("virtual.gastro", content)
	if err != nil {
		return nil
	}

	sourceFile := uriToPath(params.TextDocument.URI)
	if sourceFile == "" {
		return nil
	}
	moduleRoot := codegen.FindModuleRootForFile(sourceFile)
	if moduleRoot == "" {
		return nil
	}

	_, diags := codegen.ValidateEmbedDirectives(parsed.Frontmatter, codegen.EmbedContext{
		SourceFile: sourceFile,
		ModuleRoot: moduleRoot,
	})
	if len(diags) == 0 {
		return nil
	}

	fmLineOffset := parsed.FrontmatterLine - 1
	contentLines := strings.Split(content, "\n")

	var actions []map[string]any
	for _, d := range diags {
		if d.Kind != codegen.EmbedDiagBadVarType {
			continue
		}
		if d.DeclLine <= 0 {
			continue
		}
		absDirectiveLine := fmLineOffset + d.DirectiveLine - 1
		absDeclLine := fmLineOffset + d.DeclLine - 1
		// Match if the request range overlaps either the directive
		// line (where the squiggle lives) or the decl line (where
		// the cursor might be while the user reaches for the
		// lightbulb). LSP clients vary in which they send.
		if !rangeIntersectsLine(params.Range, absDirectiveLine) &&
			!rangeIntersectsLine(params.Range, absDeclLine) {
			continue
		}
		if absDeclLine < 0 || absDeclLine >= len(contentLines) {
			continue
		}
		typeStart, typeEnd, spanOK := varTypeSpanInLine(contentLines[absDeclLine])
		if !spanOK {
			continue
		}

		boundDiag := matchEditorDiagnostic(params.Context.Diagnostics, absDirectiveLine, d.Message)

		// `string` first — it's the markdown case, which is the
		// overwhelmingly common reason users land on this directive.
		for _, replacement := range []string{"string", "[]byte"} {
			actions = append(actions, buildVarTypeFix(
				params.TextDocument.URI,
				absDeclLine, typeStart, typeEnd,
				replacement,
				boundDiag,
			))
		}
	}
	return actions
}

// rangeIntersectsLine reports whether r touches line (0-indexed).
// Inclusive on both ends because LSP clients commonly send
// single-position ranges (start == end) and we don't want those to
// fall through the cracks at line boundaries.
func rangeIntersectsLine(r proxy.Range, line int) bool {
	return r.Start.Line <= line && r.End.Line >= line
}

// matchEditorDiagnostic walks the editor-supplied diagnostic list and
// returns the entry whose start line matches and whose message looks
// like the embed BadVarType diag, so we can attach it to the code
// action's `diagnostics` field. Returns nil if no match is found —
// the action is still valid, the editor just won't link the squiggle
// to the fix automatically.
func matchEditorDiagnostic(editorDiags []map[string]any, directiveLine int, ourMessage string) map[string]any {
	for _, ed := range editorDiags {
		r, _ := ed["range"].(map[string]any)
		start, _ := r["start"].(map[string]any)
		lineF, _ := start["line"].(float64)
		if int(lineF) != directiveLine {
			continue
		}
		if msg, _ := ed["message"].(string); msg == ourMessage {
			return ed
		}
	}
	return nil
}

// buildVarTypeFix renders a single LSP CodeAction that replaces the
// type expression at (line, start..end) with replacement. Same shape
// for both the `string` and `[]byte` variants.
func buildVarTypeFix(uri string, line, start, end int, replacement string, boundDiag map[string]any) map[string]any {
	edit := map[string]any{
		"range": map[string]any{
			"start": map[string]any{"line": line, "character": start},
			"end":   map[string]any{"line": line, "character": end},
		},
		"newText": replacement,
	}
	action := map[string]any{
		"title": fmt.Sprintf("Change var type to `%s`", replacement),
		"kind":  "quickfix",
		"edit": map[string]any{
			"changes": map[string]any{
				uri: []any{edit},
			},
		},
	}
	if boundDiag != nil {
		action["diagnostics"] = []any{boundDiag}
	}
	return action
}

// forwardCodeActionToGopls forwards the request to gopls (against the
// virtual .go file) and remaps any TextEdit ranges in the response
// back to .gastro coordinates. Returns nil when:
//   - the URI doesn't belong to a project instance,
//   - gopls is unavailable (silently — our own actions still flow),
//   - gopls returns null / errors out,
//   - the request range falls outside the frontmatter region after
//     remapping (gopls has nothing to say about the template body).
//
// The forwarded range is the user's original range mapped through the
// shadow workspace's source map. Gopls then operates on virtual
// coordinates as it would for any other request.
func (s *server) forwardCodeActionToGopls(params codeActionParams) []map[string]any {
	inst := s.instanceForURI(params.TextDocument.URI)
	if inst == nil || inst.gopls == nil {
		return nil
	}
	vf := s.findVirtualFileForURI(params.TextDocument.URI, inst)
	if vf == nil {
		return nil
	}
	relPath, err := filepath.Rel(inst.root, uriToPath(params.TextDocument.URI))
	if err != nil {
		return nil
	}
	virtualURI := "file://" + inst.workspace.VirtualFilePath(relPath)

	virtualRange := proxy.MapRangeToVirtual(params.Range, vf.SourceMap)

	goplsParams := map[string]any{
		"textDocument": map[string]any{"uri": virtualURI},
		"range":        virtualRange,
		"context": map[string]any{
			// Editor-supplied diagnostics are .gastro-coordinate; gopls
			// would mis-interpret them. Pass an empty list — gopls's
			// quick-fixes (organize imports, etc.) don't depend on
			// receiving diagnostics back from the client.
			"diagnostics": []any{},
			"only":        params.Context.Only,
		},
	}

	raw, reqErr := inst.gopls.Request("textDocument/codeAction", goplsParams)
	if reqErr != nil || len(raw) == 0 {
		return nil
	}
	return proxy.RemapCodeActionRanges(raw, vf.SourceMap)
}

// sliceContainsString reports whether s contains target. Local helper to
// avoid an import of slices for one call site.
func sliceContainsString(haystack []string, needle string) bool {
	for _, v := range haystack {
		if v == needle {
			return true
		}
	}
	return false
}
