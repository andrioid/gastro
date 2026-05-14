package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/andrioid/gastro/internal/lsp/proxy"
)

// newCodeActionTestServer wires up just enough state to run
// embedCodeActions: a tracked document and a real on-disk module so
// codegen.FindModuleRootForFile resolves. No gopls instance is created;
// the var-type quick-fix path doesn't need one.
func newCodeActionTestServer(t *testing.T, gastroFile string, content string) (*server, string) {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/m\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pages := filepath.Join(root, "pages")
	if err := os.MkdirAll(pages, 0o755); err != nil {
		t.Fatal(err)
	}
	abs := filepath.Join(pages, gastroFile)
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	uri := "file://" + abs

	s := newServer("test")
	s.documents[uri] = content
	return s, uri
}

// fullDocRange covers the entire document so range-intersection logic
// won't accidentally filter actions out in tests aimed at the actions
// themselves. Range-filtering has its own dedicated test below.
func fullDocRange() proxy.Range {
	return proxy.Range{
		Start: proxy.Position{Line: 0, Character: 0},
		End:   proxy.Position{Line: 1000, Character: 0},
	}
}

func TestEmbedCodeActions_VarTypeOffersBoth(t *testing.T) {
	content := "---\n//gastro:embed intro.md\nvar X int\n---\n<p>hi</p>\n"
	s, uri := newCodeActionTestServer(t, "page.gastro", content)

	// Create the embed target so ValidateEmbedDirectives gets past the
	// path check and only flags BadVarType. (Without it we'd also see
	// MissingFile, which our action filter ignores anyway, but keeping
	// the test focused removes a confounder.)
	root := filepath.Dir(filepath.Dir(uriToPath(uri)))
	if err := os.WriteFile(filepath.Join(root, "pages", "intro.md"), []byte("# hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	params := codeActionParams{Range: fullDocRange()}
	params.TextDocument.URI = uri

	actions := s.embedCodeActions(params)
	if len(actions) != 2 {
		t.Fatalf("expected 2 actions (string + []byte), got %d: %+v", len(actions), actions)
	}
	if title, _ := actions[0]["title"].(string); title != "Change var type to `string`" {
		t.Errorf("first action title = %q, want `string` first", title)
	}
	if title, _ := actions[1]["title"].(string); title != "Change var type to `[]byte`" {
		t.Errorf("second action title = %q, want `[]byte` second", title)
	}
	for _, a := range actions {
		if kind, _ := a["kind"].(string); kind != "quickfix" {
			t.Errorf("action kind = %q, want quickfix", kind)
		}
	}
}

func TestEmbedCodeActions_VarTypeRangeMatchesType(t *testing.T) {
	// Frontmatter line numbering: line 1 is `---`, line 2 is the
	// directive, line 3 is the var decl. LSP positions are 0-indexed
	// so the decl sits at line 2.
	content := "---\n//gastro:embed intro.md\nvar X int\n---\n"
	s, uri := newCodeActionTestServer(t, "page.gastro", content)
	if err := os.WriteFile(filepath.Join(filepath.Dir(uriToPath(uri)), "intro.md"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}

	params := codeActionParams{Range: fullDocRange()}
	params.TextDocument.URI = uri
	actions := s.embedCodeActions(params)
	if len(actions) == 0 {
		t.Fatal("expected actions")
	}

	edit := actions[0]["edit"].(map[string]any)
	changes := edit["changes"].(map[string]any)
	edits := changes[uri].([]any)
	te := edits[0].(map[string]any)
	rng := te["range"].(map[string]any)
	start := rng["start"].(map[string]any)
	end := rng["end"].(map[string]any)
	if start["line"] != 2 || end["line"] != 2 {
		t.Errorf("edit line = (%v..%v), want (2..2)", start["line"], end["line"])
	}
	// `var X int`: `int` starts at column 6, ends at column 9 (exclusive).
	if start["character"] != 6 {
		t.Errorf("edit start char = %v, want 6", start["character"])
	}
	if end["character"] != 9 {
		t.Errorf("edit end char = %v, want 9", end["character"])
	}
	if newText, _ := te["newText"].(string); newText != "string" {
		t.Errorf("first action newText = %q, want %q", newText, "string")
	}
}

func TestEmbedCodeActions_OutOfRangeIgnored(t *testing.T) {
	content := "---\n//gastro:embed intro.md\nvar X int\n---\n<p>hi</p>\n"
	s, uri := newCodeActionTestServer(t, "page.gastro", content)
	if err := os.WriteFile(filepath.Join(filepath.Dir(uriToPath(uri)), "intro.md"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Range covers the body only (line 4 is `<p>hi</p>`), nowhere near
	// the directive (line 1) or the decl (line 2). LSP convention: only
	// return actions whose target intersects the requested range.
	params := codeActionParams{
		Range: proxy.Range{
			Start: proxy.Position{Line: 4, Character: 0},
			End:   proxy.Position{Line: 4, Character: 8},
		},
	}
	params.TextDocument.URI = uri

	actions := s.embedCodeActions(params)
	if len(actions) != 0 {
		t.Errorf("expected 0 actions for out-of-range request, got %d", len(actions))
	}
}

func TestEmbedCodeActions_NonBadVarTypeIgnored(t *testing.T) {
	// Missing-file diagnostic should not produce a code action because
	// the var-type quick-fix isn't relevant. We don't (yet) ship any
	// other quick-fix categories, so this should return zero actions.
	content := "---\n//gastro:embed missing.md\nvar X string\n---\n"
	s, uri := newCodeActionTestServer(t, "page.gastro", content)

	params := codeActionParams{Range: fullDocRange()}
	params.TextDocument.URI = uri

	actions := s.embedCodeActions(params)
	if len(actions) != 0 {
		t.Errorf("expected 0 actions for non-BadVarType diags, got %d: %+v", len(actions), actions)
	}
}

func TestEmbedCodeActions_BindsDiagnostic(t *testing.T) {
	content := "---\n//gastro:embed intro.md\nvar X int\n---\n"
	s, uri := newCodeActionTestServer(t, "page.gastro", content)
	if err := os.WriteFile(filepath.Join(filepath.Dir(uriToPath(uri)), "intro.md"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Simulate the editor passing along the published diagnostic.
	// Directive line is line 1 (0-indexed) in the .gastro file.
	editorDiag := map[string]any{
		"range": map[string]any{
			"start": map[string]any{"line": float64(1), "character": float64(0)},
			"end":   map[string]any{"line": float64(1), "character": float64(23)},
		},
		"severity": float64(1),
		"message":  "//gastro:embed requires var of type `string` or `[]byte`; got `int`",
		"source":   "gastro",
	}
	params := codeActionParams{Range: fullDocRange()}
	params.TextDocument.URI = uri
	params.Context.Diagnostics = []map[string]any{editorDiag}

	actions := s.embedCodeActions(params)
	if len(actions) == 0 {
		t.Fatal("expected actions")
	}
	bound, ok := actions[0]["diagnostics"].([]any)
	if !ok || len(bound) != 1 {
		t.Fatalf("expected bound diagnostics, got %T %+v", actions[0]["diagnostics"], actions[0]["diagnostics"])
	}
	if bound[0].(map[string]any)["message"] != editorDiag["message"] {
		t.Errorf("bound diag mismatch")
	}
}

func TestHandleCodeAction_HonoursOnlyFilter(t *testing.T) {
	content := "---\n//gastro:embed intro.md\nvar X int\n---\n"
	s, uri := newCodeActionTestServer(t, "page.gastro", content)
	if err := os.WriteFile(filepath.Join(filepath.Dir(uriToPath(uri)), "intro.md"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Editor asks only for refactor.rewrite actions; we have none.
	rawParams := map[string]any{
		"textDocument": map[string]any{"uri": uri},
		"range": map[string]any{
			"start": map[string]any{"line": 0, "character": 0},
			"end":   map[string]any{"line": 100, "character": 0},
		},
		"context": map[string]any{
			"diagnostics": []any{},
			"only":        []any{"refactor.rewrite"},
		},
	}
	body, _ := json.Marshal(rawParams)
	resp := s.handleCodeAction(&jsonRPCMessage{ID: 1, Params: body})

	got, _ := resp.Result.([]map[string]any)
	if len(got) != 0 {
		t.Errorf("Only=[refactor.rewrite] should suppress quickfix, got %d actions", len(got))
	}
}

func TestHandleCodeAction_ReturnsEmptyArrayNotNull(t *testing.T) {
	// VS Code crashes on null in some response slots; verify our
	// default path is an empty array even when no document is open.
	s := newServer("test")
	rawParams := map[string]any{
		"textDocument": map[string]any{"uri": "file:///nonexistent.gastro"},
		"range": map[string]any{
			"start": map[string]any{"line": 0, "character": 0},
			"end":   map[string]any{"line": 0, "character": 0},
		},
		"context": map[string]any{"diagnostics": []any{}},
	}
	body, _ := json.Marshal(rawParams)
	resp := s.handleCodeAction(&jsonRPCMessage{ID: 1, Params: body})
	if resp.Result == nil {
		t.Fatal("Result is nil; should be empty array")
	}
	got, ok := resp.Result.([]map[string]any)
	if !ok {
		t.Fatalf("Result type = %T, want []map[string]any", resp.Result)
	}
	if len(got) != 0 {
		t.Errorf("expected empty array, got %d entries", len(got))
	}
}

func TestForwardCodeActionToGopls_NoInstanceReturnsNil(t *testing.T) {
	// When there's no project instance for the URI (or gopls didn't
	// start), the forwarder must return nil silently so our own
	// actions still flow. Regression guard for the merge logic.
	content := "---\n//gastro:embed intro.md\nvar X int\n---\n"
	s, uri := newCodeActionTestServer(t, "page.gastro", content)
	if err := os.WriteFile(filepath.Join(filepath.Dir(uriToPath(uri)), "intro.md"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}

	params := codeActionParams{Range: fullDocRange()}
	params.TextDocument.URI = uri

	// instanceForURI will create an instance and try to spawn gopls;
	// in test environments without gopls in PATH this returns nil.
	// Either way, the forwarder should not panic and should return nil
	// when gopls is not running.
	got := s.forwardCodeActionToGopls(params)
	if got != nil {
		// Gopls is available in the test env — not an error per se,
		// just outside the scope of this regression check.
		t.Logf("gopls available; received %d forwarded actions (skipping)", len(got))
	}
}

func TestCodeAction_CapabilityAdvertised(t *testing.T) {
	// Initialize the server with a minimal params blob and verify the
	// capabilities response advertises codeActionProvider with the
	// quickfix kind. Editors gate sending textDocument/codeAction on
	// this advertisement — if it regresses, the squiggle silently
	// loses its lightbulb.
	s := newServer("test")
	rawParams := map[string]any{
		"rootUri":      "file:///tmp/x",
		"capabilities": map[string]any{},
	}
	body, _ := json.Marshal(rawParams)
	resp := s.handleInitialize(&jsonRPCMessage{ID: 1, Params: body})

	result, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("Result not map[string]any: %T", resp.Result)
	}
	caps, ok := result["capabilities"].(map[string]any)
	if !ok {
		t.Fatal("missing capabilities")
	}
	cap, ok := caps["codeActionProvider"].(map[string]any)
	if !ok {
		t.Fatalf("codeActionProvider missing or wrong type: %T %+v", caps["codeActionProvider"], caps["codeActionProvider"])
	}
	kinds, ok := cap["codeActionKinds"].([]string)
	if !ok || len(kinds) == 0 || kinds[0] != "quickfix" {
		t.Errorf("codeActionKinds = %+v, want [\"quickfix\"]", cap["codeActionKinds"])
	}
}

func TestVarTypeSpanInLine(t *testing.T) {
	cases := []struct {
		name   string
		line   string
		want   string // substring extracted from line[start:end]
		wantOK bool
	}{
		{"string", "var X string", "string", true},
		{"bytes", "var X []byte", "[]byte", true},
		{"qualified", "var X template.HTML", "template.HTML", true},
		{"int with comment", "var X int  // tail", "int", true},
		{"leading whitespace", "    var X int", "int", true},
		{"leading tab", "\tvar X int", "int", true},
		{"int with =", "var X int = 0", "int", true},
		{"missing var", "x string", "", false},
		{"no name", "var ", "", false},
		{"no type", "var X ", "", false},
		{"empty", "", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			start, end, ok := varTypeSpanInLine(c.line)
			if ok != c.wantOK {
				t.Fatalf("ok = %v, want %v (line=%q)", ok, c.wantOK, c.line)
			}
			if !ok {
				return
			}
			got := c.line[start:end]
			if got != c.want {
				t.Errorf("span = %q, want %q", got, c.want)
			}
		})
	}
}
