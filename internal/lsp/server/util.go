package server

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/andrioid/gastro/internal/codegen"
	"github.com/andrioid/gastro/internal/lsp/proxy"
	"github.com/andrioid/gastro/internal/lsp/shadow"
	lsptemplate "github.com/andrioid/gastro/internal/lsp/template"
)

// uriToPath converts a file:// URI to a local filesystem path.
func uriToPath(uri string) string {
	parsed, err := url.Parse(uri)
	if err != nil {
		return strings.TrimPrefix(uri, "file://")
	}
	return parsed.Path
}

// findDotStart scans backward from the cursor on the current line to find
// the position of the '.' that starts a variable reference (e.g. in "{{ .T").
// Returns the character offset of the dot, or -1 if no dot is found.
func findDotStart(content string, line, character int) int {
	lines := strings.Split(content, "\n")
	if line < 0 || line >= len(lines) {
		return -1
	}
	lineText := lines[line]
	// Scan backward from cursor position to find a '.'
	for i := character - 1; i >= 0; i-- {
		ch := lineText[i]
		if ch == '.' {
			return i
		}
		// Stop if we hit a character that can't be part of a variable reference
		if !((ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9')) {
			return -1
		}
	}
	return -1
}

// isInsideAction checks whether the cursor (byte offset into the template body)
// is between an opening {{ and a closing }}. It scans backward from the cursor
// to find the most recent delimiter pair and returns true only if the last
// seen delimiter was an opening {{.
func isInsideAction(templateBody string, cursorOffset int) bool {
	if cursorOffset < 0 || cursorOffset > len(templateBody) {
		return false
	}

	text := templateBody[:cursorOffset]
	lastOpen := strings.LastIndex(text, "{{")
	lastClose := strings.LastIndex(text, "}}")

	return lastOpen >= 0 && lastOpen > lastClose
}

// cursorPosToBodyOffset converts a cursor position (line/character in the .gastro file)
// to a byte offset within the template body string.
func cursorPosToBodyOffset(content string, pos proxy.Position, templateBodyLine int) int {
	// templateBodyLine is 1-indexed; pos.Line is 0-indexed
	bodyStartLine := templateBodyLine - 1
	if pos.Line < bodyStartLine {
		return -1
	}

	lines := strings.Split(content, "\n")
	offset := 0
	for i := bodyStartLine; i < pos.Line && i < len(lines); i++ {
		offset += len(lines[i]) + 1 // +1 for newline
	}
	offset += pos.Character
	return offset
}

// findProjectRoot walks up from filePath to find the nearest directory
// containing go.mod. Returns fallback if no go.mod is found.
func findProjectRoot(filePath, fallback string) string {
	resolved, err := filepath.EvalSymlinks(filePath)
	if err != nil {
		resolved = filePath
	}
	dir := filepath.Dir(resolved)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return fallback
		}
		dir = parent
	}
}

// lookupInstanceLocked returns the project instance for a URI without
// acquiring any locks. The caller must already hold s.dataMu (read or write).
func (s *server) lookupInstanceLocked(uri string) *projectInstance {
	filePath := uriToPath(uri)
	if filePath == "" {
		return nil
	}
	root := findProjectRoot(filePath, s.projectDir)
	return s.instances[root]
}

// instanceForURI returns the project instance for a given file URI, creating
// one lazily if needed. Initialization (I/O, subprocess spawning) happens
// outside the lock to avoid blocking concurrent readers.
func (s *server) instanceForURI(uri string) *projectInstance {
	filePath := uriToPath(uri)
	if filePath == "" {
		return nil
	}
	root := findProjectRoot(filePath, s.projectDir)

	// Fast path: read lock
	s.dataMu.RLock()
	inst, ok := s.instances[root]
	s.dataMu.RUnlock()
	if ok {
		return inst
	}

	// Create and initialize outside the lock (I/O happens here)
	newInst := &projectInstance{
		root:                root,
		componentPropsCache: make(map[string][]codegen.StructField),
		goplsOpenFiles:      make(map[string]int),
	}
	goplsErr := s.initInstance(newInst)

	// Acquire write lock to store the instance
	s.dataMu.Lock()
	// Double-check: another goroutine may have created it while we were initializing
	if existing, ok := s.instances[root]; ok {
		s.dataMu.Unlock()
		// Clean up our duplicate instance
		if newInst.gopls != nil {
			newInst.gopls.Close()
		}
		if newInst.workspace != nil {
			newInst.workspace.Close()
		}
		return existing
	}
	s.instances[root] = newInst
	s.dataMu.Unlock()

	// Notify the editor about gopls failure (outside all locks)
	if goplsErr != nil {
		log.Printf("instance for %s: %v", root, goplsErr)
		s.notifyGoplsUnavailable(goplsErr)
	}

	return newInst
}

