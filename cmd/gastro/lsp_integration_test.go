package main_test

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// These integration tests spawn gastro lsp as a subprocess and communicate
// via JSON-RPC over stdin/stdout.

type lspClient struct {
	proc   *exec.Cmd
	stdin  io.WriteCloser
	reader *bufio.Reader
	stderr *bufio.Reader
	nextID int
}

func startLSP(t *testing.T, projectDir string) *lspClient {
	t.Helper()

	// Build gastro binary
	binPath := filepath.Join(t.TempDir(), "gastro")
	build := exec.Command("go", "build", "-o", binPath, ".")
	build.Dir = filepath.Join(projectRoot(t), "cmd", "gastro")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building gastro: %v\n%s", err, out)
	}

	proc := exec.Command(binPath, "lsp")
	proc.Dir = projectDir

	stdin, err := proc.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}

	stdout, err := proc.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}

	stderrPipe, err := proc.StderrPipe()
	if err != nil {
		t.Fatalf("stderr pipe: %v", err)
	}

	if err := proc.Start(); err != nil {
		t.Fatalf("starting gastro lsp: %v", err)
	}

	t.Cleanup(func() {
		stdin.Close()
		proc.Process.Kill()
		proc.Wait()
	})

	return &lspClient{
		proc:   proc,
		stdin:  stdin,
		reader: bufio.NewReader(stdout),
		stderr: bufio.NewReader(stderrPipe),
		nextID: 1,
	}
}

func (c *lspClient) send(method string, params any) int {
	id := c.nextID
	c.nextID++

	paramsJSON, _ := json.Marshal(params)
	msg := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  json.RawMessage(paramsJSON),
	}

	body, _ := json.Marshal(msg)
	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(body))
	c.stdin.Write([]byte(header))
	c.stdin.Write(body)
	return id
}

func (c *lspClient) notify(method string, params any) {
	paramsJSON, _ := json.Marshal(params)
	msg := map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  json.RawMessage(paramsJSON),
	}

	body, _ := json.Marshal(msg)
	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(body))
	c.stdin.Write([]byte(header))
	c.stdin.Write(body)
}

// recv reads the next LSP response (skipping any notifications).
// Notifications are messages without an "id" field.
func (c *lspClient) recv(t *testing.T, timeout time.Duration) map[string]any {
	t.Helper()

	deadline := time.After(timeout)

	for {
		type result struct {
			msg map[string]any
			err error
		}
		ch := make(chan result, 1)

		go func() {
			msg, err := readOneLSPMessage(c.reader)
			ch <- result{msg, err}
		}()

		select {
		case r := <-ch:
			if r.err != nil {
				stderr := c.drainStderr()
				t.Fatalf("reading LSP message: %v\nstderr: %s", r.err, stderr)
			}
			// Skip notifications (no "id" field) — keep reading
			if r.msg["id"] == nil {
				continue
			}
			return r.msg
		case <-deadline:
			stderr := c.drainStderr()
			t.Fatalf("timeout waiting for LSP response\nstderr: %s", stderr)
			return nil
		}
	}
}

// recvNotification reads LSP messages until it finds a notification with the
// given method name, skipping responses and other notifications.
func (c *lspClient) recvNotification(t *testing.T, method string, timeout time.Duration) map[string]any {
	t.Helper()

	deadline := time.After(timeout)

	for {
		type result struct {
			msg map[string]any
			err error
		}
		ch := make(chan result, 1)

		go func() {
			msg, err := readOneLSPMessage(c.reader)
			ch <- result{msg, err}
		}()

		select {
		case r := <-ch:
			if r.err != nil {
				stderr := c.drainStderr()
				t.Fatalf("reading LSP message: %v\nstderr: %s", r.err, stderr)
			}
			if r.msg["method"] == method {
				return r.msg
			}
			// Skip other messages
			continue
		case <-deadline:
			return nil
		}
	}
}

func (c *lspClient) drainStderr() string {
	var sb strings.Builder
	buf := make([]byte, 4096)
	for {
		if _, err := c.stderr.Peek(1); err != nil {
			break
		}
		n, err := c.stderr.Read(buf)
		if n > 0 {
			sb.Write(buf[:n])
		}
		if err != nil {
			break
		}
	}
	return sb.String()
}

func readOneLSPMessage(reader *bufio.Reader) (map[string]any, error) {
	contentLength := 0
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			break
		}
		if strings.HasPrefix(line, "Content-Length:") {
			fmt.Sscanf(strings.TrimPrefix(line, "Content-Length:"), "%d", &contentLength)
		}
	}

	if contentLength == 0 {
		return nil, fmt.Errorf("missing Content-Length")
	}

	body := make([]byte, contentLength)
	if _, err := io.ReadFull(reader, body); err != nil {
		return nil, err
	}

	var msg map[string]any
	if err := json.Unmarshal(body, &msg); err != nil {
		return nil, err
	}

	return msg, nil
}

func projectRoot(t *testing.T) string {
	t.Helper()
	// Walk up from cmd/gastro to find go.mod
	dir, _ := os.Getwd()
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find project root")
		}
		dir = parent
	}
}

func TestLSP_InitializeResponds(t *testing.T) {
	projectDir := setupTestProject(t)
	client := startLSP(t, projectDir)

	client.send("initialize", map[string]any{
		"rootUri":      "file://" + projectDir,
		"capabilities": map[string]any{},
	})

	resp := client.recv(t, 10*time.Second)

	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected result object, got: %v", resp)
	}

	caps, ok := result["capabilities"].(map[string]any)
	if !ok {
		t.Fatalf("expected capabilities, got: %v", result)
	}

	if caps["completionProvider"] == nil {
		t.Error("expected completionProvider capability")
	}
}

