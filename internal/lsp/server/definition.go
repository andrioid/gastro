package server

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/andrioid/gastro/internal/lsp/proxy"
	lsptemplate "github.com/andrioid/gastro/internal/lsp/template"
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

	// Template body: check for field/variable go-to-definition (Phase 4.4).
	// Top-level frontmatter variables already have `_ = VarName` lines in
	// the shadow file; chained sub-fields (.Agent.Name) require an
	// injected probe line so gopls can resolve the chain.
	if loc := s.fieldDefinition(params.TextDocument.URI, content, parsed, params.Position); loc != nil {
		return &jsonRPCMessage{JSONRPC: "2.0", ID: msg.ID, Result: loc}
	}

	return &jsonRPCMessage{JSONRPC: "2.0", ID: msg.ID, Result: nil}
}

// fieldDefinition resolves go-to-definition for a field or $-variable
// segment in the template body. Returns a marshalled LSP Location (or
// list of Locations) or nil if the cursor isn't on a resolvable target.
//
// Strategy:
//
//   - Top-level head segment: forward to gopls on the existing
//     `_ = VarName` suppression line so gopls jumps to the
//     frontmatter declaration. RemapDefinitionResult translates
//     virtual-file positions back to .gastro coordinates.
//   - Chained sub-segment: inject a `_ = chainExpr.SegmentName` probe,
//     ask gopls for the definition of SegmentName in that probe, then
//     restore. The result is whatever gopls reports for the underlying
//     Go field declaration — typically a non-virtual user file, which
//     passes through the remapper unchanged.
func (s *server) fieldDefinition(uri, content string, parsed *parser.File, pos proxy.Position) any {
	cursorOffset := cursorPosToBodyOffset(content, pos, parsed.TemplateBodyLine)
	if cursorOffset < 0 {
		return nil
	}
	var rfNames []string
	if inst := s.instanceForURI(uri); inst != nil {
		rfNames = s.requestFuncs.Lookup(inst.root).Names()
	}
	tree, err := lsptemplate.ParseTemplateBodyWithRequestFuncs(parsed.TemplateBody, parsed.Uses, rfNames)
	if err != nil || tree == nil {
		return nil
	}
	target := lsptemplate.NodeAtCursor(tree, cursorOffset)
	if target == nil {
		return nil
	}
	if target.Kind != "field" && target.Kind != "variable" {
		return nil
	}

	scope := lsptemplate.CursorScope(tree, cursorOffset)

	if target.ChainIdx == 0 {
		return s.headFieldDefinition(uri, target, scope)
	}
	return s.chainedFieldDefinition(uri, target, scope)
}

// headFieldDefinition handles the cursor landing on the first segment
// of a chain (the only segment for a non-chained reference). Top-level
// frontmatter variables already have `_ = VarName` suppression lines
// in the shadow source, so a textDocument/definition request on that
// line jumps gopls to the frontmatter declaration; the virtual→gastro
// remapper then aligns the result with the source the user is editing.
//
// Inside range/with, the head segment is a field on the element type;
// gopls would need a probe to resolve it. That case is delegated to
// chainedFieldDefinition with an empty prefix — it builds a probe
// `_ = RangeVar[0].SegName` and asks gopls to resolve the field there.
func (s *server) headFieldDefinition(uri string, target *lsptemplate.HoverTarget, scope lsptemplate.ScopeInfo) any {
	if scope.Depth > 0 && scope.RangeVar != "" {
		return s.chainedFieldDefinition(uri, target, scope)
	}

	inst := s.instanceForURI(uri)
	if inst == nil || inst.gopls == nil {
		return nil
	}
	gastroPath := uriToPath(uri)
	relPath, err := filepath.Rel(inst.root, gastroPath)
	if err != nil {
		return nil
	}
	vf := inst.workspace.GetFile(relPath)
	if vf == nil {
		return nil
	}
	virtualPath := inst.workspace.VirtualFilePath(relPath)
	virtualURI := "file://" + virtualPath

	// Find the `_ = VarName` line. queryVariableTypes uses the same
	// scan so this lookup is cheap and exhaustive.
	line, char, found := findSuppressionLine(vf.GoSource, target.Name)
	if !found {
		return nil
	}

	defParams := map[string]any{
		"textDocument": map[string]any{"uri": virtualURI},
		"position":     map[string]any{"line": line, "character": char},
	}
	raw, err := inst.gopls.Request("textDocument/definition", defParams)
	if err != nil || len(raw) == 0 {
		return nil
	}
	return json.RawMessage(proxy.RemapDefinitionResult(raw, s.virtualURIChecker(uri)))
}

