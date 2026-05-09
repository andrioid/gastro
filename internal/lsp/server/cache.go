package server

import (
	"github.com/andrioid/gastro/internal/codegen"
	lsptemplate "github.com/andrioid/gastro/internal/lsp/template"
)

// cacheEntry distinguishes "we have a real answer" from
// "we tried, the answer was nothing". Absence from the cache always
// means "we don't yet know". Positive is the common path; Negative
// is opted into per cache and documented at the declaration.
type cacheEntry[T any] struct {
	value    T
	negative bool // true when we cached a definitive empty answer
}

// HasValue reports whether the cache entry holds a positive result.
// When HasValue returns false, the entry represents a cached negative
// (e.g. "component has no Props struct"). Call Value to retrieve the
// positive value after checking HasValue.
func (e cacheEntry[T]) HasValue() bool { return !e.negative }

// Value returns the cached value. The caller should check HasValue first.
func (e cacheEntry[T]) Value() T { return e.value }

// ---------------------------------------------------------------------------
// Server-level cache helpers (protected by dataMu).
// These are safe to call without holding dataMu — they acquire it internally.
// Callers that already hold dataMu should access the map fields directly.
// ---------------------------------------------------------------------------

// getTypeCache returns the cached type map for a URI, or nil on miss.
func (s *server) getTypeCache(uri string) map[string]string {
	s.dataMu.RLock()
	defer s.dataMu.RUnlock()
	return s.typeCache[uri]
}

// setTypeCache stores a positive type result. Only call when len(types) > 0.
func (s *server) setTypeCache(uri string, types map[string]string) {
	s.dataMu.Lock()
	s.typeCache[uri] = types
	s.dataMu.Unlock()
}

// getFieldCacheEntry returns fields for a (URI, varName) pair, or nil + false.
func (s *server) getFieldCacheEntry(uri, varName string) ([]fieldInfo, bool) {
	s.dataMu.RLock()
	defer s.dataMu.RUnlock()
	if perURI, ok := s.fieldCache[uri]; ok {
		fields, ok := perURI[varName]
		return fields, ok
	}
	return nil, false
}

// setFieldCacheEntry stores fields for a (URI, varName) pair.
func (s *server) setFieldCacheEntry(uri, varName string, fields []fieldInfo) {
	s.dataMu.Lock()
	defer s.dataMu.Unlock()
	if s.fieldCache[uri] == nil {
		s.fieldCache[uri] = make(map[string][]fieldInfo)
	}
	s.fieldCache[uri][varName] = fields
}

// getTypeFieldCacheEntry returns cached field entries for a (URI, typeName), or nil + false.
func (s *server) getTypeFieldCacheEntry(uri, typeName string) ([]lsptemplate.FieldEntry, bool) {
	s.dataMu.RLock()
	defer s.dataMu.RUnlock()
	if perURI, ok := s.typeFieldCache[uri]; ok {
		entries, ok := perURI[typeName]
		return entries, ok
	}
	return nil, false
}

// setTypeFieldCacheEntry stores field entries for a (URI, typeName) pair.
func (s *server) setTypeFieldCacheEntry(uri, typeName string, entries []lsptemplate.FieldEntry) {
	s.dataMu.Lock()
	defer s.dataMu.Unlock()
	if s.typeFieldCache[uri] == nil {
		s.typeFieldCache[uri] = make(map[string][]lsptemplate.FieldEntry)
	}
	s.typeFieldCache[uri][typeName] = entries
}

// ---------------------------------------------------------------------------
// Instance-level cache helpers (protected by inst.mu).
// These are safe to call without holding inst.mu — they acquire it internally.
// Callers that already hold inst.mu should access the map fields directly.
// ---------------------------------------------------------------------------

// getComponentPropsCacheEntry returns cached component props for a path, or the
// zero value + false.
func (inst *projectInstance) getComponentPropsCacheEntry(path string) (cacheEntry[[]codegen.StructField], bool) {
	inst.mu.RLock()
	defer inst.mu.RUnlock()
	v, ok := inst.componentPropsCache[path]
	return v, ok
}

// setComponentPropsCacheEntry stores component props (positive or negative) for a path.
func (inst *projectInstance) setComponentPropsCacheEntry(path string, entry cacheEntry[[]codegen.StructField]) {
	inst.mu.Lock()
	defer inst.mu.Unlock()
	inst.componentPropsCache[path] = entry
}

// deleteComponentPropsCacheEntry removes cached props for a path.
func (inst *projectInstance) deleteComponentPropsCacheEntry(path string) {
	inst.mu.Lock()
	defer inst.mu.Unlock()
	delete(inst.componentPropsCache, path)
}

// getGoplsOpenFileVersion returns the current version for a virtual URI, or 0 + false.
func (inst *projectInstance) getGoplsOpenFileVersion(virtualURI string) (int, bool) {
	inst.mu.RLock()
	defer inst.mu.RUnlock()
	v, ok := inst.goplsOpenFiles[virtualURI]
	return v, ok
}

// setGoplsOpenFileVersion sets the version for a virtual URI.
func (inst *projectInstance) setGoplsOpenFileVersion(virtualURI string, version int) {
	inst.mu.Lock()
	defer inst.mu.Unlock()
	inst.goplsOpenFiles[virtualURI] = version
}

// incGoplsOpenFileVersion atomically increments the version for a virtual URI
// and returns the new value. Creates the entry with version 1 if absent.
func (inst *projectInstance) incGoplsOpenFileVersion(virtualURI string) int {
	inst.mu.Lock()
	defer inst.mu.Unlock()
	inst.goplsOpenFiles[virtualURI]++
	return inst.goplsOpenFiles[virtualURI]
}

// isGoplsReady returns true if gopls has sent its first publishDiagnostics.
func (inst *projectInstance) isGoplsReady() bool {
	inst.mu.RLock()
	defer inst.mu.RUnlock()
	return inst.goplsReady
}

// setGoplsReady marks gopls as ready (first publishDiagnostics received).
func (inst *projectInstance) setGoplsReady() {
	inst.mu.Lock()
	defer inst.mu.Unlock()
	inst.goplsReady = true
}
