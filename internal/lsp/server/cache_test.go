package server

import (
	"testing"

	"github.com/andrioid/gastro/internal/codegen"
)

// TestTypeCache_DoesNotPersistTransientEmpty verifies that we never store an
// empty result in the type cache. The positive-only contract means absence
// always means "ask again".
func TestTypeCache_DoesNotPersistTransientEmpty(t *testing.T) {
	s := newServer("test")
	uri := "file:///test.gastro"

	// Cache starts empty
	if cached := s.getTypeCache(uri); cached != nil {
		t.Fatal("expected empty cache initially")
	}

	// Store a positive result
	s.setTypeCache(uri, map[string]string{"X": "string"})
	if cached := s.getTypeCache(uri); cached == nil || cached["X"] != "string" {
		t.Fatal("expected cached type after setTypeCache")
	}

	// A different URI with no stored value should remain empty
	uri2 := "file:///test2.gastro"
	if cached := s.getTypeCache(uri2); cached != nil {
		t.Fatal("expected empty cache for URI that was never stored")
	}

	// The original URI's entry should still be intact
	if cached := s.getTypeCache(uri); cached == nil || cached["X"] != "string" {
		t.Fatal("existing cache entry should survive after unrelated access")
	}

	// Delete the entry — cache miss after delete
	s.dataMu.Lock()
	delete(s.typeCache, uri)
	s.dataMu.Unlock()
	if cached := s.getTypeCache(uri); cached != nil {
		t.Fatal("expected empty cache after delete")
	}
}

// TestComponentPropsCache_NegativeEntryHonoured verifies that the component
// props cache correctly stores and retrieves both positive and negative entries,
// and that deletion clears them.
func TestComponentPropsCache_NegativeEntryHonoured(t *testing.T) {
	inst := &projectInstance{
		root:                "",
		componentPropsCache: make(map[string]cacheEntry[[]codegen.StructField]),
	}
	path := "a/b/c.gastro"

	// No entry yet — cache miss
	if _, ok := inst.getComponentPropsCacheEntry(path); ok {
		t.Fatal("expected cache miss initially")
	}

	// Store a negative entry (component has no Props struct)
	inst.setComponentPropsCacheEntry(path, cacheEntry[[]codegen.StructField]{negative: true})

	// Should hit cache with HasValue() == false
	entry, ok := inst.getComponentPropsCacheEntry(path)
	if !ok {
		t.Fatal("expected cache hit for negative entry")
	}
	if entry.HasValue() {
		t.Fatal("expected HasValue() == false for negative entry")
	}

	// Replace with a positive entry
	inst.setComponentPropsCacheEntry(path, cacheEntry[[]codegen.StructField]{
		value: []codegen.StructField{{Name: "Title", Type: "string"}},
	})

	// Should hit cache with HasValue() == true and correct value
	entry, ok = inst.getComponentPropsCacheEntry(path)
	if !ok {
		t.Fatal("expected cache hit for positive entry")
	}
	if !entry.HasValue() {
		t.Fatal("expected HasValue() == true for positive entry")
	}
	props := entry.Value()
	if len(props) != 1 || props[0].Name != "Title" || props[0].Type != "string" {
		t.Fatalf("unexpected props value: %+v", props)
	}

	// Delete the entry — cache miss after deletion
	inst.deleteComponentPropsCacheEntry(path)
	if _, ok := inst.getComponentPropsCacheEntry(path); ok {
		t.Fatal("expected cache miss after delete")
	}
}

// TestComponentPropsCache_NegativeToPositiveTransition verifies that a negative
// entry can be overwritten with a positive one (and vice versa).
func TestComponentPropsCache_NegativeToPositiveTransition(t *testing.T) {
	inst := &projectInstance{
		root:                "",
		componentPropsCache: make(map[string]cacheEntry[[]codegen.StructField]),
	}
	path := "components/card.gastro"

	// Start negative
	inst.setComponentPropsCacheEntry(path, cacheEntry[[]codegen.StructField]{negative: true})
	entry, _ := inst.getComponentPropsCacheEntry(path)
	if entry.HasValue() {
		t.Fatal("expected negative entry")
	}

	// Overwrite with positive
	inst.setComponentPropsCacheEntry(path, cacheEntry[[]codegen.StructField]{
		value: []codegen.StructField{{Name: "Body", Type: "template.HTML"}},
	})
	entry, _ = inst.getComponentPropsCacheEntry(path)
	if !entry.HasValue() {
		t.Fatal("expected positive entry after overwrite")
	}
	if len(entry.Value()) != 1 || entry.Value()[0].Name != "Body" {
		t.Fatal("unexpected props after transition")
	}

	// Overwrite back to negative
	inst.setComponentPropsCacheEntry(path, cacheEntry[[]codegen.StructField]{negative: true})
	entry, _ = inst.getComponentPropsCacheEntry(path)
	if entry.HasValue() {
		t.Fatal("expected negative entry after second overwrite")
	}
}
