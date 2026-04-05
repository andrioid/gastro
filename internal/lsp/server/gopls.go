package server

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/andrioid/gastro/internal/lsp/proxy"
	"github.com/andrioid/gastro/internal/lsp/shadow"
	lsptemplate "github.com/andrioid/gastro/internal/lsp/template"
)

// syncToGopls updates the virtual .go file in the shadow workspace and
// notifies gopls about the change. Sends didOpen on first sync for a file,
// didChange on subsequent syncs.
func (s *server) syncToGopls(gastroURI, content string) {
	inst := s.instanceForURI(gastroURI)
	if inst == nil || inst.gopls == nil {
		return
	}

	// Convert URI to relative path for the workspace
	gastroPath := uriToPath(gastroURI)
	relPath, err := filepath.Rel(inst.root, gastroPath)
	if err != nil {
		log.Printf("cannot compute relative path: %v", err)
		return
	}

	vf, err := inst.workspace.UpdateFile(relPath, content)
	if err != nil {
		log.Printf("updating virtual file: %v", err)
		return
	}

	virtualPath := inst.workspace.VirtualFilePath(relPath)
	virtualURI := "file://" + virtualPath
	log.Printf("syncToGopls: gastro=%s virtual=%s", relPath, virtualURI)

	version, alreadyOpen := inst.goplsOpenFiles[virtualURI]
	if !alreadyOpen {
		// First time: send didOpen
		version = 1
		inst.goplsOpenFiles[virtualURI] = version
		if err := inst.gopls.Notify("textDocument/didOpen", map[string]any{
			"textDocument": map[string]any{
				"uri":        virtualURI,
				"languageId": "go",
				"version":    version,
				"text":       vf.GoSource,
			},
		}); err != nil {
			log.Printf("gopls didOpen error: %v", err)
		}
	} else {
		// Subsequent: send didChange with incremented version
		version++
		inst.goplsOpenFiles[virtualURI] = version
		if err := inst.gopls.Notify("textDocument/didChange", map[string]any{
			"textDocument": map[string]any{
				"uri":     virtualURI,
				"version": version,
			},
			"contentChanges": []map[string]any{
				{"text": vf.GoSource},
			},
		}); err != nil {
			log.Printf("gopls didChange error: %v", err)
		}
	}
}

// forwardToGopls sends a request to gopls with mapped positions and returns
// the result with positions mapped back.
func (s *server) forwardToGopls(gastroURI, method string, pos proxy.Position) any {
	inst := s.instanceForURI(gastroURI)
	if inst == nil || inst.gopls == nil {
		return nil
	}

	gastroPath := uriToPath(gastroURI)
	relPath, err := filepath.Rel(inst.root, gastroPath)
	if err != nil {
		return nil
	}

	vf := inst.workspace.GetFile(relPath)
	if vf == nil {
		return nil
	}

	// Map position to virtual file coordinates
	virtualPos := proxy.MapPositionToVirtual(pos, vf.SourceMap)
	log.Printf("forwardToGopls: %s gastro pos=%+v -> virtual pos=%+v", method, pos, virtualPos)
	virtualPath := inst.workspace.VirtualFilePath(relPath)
	virtualURI := "file://" + virtualPath

	goplsParams := map[string]any{
		"textDocument": map[string]any{
			"uri": virtualURI,
		},
		"position": virtualPos,
	}

	result, err := inst.gopls.Request(method, goplsParams)
	if err != nil {
		log.Printf("gopls %s error: %v", method, err)
		return nil
	}

	// Position remapping is handled by each caller (handleCompletion,
	// handleHover, handleDefinition) since the response formats differ.
	return json.RawMessage(result)
}

// queryVariableTypes queries gopls for the types of exported frontmatter
// variables by sending textDocument/hover requests on the `_ = VarName`
// suppression lines in the virtual file. Returns a cached map of varName to
// type string (e.g. "[]db.Post", "string"). Results are cached per URI and
// invalidated on document changes.
func (s *server) queryVariableTypes(gastroURI string) map[string]string {
	if cached, ok := s.typeCache[gastroURI]; ok {
		return cached
	}

	inst := s.instanceForURI(gastroURI)
	if inst == nil || inst.gopls == nil {
		return nil
	}

	gastroPath := uriToPath(gastroURI)
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

	types := make(map[string]string)

	// Find `_ = VarName` lines in the virtual source and hover on VarName
	for lineIdx, line := range strings.Split(vf.GoSource, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "_ = ") {
			continue
		}
		varName := strings.TrimPrefix(trimmed, "_ = ")
		if varName == "" {
			continue
		}

		// Position the cursor on the variable name (after "_ = ")
		charOffset := strings.Index(line, "_ = ") + 4
		hoverParams := map[string]any{
			"textDocument": map[string]any{"uri": virtualURI},
			"position":     map[string]any{"line": lineIdx, "character": charOffset},
		}

		result, err := inst.gopls.Request("textDocument/hover", hoverParams)
		if err != nil {
			log.Printf("gopls hover error for %s: %v", varName, err)
			continue
		}

		typeStr := parseTypeFromHover(result)
		if typeStr != "" {
			types[varName] = typeStr
			log.Printf("type for %s: %s", varName, typeStr)
		}
	}

	s.typeCache[gastroURI] = types
	return types
}

