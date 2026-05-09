package server

import (
	"encoding/json"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"time"

	"github.com/andrioid/gastro/internal/codegen"
	"github.com/andrioid/gastro/internal/lsp/proxy"
	lsptemplate "github.com/andrioid/gastro/internal/lsp/template"
	"github.com/andrioid/gastro/internal/parser"
)

// LSP DiagnosticSeverity values
// https://microsoft.github.io/language-server-protocol/specifications/lsp/3.17/specification/#diagnosticSeverity
const (
	diagnosticSeverityError   = 1
	diagnosticSeverityWarning = 2
)

// Stale-diagnostic retry tuning. The walker depends on gopls type / field
// info to validate range/with field accesses. On a cold workspace gopls
// can take a few seconds to load the virtual package, during which probes
// silently return empty. We retry a small bounded number of times with a
// short delay so the diagnostics catch up once gopls warms up, but cap the
// loop so a genuinely-empty type or a broken gopls install can't spin
// forever.
const (
	templateDiagsMaxRetries = 5
	templateDiagsRetryDelay = 250 * time.Millisecond
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
	sawEmptyResolve := false
	if inst != nil && inst.gopls != nil {
		resolver = func(typeName string, chainExpr string) []lsptemplate.FieldEntry {
			res := s.resolveFieldsViaChain(uri, typeName, chainExpr)
			if len(res) == 0 {
				sawEmptyResolve = true
			}
			return res
		}
	}

	// Resolve component Props structs for prop validation
	propsMap := s.resolveAllComponentProps(parsed.Uses, inst)

	templateDiags := lsptemplate.Diagnose(parsed.TemplateBody, info, parsed.Uses, types, resolver, propsMap)

	// Convert to LSP diagnostic format, offsetting positions by the template body start line.
	// TemplateBodyLine is 1-indexed; LSP positions are 0-indexed.
	bodyLineOffset := parsed.TemplateBodyLine - 1
	lspDiags := make([]map[string]any, 0, len(templateDiags)+len(info.Warnings))

	// Surface frontmatter warnings (e.g. bare gastro.Props()) as LSP diagnostics.
	// Warning.Line is 1-indexed within the original frontmatter.
	// FrontmatterLine is the 1-indexed line in the file where frontmatter content starts.
	fmLineOffset := parsed.FrontmatterLine - 1
	for _, w := range info.Warnings {
		warnLine := fmLineOffset + w.Line - 1
		lines := strings.Split(content, "\n")
		lineLen := 0
		if warnLine < len(lines) {
			lineLen = len(lines[warnLine])
		}
		lspDiags = append(lspDiags, map[string]any{
			"range": map[string]any{
				"start": map[string]any{"line": warnLine, "character": 0},
				"end":   map[string]any{"line": warnLine, "character": lineLen},
			},
			"severity": diagnosticSeverityWarning,
			"message":  fmt.Sprintf("warning: %s", w.Message),
			"source":   "gastro",
		})
	}

	for _, d := range templateDiags {
		severity := d.Severity
		if severity == 0 {
			severity = diagnosticSeverityError
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

	// Mark the URI stale until we're confident gopls produced real results.
	// Two windows can cause silent under-reporting in range/with field
	// validation:
	//   1. queryVariableTypes returns an empty typeMap because gopls hasn't
	//      loaded the virtual file yet — the walker has no type info to act
	//      on at all.
	//   2. typeMap is populated but the field-resolver probes return empty
	//      because gopls is still building completion data for the package.
	// In both cases the walker silently skips field validation. The fix is
	// to flag the URI stale and trigger a re-run; sawEmptyResolve covers
	// the second window even after goplsReady flipped to true mid-run.
	hadExports := len(info.ExportedVars) > 0

	s.dataMu.Lock()
	goplsNowReady := inst != nil && inst.goplsReady
	stale := (!goplsNowReady && hadExports) || sawEmptyResolve
	s.templateDiags[uri] = lspDiags
	if stale {
		s.templateDiagsStale[uri] = true
	} else {
		delete(s.templateDiagsStale, uri)
		delete(s.templateDiagsRetries, uri)
	}
	s.publishMergedDiagnostics(uri)

	// If we set stale AFTER gopls already flipped to ready (a race where
	// handleGoplsNotification ran during the walker, before this critical
	// section), the per-instance ready transition won't fire again to drive
	// a re-run. Schedule one ourselves, capped by templateDiagsMaxRetries
	// so a broken probe response can't loop us forever.
	selfRetry := stale && goplsNowReady && s.templateDiagsRetries[uri] < templateDiagsMaxRetries
	if selfRetry {
		s.templateDiagsRetries[uri]++
		delete(s.templateDiagsStale, uri) // consume so handleGoplsNotification doesn't double-trigger
	}
	s.dataMu.Unlock()

	if selfRetry {
		go func() {
			time.Sleep(templateDiagsRetryDelay)
			s.dataMu.RLock()
			doc := s.documents[uri]
			s.dataMu.RUnlock()
			if doc != "" {
				s.runTemplateDiagnostics(uri, doc)
			}
		}()
	}
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

	// First publishDiagnostics for this instance signals gopls has finished
	// its initial analysis pass. After that, probe queries will return real
	// results, so any template diagnostics that were computed in the
	// warm-up window need to be re-run. We schedule re-runs on goroutines
	// so we don't recurse under dataMu, and consume templateDiagsStale
	// atomically so probe-induced publishes during the re-run don't queue
	// duplicate work. The retry counter is bounded by templateDiagsMaxRetries
	// so a broken probe can't loop us forever.
	if !inst.goplsReady {
		inst.goplsReady = true
		for staleURI := range s.templateDiagsStale {
			content := s.documents[staleURI]
			if content == "" || s.templateDiagsRetries[staleURI] >= templateDiagsMaxRetries {
				continue
			}
			delete(s.templateDiagsStale, staleURI)
			s.templateDiagsRetries[staleURI]++
			go s.runTemplateDiagnostics(staleURI, content)
		}
		return
	}

	if s.templateDiagsStale[gastroURI] && s.templateDiagsRetries[gastroURI] < templateDiagsMaxRetries {
		delete(s.templateDiagsStale, gastroURI)
		s.templateDiagsRetries[gastroURI]++
		content := s.documents[gastroURI]
		if content != "" {
			go s.runTemplateDiagnostics(gastroURI, content)
		}
	}
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
