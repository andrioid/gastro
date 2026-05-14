package server

// Integration tests for the three request-aware LSP features:
//
//   - hover on {{ t "..." }} surfaces source location (file:line of the
//     FuncMap key in main.go) plus a "request-aware helper" badge,
//   - go-to-definition jumps into the FuncMap literal entry, and
//   - main.go gets an info-level diagnostic for any WithRequestFuncs(...)
//     call whose argument is a non-literal FuncMap.
//
// These tests pre-populate s.instances with a minimal projectInstance so
// instanceForURI doesn't try to spawn gopls / build a shadow workspace.
// The request-aware code paths don't need those bits — they only consult
// s.requestFuncs, which we warm by writing main.go on disk and calling
// Lookup.

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/andrioid/gastro/internal/codegen"
	"github.com/andrioid/gastro/internal/lsp/proxy"
	"github.com/andrioid/gastro/internal/parser"
)

// setupRFProject writes a project root containing the supplied main.go
// and returns the root + a freshly-constructed server with the project
// pre-registered. The server's writeToClient still goes to stdout —
// callers that need to capture diagnostics should call captureStdout
// around the publishing function.
func setupRFProject(t *testing.T, mainGo string) (string, *server, string) {
	t.Helper()
	dir := t.TempDir()
	writeGoMod(t, dir)
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(mainGo), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}

	s := newServer("test")
	s.projectDir = dir
	s.instances[dir] = &projectInstance{
		root:                dir,
		componentPropsCache: make(map[string]cacheEntry[[]codegen.StructField]),
		goplsOpenFiles:      make(map[string]int),
	}

	gastroURI := "file://" + filepath.Join(dir, "page.gastro")
	return dir, s, gastroURI
}

// TestRequestFuncHover_LiteralBinder: hover on a template invocation of a
// request-aware helper returns a markdown payload that names the helper
// as "request-aware" and points at the main.go declaration.
func TestRequestFuncHover_LiteralBinder(t *testing.T) {
	const mainGo = `package main

import (
	"html/template"
	"net/http"

	gastro "example.com/.gastro"
)

func main() {
	gastro.New(
		gastro.WithRequestFuncs(func(r *http.Request) template.FuncMap {
			return template.FuncMap{
				"t": func(s string) string { return s },
			}
		}),
	)
}
`
	dir, s, uri := setupRFProject(t, mainGo)
	// Warm the cache so HelperAt returns a hit.
	entry := s.requestFuncs.Lookup(dir)
	if _, ok := entry.HelperAt("t"); !ok {
		t.Fatalf("expected t to be discovered; got names %v", entry.Names())
	}

	const content = "---\nTitle := \"x\"\n_ = Title\n---\n<p>{{ t \"Welcome\" }}</p>\n"
	s.documents[uri] = content
	parsed, err := parser.Parse("page.gastro", content)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	// Position cursor on the "t" in {{ t "Welcome" }}.
	// Line 0: "---", lines 1-2 frontmatter, line 3: "---", line 4: body.
	pos := proxy.Position{Line: 4, Character: 6}
	got := s.templateHover(uri, content, pos, parsed)
	if got == nil {
		t.Fatal("expected hover result, got nil")
	}
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("hover result is not a map: %T", got)
	}
	contents := m["contents"].(map[string]any)
	value, _ := contents["value"].(string)
	if !strings.Contains(value, "request-aware helper") {
		t.Errorf("hover should mark helper as request-aware; got %q", value)
	}
	if !strings.Contains(value, "main.go") {
		t.Errorf("hover should reference main.go source; got %q", value)
	}
	if !strings.Contains(value, "WithRequestFuncs[0]") {
		t.Errorf("hover should name the binder index; got %q", value)
	}
}

// TestRequestFuncDefinition_LiteralBinder: definition on a request-aware
// helper returns an LSP Location pointing into the FuncMap literal entry
// in main.go.
func TestRequestFuncDefinition_LiteralBinder(t *testing.T) {
	const mainGo = `package main

import (
	"html/template"
	"net/http"

	gastro "example.com/.gastro"
)

func main() {
	gastro.New(
		gastro.WithRequestFuncs(func(r *http.Request) template.FuncMap {
			return template.FuncMap{
				"csrfToken": func() string { return "" },
			}
		}),
	)
}
`
	dir, s, uri := setupRFProject(t, mainGo)
	entry := s.requestFuncs.Lookup(dir)
	info, ok := entry.HelperAt("csrfToken")
	if !ok {
		t.Fatalf("expected csrfToken to be discovered")
	}

	const content = "---\nTitle := \"x\"\n_ = Title\n---\n<input value={{ csrfToken }}>\n"
	s.documents[uri] = content
	parsed, err := parser.Parse("page.gastro", content)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	// Cursor on the "c" of csrfToken in `{{ csrfToken }}` on body line.
	pos := proxy.Position{Line: 4, Character: 16}
	got := s.requestFuncDefinition(uri, content, parsed, pos)
	if got == nil {
		t.Fatal("expected definition result, got nil")
	}
	loc, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("definition is not a map: %T", got)
	}
	wantURI := "file://" + info.File
	if loc["uri"] != wantURI {
		t.Errorf("uri = %v, want %v", loc["uri"], wantURI)
	}
	rng := loc["range"].(map[string]any)
	start := rng["start"].(map[string]any)
	// info.Line is 1-indexed; LSP positions are 0-indexed.
	if start["line"] != info.Line-1 {
		t.Errorf("start.line = %v, want %v", start["line"], info.Line-1)
	}
}

