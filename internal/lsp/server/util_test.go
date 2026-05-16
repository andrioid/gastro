package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// mkdirs is a tiny helper for setting up nested directories in tests.
func mkdirs(t *testing.T, root string, paths ...string) {
	t.Helper()
	for _, p := range paths {
		if err := os.MkdirAll(filepath.Join(root, p), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", p, err)
		}
	}
}

// touch creates an empty file at root/path, ensuring parent dirs exist.
func touch(t *testing.T, root, path string) string {
	t.Helper()
	full := filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir parents for %s: %v", path, err)
	}
	if err := os.WriteFile(full, nil, 0o644); err != nil {
		t.Fatalf("touch %s: %v", path, err)
	}
	return full
}

// pathEqual compares two paths after resolving symlinks. Needed because
// t.TempDir() on macOS returns paths under /var that EvalSymlinks rewrites
// to /private/var. findProjectRoot calls EvalSymlinks internally, so the
// returned path may differ textually from the test's expected path even when
// they refer to the same directory.
func pathEqual(a, b string) bool {
	ra, _ := filepath.EvalSymlinks(a)
	rb, _ := filepath.EvalSymlinks(b)
	if ra == "" {
		ra = a
	}
	if rb == "" {
		rb = b
	}
	return ra == rb
}

