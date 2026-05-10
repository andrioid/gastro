// Package md renders markdown to HTML using goldmark + chroma syntax
// highlighting. Copy and modify for your own gastro project; see
// docs/markdown.md for the rationale on why the framework no longer
// ships a built-in markdown helper.
package md

import (
	"bytes"
	"fmt"
	"html/template"

	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	highlighting "github.com/yuin/goldmark-highlighting/v2"
)

// mdRenderer is created once at package init. goldmark renderers are
// safe for concurrent use across goroutines once configured, so a
// single shared instance is fine for a static-content site.
//
// (Named `mdRenderer` rather than `renderer` to avoid shadowing the
// `github.com/yuin/goldmark/renderer` import pulled in by the mermaid
// extension in mermaid.go.)
var mdRenderer = goldmark.New(
	goldmark.WithExtensions(
		extension.GFM,
		extension.Footnote,
		highlighting.NewHighlighting(
			highlighting.WithStyle("github"),
			highlighting.WithFormatOptions(chromahtml.WithClasses(true)),
		),
		// Mermaid: ```mermaid fences become <pre class="mermaid">…</pre>
		// and are rendered client-side by mermaid.js (loaded lazily in
		// components/layout.gastro). See md/mermaid.go.
		NewMermaid(),
	),
)

// MustRender converts markdown source to HTML, panicking on error.
// Intended for use at package init via:
//
//	//gastro:embed page.md
//	var pageRaw string
//	var pageHTML = md.MustRender(pageRaw)
//
// A panic at init is a deliberate fail-fast: a malformed .md file means
// the binary won't start, surfacing the problem during deploy rather
// than at first request. Run `go build ./...` or `go tool gastro check`
// in CI to catch regressions before they reach prod.
func MustRender(src string) template.HTML {
	h, err := Render(src)
	if err != nil {
		panic(fmt.Errorf("md: %w", err))
	}
	return h
}

// Render converts markdown source to HTML, returning the rendered
// template.HTML and any error from goldmark. Use Render (rather than
// MustRender) for dynamic markdown loaded per-request from a database
// or filesystem, where a render failure should produce a 500 rather
// than panic.
func Render(src string) (template.HTML, error) {
	var buf bytes.Buffer
	if err := mdRenderer.Convert([]byte(src), &buf); err != nil {
		return "", err
	}
	return template.HTML(buf.String()), nil
}