func TestLSP_TemplateCompletions(t *testing.T) {
	projectDir := setupTestProject(t)
	client := startLSP(t, projectDir)

	// Initialize
	client.send("initialize", map[string]any{
		"rootUri":      "file://" + projectDir,
		"capabilities": map[string]any{},
	})
	client.recv(t, 10*time.Second)
	client.notify("initialized", map[string]any{})

	// Open a .gastro file — cursor will be after ".T" on line 3
	// line 3: <h1>{{ .T }}</h1>
	//                  ^ char 7 is '.', char 9 is after ".T"
	gastroContent := "---\nTitle := \"Hello\"\n---\n<h1>{{ .T }}</h1>"
	fileURI := "file://" + filepath.Join(projectDir, "pages", "index.gastro")

	client.notify("textDocument/didOpen", map[string]any{
		"textDocument": map[string]any{
			"uri":        fileURI,
			"languageId": "gastro",
			"version":    1,
			"text":       gastroContent,
		},
	})

	// Request completions at cursor position after ".T" (line 3, char 9)
	client.send("textDocument/completion", map[string]any{
		"textDocument": map[string]any{"uri": fileURI},
		"position":     map[string]any{"line": 3, "character": 9},
	})

	resp := client.recv(t, 10*time.Second)

	result := resp["result"]
	if result == nil {
		t.Fatal("expected completion result, got nil")
	}

	items, ok := result.([]any)
	if !ok {
		t.Fatalf("expected array of completions, got: %T", result)
	}

	// Should include the exported variable .Title with a textEdit
	found := false
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if m["label"] != ".Title" {
			continue
		}
		found = true

		// Should have filterText for editor fuzzy matching
		if m["filterText"] != ".Title" {
			t.Errorf("expected filterText='.Title', got %v", m["filterText"])
		}

		// Should have textEdit (not insertText) to avoid double-dot
		te, hasTextEdit := m["textEdit"].(map[string]any)
		if !hasTextEdit {
			t.Fatal("expected textEdit on variable completion, got insertText")
		}
		if te["newText"] != ".Title" {
			t.Errorf("expected textEdit.newText='.Title', got %v", te["newText"])
		}

		// The textEdit range should start at the dot (char 7) and end at cursor (char 9)
		rng, _ := te["range"].(map[string]any)
		start, _ := rng["start"].(map[string]any)
		end, _ := rng["end"].(map[string]any)
		if start["character"] != float64(7) {
			t.Errorf("expected textEdit start character=7, got %v", start["character"])
		}
		if end["character"] != float64(9) {
			t.Errorf("expected textEdit end character=9, got %v", end["character"])
		}
		break
	}
	if !found {
		t.Errorf("expected .Title in completions, got: %v", items)
	}
}

func TestLSP_TemplateDiagnostics(t *testing.T) {
	projectDir := setupTestProject(t)
	client := startLSP(t, projectDir)

	client.send("initialize", map[string]any{
		"rootUri":      "file://" + projectDir,
		"capabilities": map[string]any{},
	})
	client.recv(t, 10*time.Second)
	client.notify("initialized", map[string]any{})

	// Open a file with an unknown template variable
	gastroContent := "---\nTitle := \"Hello\"\n---\n<h1>{{ .Unknown }}</h1>"
	fileURI := "file://" + filepath.Join(projectDir, "pages", "index.gastro")

	client.notify("textDocument/didOpen", map[string]any{
		"textDocument": map[string]any{
			"uri":        fileURI,
			"languageId": "gastro",
			"version":    1,
			"text":       gastroContent,
		},
	})

	// Wait for a publishDiagnostics notification containing the .Unknown
	// diagnostic. Multiple notifications may arrive (e.g. an empty one from
	// gopls before template diagnostics are published), so we loop until we
	// find the one we're looking for.
	var diags []any
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		notification := client.recvNotification(t, "textDocument/publishDiagnostics", 2*time.Second)
		if notification == nil {
			continue
		}

		params, _ := notification["params"].(map[string]any)
		d, _ := params["diagnostics"].([]any)
		t.Logf("publishDiagnostics: count=%d", len(d))

		for _, diag := range d {
			dm, _ := diag.(map[string]any)
			msg, _ := dm["message"].(string)
			if strings.Contains(msg, ".Unknown") {
				diags = d
				break
			}
		}
		if diags != nil {
			break
		}
	}

	if diags == nil {
		t.Fatal("expected diagnostic for .Unknown, got none within deadline")
	}

	for _, d := range diags {
		dm, _ := d.(map[string]any)
		msg, _ := dm["message"].(string)
		if strings.Contains(msg, ".Unknown") {
			if dm["source"] != "gastro" {
				t.Errorf("expected source='gastro', got %v", dm["source"])
			}
			break
		}
	}
}

