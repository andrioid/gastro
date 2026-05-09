package server

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"github.com/andrioid/gastro/internal/codegen"
	lsptemplate "github.com/andrioid/gastro/internal/lsp/template"
)

// TestCacheConcurrentAccess exercises concurrent reads/writes/deletes on all
// shared maps that were previously unguarded. The test must pass under -race.
func TestCacheConcurrentAccess(t *testing.T) {
	tmpDir := t.TempDir()
	writeGoMod(t, tmpDir)

	s := newServer("test")
	s.projectDir = tmpDir

	inst := &projectInstance{
		root:                tmpDir,
		componentPropsCache: make(map[string]cacheEntry[[]codegen.StructField]),
		goplsOpenFiles:      make(map[string]int),
	}
	s.instances[tmpDir] = inst

	uri1 := "file://" + filepath.Join(tmpDir, "page1.gastro")
	uri2 := "file://" + filepath.Join(tmpDir, "page2.gastro")

	const N = 50
	const goroutinesPerIter = 17
	var wg sync.WaitGroup
	wg.Add(N * goroutinesPerIter)

	for i := 0; i < N; i++ {
		// typeCache: read and delete concurrently
		go func() {
			defer wg.Done()
			s.setTypeCache(uri1, map[string]string{"X": "string"})
		}()
		go func() {
			defer wg.Done()
			s.dataMu.Lock()
			delete(s.typeCache, uri1)
			s.dataMu.Unlock()
		}()
		go func() {
			defer wg.Done()
			_ = s.getTypeCache(uri1)
		}()

		// fieldCache and typeFieldCache
		go func() {
			defer wg.Done()
			s.setFieldCacheEntry(uri1, "X", []fieldInfo{{Label: "Name", Detail: "string"}})
		}()
		go func() {
			defer wg.Done()
			_, _ = s.getFieldCacheEntry(uri1, "X")
		}()
		go func() {
			defer wg.Done()
			s.setTypeFieldCacheEntry(uri1, "T", []lsptemplate.FieldEntry{{Name: "Name", Type: "string"}})
		}()
		go func() {
			defer wg.Done()
			_, _ = s.getTypeFieldCacheEntry(uri1, "T")
		}()
		go func() {
			defer wg.Done()
			s.dataMu.Lock()
			delete(s.typeFieldCache, uri1)
			delete(s.fieldCache, uri1)
			s.dataMu.Unlock()
		}()

		// componentPropsCache
		go func() {
			defer wg.Done()
			inst.setComponentPropsCacheEntry("a/b/c.gastro", cacheEntry[[]codegen.StructField]{value: []codegen.StructField{{Name: "Title", Type: "string"}}})
		}()
		go func() {
			defer wg.Done()
			_, _ = inst.getComponentPropsCacheEntry("a/b/c.gastro")
		}()
		go func() {
			defer wg.Done()
			inst.deleteComponentPropsCacheEntry("a/b/c.gastro")
		}()

		// goplsOpenFiles
		go func() {
			defer wg.Done()
			v := "file:///virtual/file.go"
			inst.setGoplsOpenFileVersion(v, 1)
		}()
		go func() {
			defer wg.Done()
			v := "file:///virtual/file.go"
			_, _ = inst.getGoplsOpenFileVersion(v)
		}()
		go func() {
			defer wg.Done()
			v := "file:///virtual/file.go"
			inst.incGoplsOpenFileVersion(v)
		}()

		// Cross-URI access to race on different keys in same inner map
		go func() {
			defer wg.Done()
			s.setFieldCacheEntry(uri2, "Y", []fieldInfo{{Label: "Count", Detail: "int"}})
		}()
		go func() {
			defer wg.Done()
			_, _ = s.getFieldCacheEntry(uri2, "Y")
		}()
		go func() {
			defer wg.Done()
			s.dataMu.Lock()
			delete(s.fieldCache, uri2)
			s.dataMu.Unlock()
		}()
	}

	wg.Wait()
}

// TestCacheConcurrentReadWriteDifferentUris verifies that concurrent reads and
// writes on different URIs don't trigger the race detector.
func TestCacheConcurrentReadWriteDifferentUris(t *testing.T) {
	s := newServer("test")
	const N = 20

	var wg sync.WaitGroup
	wg.Add(N * 3)
	for i := 0; i < N; i++ {
		uri := mkURI(i)
		go func(u string) {
			defer wg.Done()
			s.setTypeCache(u, map[string]string{"A": "int"})
		}(uri)
		go func(u string) {
			defer wg.Done()
			_ = s.getTypeCache(u)
		}(uri)
		go func(u string) {
			defer wg.Done()
			s.dataMu.Lock()
			delete(s.typeCache, u)
			s.dataMu.Unlock()
		}(uri)
	}
	wg.Wait()
}

func mkURI(i int) string { return "file:///tmp/test-" + fmt.Sprint(i) + ".gastro" }