// initInstance creates the shadow workspace, starts gopls, and discovers
// components for a project instance. Gopls failure is non-fatal: the instance
// remains usable for template features even without Go intelligence.
// Returns a non-nil error only to signal gopls unavailability (not a hard failure).
func (s *server) initInstance(inst *projectInstance) error {
	var err error
	inst.workspace, err = shadow.NewWorkspace(inst.root)
	if err != nil {
		return fmt.Errorf("creating shadow workspace: %w", err)
	}

	inst.components = discoverComponentsIn(inst.root)
	inst.componentsScannedAt = time.Now()

	inst.gopls, err = proxy.NewGoplsProxy(inst.workspace.Dir(), func(method string, params json.RawMessage) {
		s.handleGoplsNotification(method, params, inst)
	})
	if err != nil {
		// Gopls unavailable — keep workspace alive for template features
		inst.gopls = nil
		inst.goplsError = fmt.Errorf("gopls unavailable: %w", err)
		log.Printf("initialized instance for %s (%d components, gopls: unavailable)", inst.root, len(inst.components))
		return inst.goplsError
	}

	log.Printf("initialized instance for %s (%d components, gopls: ok)", inst.root, len(inst.components))
	return nil
}

// getComponents returns discovered components, re-scanning the components/
// directory if the cache is older than 2 seconds. This ensures newly created
// component files are picked up without requiring an LSP restart.
func (inst *projectInstance) getComponents() []componentInfo {
	const cacheTTL = 2 * time.Second

	inst.componentsMu.RLock()
	if time.Since(inst.componentsScannedAt) < cacheTTL {
		components := inst.components
		inst.componentsMu.RUnlock()
		return components
	}
	inst.componentsMu.RUnlock()

	inst.componentsMu.Lock()
	defer inst.componentsMu.Unlock()

	// Double-check: another goroutine may have refreshed while we waited
	if time.Since(inst.componentsScannedAt) < cacheTTL {
		return inst.components
	}

	inst.components = discoverComponentsIn(inst.root)
	inst.componentsScannedAt = time.Now()
	return inst.components
}

// discoverComponentsIn recursively scans the components/ directory under a
// project root for .gastro files. This enables auto-import completions when
// the user types a component name for a component that isn't yet imported.
func discoverComponentsIn(projectRoot string) []componentInfo {
	componentsDir := filepath.Join(projectRoot, "components")
	var components []componentInfo

	filepath.WalkDir(componentsDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") && path != componentsDir {
				return filepath.SkipDir
			}
			return nil
		}

		// Skip symlinks to avoid infinite loops
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}

		if !strings.HasSuffix(d.Name(), ".gastro") {
			return nil
		}

		relPath, err := filepath.Rel(projectRoot, path)
		if err != nil {
			return nil
		}

		// Convert kebab-case filename to PascalCase component name
		name := strings.TrimSuffix(d.Name(), ".gastro")
		parts := strings.Split(name, "-")
		var pascal strings.Builder
		for _, part := range parts {
			if part == "" {
				continue
			}
			pascal.WriteString(strings.ToUpper(part[:1]) + part[1:])
		}

		components = append(components, componentInfo{
			Name: pascal.String(),
			Path: relPath,
		})

		return nil
	})

	return components
}

// elementTypeFromContainer is a convenience alias for the template package function.
// Kept for backward compatibility with existing callers in this file.
func elementTypeFromContainer(typeStr string) string {
	return lsptemplate.ElementTypeFromContainer(typeStr)
}
