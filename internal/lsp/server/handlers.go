package server

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
)

type initializeParams struct {
	RootURI      string `json:"rootUri"`
	RootPath     string `json:"rootPath"`
	Capabilities struct {
		TextDocument struct {
			Completion struct {
				CompletionItem struct {
					SnippetSupport bool `json:"snippetSupport"`
				} `json:"completionItem"`
			} `json:"completion"`
		} `json:"textDocument"`
	} `json:"capabilities"`
}

func (s *server) handleInitialize(msg *jsonRPCMessage) *jsonRPCMessage {
	var params initializeParams
	json.Unmarshal(msg.Params, &params)

	s.snippetSupport = params.Capabilities.TextDocument.Completion.CompletionItem.SnippetSupport

	// Determine project root
	s.projectDir = uriToPath(params.RootURI)
	if s.projectDir == "" {
		s.projectDir = params.RootPath
	}
	if s.projectDir == "" {
		s.projectDir, _ = os.Getwd()
	}
	log.Printf("project dir (fallback root): %s", s.projectDir)

	// Validate GASTRO_PROJECT once at startup so a typo gets a single,
	// loud warning instead of silently being ignored on every file open.
	// findProjectRoot also re-checks per call (and stays silent there).
	if env := os.Getenv("GASTRO_PROJECT"); env != "" {
		abs, absErr := filepath.Abs(env)
		if absErr != nil {
			log.Printf("warning: GASTRO_PROJECT=%q is not a valid path: %v (falling back to heuristic)", env, absErr)
		} else if info, err := os.Stat(abs); err != nil {
			log.Printf("warning: GASTRO_PROJECT=%q does not exist: %v (falling back to heuristic)", env, err)
		} else if !info.IsDir() {
			log.Printf("warning: GASTRO_PROJECT=%q is not a directory (falling back to heuristic)", env)
		} else {
			log.Printf("GASTRO_PROJECT=%s (pinning all .gastro files to this root)", abs)
		}
	}

	// Instances are created lazily when files are opened (see instanceForURI)

	return &jsonRPCMessage{
		JSONRPC: "2.0",
		ID:      msg.ID,
		Result: map[string]any{
			"capabilities": map[string]any{
				"textDocumentSync": 1, // Full sync
				"completionProvider": map[string]any{
					"triggerCharacters": []string{".", "{", "|"},
				},
				"hoverProvider":              true,
				"definitionProvider":         true,
				"documentFormattingProvider": true,
			},
			"serverInfo": map[string]any{
				"name":    "gastro-lsp",
				"version": s.version,
			},
		},
	}
}

type didOpenParams struct {
	TextDocument struct {
		URI        string `json:"uri"`
		LanguageID string `json:"languageId"`
		Version    int    `json:"version"`
		Text       string `json:"text"`
	} `json:"textDocument"`
}

func (s *server) handleDidOpen(msg *jsonRPCMessage) {
	var params didOpenParams
	json.Unmarshal(msg.Params, &params)

	uri := params.TextDocument.URI

	s.dataMu.Lock()
	s.documents[uri] = params.TextDocument.Text
	s.dataMu.Unlock()

	log.Printf("opened: %s", uri)

	// Sync to gopls first so it has the virtual file before we ask it for
	// variable types. queryVariableTypes works by sending hover requests
	// against `_ = VarName` lines in the virtual source — those return
	// empty unless gopls has loaded the file. Even so, gopls finishes
	// analysis asynchronously, so the initial run here will typically
	// still observe an empty typeMap; runTemplateDiagnostics flags the
	// URI stale and handleGoplsNotification re-runs once gopls publishes
	// its first diagnostic set for the virtual file.
	s.syncToGopls(uri, params.TextDocument.Text)
	s.runTemplateDiagnostics(uri, params.TextDocument.Text)
}

type didChangeParams struct {
	TextDocument struct {
		URI     string `json:"uri"`
		Version int    `json:"version"`
	} `json:"textDocument"`
	ContentChanges []struct {
		Text string `json:"text"`
	} `json:"contentChanges"`
}

func (s *server) handleDidChange(msg *jsonRPCMessage) {
	var params didChangeParams
	json.Unmarshal(msg.Params, &params)

	if len(params.ContentChanges) > 0 {
		uri := params.TextDocument.URI
		content := params.ContentChanges[0].Text

		s.dataMu.Lock()
		s.documents[uri] = content
		delete(s.typeCache, uri) // invalidate caches on change
		delete(s.fieldCache, uri)
		delete(s.typeFieldCache, uri)
		delete(s.templateDiagsRetries, uri)
		s.invalidateComponentPropsCache(uri)
		s.dataMu.Unlock()

		// Push the new content to gopls before running template diagnostics:
		// queryVariableTypes hovers against the virtual file, so gopls needs
		// the latest content for type info to reflect the user's edits. If
		// gopls hasn't finished re-analysing yet, runTemplateDiagnostics
		// marks the URI stale and handleGoplsNotification re-runs once gopls
		// publishes its updated diagnostic set.
		s.syncToGopls(uri, content)
		s.runTemplateDiagnostics(uri, content)
	}
}

type didCloseParams struct {
	TextDocument struct {
		URI string `json:"uri"`
	} `json:"textDocument"`
}

func (s *server) handleDidClose(msg *jsonRPCMessage) {
	var params didCloseParams
	json.Unmarshal(msg.Params, &params)
	uri := params.TextDocument.URI

	s.dataMu.Lock()
	delete(s.documents, uri)
	delete(s.goplsDiags, uri)
	delete(s.templateDiags, uri)
	delete(s.typeCache, uri)
	delete(s.fieldCache, uri)
	delete(s.typeFieldCache, uri)
	delete(s.templateDiagsStale, uri)
	delete(s.templateDiagsRetries, uri)
	delete(s.templateDiagsSkipLogAt, uri)
	s.invalidateComponentPropsCache(uri)
	s.dataMu.Unlock()
}
