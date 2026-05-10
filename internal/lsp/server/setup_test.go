package server

import (
	"os"
	"testing"
)

// TestMain establishes a known-clean environment for the entire
// internal/lsp/server test package before any test runs.
//
// Why this exists: many tests in this package exercise findProjectRoot
// directly (TestFindProjectRoot_*) or transitively (instanceForURI,
// completion-context walks). findProjectRoot honours GASTRO_PROJECT
// when it points at an existing directory, so a developer with the
// env var pinned in their shell — common because contributors set
// GASTRO_PROJECT for `mise dev` against an external project — would
// see ~10 phantom failures: every t.TempDir()-based test thinks the
// pinned directory is the project root.
//
// The fix is to unset GASTRO_PROJECT once for the whole package, so
// every test starts from a deterministic baseline. The five
// TestFindProjectRoot_GastroProjectEnv_* tests that do want the env
// set continue to work because t.Setenv scopes their pin to the
// individual test.
//
// Documented in docs/history/lsp-shadow-audit.md §6.1 (resolved 2026-05-10).
func TestMain(m *testing.M) {
	os.Unsetenv("GASTRO_PROJECT")
	os.Exit(m.Run())
}