func TestLSP_TemplateHover(t *testing.T) {
	projectDir := setupTestProject(t)
	client := startLSP(t, projectDir)

	client.send("initialize", map[string]any{
		"rootUri":      "file://" + projectDir,
		"capabilities": map[string]any{},
	})
	client.recv(t, 10*time.Second)
	client.notify("initialized", map[string]any{})

	// line 3: <h1>{{ .Title }}</h1>
	//                 ^-- char 7 is the dot, char 8 is 'T'
	gastroContent := "---\nTitle := \"Hello\"\n---\n<h1>{{ .Title }}</h1>"
	fileURI := "file://" + filepath.Join(projectDir, "pages", "index.gastro")

	client.notify("textDocument/didOpen", map[string]any{
		"textDocument": map[string]any{
			"uri":        fileURI,
			"languageId": "gastro",
			"version":    1,
			"text":       gastroContent,
		},
	})

	// Hover on ".Title" — cursor on the 'T' at char 8
	client.send("textDocument/hover", map[string]any{
		"textDocument": map[string]any{"uri": fileURI},
		"position":     map[string]any{"line": 3, "character": 8},
	})

	resp := client.recv(t, 10*time.Second)
	result := resp["result"]
	if result == nil {
		t.Fatal("expected hover result, got nil")
	}

	rm, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("expected hover result as map, got %T", result)
	}

	contents, _ := rm["contents"].(map[string]any)
	if contents == nil {
		t.Fatal("expected hover contents, got nil")
	}

	value, _ := contents["value"].(string)
	if value == "" {
		t.Fatal("expected non-empty hover value")
	}

	// Should mention "frontmatter variable"
	if !strings.Contains(value, "frontmatter variable") {
		t.Errorf("expected hover to mention 'frontmatter variable', got: %s", value)
	}
}

func TestLSP_TemplateHover_Function(t *testing.T) {
	projectDir := setupTestProject(t)
	client := startLSP(t, projectDir)

	client.send("initialize", map[string]any{
		"rootUri":      "file://" + projectDir,
		"capabilities": map[string]any{},
	})
	client.recv(t, 10*time.Second)
	client.notify("initialized", map[string]any{})

	// line 3: <h1>{{ .Title | upper }}</h1>
	//                          ^-- "upper" starts at char 16
	gastroContent := "---\nTitle := \"Hello\"\n---\n<h1>{{ .Title | upper }}</h1>"
	fileURI := "file://" + filepath.Join(projectDir, "pages", "index.gastro")

	client.notify("textDocument/didOpen", map[string]any{
		"textDocument": map[string]any{
			"uri":        fileURI,
			"languageId": "gastro",
			"version":    1,
			"text":       gastroContent,
		},
	})

	// Hover on "upper"
	client.send("textDocument/hover", map[string]any{
		"textDocument": map[string]any{"uri": fileURI},
		"position":     map[string]any{"line": 3, "character": 17},
	})

	resp := client.recv(t, 10*time.Second)
	result := resp["result"]
	if result == nil {
		t.Fatal("expected hover result for function, got nil")
	}

	rm, _ := result.(map[string]any)
	contents, _ := rm["contents"].(map[string]any)
	value, _ := contents["value"].(string)

	if !strings.Contains(value, "template function") {
		t.Errorf("expected hover to mention 'template function', got: %s", value)
	}
	if !strings.Contains(value, "func(string) string") {
		t.Errorf("expected hover to show function signature, got: %s", value)
	}
}

func TestLSP_FrontmatterCompletions(t *testing.T) {
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls not in PATH, skipping frontmatter completion test")
	}
	// Fixed: virtual files no longer start with _ (Go ignores _-prefixed files)

	projectDir := setupTestProject(t)
	client := startLSP(t, projectDir)

	// Initialize
	client.send("initialize", map[string]any{
		"rootUri":      "file://" + projectDir,
		"capabilities": map[string]any{},
	})
	client.recv(t, 10*time.Second)
	client.notify("initialized", map[string]any{})

	// Open a .gastro file — request completions after "fmt.S"
	// Note: gopls requires at least one character after the dot to complete
	gastroContent := "---\nimport \"fmt\"\n\nTitle := fmt.S\n---\n<h1>{{ .Title }}</h1>"
	fileURI := "file://" + filepath.Join(projectDir, "pages", "index.gastro")

	client.notify("textDocument/didOpen", map[string]any{
		"textDocument": map[string]any{
			"uri":        fileURI,
			"languageId": "gastro",
			"version":    1,
			"text":       gastroContent,
		},
	})

	// Give gopls time to initialize and analyze the file
	time.Sleep(5 * time.Second)

	// Request completions in frontmatter (line 3, after "fmt.S")
	// "---\n" is line 0, "import..." is line 1, "" is line 2, "Title := fmt.S" is line 3
	// "fmt.S" starts at character 9, cursor after S is at character 14
	client.send("textDocument/completion", map[string]any{
		"textDocument": map[string]any{"uri": fileURI},
		"position":     map[string]any{"line": 3, "character": 14},
	})

	resp := client.recv(t, 15*time.Second)

	result := resp["result"]
	if result == nil {
		t.Fatal("expected completion result from gopls, got nil")
	}

	// gopls returns a CompletionList object with an "items" field,
	// or a plain array. Check both.
	var items []any
	switch v := result.(type) {
	case []any:
		items = v
	case map[string]any:
		if itemsList, ok := v["items"].([]any); ok {
			items = itemsList
		}
	}

	if len(items) == 0 {
		t.Fatalf("expected gopls to return completions for fmt.S, got empty result: %v", result)
	}

	// Should contain fmt.Sprintf, fmt.Sprint, etc.
	var labels []string
	foundSprint := false
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		label, _ := m["label"].(string)
		labels = append(labels, label)
		if strings.Contains(label, "Sprint") {
			foundSprint = true
		}
	}
	if !foundSprint {
		t.Errorf("expected fmt.Sprint* in gopls completions, got: %v", labels)
	}
}

