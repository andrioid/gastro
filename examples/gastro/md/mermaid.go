package md

// Mermaid extension for goldmark. Replaces fenced code blocks tagged
// `mermaid` with a custom AST node that renders to
// `<pre class="mermaid">…</pre>`. The browser-side mermaid.js loader
// (see components/layout.gastro) finds those elements and swaps in
// SVG diagrams on first paint.
//
// Why an AST transformer rather than a higher-priority FencedCodeBlock
// renderer? goldmark-highlighting registers its own renderer for
// FencedCodeBlock, and goldmark's renderer registry is last-write-wins
// per kind. Overriding by priority works, but then *every* code block
// has to either be re-implemented in our renderer or routed back to
// chroma — awkward. Replacing only the mermaid nodes with a distinct
// AST kind leaves chroma's renderer alone and keeps the two concerns
// independent.
//
// No new Go dependencies: this uses only the goldmark APIs already
// pulled in for syntax highlighting.

import (
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// kindMermaid is the AST node kind used for mermaid blocks.
var kindMermaid = ast.NewNodeKind("MermaidBlock")

// mermaidBlock is a leaf block holding the raw mermaid source (the
// fenced block's content lines, verbatim). Rendering is trivial: emit
// the content inside `<pre class="mermaid">` so mermaid.js can find
// it and render an SVG client-side.
type mermaidBlock struct {
	ast.BaseBlock
}

func (n *mermaidBlock) Kind() ast.NodeKind { return kindMermaid }

func (n *mermaidBlock) Dump(source []byte, level int) {
	ast.DumpHelper(n, source, level, nil, nil)
}

// mermaidTransformer walks the parsed AST and rewrites every
// `FencedCodeBlock` whose info string starts with "mermaid" into a
// mermaidBlock. Running as a parser-stage transformer (rather than at
// render time) means downstream renderers — including the chroma
// highlighter — never see these nodes at all.
type mermaidTransformer struct{}

func (t *mermaidTransformer) Transform(doc *ast.Document, reader text.Reader, _ parser.Context) {
	source := reader.Source()

	// Collect first, mutate after: replacing children mid-walk would
	// invalidate the iteration.
	var targets []*ast.FencedCodeBlock
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		cb, ok := n.(*ast.FencedCodeBlock)
		if !ok {
			return ast.WalkContinue, nil
		}
		if string(cb.Language(source)) != "mermaid" {
			return ast.WalkContinue, nil
		}
		targets = append(targets, cb)
		return ast.WalkSkipChildren, nil
	})

	for _, cb := range targets {
		mb := &mermaidBlock{}
		mb.SetLines(cb.Lines())
		parent := cb.Parent()
		if parent == nil {
			continue
		}
		parent.ReplaceChild(parent, cb, mb)
	}
}

// mermaidNodeRenderer renders mermaidBlock to HTML. The output is the
// raw mermaid source (HTML-escaped for safety) wrapped in a
// `<pre class="mermaid">` element. mermaid.js' default scanner picks
// up that selector and replaces the element with an SVG.
type mermaidNodeRenderer struct{}

func (r *mermaidNodeRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(kindMermaid, r.render)
}

func (r *mermaidNodeRenderer) render(w util.BufWriter, source []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	_, _ = w.WriteString(`<pre class="mermaid">`)
	lines := n.Lines()
	for i := 0; i < lines.Len(); i++ {
		seg := lines.At(i)
		_, _ = w.Write(util.EscapeHTML(seg.Value(source)))
	}
	_, _ = w.WriteString("</pre>\n")
	return ast.WalkSkipChildren, nil
}

// mermaidExt wires the transformer and the renderer into a goldmark
// instance. It implements goldmark.Extender so it can be passed via
// `goldmark.WithExtensions(...)`.
type mermaidExt struct{}

// NewMermaid returns a goldmark extension that renders fenced
// ```mermaid blocks as `<pre class="mermaid">` elements. Pair with a
// client-side mermaid.js loader (see components/layout.gastro for the
// lazy loader used by this example site).
func NewMermaid() goldmark.Extender { return &mermaidExt{} }

func (e *mermaidExt) Extend(m goldmark.Markdown) {
	m.Parser().AddOptions(parser.WithASTTransformers(
		util.Prioritized(&mermaidTransformer{}, 0),
	))
	m.Renderer().AddOptions(renderer.WithNodeRenderers(
		util.Prioritized(&mermaidNodeRenderer{}, 0),
	))
}