// parseTypeFromHover extracts the type string from a gopls hover response.
// gopls returns hover contents as markdown with the type on the first code line,
// typically formatted like: ```go\nvar VarName TypeName\n```
// or just the type expression in a code block.
func parseTypeFromHover(raw json.RawMessage) string {
	var hover struct {
		Contents struct {
			Kind  string `json:"kind"`
			Value string `json:"value"`
		} `json:"contents"`
	}
	if err := json.Unmarshal(raw, &hover); err != nil {
		return ""
	}

	value := hover.Contents.Value
	if value == "" {
		return ""
	}

	// gopls hover format: ```go\nvar name type\n``` or ```go\nname type\n```
	// Extract lines from the code block
	for _, line := range strings.Split(value, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line == "```go" || line == "```" {
			continue
		}
		// "var Posts []db.Post" → extract type after the name
		if strings.HasPrefix(line, "var ") {
			parts := strings.Fields(line)
			if len(parts) >= 3 {
				return strings.Join(parts[2:], " ")
			}
		}
		// "Posts []db.Post" → extract type after the name
		parts := strings.Fields(line)
		if len(parts) >= 2 {
			return strings.Join(parts[1:], " ")
		}
	}

	return ""
}

// queryFieldsFromGopls queries gopls for fields of the given type by
// temporarily injecting a probe line into the virtual file.
func (s *server) queryFieldsFromGopls(gastroURI, varName, typeName string) []fieldInfo {
	inst := s.instanceForURI(gastroURI)
	if inst == nil || inst.gopls == nil {
		return nil
	}

	gastroPath := uriToPath(gastroURI)
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

	// Find the `_ = VarName` line and inject a probe line after it.
	// The probe accesses an element and triggers completion on its fields.
	goLines := strings.Split(vf.GoSource, "\n")
	probeLine := -1
	probeText := fmt.Sprintf("\t_ = %s[0].", varName)

	for i, line := range goLines {
		if strings.TrimSpace(line) == "_ = "+varName {
			probeLine = i + 1
			break
		}
	}

	if probeLine < 0 {
		return nil
	}

	// Inject probe line
	newLines := make([]string, 0, len(goLines)+1)
	newLines = append(newLines, goLines[:probeLine]...)
	newLines = append(newLines, probeText)
	newLines = append(newLines, goLines[probeLine:]...)
	probeSource := strings.Join(newLines, "\n")

	// Write the modified virtual file
	if err := os.WriteFile(virtualPath, []byte(probeSource), 0o644); err != nil {
		return nil
	}

	// Sync the change to gopls
	version := inst.goplsOpenFiles[virtualURI] + 1
	inst.goplsOpenFiles[virtualURI] = version
	inst.gopls.Notify("textDocument/didChange", map[string]any{
		"textDocument": map[string]any{
			"uri":     virtualURI,
			"version": version,
		},
		"contentChanges": []map[string]any{
			{"text": probeSource},
		},
	})

	// Request completions at the dot position on the probe line
	completionParams := map[string]any{
		"textDocument": map[string]any{"uri": virtualURI},
		"position":     map[string]any{"line": probeLine, "character": len(probeText)},
	}

	result, err := inst.gopls.Request("textDocument/completion", completionParams)
	if err != nil {
		log.Printf("gopls completion for fields error: %v", err)
		s.restoreVirtualFile(virtualPath, vf, virtualURI, inst)
		return nil
	}

	// Parse the completion response
	fields := parseFieldCompletions(result)

	// Restore the original virtual file
	s.restoreVirtualFile(virtualPath, vf, virtualURI, inst)

	return fields
}

