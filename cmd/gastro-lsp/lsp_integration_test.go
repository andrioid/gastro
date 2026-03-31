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

// These integration tests spawn gastro-lsp as a subprocess and communicate
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

	// Build gastro-lsp binary
	binPath := filepath.Join(t.TempDir(), "gastro-lsp")
	build := exec.Command("go", "build", "-o", binPath, ".")
	build.Dir = filepath.Join(projectRoot(t), "cmd", "gastro-lsp")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building gastro-lsp: %v\n%s", err, out)
	}

	proc := exec.Command(binPath)
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
		t.Fatalf("starting gastro-lsp: %v", err)
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
	// Walk up from cmd/gastro-lsp to find go.mod
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

	// Open a .gastro file
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

	// Request completions in the template body (line 3, after second ---)
	client.send("textDocument/completion", map[string]any{
		"textDocument": map[string]any{"uri": fileURI},
		"position":     map[string]any{"line": 3, "character": 5},
	})

	resp := client.recv(t, 10*time.Second)

	result := resp["result"]
	if result == nil {
		t.Fatal("expected completion result, got nil")
	}

	// Should return an array of completion items
	items, ok := result.([]any)
	if !ok {
		t.Fatalf("expected array of completions, got: %T", result)
	}

	// Should include the exported variable .Title
	found := false
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if m["label"] == ".Title" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected .Title in completions, got: %v", items)
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