// goMod writes a minimal go.mod at root/path.
func goMod(t *testing.T, root, path string) {
	t.Helper()
	full := filepath.Join(root, path, "go.mod")
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir parents for %s: %v", path, err)
	}
	if err := os.WriteFile(full, []byte("module testproject\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
}

func TestFindProjectRoot_FlatLayout(t *testing.T) {
	// Standard case: <root>/go.mod + <root>/components/foo.gastro
	root := t.TempDir()
	goMod(t, root, ".")
	file := touch(t, root, "components/foo.gastro")

	got := findProjectRoot(file, "/fallback")
	if !pathEqual(got, root) {
		t.Errorf("got %q, want %q", got, root)
	}
}

func TestFindProjectRoot_NestedLayout_GitPMCase(t *testing.T) {
	// The bug this whole change exists to fix:
	// <root>/go.mod (only here) + <root>/internal/web/components/foo.gastro
	// The structural marker (components/) wins over the outer go.mod.
	root := t.TempDir()
	goMod(t, root, ".")
	want := filepath.Join(root, "internal", "web")
	file := touch(t, root, "internal/web/components/board.gastro")

	got := findProjectRoot(file, "/fallback")
	if !pathEqual(got, want) {
		t.Errorf("got %q, want %q (outer go.mod should NOT win when a structural marker is closer)", got, want)
	}
}

func TestFindProjectRoot_NestedLayout_PagesMarker(t *testing.T) {
	root := t.TempDir()
	goMod(t, root, ".")
	want := filepath.Join(root, "internal", "web")
	file := touch(t, root, "internal/web/pages/index.gastro")

	got := findProjectRoot(file, "/fallback")
	if !pathEqual(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFindProjectRoot_DeepComponentsNesting(t *testing.T) {
	// Components can be organised in subdirectories: components/ui/forms/foo.gastro
	root := t.TempDir()
	goMod(t, root, ".")
	file := touch(t, root, "components/ui/forms/input.gastro")

	got := findProjectRoot(file, "/fallback")
	if !pathEqual(got, root) {
		t.Errorf("got %q, want %q (deep nesting should still resolve to project root)", got, root)
	}
}

func TestFindProjectRoot_NoStructuralMarker_FallsBackToGoMod(t *testing.T) {
	// .gastro file directly under module root (unusual but valid edge case).
	// Without pages/ or components/ ancestor, the go.mod dir is the answer.
	root := t.TempDir()
	goMod(t, root, ".")
	file := touch(t, root, "stray.gastro")

	got := findProjectRoot(file, "/fallback")
	if !pathEqual(got, root) {
		t.Errorf("got %q, want %q (should fall back to go.mod dir)", got, root)
	}
}

func TestFindProjectRoot_NoGoMod_FallsBackToFallback(t *testing.T) {
	// File outside any Go module: walk hits filesystem root, returns fallback.
	root := t.TempDir()
	file := touch(t, root, "stray.gastro")

	got := findProjectRoot(file, "/my/fallback")
	if got != "/my/fallback" {
		t.Errorf("got %q, want %q", got, "/my/fallback")
	}
}

func TestFindProjectRoot_NestedModules_StopsAtNearestGoMod(t *testing.T) {
	// Two go.mods: <root>/go.mod and <root>/sub/go.mod.
	// File at <root>/sub/random/foo.gastro (no structural marker).
	// Should return <root>/sub (nearest go.mod), not <root>.
	root := t.TempDir()
	goMod(t, root, ".")
	goMod(t, root, "sub")
	file := touch(t, root, "sub/random/foo.gastro")

	want := filepath.Join(root, "sub")
	got := findProjectRoot(file, "/fallback")
	if !pathEqual(got, want) {
		t.Errorf("got %q, want %q (should stop at the nearest go.mod)", got, want)
	}
}

func TestFindProjectRoot_StructuralMarkerBeatsCloserGoMod(t *testing.T) {
	// Edge case: the structural marker is reached before go.mod when walking up.
	// File at <root>/internal/web/components/foo.gastro, with go.mod at <root>.
	// (No go.mod at <root>/internal/web.)
	// We hit "components" before any go.mod check, so we return its parent
	// regardless of where go.mod lives. This is the desired behavior.
	root := t.TempDir()
	goMod(t, root, ".")
	want := filepath.Join(root, "internal", "web")
	file := touch(t, root, "internal/web/components/foo.gastro")

	got := findProjectRoot(file, "/fallback")
	if !pathEqual(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFindProjectRoot_GastroProjectEnv_ValidPin(t *testing.T) {
	// GASTRO_PROJECT pins the root regardless of file location.
	root := t.TempDir()
	goMod(t, root, ".")
	mkdirs(t, root, "components")
	file := touch(t, root, "components/foo.gastro")

	pinned := t.TempDir() // a totally different dir
	t.Setenv("GASTRO_PROJECT", pinned)

	got := findProjectRoot(file, "/fallback")
	if !pathEqual(got, pinned) {
		t.Errorf("got %q, want %q (env var should override heuristic)", got, pinned)
	}
}

func TestFindProjectRoot_GastroProjectEnv_RelativePath(t *testing.T) {
	// Relative GASTRO_PROJECT should be resolved against cwd at call time.
	pinned := t.TempDir()
	parent := filepath.Dir(pinned)
	rel, err := filepath.Rel(parent, pinned)
	if err != nil {
		t.Fatalf("rel: %v", err)
	}

	// Chdir so the relative path resolves correctly.
	origWd, _ := os.Getwd()
	if err := os.Chdir(parent); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { os.Chdir(origWd) })

	t.Setenv("GASTRO_PROJECT", rel)

	got := findProjectRoot("/some/file.gastro", "/fallback")
	if !pathEqual(got, pinned) {
		t.Errorf("got %q, want %q", got, pinned)
	}
}

func TestFindProjectRoot_GastroProjectEnv_NonexistentFallsBack(t *testing.T) {
	// If GASTRO_PROJECT is invalid, fall back to the heuristic — don't crash.
	root := t.TempDir()
	goMod(t, root, ".")
	file := touch(t, root, "components/foo.gastro")

	t.Setenv("GASTRO_PROJECT", "/this/does/not/exist/anywhere")

	got := findProjectRoot(file, "/fallback")
	if !pathEqual(got, root) {
		t.Errorf("got %q, want %q (invalid env var should be ignored)", got, root)
	}
}

func TestFindProjectRoot_GastroProjectEnv_PointsToFile(t *testing.T) {
	// If GASTRO_PROJECT is a file (not a dir), fall back to heuristic.
	root := t.TempDir()
	goMod(t, root, ".")
	file := touch(t, root, "components/foo.gastro")

	notADir := touch(t, t.TempDir(), "i-am-a-file")
	t.Setenv("GASTRO_PROJECT", notADir)

	got := findProjectRoot(file, "/fallback")
	if !pathEqual(got, root) {
		t.Errorf("got %q, want %q (env var pointing to a file should be ignored)", got, root)
	}
}

func TestFindProjectRoot_GastroProjectEnv_Empty(t *testing.T) {
	// Explicitly empty env var should behave the same as unset.
	root := t.TempDir()
	goMod(t, root, ".")
	file := touch(t, root, "components/foo.gastro")

	t.Setenv("GASTRO_PROJECT", "")

	got := findProjectRoot(file, "/fallback")
	if !pathEqual(got, root) {
		t.Errorf("got %q, want %q", got, root)
	}
}

func TestFindProjectRoot_SymlinkResolution(t *testing.T) {
	// Symlinked .gastro file should resolve to its real location.
	root := t.TempDir()
	goMod(t, root, ".")
	realFile := touch(t, root, "internal/web/components/foo.gastro")

	linkDir := t.TempDir()
	link := filepath.Join(linkDir, "link.gastro")
	if err := os.Symlink(realFile, link); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}

	want := filepath.Join(root, "internal", "web")
	got := findProjectRoot(link, "/fallback")
	if !pathEqual(got, want) {
		t.Errorf("got %q, want %q (symlink should resolve to real file's project)", got, want)
	}
}

// TestCanonicalizeURI_ResolvesSymlinkedDirectory is the regression test
// for the macOS /var ↔ /private/var bug described on canonicalizeURI.
// We simulate it via an explicit symlink so the test runs the same way
// on every OS. Before the fix, a URI under linkDir/... and findProjectRoot's
// resolved path under realDir/... would disagree and findGastroURIForVirtualURI
// would silently fail every shadow↔source mapping.
func TestCanonicalizeURI_ResolvesSymlinkedDirectory(t *testing.T) {
	// t.TempDir() itself returns symlinked paths on macOS (/var →
	// /private/var), so resolve it up-front to get the fully canonical
	// real directory. Without this the "want" expectation below would
	// still contain unresolved /var components, even though the symlink
	// we explicitly created points correctly.
	realDirRaw := t.TempDir()
	realDir, err := filepath.EvalSymlinks(realDirRaw)
	if err != nil {
		t.Fatal(err)
	}
	realFile := touch(t, realDir, "pages/index.gastro")

	linkParentRaw := t.TempDir()
	linkParent, err := filepath.EvalSymlinks(linkParentRaw)
	if err != nil {
		t.Fatal(err)
	}
	linkDir := filepath.Join(linkParent, "projlink")
	if err := os.Symlink(realDir, linkDir); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}
	linkedFile := filepath.Join(linkDir, "pages", "index.gastro")

	// Sanity check the symlink actually points where we think.
	if _, err := os.Stat(linkedFile); err != nil {
		t.Fatalf("symlinked file not reachable: %v", err)
	}

	linkURI := "file://" + linkedFile
	got := canonicalizeURI(linkURI)
	want := "file://" + realFile
	if got != want {
		t.Errorf("canonicalizeURI(%q):\n  got  %q\n  want %q", linkURI, got, want)
	}
}

// TestCanonicalizeURI_PreservesAlreadyCanonical: when the path has no
// symlinks to resolve, the input URI is returned verbatim (no allocation
// for re-prefixing "file://").
func TestCanonicalizeURI_PreservesAlreadyCanonical(t *testing.T) {
	root := t.TempDir()
	// t.TempDir() returns symlinked /var/folders/... on macOS; resolve
	// up-front so the test exercises the no-op branch on every OS.
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	file := touch(t, resolved, "pages/index.gastro")

	uri := "file://" + file
	got := canonicalizeURI(uri)
	if got != uri {
		t.Errorf("canonicalizeURI should be a no-op for already-canonical URI:\n  got  %q\n  want %q", got, uri)
	}
}

// TestCanonicalizeURI_PreservesNonexistentPath: documents that exist
// only as didOpen text payloads (unsaved buffers, the test pattern in
// cmd/gastro/lsp_integration_test.go) have no on-disk file to
// EvalSymlinks. canonicalizeURI must pass those through unchanged so
// the existing tests — which never write the .gastro file to disk —
// keep matching s.documents keys against handler params.
func TestCanonicalizeURI_PreservesNonexistentPath(t *testing.T) {
	root := t.TempDir()
	nonexistent := filepath.Join(root, "never-written.gastro")
	uri := "file://" + nonexistent

	got := canonicalizeURI(uri)
	if got != uri {
		t.Errorf("canonicalizeURI should pass nonexistent paths through:\n  got  %q\n  want %q", got, uri)
	}
}

// TestCanonicalizeURI_EmptyAndMalformed: defensive coverage for the
// degenerate inputs that show up in handler error paths (e.g. a
// malformed initialize message with no rootUri).
func TestCanonicalizeURI_EmptyAndMalformed(t *testing.T) {
	for _, uri := range []string{"", "not-a-uri", "https://example.com"} {
		if got := canonicalizeURI(uri); got != uri {
			t.Errorf("canonicalizeURI(%q) = %q, want %q (no-op fallback)", uri, got, uri)
		}
	}
}

// TestHandleDidOpen_CanonicalizesURIForLookup: the end-to-end fix.
// Editor opens via a symlinked path; subsequent handlers find the
// document regardless of which URI form they pass. Before the fix,
// only the exact URI used in didOpen would match — a later handler
// querying the canonical form would miss.
func TestHandleDidOpen_CanonicalizesURIForLookup(t *testing.T) {
	realDirRaw := t.TempDir()
	realDir, err := filepath.EvalSymlinks(realDirRaw)
	if err != nil {
		t.Fatal(err)
	}
	linkParentRaw := t.TempDir()
	linkParent, err := filepath.EvalSymlinks(linkParentRaw)
	if err != nil {
		t.Fatal(err)
	}
	linkDir := filepath.Join(linkParent, "projlink")
	if err := os.Symlink(realDir, linkDir); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}
	goMod(t, realDir, ".")
	realFile := touch(t, realDir, "pages/index.gastro")
	linkedFile := filepath.Join(linkDir, "pages", "index.gastro")

	s := newServer("test")
	s.projectDir = realDir

	// didOpen with the symlinked URI — simulates an editor that
	// hasn't canonicalised the path.
	linkedURI := "file://" + linkedFile
	open := &jsonRPCMessage{Method: "textDocument/didOpen"}
	open.Params, _ = json.Marshal(map[string]any{
		"textDocument": map[string]any{
			"uri":        linkedURI,
			"languageId": "gastro",
			"version":    1,
			"text":       "---\nTitle := \"Hi\"\n---\n<h1>{{ .Title }}</h1>",
		},
	})
	s.handleDidOpen(open)

	// The stored key must be the canonical URI (built from realFile).
	canonicalURI := "file://" + realFile
	s.dataMu.RLock()
	_, hasCanonical := s.documents[canonicalURI]
	_, hasLinked := s.documents[linkedURI]
	s.dataMu.RUnlock()

	if !hasCanonical {
		t.Errorf("expected canonical URI %q in s.documents (keys: %v)", canonicalURI, mapKeys(s.documents))
	}
	if hasLinked && linkedURI != canonicalURI {
		t.Errorf("non-canonical URI %q should NOT be a key when a symlink resolved", linkedURI)
	}
}

func mapKeys[K comparable, V any](m map[K]V) []K {
	out := make([]K, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