// probeFieldsViaChain queries gopls for fields by injecting a probe line
// with the given chain expression (e.g. "Posts[0]" or "Posts[0].Comments[0]").
func (s *server) probeFieldsViaChain(uri, chainExpr string, inst *projectInstance) []fieldInfo {
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

	goLines := strings.Split(vf.GoSource, "\n")
	probeLine := -1
	probeText := fmt.Sprintf("\t_ = %s.", chainExpr)

	// Find the closing brace of the handler function to insert before it
	for i, line := range goLines {
		if strings.TrimSpace(line) == "}" && i > 0 {
			// Check if previous line is a "_ = VarName" suppression line
			prev := strings.TrimSpace(goLines[i-1])
			if strings.HasPrefix(prev, "_ = ") {
				probeLine = i
				break
			}
		}
	}

	if probeLine < 0 {
		return nil
	}

	newLines := make([]string, 0, len(goLines)+1)
	newLines = append(newLines, goLines[:probeLine]...)
	newLines = append(newLines, probeText)
	newLines = append(newLines, goLines[probeLine:]...)
	probeSource := strings.Join(newLines, "\n")

	if err := os.WriteFile(virtualPath, []byte(probeSource), 0o644); err != nil {
		return nil
	}

	version := inst.goplsOpenFiles[virtualURI] + 1
	inst.goplsOpenFiles[virtualURI] = version
	inst.gopls.Notify("textDocument/didChange", map[string]any{
		"textDocument": map[string]any{
			"uri":     virtualURI,
			"version": version,
		},
		"contentChanges": []map[string]any{
			{"text": probeSource},
		},
	})

	completionParams := map[string]any{
		"textDocument": map[string]any{"uri": virtualURI},
		"position":     map[string]any{"line": probeLine, "character": len(probeText)},
	}

	result, err := inst.gopls.Request("textDocument/completion", completionParams)
	if err != nil {
		log.Printf("gopls completion for chain probe error: %v", err)
		s.restoreVirtualFile(virtualPath, vf, virtualURI, inst)
		return nil
	}

	fields := parseFieldCompletions(result)
	s.restoreVirtualFile(virtualPath, vf, virtualURI, inst)
	return fields
}

// restoreVirtualFile writes back the original virtual file content and syncs to gopls.
func (s *server) restoreVirtualFile(virtualPath string, vf *shadow.VirtualFile, virtualURI string, inst *projectInstance) {
	os.WriteFile(virtualPath, []byte(vf.GoSource), 0o644)
	version := inst.goplsOpenFiles[virtualURI] + 1
	inst.goplsOpenFiles[virtualURI] = version
	inst.gopls.Notify("textDocument/didChange", map[string]any{
		"textDocument": map[string]any{
			"uri":     virtualURI,
			"version": version,
		},
		"contentChanges": []map[string]any{
			{"text": vf.GoSource},
		},
	})
}

// parseFieldCompletions extracts field names and types from a gopls
// completion response.
func parseFieldCompletions(raw json.RawMessage) []fieldInfo {
	// gopls returns either {items: [...]} or [...] directly
	var response struct {
		Items []struct {
			Label  string `json:"label"`
			Detail string `json:"detail"`
			Kind   int    `json:"kind"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		// Try as plain array
		var items []struct {
			Label  string `json:"label"`
			Detail string `json:"detail"`
			Kind   int    `json:"kind"`
		}
		if err := json.Unmarshal(raw, &items); err != nil {
			return nil
		}
		for _, item := range items {
			response.Items = append(response.Items, item)
		}
	}

	var fields []fieldInfo
	for _, item := range response.Items {
		// Kind 5 = Field, Kind 2 = Method — include both
		if item.Kind == 5 || item.Kind == 2 {
			fields = append(fields, fieldInfo{
				Label:  item.Label,
				Detail: item.Detail,
			})
		}
	}

	return fields
}

// getCachedFields returns the field list for a variable, using the cache or
// querying gopls on a cache miss.
func (s *server) getCachedFields(uri, varName string) []fieldInfo {
	if perURI, ok := s.fieldCache[uri]; ok {
		if fields, ok := perURI[varName]; ok {
			return fields
		}
	}

	types := s.queryVariableTypes(uri)
	if types == nil {
		return nil
	}
	typeStr, ok := types[varName]
	if !ok {
		return nil
	}

	elemType := elementTypeFromContainer(typeStr)
	if elemType == "" {
		elemType = typeStr
	}
	queryType := strings.TrimPrefix(elemType, "*")

	fields := s.queryFieldsFromGopls(uri, varName, queryType)
	if fields == nil {
		return nil
	}

	if s.fieldCache[uri] == nil {
		s.fieldCache[uri] = make(map[string][]fieldInfo)
	}
	s.fieldCache[uri][varName] = fields
	return fields
}

// resolveFieldsViaChain resolves a type's fields by probing gopls with
// a chain expression. Results are cached per URI + type name.
func (s *server) resolveFieldsViaChain(uri, typeName, chainExpr string) []lsptemplate.FieldEntry {
	// Check type-keyed cache
	if perURI, ok := s.typeFieldCache[uri]; ok {
		if entries, ok := perURI[typeName]; ok {
			return entries
		}
	}

	rfInst := s.instanceForURI(uri)
	if rfInst == nil || rfInst.gopls == nil || chainExpr == "" {
		return nil
	}

	fields := s.probeFieldsViaChain(uri, chainExpr, rfInst)
	if fields == nil {
		return nil
	}

	entries := make([]lsptemplate.FieldEntry, len(fields))
	for i, f := range fields {
		entries[i] = lsptemplate.FieldEntry{Name: f.Label, Type: f.Detail}
	}

	if s.typeFieldCache[uri] == nil {
		s.typeFieldCache[uri] = make(map[string][]lsptemplate.FieldEntry)
	}
	s.typeFieldCache[uri][typeName] = entries
	return entries
}