func TestLSP_FrontmatterDiagnostics(t *testing.T) {
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls not in PATH, skipping frontmatter diagnostics test")
	}
	// Fixed: virtual files no longer start with _ (Go ignores _-prefixed files)

	projectDir := setupTestProject(t)
	client := startLSP(t, projectDir)

	// Initialize
	client.send("initialize", map[string]any{
		"rootUri":      "file://" + projectDir,
		"capabilities": map[string]any{},
	})
	client.recv(t, 10*time.Second)
	client.notify("initialized", map[string]any{})

	// Open a file with an undefined variable reference
	gastroContent := "---\nposts := \"hello\"\nPosts := postss\n---\n<h1>{{ .Posts }}</h1>"
	fileURI := "file://" + filepath.Join(projectDir, "pages", "index.gastro")

	client.notify("textDocument/didOpen", map[string]any{
		"textDocument": map[string]any{
			"uri":        fileURI,
			"languageId": "gastro",
			"version":    1,
			"text":       gastroContent,
		},
	})

	// Wait for diagnostics notification with actual diagnostics.
	// gopls often sends an initial empty publishDiagnostics, followed by
	// a non-empty one after analysis completes. We need to wait for the
	// non-empty one.
	var diagnostics []any
	var diagURI string
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		notification := client.recvNotification(t, "textDocument/publishDiagnostics", 5*time.Second)
		if notification == nil {
			continue
		}

		params, _ := notification["params"].(map[string]any)
		if params == nil {
			continue
		}

		uri, _ := params["uri"].(string)
		diags, _ := params["diagnostics"].([]any)
		t.Logf("publishDiagnostics: uri=%s count=%d", uri, len(diags))

		if len(diags) > 0 {
			diagURI = uri
			diagnostics = diags
			break
		}
	}

	if len(diagnostics) == 0 {
		t.Fatal("expected at least one diagnostic for undefined variable 'postss'")
	}

	// The URI should point to the .gastro file
	if !strings.Contains(diagURI, ".gastro") {
		t.Errorf("diagnostic URI should reference .gastro file, got: %s", diagURI)
	}

	// Check that at least one diagnostic mentions the undefined variable
	// and that it points to the correct gastro line.
	// The gastro content is:
	//   line 0: ---
	//   line 1: posts := "hello"
	//   line 2: Posts := postss       <-- "undefined: postss" should be here
	//   line 3: ---
	//   line 4: <h1>{{ .Posts }}</h1>
	foundUndefined := false
	for _, d := range diagnostics {
		dm, _ := d.(map[string]any)
		msg, _ := dm["message"].(string)
		if strings.Contains(msg, "undefined") || strings.Contains(msg, "undeclared") {
			foundUndefined = true

			// Verify the diagnostic points to gastro line 2 (0-indexed)
			r, _ := dm["range"].(map[string]any)
			if r != nil {
				start, _ := r["start"].(map[string]any)
				if start != nil {
					line, _ := start["line"].(float64)
					if int(line) != 2 {
						t.Errorf("diagnostic for 'postss' should be on gastro line 2 (0-indexed), got line %d", int(line))
					}
				}
			}
			break
		}
	}
	if !foundUndefined {
		msgs := make([]string, 0)
		for _, d := range diagnostics {
			dm, _ := d.(map[string]any)
			msg, _ := dm["message"].(string)
			msgs = append(msgs, msg)
		}
		t.Errorf("expected diagnostic about undefined variable, got: %v", msgs)
	}
}

func TestLSP_FrontmatterLocalVarCompletions(t *testing.T) {
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls not in PATH, skipping local var completion test")
	}

	projectDir := setupTestProject(t)
	client := startLSP(t, projectDir)

	// Initialize
	client.send("initialize", map[string]any{
		"rootUri":      "file://" + projectDir,
		"capabilities": map[string]any{},
	})
	client.recv(t, 10*time.Second)
	client.notify("initialized", map[string]any{})

	// Open a .gastro file with a local variable and a partial reference.
	// The cursor will be after "myV" on the line "_ = myV"
	//   line 0: ---
	//   line 1: myVariable := "hello"
	//   line 2: _ = myV              <-- cursor at character 7 (after "myV")
	//   line 3: ---
	//   line 4: <h1>hi</h1>
	gastroContent := "---\nmyVariable := \"hello\"\n_ = myV\n---\n<h1>hi</h1>"
	fileURI := "file://" + filepath.Join(projectDir, "pages", "index.gastro")

	client.notify("textDocument/didOpen", map[string]any{
		"textDocument": map[string]any{
			"uri":        fileURI,
			"languageId": "gastro",
			"version":    1,
			"text":       gastroContent,
		},
	})

	// Wait for gopls to be ready by waiting for a publishDiagnostics notification.
	// This ensures gopls has finished loading before we send the completion request.
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		notification := client.recvNotification(t, "textDocument/publishDiagnostics", 5*time.Second)
		if notification != nil {
			break
		}
	}

	// Request completions after "myV" on line 2, character 7
	client.send("textDocument/completion", map[string]any{
		"textDocument": map[string]any{"uri": fileURI},
		"position":     map[string]any{"line": 2, "character": 7},
	})

	resp := client.recv(t, 15*time.Second)
	result := resp["result"]
	if result == nil {
		t.Fatal("expected completion result, got nil")
	}

	// Extract items
	var items []any
	switch v := result.(type) {
	case []any:
		items = v
	case map[string]any:
		if list, ok := v["items"].([]any); ok {
			items = list
		}
	}

	if len(items) == 0 {
		t.Fatalf("expected completions for local var 'myV', got empty result: %v", result)
	}

	// Should contain "myVariable"
	var labels []string
	foundMyVar := false
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		label, _ := m["label"].(string)
		labels = append(labels, label)
		if label == "myVariable" {
			foundMyVar = true

			// Verify textEdit range points to gastro coordinates (line 2), not virtual
			if te, ok := m["textEdit"].(map[string]any); ok {
				if r, ok := te["range"].(map[string]any); ok {
					if start, ok := r["start"].(map[string]any); ok {
						line := int(start["line"].(float64))
						if line != 2 {
							t.Errorf("textEdit.range.start.line = %d, want 2 (gastro coordinates)", line)
						}
					}
				}
			}
		}
	}

	if !foundMyVar {
		t.Errorf("expected 'myVariable' in completions, got: %v", labels)
	}
}

