package main

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// setupGastroAppProject creates a library-mode project whose app serves
// the generated gastro router, under the repo's tmp/ (see
// setupLibProject for why not t.TempDir). The module resolves gastro to
// this checkout.
func setupGastroAppProject(t *testing.T) string {
	t.Helper()
	root := setupLibProject(t)
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	goSum, err := os.ReadFile(filepath.Join(repoRoot, "go.sum"))
	if err != nil {
		t.Fatalf("read go.sum: %v", err)
	}

	mustWriteT(t, filepath.Join(root, "go.mod"), "module testapp\n\n"+
		"go 1.27.2\n\n"+
		"require github.com/andrioid/gastro v0.0.0\n\n"+
		"replace github.com/andrioid/gastro => "+repoRoot+"\n")
	mustWriteT(t, filepath.Join(root, "go.sum"), string(goSum))
	mustWriteT(t, filepath.Join(root, "cmd", "myapp", "main.go"), `package main

import (
	"net/http"
	"os"

	gastro "testapp/.gastro"
)

func main() {
	if err := http.ListenAndServe(":"+os.Getenv("PORT"), gastro.New().Handler()); err != nil {
		panic(err)
	}
}
`)
	mustWriteT(t, filepath.Join(root, "components", "card.gastro"),
		"---\ntype Props struct { Title string }\nTitle := gastro.Props().Title\n---\n<p>{{ .Title }}</p>\n")
	mustWriteT(t, filepath.Join(root, "pages", "index.gastro"),
		"---\nimport Card \"components/card.gastro\"\n---\n{{ Card (dict \"Title\" \"first\") }}\n")
	return root
}

// TestWatch_GenerateErrorEmitsBuildErrorAndKeepsServing: introducing a
// bad dict key under `gastro watch` sends the positioned diagnostic to
// the browser as a build-error event, and the running app keeps serving
// the last good build (dev mode reads templates from .gastro/ on every
// request, so a partially written tree would show up immediately).
func TestWatch_GenerateErrorEmitsBuildErrorAndKeepsServing(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test — spawns processes")
	}
	root := setupGastroAppProject(t)

	port := freePort(t)
	t.Setenv("PORT", fmt.Sprint(port))
	t.Setenv("GASTRO_DEV_ROOT", root)

	h := startWatch(t, root,
		"--debounce", "30ms",
		"--build", "go build -o tmp/app ./cmd/myapp",
		"--run", "tmp/app",
	)
	if err := waitForServer(t, port, coldStartTimeout); err != nil {
		t.Fatalf("initial server never came up: %v", err)
	}
	if body := getBody(t, port); !strings.Contains(body, "<p>first</p>") {
		t.Fatalf("initial page = %q, want it to render the card", body)
	}

	// Subscribe to the dev-reload stream before breaking the page.
	ctx, cancel := context.WithTimeout(context.Background(), integrationTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/__gastro/reload", port), nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("subscribe to dev reload: %v", err)
	}
	defer resp.Body.Close()
	lines := make(chan string)
	go func() {
		defer close(lines)
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			select {
			case lines <- sc.Text():
			case <-ctx.Done():
				return
			}
		}
	}()
	// Give the server a moment to register the subscriber.
	time.Sleep(200 * time.Millisecond)

	touchLater(t, filepath.Join(root, "pages", "index.gastro"),
		"---\nimport Card \"components/card.gastro\"\n---\n{{ Card (dict \"Titel\" \"second\") }}\n")

	const wantData = `data: pages/index.gastro:4:15: error: unknown prop "Titel" on component Card`
	var inBuildError, found bool
	for line := range lines {
		switch {
		case line == "event: build-error":
			inBuildError = true
		case line == "":
			inBuildError = false
		case inBuildError && strings.HasPrefix(line, wantData):
			found = true
		}
		if found {
			break
		}
	}
	if !found {
		t.Fatalf("no build-error event carrying %q before the timeout", wantData)
	}

	if body := getBody(t, port); !strings.Contains(body, "<p>first</p>") {
		t.Errorf("after the failed generate the page = %q, want the last good build", body)
	}
	h.Stop()
}
