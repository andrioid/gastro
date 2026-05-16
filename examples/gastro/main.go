package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	gastro "gastro-website/.gastro"
	"gastro-website/lspdemo"

	"github.com/google/shlex"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "4242"
	}

	// Boot the live-LSP demo before binding routes so the page
	// handler can pull demo.Frontmatter() / demo.Body() at render
	// time. Boot blocks until the LSP is ready or the timeout fires;
	// a timeout returns a degraded Demo (no hover, static squiggle-
	// free fallback). See lspdemo.Boot for the full lifecycle.
	bootCtx, bootCancel := context.WithCancel(context.Background())
	defer bootCancel()
	demo, err := lspdemo.Boot(bootCtx, resolveBootOptions())
	if err != nil {
		log.Fatalf("lspdemo boot: %v", err)
	}
	defer demo.Close()
	lspdemo.Register(demo)

	mux := http.NewServeMux()

	// Traditional form handler (Post/Redirect/Get)
	mux.HandleFunc("POST /guestbook", handleGuestbookPost)

	// Live-LSP demo hover endpoint (Datastar SSE)
	mux.HandleFunc("GET /api/lsp-demo/hover", newLSPDemoHoverHandler(demo))

	// Datastar SSE endpoints
	mux.HandleFunc("GET /api/ds/search", handleDsSearch)
	mux.HandleFunc("POST /api/ds/add", handleDsAdd)
	mux.HandleFunc("GET /api/ds/edit/{id}", handleDsEdit)
	mux.HandleFunc("POST /api/ds/save/{id}", handleDsSave)

	// HTMX endpoints (plain HTML fragments)
	mux.HandleFunc("GET /api/htmx/search", handleHtmxSearch)
	mux.HandleFunc("POST /api/htmx/add", handleHtmxAdd)
	mux.HandleFunc("GET /api/htmx/edit/{id}", handleHtmxEdit)
	mux.HandleFunc("POST /api/htmx/save/{id}", handleHtmxSave)

	// Gastro page routes (catch-all)
	mux.Handle("/", gastro.Routes())

	fmt.Printf("Listening on http://localhost:%s\n", port)
	log.Fatal(http.ListenAndServe(":"+port, mux))
}

// resolveBootOptions picks up the gastro source path and LSP command
// from the environment, falling back to dev-friendly defaults that
// work when running `go run .` from the examples/gastro directory.
//
// Environment overrides (set in the Dockerfile or by ops):
//
//	GASTRO_SOURCE_ROOT  — absolute path to the gastro module source
//	                      tree on disk. Required at runtime so the
//	                      temp project's go.mod can `replace` the
//	                      gastro module to a real on-disk copy.
//	GASTRO_LSP_CMD      — command line for `gastro lsp` as a single
//	                      string, e.g. "/usr/local/bin/gastro lsp".
//	                      Split on whitespace via strings.Fields.
func resolveBootOptions() lspdemo.BootOptions {
	source := os.Getenv("GASTRO_SOURCE_ROOT")
	if source == "" {
		// Dev fallback: from examples/gastro the repo root is
		// ../.. relative to cwd. Resolve to an absolute path so
		// the go.mod's `replace` directive is unambiguous.
		if cwd, err := os.Getwd(); err == nil {
			if abs, err := filepath.Abs(filepath.Join(cwd, "..", "..")); err == nil {
				source = abs
			}
		}
	}

	cmd := []string{"gastro", "lsp"}
	if raw := os.Getenv("GASTRO_LSP_CMD"); raw != "" {
		// Use shlex (not strings.Fields) so operators can use
		// quoting for paths with spaces, e.g.
		//   GASTRO_LSP_CMD='"/opt/my tools/gastro" lsp'
		// strings.Fields would silently mangle that into four
		// args, then exec.Command would fail with a confusing
		// "file not found" pointing at the first token.
		parsed, err := shlex.Split(raw)
		if err != nil {
			log.Fatalf("GASTRO_LSP_CMD: invalid shell syntax: %v", err)
		}
		if len(parsed) == 0 {
			log.Fatalf("GASTRO_LSP_CMD: parsed to empty command")
		}
		cmd = parsed
	} else if _, err := os.Stat("go.mod"); err == nil && source != "" {
		// Dev fallback: no installed `gastro` binary, run from
		// source. `go run` adds ~500ms of warm-up per invocation
		// (we only spawn once at app boot so it's fine).
		cmd = []string{"go", "run", "github.com/andrioid/gastro/cmd/gastro", "lsp"}
	}

	return lspdemo.BootOptions{
		GastroSourceRoot: source,
		LSPCommand:       cmd,
		BootTimeout:      15 * time.Second,
	}
}


