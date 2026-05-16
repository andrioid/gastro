package main

import (
	"context"
	"fmt"
	"html/template"
	"net/http"
	"strconv"
	"time"

	"gastro-website/lspdemo"
	"gastro-website/md"

	"github.com/andrioid/gastro/pkg/gastro/datastar"
)

// handleLSPDemoHover serves the live-LSP demo's hover tooltip.
//
// The handler is wired up only when lspdemo.Boot returned a
// non-degraded Demo (see main.go). It accepts LSP-style 0-indexed
// coordinates via ?l= and ?c=, queries the LSP, renders the
// returned markdown via md.Render, and patches the tooltip element
// via Datastar SSE.
//
// On every code path \u2014 success, no-hover, error \u2014 the response
// patches #lsp-tooltip. An empty patch hides the tooltip (the CSS
// uses the absence of a child element as the "hidden" signal),
// which matches the mouseleave behaviour client-side.
// maxHoverPosition caps the line/column query parameters to a sane
// upper bound. The embedded demo file is ~12 lines long; anything
// beyond a few thousand is a probe or a bug. Capping cheaply means
// the hover cache (in lspdemo.Demo) can't be poisoned with arbitrary
// (line, char) pairs by a remote attacker spinning through the
// int32 space.
const maxHoverPosition = 10_000

// maxHoverMarkdownBytes bounds the markdown body fed to goldmark.
// gopls normally returns <2 KB; anything larger is almost certainly
// a doc dump from a transitive dep and not worth shipping over SSE.
const maxHoverMarkdownBytes = 64 * 1024

func newLSPDemoHoverHandler(demo *lspdemo.Demo) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		line, err1 := strconv.Atoi(r.URL.Query().Get("l"))
		col, err2 := strconv.Atoi(r.URL.Query().Get("c"))
		if err1 != nil || err2 != nil ||
			line < 0 || col < 0 ||
			line > maxHoverPosition || col > maxHoverPosition {
			http.Error(w, "l and c must be integers in [0, 10000]", http.StatusBadRequest)
			return
		}

		// Bound LSP calls so a stuck gopls can't tie up an HTTP
		// goroutine forever. 2s is generous \u2014 typical hover
		// roundtrips are 5-50ms.
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()

		markdown, err := demo.HoverOrDiagnostic(ctx, line, col)
		sse := datastar.NewSSE(w, r)
		if err != nil || markdown == "" {
			// Empty tooltip. Datastar replaces the element's
			// contents, so this clears whatever was last shown.
			sse.PatchElements(emptyTooltip())
			return
		}

		// Defensive cap: a pathologically large hover body would
		// still be safely escaped by goldmark, but capping here
		// avoids spending CPU on a megabyte of doc text and keeps
		// the SSE frame small.
		if len(markdown) > maxHoverMarkdownBytes {
			markdown = markdown[:maxHoverMarkdownBytes] + "\n\n... (truncated)"
		}

		rendered, mdErr := md.Render(markdown)
		if mdErr != nil {
			// Fall back to plain-text so the visitor at least sees
			// the LSP's response. md.Render only errors on
			// pathological input.
			rendered = template.HTML("<pre>" + template.HTMLEscapeString(markdown) + "</pre>")
		}
		sse.PatchElements(tooltipHTML(rendered))
	}
}

// tooltipShell renders the always-the-same outer <div> with the
// Datastar bindings. Both the populated and empty tooltip patches
// share the same shell so the bindings stay attached across SSE
// replacements. Must stay in sync with the initial markup in
// pages/index.gastro — same id, classes, and bindings.
const tooltipShell = `<div id="lsp-tooltip" class="lsp-hover" ` +
	`data-class:visible="$hover.show" ` +
	`data-style:--lsp-x="$hover.x + 'px'" ` +
	`data-style:--lsp-y="$hover.y + 'px'">%s</div>`

func tooltipHTML(body template.HTML) string {
	return fmt.Sprintf(tooltipShell, fmt.Sprintf(`<div class="lsp-hover-body">%s</div>`, body))
}

func emptyTooltip() string {
	return fmt.Sprintf(tooltipShell, "")
}