func TestLSP_UnhandledRequestGetsResponse(t *testing.T) {
	projectDir := setupTestProject(t)
	client := startLSP(t, projectDir)

	// Initialize
	client.send("initialize", map[string]any{
		"rootUri":      "file://" + projectDir,
		"capabilities": map[string]any{},
	})
	client.recv(t, 10*time.Second)

	// Send an unknown request — should get a response (not hang)
	client.send("textDocument/documentSymbol", map[string]any{
		"textDocument": map[string]any{"uri": "file:///nonexistent"},
	})

	resp := client.recv(t, 5*time.Second)

	if resp["id"] == nil {
		t.Error("expected response with an id")
	}
}

// setupTestProject creates a minimal Go project with a pages/ directory
// for testing the LSP server.
func setupTestProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module testproject\n\ngo 1.22\n"), 0o644)
	os.MkdirAll(filepath.Join(dir, "pages"), 0o755)

	return dir
}

// setupTestProjectWithComponents creates a Go project with pages/ and
// components/ directories, including a Card component with a Props struct.
func setupTestProjectWithComponents(t *testing.T) string {
	t.Helper()
	dir := setupTestProject(t)

	os.MkdirAll(filepath.Join(dir, "components"), 0o755)

	cardContent := `---
type Props struct {
	Title string
	Body  string
}

Title := gastro.Props().Title
Body := gastro.Props().Body
---
<div class="card">
	<h2>{{ .Title }}</h2>
	<p>{{ .Body }}</p>
</div>`

	os.WriteFile(filepath.Join(dir, "components", "card.gastro"), []byte(cardContent), 0o644)

	return dir
}

func TestLSP_ComponentPropDiagnostics(t *testing.T) {
	projectDir := setupTestProjectWithComponents(t)
	client := startLSP(t, projectDir)

	client.send("initialize", map[string]any{
		"rootUri":      "file://" + projectDir,
		"capabilities": map[string]any{},
	})
	client.recv(t, 10*time.Second)
	client.notify("initialized", map[string]any{})

	// Open a page that uses Card with an unknown prop
	gastroContent := `---
import Card "components/card.gastro"
Title := "Hello"
---
{{ Card (dict "Title" .Title "Bogus" "bad") }}`

	fileURI := "file://" + filepath.Join(projectDir, "pages", "index.gastro")
	client.notify("textDocument/didOpen", map[string]any{
		"textDocument": map[string]any{
			"uri":        fileURI,
			"languageId": "gastro",
			"version":    1,
			"text":       gastroContent,
		},
	})

	// Collect publishDiagnostics notifications — there may be multiple as
	// gopls and template diagnostics arrive separately and get merged.
	foundUnknown := false
	foundMissing := false
	for attempts := 0; attempts < 5; attempts++ {
		notification := client.recvNotification(t, "textDocument/publishDiagnostics", 3*time.Second)
		if notification == nil {
			break
		}

		params, _ := notification["params"].(map[string]any)
		diags, _ := params["diagnostics"].([]any)

		for _, d := range diags {
			dm, _ := d.(map[string]any)
			msg, _ := dm["message"].(string)
			if strings.Contains(msg, `unknown prop "Bogus"`) {
				foundUnknown = true
				sev, _ := dm["severity"].(float64)
				if int(sev) != 1 {
					t.Errorf("unknown prop should be severity 1 (Error), got %v", sev)
				}
			}
			if strings.Contains(msg, `missing prop "Body"`) {
				foundMissing = true
				sev, _ := dm["severity"].(float64)
				if int(sev) != 2 {
					t.Errorf("missing prop should be severity 2 (Warning), got %v", sev)
				}
			}
		}

		if foundUnknown && foundMissing {
			break
		}
	}
	if !foundUnknown {
		stderr := client.drainStderr()
		t.Errorf("expected diagnostic for unknown prop 'Bogus'\nstderr: %s", stderr)
	}
	if !foundMissing {
		stderr := client.drainStderr()
		t.Errorf("expected diagnostic for missing prop 'Body'\nstderr: %s", stderr)
	}
}