// chainedFieldDefinition resolves go-to-definition for a sub-segment
// (or for a head segment inside a range/with scope). Builds the
// chain expression that resolves to the segment's parent value, then
// injects `_ = <chainExpr>.SegName` as a probe and asks gopls to
// resolve SegName.
//
// The probe is removed before returning regardless of outcome — the
// virtual file is restored to its original contents and the change
// is replayed to gopls so subsequent requests see consistent state.
func (s *server) chainedFieldDefinition(uri string, target *lsptemplate.HoverTarget, scope lsptemplate.ScopeInfo) any {
	inst := s.instanceForURI(uri)
	if inst == nil || inst.gopls == nil {
		return nil
	}

	prefixExpr := buildChainPrefixExpr(target, scope)
	if prefixExpr == "" {
		return nil
	}

	gastroPath := uriToPath(uri)
	relPath, err := filepath.Rel(inst.root, gastroPath)
	if err != nil {
		return nil
	}
	vf := inst.workspace.GetFile(relPath)
	if vf == nil {
		return nil
	}
	virtualPath := inst.workspace.VirtualFilePath(relPath)
	virtualURI := "file://" + virtualPath

	probeText := fmt.Sprintf("\t_ = %s.%s", prefixExpr, target.Name)
	probeLine, ok := insertProbe(vf.GoSource, probeText)
	if !ok {
		return nil
	}
	probeSource := applyInsertProbe(vf.GoSource, probeText, probeLine)

	if err := os.WriteFile(virtualPath, []byte(probeSource), 0o644); err != nil {
		return nil
	}
	defer s.restoreVirtualFile(virtualPath, vf, virtualURI, inst)

	version := inst.incGoplsOpenFileVersion(virtualURI)
	inst.gopls.Notify("textDocument/didChange", map[string]any{
		"textDocument":   map[string]any{"uri": virtualURI, "version": version},
		"contentChanges": []map[string]any{{"text": probeSource}},
	})

	// Cursor on the SegmentName ident in the probe line.
	nameStart := len("\t_ = ") + len(prefixExpr) + 1 // tab + "_ = " + prefix + "."
	defParams := map[string]any{
		"textDocument": map[string]any{"uri": virtualURI},
		"position":     map[string]any{"line": probeLine, "character": nameStart + 1}, // inside the name, not on the dot
	}
	raw, err := inst.gopls.Request("textDocument/definition", defParams)
	if err != nil || len(raw) == 0 {
		log.Printf("chain definition probe error for %q: %v", probeText, err)
		return nil
	}
	return json.RawMessage(proxy.RemapDefinitionResult(raw, s.virtualURIChecker(uri)))
}

// buildChainPrefixExpr returns the Go expression that evaluates to the
// value the cursor's segment is a field of. Mirrors the logic used in
// hoverFieldType so hover and definition agree on the synthesised
// expression — deviation between them would mean gopls returns a type
// for hover that doesn't match the field it would jump to on definition.
func buildChainPrefixExpr(target *lsptemplate.HoverTarget, scope lsptemplate.ScopeInfo) string {
	if target.ChainIdx == 0 {
		if scope.Depth > 0 && scope.RangeVar != "" {
			return scope.RangeVar + "[0]"
		}
		return ""
	}
	if scope.Depth == 0 {
		return strings.Join(target.Chain[:target.ChainIdx], ".")
	}
	if scope.RangeVar != "" {
		parts := append([]string{scope.RangeVar + "[0]"}, target.Chain[:target.ChainIdx]...)
		return strings.Join(parts, ".")
	}
	return strings.Join(target.Chain[:target.ChainIdx], ".")
}

// findSuppressionLine locates the `_ = name` line in a shadow Go
// source. Returns the 0-indexed line and the character offset of the
// name itself. Used by headFieldDefinition to position gopls'
// definition request on the variable's reference rather than its
// declaration site, which matches what users expect when invoking
// go-to-definition on a template variable.
func findSuppressionLine(source, name string) (line, char int, found bool) {
	prefix := "_ = " + name
	for i, ln := range strings.Split(source, "\n") {
		trim := strings.TrimSpace(ln)
		if trim != prefix {
			continue
		}
		off := strings.Index(ln, prefix)
		if off < 0 {
			continue
		}
		return i, off + len("_ = "), true
	}
	return 0, 0, false
}

// insertProbe returns the line index at which a probe assignment should
// be inserted in the shadow Go source so it sits in the same scope as
// the trailing block of `_ = VarName` suppression lines emitted by the
// codegen. The returned index is one past the LAST suppression line,
// pushing the probe immediately after the suppression block but ahead
// of any later code (BodyWritten check, __data construction, template
// execution) that the page-model and component-model handlers emit.
//
// Earlier versions of this function searched for a closing `}`
// immediately preceded by a `_ = ` line. That heuristic broke when the
// codegen started emitting more code between the suppression block and
// the function close — the anchor never matched, the probe silently
// returned `nil`, and chain-based go-to-definition / hover regressed
// for every page and component without any test catching it.
// codegen_lsp_contract_test.go locks in the contract this function
// depends on.
func insertProbe(source, _ string) (int, bool) {
	lines := strings.Split(source, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.HasPrefix(strings.TrimSpace(lines[i]), "_ = ") {
			return i + 1, true
		}
	}
	return 0, false
}

// applyInsertProbe returns a new source string with probeText inserted
// at the given line index. The original line at probeLine and below
// shift down by one.
func applyInsertProbe(source, probeText string, probeLine int) string {
	lines := strings.Split(source, "\n")
	out := make([]string, 0, len(lines)+1)
	out = append(out, lines[:probeLine]...)
	out = append(out, probeText)
	out = append(out, lines[probeLine:]...)
	return strings.Join(out, "\n")
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