// captureStdout redirects os.Stdout for the duration of fn and returns
// everything written to it. Used to capture LSP notifications emitted via
// writeToClient.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w

	var mu sync.Mutex
	var out strings.Builder
	done := make(chan struct{})
	go func() {
		defer close(done)
		br := bufio.NewReader(r)
		buf := make([]byte, 4096)
		for {
			n, err := br.Read(buf)
			if n > 0 {
				mu.Lock()
				out.Write(buf[:n])
				mu.Unlock()
			}
			if err == io.EOF || err != nil {
				return
			}
		}
	}()

	fn()
	w.Close()
	<-done
	os.Stdout = orig
	mu.Lock()
	defer mu.Unlock()
	return out.String()
}

// TestNonLiteralBinderInfoDiagnostic_Published: a binder whose FuncMap is
// built dynamically causes publishRequestFuncDiagnostics to emit an
// info-level diagnostic for main.go, with the call site as the range.
func TestNonLiteralBinderInfoDiagnostic_Published(t *testing.T) {
	const mainGo = `package main

import (
	"html/template"
	"net/http"

	gastro "example.com/.gastro"
)

func main() {
	gastro.New(
		gastro.WithRequestFuncs(func(r *http.Request) template.FuncMap {
			fm := make(template.FuncMap)
			fm["dynamic"] = func() string { return "x" }
			return fm
		}),
	)
}
`
	dir, s, _ := setupRFProject(t, mainGo)

	// Capture stdout (where writeToClient writes JSON-RPC notifications).
	out := captureStdout(t, func() {
		s.publishRequestFuncDiagnostics(dir)
	})

	notif := findPublishDiagnostics(t, out, "file://"+filepath.Join(dir, "main.go"))
	if notif == nil {
		t.Fatal("expected publishDiagnostics notification for main.go; got none")
	}
	diags, _ := notif["diagnostics"].([]any)
	if len(diags) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d", len(diags))
	}
	d := diags[0].(map[string]any)
	if int(d["severity"].(float64)) != diagnosticSeverityInfo {
		t.Errorf("severity = %v, want info (%d)", d["severity"], diagnosticSeverityInfo)
	}
	msg, _ := d["message"].(string)
	if !strings.Contains(msg, "non-literal") {
		t.Errorf("diagnostic message should mention non-literal; got %q", msg)
	}
	if !strings.Contains(msg, "template.FuncMap{") {
		t.Errorf("diagnostic message should point at the workaround; got %q", msg)
	}
}

// TestRequestFuncDiagnostics_Idempotent: publishing twice for the same
// modtime emits only one notification (the second call short-circuits via
// shouldPublish). After main.go's modtime bumps, re-publishing fires
// again so a fixed binder clears the diagnostic.
func TestRequestFuncDiagnostics_Idempotent(t *testing.T) {
	const mainGo = `package main

import (
	"html/template"
	"net/http"

	gastro "example.com/.gastro"
)

func main() {
	gastro.New(
		gastro.WithRequestFuncs(func(r *http.Request) template.FuncMap {
			fm := template.FuncMap{}
			return fm
		}),
	)
}
`
	dir, s, _ := setupRFProject(t, mainGo)

	out := captureStdout(t, func() {
		s.publishRequestFuncDiagnostics(dir)
		s.publishRequestFuncDiagnostics(dir)
	})

	count := strings.Count(out, `"method":"textDocument/publishDiagnostics"`)
	if count != 1 {
		t.Errorf("expected exactly 1 publish on repeated call at same modtime; got %d\nout=%s", count, out)
	}
}

// findPublishDiagnostics scans the captured JSON-RPC stream for a
// publishDiagnostics notification with the given URI and returns its
// params, or nil. Handles Content-Length-framed messages (the stdio LSP
// wire format).
func findPublishDiagnostics(t *testing.T, raw, wantURI string) map[string]any {
	t.Helper()
	// LSP messages are Content-Length: N\r\n\r\n{json}. Split on the
	// header and parse each JSON body. Cheap regex-free approach.
	for {
		idx := strings.Index(raw, "Content-Length:")
		if idx < 0 {
			return nil
		}
		raw = raw[idx:]
		end := strings.Index(raw, "\r\n\r\n")
		if end < 0 {
			return nil
		}
		// Parse the length value.
		header := raw[len("Content-Length:"):end]
		header = strings.TrimSpace(header)
		var length int
		for _, c := range header {
			if c < '0' || c > '9' {
				break
			}
			length = length*10 + int(c-'0')
		}
		body := raw[end+4 : end+4+length]
		raw = raw[end+4+length:]

		var msg map[string]any
		if err := json.Unmarshal([]byte(body), &msg); err != nil {
			continue
		}
		if msg["method"] != "textDocument/publishDiagnostics" {
			continue
		}
		params, _ := msg["params"].(map[string]any)
		if params["uri"] == wantURI {
			return params
		}
	}
}