func TestLSP_ComponentPropDiagnostics_BareCall(t *testing.T) {
	projectDir := setupTestProjectWithComponents(t)
	client := startLSP(t, projectDir)

	client.send("initialize", map[string]any{
		"rootUri":      "file://" + projectDir,
		"capabilities": map[string]any{},
	})
	client.recv(t, 10*time.Second)
	client.notify("initialized", map[string]any{})

	// Open a page with a bare component call (no dict args)
	gastroContent := `---
import Card "components/card.gastro"
Title := "Hello"
---
{{ Card }}`

	fileURI := "file://" + filepath.Join(projectDir, "pages", "index.gastro")
	client.notify("textDocument/didOpen", map[string]any{
		"textDocument": map[string]any{
			"uri":        fileURI,
			"languageId": "gastro",
			"version":    1,
			"text":       gastroContent,
		},
	})

	// Collect diagnostics — bare call should produce missing-prop warnings
	foundMissingTitle := false
	foundMissingBody := false
	for attempts := 0; attempts < 5; attempts++ {
		notification := client.recvNotification(t, "textDocument/publishDiagnostics", 3*time.Second)
		if notification == nil {
			break
		}

		params, _ := notification["params"].(map[string]any)
		diags, _ := params["diagnostics"].([]any)

		for _, d := range diags {
			dm, _ := d.(map[string]any)
			msg, _ := dm["message"].(string)
			if strings.Contains(msg, `missing prop "Title"`) {
				foundMissingTitle = true
			}
			if strings.Contains(msg, `missing prop "Body"`) {
				foundMissingBody = true
			}
		}

		if foundMissingTitle && foundMissingBody {
			break
		}
	}
	if !foundMissingTitle {
		stderr := client.drainStderr()
		t.Errorf("expected diagnostic for missing prop 'Title' on bare {{ Card }}\nstderr: %s", stderr)
	}
	if !foundMissingBody {
		stderr := client.drainStderr()
		t.Errorf("expected diagnostic for missing prop 'Body' on bare {{ Card }}\nstderr: %s", stderr)
	}
}

func TestLSP_ComponentHover(t *testing.T) {
	projectDir := setupTestProjectWithComponents(t)
	client := startLSP(t, projectDir)

	client.send("initialize", map[string]any{
		"rootUri":      "file://" + projectDir,
		"capabilities": map[string]any{},
	})
	client.recv(t, 10*time.Second)
	client.notify("initialized", map[string]any{})

	// line 4: {{ Card (dict "Title" .Title) }}
	//              ^--- 'C' is at char 3 (0-indexed), 'Card' is chars 3-6
	gastroContent := "---\nimport Card \"components/card.gastro\"\nTitle := \"Hello\"\n---\n{{ Card (dict \"Title\" .Title) }}"
	fileURI := "file://" + filepath.Join(projectDir, "pages", "index.gastro")

	client.notify("textDocument/didOpen", map[string]any{
		"textDocument": map[string]any{
			"uri":        fileURI,
			"languageId": "gastro",
			"version":    1,
			"text":       gastroContent,
		},
	})

	// Hover on "Card" (line 4, char 4 — inside the component name)
	client.send("textDocument/hover", map[string]any{
		"textDocument": map[string]any{"uri": fileURI},
		"position":     map[string]any{"line": 4, "character": 4},
	})

	resp := client.recv(t, 10*time.Second)
	result := resp["result"]
	if result == nil {
		t.Fatal("expected hover result, got nil")
	}

	rm, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("expected hover result as map, got %T", result)
	}

	contents, _ := rm["contents"].(map[string]any)
	if contents == nil {
		t.Fatal("expected hover contents, got nil")
	}

	value, _ := contents["value"].(string)
	if !strings.Contains(value, "Props struct") {
		t.Errorf("expected hover to show Props struct, got: %s", value)
	}
	if !strings.Contains(value, "Title") || !strings.Contains(value, "string") {
		t.Errorf("expected hover to show Title field, got: %s", value)
	}
	if !strings.Contains(value, "Body") {
		t.Errorf("expected hover to show Body field, got: %s", value)
	}
	if !strings.Contains(value, "components/card.gastro") {
		t.Errorf("expected hover to show component path, got: %s", value)
	}
}

func TestLSP_ComponentDefinition(t *testing.T) {
	projectDir := setupTestProjectWithComponents(t)
	client := startLSP(t, projectDir)

	client.send("initialize", map[string]any{
		"rootUri":      "file://" + projectDir,
		"capabilities": map[string]any{},
	})
	client.recv(t, 10*time.Second)
	client.notify("initialized", map[string]any{})

	// line 4: {{ Card (dict "Title" .Title) }}
	gastroContent := "---\nimport Card \"components/card.gastro\"\nTitle := \"Hello\"\n---\n{{ Card (dict \"Title\" .Title) }}"
	fileURI := "file://" + filepath.Join(projectDir, "pages", "index.gastro")

	client.notify("textDocument/didOpen", map[string]any{
		"textDocument": map[string]any{
			"uri":        fileURI,
			"languageId": "gastro",
			"version":    1,
			"text":       gastroContent,
		},
	})

	// Go-to-definition on "Card" (line 4, char 4)
	client.send("textDocument/definition", map[string]any{
		"textDocument": map[string]any{"uri": fileURI},
		"position":     map[string]any{"line": 4, "character": 4},
	})

	resp := client.recv(t, 10*time.Second)
	result := resp["result"]
	if result == nil {
		t.Fatal("expected definition result, got nil")
	}

	loc, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("expected Location object, got %T", result)
	}

	uri, _ := loc["uri"].(string)
	expectedURI := "file://" + filepath.Join(projectDir, "components", "card.gastro")
	if uri != expectedURI {
		t.Errorf("expected uri=%s, got %s", expectedURI, uri)
	}

	r, _ := loc["range"].(map[string]any)
	start, _ := r["start"].(map[string]any)
	if start["line"] != float64(0) || start["character"] != float64(0) {
		t.Errorf("expected definition at line 0, char 0, got line=%v char=%v", start["line"], start["character"])
	}
}

