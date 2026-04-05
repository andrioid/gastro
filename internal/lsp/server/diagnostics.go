package server

import (
	"encoding/json"
	"log"
	"path/filepath"
	"strings"

	"github.com/andrioid/gastro/internal/codegen"
	"github.com/andrioid/gastro/internal/lsp/proxy"
	lsptemplate "github.com/andrioid/gastro/internal/lsp/template"
	"github.com/andrioid/gastro/internal/parser"
)

// runTemplateDiagnostics parses the document, runs template-body diagnostics
// (unknown variables, invalid syntax, unknown components), caches the results,
// and publishes the merged diagnostic set.
func (s *server) runTemplateDiagnostics(uri, content string) {
	parsed, err := parser.Parse("virtual.gastro", content)
	if err != nil {
		s.publishParseDiagnostic(uri, content, err.Error(), findDelimiterLine(content))
		return
	}

	info, err := codegen.AnalyzeFrontmatter(parsed.Frontmatter)
	if err != nil {
		// Position on the frontmatter start line (1-indexed to 0-indexed)
		errLine := 0
		if parsed.FrontmatterLine > 0 {
			errLine = parsed.FrontmatterLine - 1
		}
		s.publishParseDiagnostic(uri, content, err.Error(), errLine)
		return
	}

	// Build type map and field resolver for scope-aware diagnostics.
	// These may be nil if gopls is not available yet.
	types := s.queryVariableTypes(uri)
	inst := s.instanceForURI(uri)
	var resolver lsptemplate.FieldResolver
	if inst != nil && inst.gopls != nil {
		resolver = func(typeName string, chainExpr string) []lsptemplate.FieldEntry {
			return s.resolveFieldsViaChain(uri, typeName, chainExpr)
		}
	}

	// Resolve component Props structs for prop validation
	propsMap := s.resolveAllComponentProps(parsed.Uses, inst)

	templateDiags := lsptemplate.Diagnose(parsed.TemplateBody, info, parsed.Uses, types, resolver, propsMap)

	// Convert to LSP diagnostic format, offsetting positions by the template body start line.
	// TemplateBodyLine is 1-indexed; LSP positions are 0-indexed.
	bodyLineOffset := parsed.TemplateBodyLine - 1
	lspDiags := make([]map[string]any, 0, len(templateDiags))
	for _, d := range templateDiags {
		severity := d.Severity
		if severity == 0 {
			severity = 1 // Default to Error
		}
		lspDiags = append(lspDiags, map[string]any{
			"range": map[string]any{
				"start": map[string]any{"line": d.StartLine + bodyLineOffset, "character": d.StartChar},
				"end":   map[string]any{"line": d.EndLine + bodyLineOffset, "character": d.EndChar},
			},
			"severity": severity,
			"message":  d.Message,
			"source":   "gastro",
		})
	}

	s.dataMu.Lock()
	s.templateDiags[uri] = lspDiags
	s.publishMergedDiagnostics(uri)
	s.dataMu.Unlock()
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

// publishParseDiagnostic surfaces a parse-level error as an LSP diagnostic.
// It highlights the given line (0-indexed) and publishes the diagnostic.
// The caller must NOT hold s.dataMu.
func (s *server) publishParseDiagnostic(uri, content, message string, line int) {
	lineLen := 0
	if lines := strings.SplitN(content, "\n", line+2); line < len(lines) {
		lineLen = len(lines[line])
	}

	s.dataMu.Lock()
	s.templateDiags[uri] = []map[string]any{{
		"range": map[string]any{
			"start": map[string]any{"line": line, "character": 0},
			"end":   map[string]any{"line": line, "character": lineLen},
		},
		"severity": 1,
		"message":  message,
		"source":   "gastro",
	}}
	s.publishMergedDiagnostics(uri)
	s.dataMu.Unlock()
}

// findDelimiterLine returns the 0-indexed line number of the first --- delimiter
// in content, or 0 if not found.
func findDelimiterLine(content string) int {
	for i, line := range strings.Split(content, "\n") {
		if strings.TrimSpace(line) == "---" {
			return i
		}
	}
	return 0
}

// handleGoplsNotification processes async notifications from gopls
// (e.g., publishDiagnostics) and forwards them to the editor with mapped positions.
func (s *server) handleGoplsNotification(method string, params json.RawMessage, inst *projectInstance) {
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

	// Lock for the duration: we read documents, write goplsDiags, read
	// templateDiags (via publishMergedDiagnostics). This runs on the
	// gopls reader goroutine so it races with the main message loop.
	s.dataMu.Lock()
	defer s.dataMu.Unlock()

	// Find which .gastro file this virtual file corresponds to
	log.Printf("gopls diagnostic: uri=%s diags=%d", diagParams.URI, len(diagParams.Diagnostics))
	gastroURI := s.findGastroURIForVirtualURI(diagParams.URI, inst)
	if gastroURI == "" {
		log.Printf("gopls diagnostic: no matching .gastro file for %s", diagParams.URI)
		return
	}

	vf := s.findVirtualFileForURI(gastroURI, inst)
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

// resolveAllComponentProps builds a map from component name to its Props
// struct fields, using the cache where possible.
func (s *server) resolveAllComponentProps(uses []parser.UseDeclaration, inst *projectInstance) map[string][]codegen.StructField {
	if inst == nil || len(uses) == 0 {
		return nil
	}

	result := make(map[string][]codegen.StructField, len(uses))
	for _, u := range uses {
		if cached, ok := inst.componentPropsCache[u.Path]; ok {
			if cached != nil {
				result[u.Name] = cached
			}
			continue
		}

		fields, err := lsptemplate.ResolveComponentProps(inst.root, u.Path, s.documents)
		if err != nil {
			log.Printf("resolving component props for %s: %v", u.Path, err)
			continue
		}

		inst.componentPropsCache[u.Path] = fields
		if fields != nil {
			result[u.Name] = fields
		}
	}

	return result
}

// invalidateComponentPropsCache removes cached props for a component file
// when it changes. The caller must hold s.dataMu (read or write).
func (s *server) invalidateComponentPropsCache(uri string) {
	filePath := uriToPath(uri)
	if filePath == "" {
		return
	}
	inst := s.lookupInstanceLocked(uri)
	if inst == nil {
		return
	}
	relPath, err := filepath.Rel(inst.root, filePath)
	if err != nil {
		return
	}
	delete(inst.componentPropsCache, relPath)
}