func TestLSP_SingleDelimiterDiagnostic(t *testing.T) {
	projectDir := setupTestProject(t)
	client := startLSP(t, projectDir)

	client.send("initialize", map[string]any{
		"rootUri":      "file://" + projectDir,
		"capabilities": map[string]any{},
	})
	client.recv(t, 10*time.Second)
	client.notify("initialized", map[string]any{})

	// A file with a single --- delimiter is a parse error
	gastroContent := "---\n<h1>Hello</h1>"
	fileURI := "file://" + filepath.Join(projectDir, "pages", "index.gastro")

	client.notify("textDocument/didOpen", map[string]any{
		"textDocument": map[string]any{
			"uri":        fileURI,
			"languageId": "gastro",
			"version":    1,
			"text":       gastroContent,
		},
	})

	// Wait for a diagnostic about the missing closing delimiter
	var found bool
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		notification := client.recvNotification(t, "textDocument/publishDiagnostics", 2*time.Second)
		if notification == nil {
			continue
		}

		params, _ := notification["params"].(map[string]any)
		diags, _ := params["diagnostics"].([]any)

		for _, d := range diags {
			dm, _ := d.(map[string]any)
			msg, _ := dm["message"].(string)
			if strings.Contains(msg, "missing closing --- delimiter") {
				found = true
				// Should be an error (severity 1)
				if sev, ok := dm["severity"].(float64); !ok || sev != 1 {
					t.Errorf("expected severity=1 (Error), got %v", dm["severity"])
				}
				// Should come from gastro source
				if dm["source"] != "gastro" {
					t.Errorf("expected source='gastro', got %v", dm["source"])
				}
				break
			}
		}
		if found {
			break
		}
	}

	if !found {
		t.Fatal("expected diagnostic for missing closing --- delimiter, got none within deadline")
	}
}

func TestLSP_NoFrontmatterNoDiagnostics(t *testing.T) {
	projectDir := setupTestProject(t)
	client := startLSP(t, projectDir)

	client.send("initialize", map[string]any{
		"rootUri":      "file://" + projectDir,
		"capabilities": map[string]any{},
	})
	client.recv(t, 10*time.Second)
	client.notify("initialized", map[string]any{})

	// A file with no frontmatter should not produce parse errors
	gastroContent := "<h1>Hello World</h1>"
	fileURI := "file://" + filepath.Join(projectDir, "pages", "index.gastro")

	client.notify("textDocument/didOpen", map[string]any{
		"textDocument": map[string]any{
			"uri":        fileURI,
			"languageId": "gastro",
			"version":    1,
			"text":       gastroContent,
		},
	})

	// Collect diagnostics — should have none (or only empty arrays)
	var errorDiags []string
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		notification := client.recvNotification(t, "textDocument/publishDiagnostics", 2*time.Second)
		if notification == nil {
			break
		}

		params, _ := notification["params"].(map[string]any)
		diags, _ := params["diagnostics"].([]any)

		for _, d := range diags {
			dm, _ := d.(map[string]any)
			msg, _ := dm["message"].(string)
			if sev, ok := dm["severity"].(float64); ok && sev == 1 {
				errorDiags = append(errorDiags, msg)
			}
		}
	}

	if len(errorDiags) > 0 {
		t.Errorf("expected no error diagnostics for no-frontmatter file, got: %v", errorDiags)
	}
}

func TestLSP_Formatting(t *testing.T) {
	projectDir := setupTestProject(t)
	client := startLSP(t, projectDir)

	client.send("initialize", map[string]any{
		"rootUri":      "file://" + projectDir,
		"capabilities": map[string]any{},
	})
	resp := client.recv(t, 10*time.Second)

	// Verify the server advertises documentFormattingProvider
	result, _ := resp["result"].(map[string]any)
	caps, _ := result["capabilities"].(map[string]any)
	if caps["documentFormattingProvider"] != true {
		t.Fatalf("expected documentFormattingProvider=true, got %v", caps["documentFormattingProvider"])
	}

	client.notify("initialized", map[string]any{})

	// Open an unformatted .gastro file (messy indentation, no trailing newline)
	// The formatter should normalize indentation to tabs and ensure a trailing newline.
	unformatted := "---\nTitle := \"Hello\"\n---\n  <div>\n      <h1>{{ .Title }}</h1>\n  </div>"
	fileURI := "file://" + filepath.Join(projectDir, "pages", "index.gastro")

	client.notify("textDocument/didOpen", map[string]any{
		"textDocument": map[string]any{
			"uri":        fileURI,
			"languageId": "gastro",
			"version":    1,
			"text":       unformatted,
		},
	})

	// Request formatting
	client.send("textDocument/formatting", map[string]any{
		"textDocument": map[string]any{"uri": fileURI},
		"options":      map[string]any{"tabSize": 4, "insertSpaces": false},
	})

	resp = client.recv(t, 10*time.Second)
	edits, ok := resp["result"].([]any)
	if !ok || len(edits) == 0 {
		t.Fatalf("expected formatting edits, got: %v", resp["result"])
	}

	edit, _ := edits[0].(map[string]any)
	newText, _ := edit["newText"].(string)

	if newText == "" {
		t.Fatal("expected non-empty newText in formatting edit")
	}

	// The formatted output should end with a newline
	if !strings.HasSuffix(newText, "\n") {
		t.Errorf("formatted text should end with newline, got: %q", newText[len(newText)-20:])
	}

	// The formatted output should differ from the input
	if newText == unformatted {
		t.Error("formatted text should differ from unformatted input")
	}
}

func TestLSP_Formatting_NoOp(t *testing.T) {
	projectDir := setupTestProject(t)
	client := startLSP(t, projectDir)

	client.send("initialize", map[string]any{
		"rootUri":      "file://" + projectDir,
		"capabilities": map[string]any{},
	})
	client.recv(t, 10*time.Second)
	client.notify("initialized", map[string]any{})

	// Open an already-formatted file
	formatted := "---\nTitle := \"Hello\"\n---\n<div>\n\t<h1>{{ .Title }}</h1>\n</div>\n"
	fileURI := "file://" + filepath.Join(projectDir, "pages", "index.gastro")

	client.notify("textDocument/didOpen", map[string]any{
		"textDocument": map[string]any{
			"uri":        fileURI,
			"languageId": "gastro",
			"version":    1,
			"text":       formatted,
		},
	})

	client.send("textDocument/formatting", map[string]any{
		"textDocument": map[string]any{"uri": fileURI},
		"options":      map[string]any{"tabSize": 4, "insertSpaces": false},
	})

	resp := client.recv(t, 10*time.Second)

	// Should return empty edits (no changes needed)
	edits, ok := resp["result"].([]any)
	if !ok {
		t.Fatalf("expected result to be an array, got: %T (%v)", resp["result"], resp["result"])
	}
	if len(edits) != 0 {
		t.Errorf("expected empty edits for already-formatted file, got %d edits", len(edits))
	}
}

func TestLSP_SnippetCompletions_ComponentWithProps(t *testing.T) {
	projectDir := setupTestProjectWithComponents(t)
	client := startLSP(t, projectDir)

	// Initialize WITH snippet support
	client.send("initialize", map[string]any{
		"rootUri": "file://" + projectDir,
		"capabilities": map[string]any{
			"textDocument": map[string]any{
				"completion": map[string]any{
					"completionItem": map[string]any{
						"snippetSupport": true,
					},
				},
			},
		},
	})
	client.recv(t, 10*time.Second)
	client.notify("initialized", map[string]any{})

	// Open a page that imports Card; cursor inside {{ }} after "C"
	gastroContent := "---\nimport Card \"components/card.gastro\"\nTitle := \"Hello\"\n---\n<div>{{ C }}</div>"
	fileURI := "file://" + filepath.Join(projectDir, "pages", "index.gastro")

	client.notify("textDocument/didOpen", map[string]any{
		"textDocument": map[string]any{
			"uri":        fileURI,
			"languageId": "gastro",
			"version":    1,
			"text":       gastroContent,
		},
	})

	// Request completion at the "C" position (line 4, char 8)
	client.send("textDocument/completion", map[string]any{
		"textDocument": map[string]any{"uri": fileURI},
		"position":     map[string]any{"line": 4, "character": 8},
	})

	resp := client.recv(t, 10*time.Second)
	items, ok := resp["result"].([]any)
	if !ok {
		t.Fatalf("expected result array, got: %T (%v)", resp["result"], resp["result"])
	}

	// Find the Card completion item
	var cardItem map[string]any
	for _, item := range items {
		m, _ := item.(map[string]any)
		if m["label"] == "Card" {
			cardItem = m
			break
		}
	}
	if cardItem == nil {
		t.Fatal("expected Card completion item")
	}

	// Verify it has snippet format
	insertTextFormat, _ := cardItem["insertTextFormat"].(float64)
	if insertTextFormat != 2 {
		t.Errorf("expected insertTextFormat=2 (Snippet), got %v", cardItem["insertTextFormat"])
	}

	// Verify the insertText contains dict with prop tabstops
	insertText, _ := cardItem["insertText"].(string)
	if !strings.Contains(insertText, "(dict") {
		t.Errorf("expected snippet with (dict, got %q", insertText)
	}
	if !strings.Contains(insertText, `"Title"`) {
		t.Errorf("expected snippet to contain Title prop, got %q", insertText)
	}
	if !strings.Contains(insertText, `"Body"`) {
		t.Errorf("expected snippet to contain Body prop, got %q", insertText)
	}
	if !strings.Contains(insertText, "${1:") {
		t.Errorf("expected snippet tabstops, got %q", insertText)
	}
}

func TestLSP_SnippetCompletions_DisabledWithoutCapability(t *testing.T) {
	projectDir := setupTestProjectWithComponents(t)
	client := startLSP(t, projectDir)

	// Initialize WITHOUT snippet support
	client.send("initialize", map[string]any{
		"rootUri":      "file://" + projectDir,
		"capabilities": map[string]any{},
	})
	client.recv(t, 10*time.Second)
	client.notify("initialized", map[string]any{})

	gastroContent := "---\nimport Card \"components/card.gastro\"\nTitle := \"Hello\"\n---\n<div>{{ C }}</div>"
	fileURI := "file://" + filepath.Join(projectDir, "pages", "index.gastro")

	client.notify("textDocument/didOpen", map[string]any{
		"textDocument": map[string]any{
			"uri":        fileURI,
			"languageId": "gastro",
			"version":    1,
			"text":       gastroContent,
		},
	})

	client.send("textDocument/completion", map[string]any{
		"textDocument": map[string]any{"uri": fileURI},
		"position":     map[string]any{"line": 4, "character": 8},
	})

	resp := client.recv(t, 10*time.Second)
	items, ok := resp["result"].([]any)
	if !ok {
		t.Fatalf("expected result array, got: %T (%v)", resp["result"], resp["result"])
	}

	// Find the Card completion item
	for _, item := range items {
		m, _ := item.(map[string]any)
		if m["label"] == "Card" {
			// Should NOT have insertTextFormat=2
			if m["insertTextFormat"] != nil {
				itf, _ := m["insertTextFormat"].(float64)
				if itf == 2 {
					t.Error("expected no snippet format when client doesn't support snippets")
				}
			}
			// insertText should be plain component name
			insertText, _ := m["insertText"].(string)
			if strings.Contains(insertText, "${") {
				t.Errorf("expected plain text insertText, got snippet syntax: %q", insertText)
			}
			return
		}
	}
	t.Fatal("expected Card completion item")
}
