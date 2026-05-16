package lspdemo

import (
	"fmt"
	"html"
	"html/template"
	"strings"

	"gastro-website/lspclient"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
)

// Renderer turns the embedded demo .gastro source into the homepage
// live-LSP window: a single macOS-style code window showing the entire
// .gastro file, with hoverable identifier spans and absolute-positioned
// squiggle overlays for diagnostics. It does NOT talk to the LSP
// itself — the diagnostics are snapshotted at app boot, passed in here,
// and baked into the SSR output.
//
// Each identifier in the rendered source becomes a hoverable span
// carrying its (line, character) position in the file's coordinates
// (0-indexed LSP-style). Because the rendered window starts at file
// line 0 (the opening `---`), those coordinates double as both the
// LSP query position and the squiggle's CSS line offset.
type Renderer struct {
	source      string
	diagnostics []lspclient.Diagnostic

	// Split coordinates in 0-indexed LSP-style line numbers.
	frontmatterStart int // first line of frontmatter content (after opening `---`)
	frontmatterEnd   int // exclusive: line of the closing `---`
	bodyStart        int // first line of body content (after closing `---`)
	bodyEnd          int // exclusive: one past last line
}

// NewRenderer parses source into a frontmatter/body split and stores
// the diagnostics snapshot. Returns an error if the source isn't a
// valid `---` ... `---` ...body shape (the embedded demo file always
// is; the error path is for future safety).
func NewRenderer(source string, diagnostics []lspclient.Diagnostic) (*Renderer, error) {
	lines := strings.Split(source, "\n")

	// Locate the two --- delimiters.
	delims := make([]int, 0, 2)
	for i, line := range lines {
		if strings.TrimSpace(line) == "---" {
			delims = append(delims, i)
			if len(delims) == 2 {
				break
			}
		}
	}
	if len(delims) != 2 {
		return nil, fmt.Errorf("lspdemo: source must contain two `---` delimiters, found %d", len(delims))
	}

	return &Renderer{
		source:           source,
		diagnostics:      diagnostics,
		frontmatterStart: delims[0] + 1,
		frontmatterEnd:   delims[1],
		bodyStart:        delims[1] + 1,
		bodyEnd:          len(lines),
	}, nil
}

// diagnosticAt returns the message of the first diagnostic whose
// range covers (line, char), or "" if no diagnostic does. Used by
// HoverOrDiagnostic to surface diagnostic text on identifiers gopls
// can't hover. LSP positions are 0-indexed; the range comparison
// treats end as exclusive (the LSP convention).
func (r *Renderer) diagnosticAt(line, char int) string {
	for _, d := range r.diagnostics {
		if positionInRange(d.Range, line, char) {
			return d.Message
		}
	}
	return ""
}

// positionInRange reports whether (line, char) falls inside the
// half-open range [Start, End). Multi-line ranges treat the start
// and end lines specially; intermediate lines are fully inside.
func positionInRange(rng lspclient.Range, line, char int) bool {
	if line < rng.Start.Line || line > rng.End.Line {
		return false
	}
	if line == rng.Start.Line && char < rng.Start.Character {
		return false
	}
	if line == rng.End.Line && char >= rng.End.Character {
		return false
	}
	return true
}

// Render returns the highlighted code + squiggle overlays for the
// entire .gastro file. The macOS-style window chrome (traffic-light
// bar, filename, dark background) is supplied by the shared
// <CodeWindow> component; the page template wraps this output:
//
//	{{ wrap CodeWindow (dict "Title" "components/greeting.gastro") }}
//	    {{ .LSPDemoFile }}
//	{{ end }}
//
// The output is: the opening `---`, the frontmatter (Go-lexed), the
// closing `---`, the body (HTML-lexed with `{{ }}` template actions
// hand-walked for hoverable field references), then absolutely-
// positioned squiggle <span>s. All identifier hover spans and
// squiggle overlays carry file-absolute coordinates so the LSP query
// position and the squiggle's CSS line offset are the same number.
func (r *Renderer) Render() template.HTML {
	lines := strings.Split(r.source, "\n")

	// Build the highlighted-code stream by concatenating, in file order:
	//   line 0:                 opening `---`     (CommentPreproc class)
	//   lines [fmStart..fmEnd): frontmatter Go    (renderGoChunk)
	//   line fmEnd:             closing `---`     (CommentPreproc class)
	//   lines [bodyStart..end): body HTML+actions (renderBodyChunk)
	//
	// Joining with `\n` keeps the visual layout intact while the
	// chunks themselves carry no leading/trailing newlines.
	delimSpan := `<span class="cp">---</span>`

	fmChunk := strings.Join(lines[r.frontmatterStart:r.frontmatterEnd], "\n")
	bodyChunk := strings.Join(lines[r.bodyStart:r.bodyEnd], "\n")

	var code strings.Builder
	code.WriteString(delimSpan)
	code.WriteString("\n")
	code.WriteString(renderGoChunk(fmChunk, r.frontmatterStart))
	code.WriteString("\n")
	code.WriteString(delimSpan)
	if r.bodyStart < r.bodyEnd {
		code.WriteString("\n")
		code.WriteString(renderBodyChunk(bodyChunk, r.bodyStart))
	}

	// Squiggle overlays for every diagnostic. Coordinates are
	// file-absolute (0-indexed LSP-style); the CSS
	//   top: calc(1.25rem + var(--lsp-line) * 1lh)
	// lands the wave on the right line because the <pre> starts at
	// file line 0 (the opening `---`) and 1.25rem matches the
	// .code-window-body padding-top.
	var squiggles strings.Builder
	for _, d := range r.diagnostics {
		startCol := d.Range.Start.Character
		endCol := d.Range.End.Character
		if d.Range.End.Line != d.Range.Start.Line {
			// Multi-line diagnostic: cap at the end of the start line.
			lineLen := 0
			if d.Range.Start.Line < len(lines) {
				lineLen = len(lines[d.Range.Start.Line])
			}
			endCol = lineLen
		}
		width := endCol - startCol
		if width <= 0 {
			width = 1
		}
		fmt.Fprintf(&squiggles,
			`<span class="lsp-squiggle" style="--lsp-line:%d;--lsp-col:%d;--lsp-width:%d" title="%s"></span>`,
			d.Range.Start.Line, startCol, width, html.EscapeString(d.Message))
	}

	// Inner content only: code <pre> + squiggle overlays. The page
	// template wraps this in <CodeWindow> for the chrome.
	//
	// `chroma` class on the <pre> so existing per-token color rules
	// (.chroma .kd, .chroma .nx, etc.) in tailwind.css apply without
	// needing a parallel rule set. The `lsp-code` class scopes the
	// squiggle font-metric overrides so 1ch / 1lh resolve against the
	// monospace cell size, matching the rendered glyph grid.
	var b strings.Builder
	fmt.Fprintf(&b, `<pre class="lsp-code chroma"><code>%s</code></pre>`, code.String())
	b.WriteString(squiggles.String())
	return template.HTML(b.String())
}

// -----------------------------------------------------------------------------
// Go frontmatter rendering
// -----------------------------------------------------------------------------

// renderGoChunk lexes chunk as Go source and emits chroma-styled HTML.
// Identifier tokens (any chroma.Name subcategory) are wrapped in
// hoverable .lsp-ident spans carrying their ORIGINAL file (line, col)
// in 0-indexed LSP coordinates.
//
// originLine is the 0-indexed line in the full source where chunk
// starts; we offset every token's line by this to recover file coords.
func renderGoChunk(chunk string, originLine int) string {
	lexer := lexers.Get("go")
	if lexer == nil {
		return html.EscapeString(chunk)
	}
	lexer = chroma.Coalesce(lexer)
	it, err := lexer.Tokenise(nil, chunk)
	if err != nil {
		return html.EscapeString(chunk)
	}

	var out strings.Builder
	line, col := 0, 0
	for tok := it(); tok != chroma.EOF; tok = it() {
		emitToken(&out, tok, line+originLine, col)
		// Advance our (line, col) tracker by the token's value.
		line, col = advance(line, col, tok.Value)
	}
	return out.String()
}

// emitToken writes one chroma token as HTML. If the token is a Name
// (any subcategory), it's wrapped in an .lsp-ident hover span.
func emitToken(out *strings.Builder, tok chroma.Token, line, col int) {
	escaped := html.EscapeString(tok.Value)

	if isHoverable(tok.Type) {
		out.WriteString(hoverableSpan(escaped, chromaClass(tok.Type), line, col))
		return
	}

	cls := chromaClass(tok.Type)
	if cls == "" {
		out.WriteString(escaped)
		return
	}
	fmt.Fprintf(out, `<span class="%s">%s</span>`, cls, escaped)
}

// isHoverable returns true for token types we want LSP hover for.
// All Name* tokens qualify; keywords, punctuation, comments, literals,
// and whitespace do not.
func isHoverable(t chroma.TokenType) bool {
	return t.Category() == chroma.Name
}

// chromaClass returns the chroma CSS class for a token type, e.g.
// "n", "nf", "kd". Empty string for token types chroma styles
// implicitly (whitespace, text, error). Mirrors the
// (*html.Formatter).class() logic.
func chromaClass(t chroma.TokenType) string {
	for tt := t; tt != 0; tt = tt.Parent() {
		if cls, ok := chroma.StandardTypes[tt]; ok {
			if cls != "" {
				return cls
			}
			return ""
		}
	}
	return chroma.StandardTypes[t]
}

// advance walks value char-by-char, updating (line, col) the same way
// an editor cursor would. Tabs count as 1 column to match the LSP
// convention (utf-16 code units, but for ASCII source the difference
// doesn't matter).
func advance(line, col int, value string) (int, int) {
	for _, r := range value {
		if r == '\n' {
			line++
			col = 0
			continue
		}
		col++
	}
	return line, col
}

// hoverableSpan returns the wrapper for one identifier token.
//
// The data-on:mouseenter handler reads data-l / data-c off the
// element at event time, so all spans can share the same handler
// string regardless of position. The 150ms debounce matches the
// 'Interaction' decision in the plan. mouseleave hides the tooltip
// immediately; the next mouseenter on another span replaces its
// content.
func hoverableSpan(text, chromaCls string, line, col int) string {
	// Build space-separated class list: lsp-ident + chroma class (if any).
	cls := "lsp-ident"
	if chromaCls != "" {
		cls += " " + chromaCls
	}

	// The mouseenter expression is intentionally inline rather than a
	// helper script: keeping it next to the data attribute makes the
	// span self-contained, and Datastar v1 doesn't have an event-
	// delegation primitive that would let us factor it onto the
	// wrapper div. Inline cost is ~330 bytes per span; demo has on
	// the order of 30 spans so the markup overhead is bounded.
	//
	// Coordinates are computed relative to the .lsp-demo-wrap (which
	// is the tooltip's offset parent) by subtracting the wrap's rect
	// from the target's. Using offsetLeft/offsetTop directly would
	// give window-local coords, mismatched against the tooltip's
	// containing block.
	const expr = "const w = evt.target.closest('.lsp-demo-wrap').getBoundingClientRect(); " +
		"const t = evt.target.getBoundingClientRect(); " +
		"$hover.x = t.left - w.left + t.width/2; " +
		"$hover.y = t.top - w.top + t.height; " +
		"$hover.show = true; " +
		"@get('/api/lsp-demo/hover?l=' + evt.target.dataset.l + '&c=' + evt.target.dataset.c)"

	return fmt.Sprintf(
		`<span class="%s" data-l="%d" data-c="%d" data-on:mouseenter__debounce.150ms="%s" data-on:mouseleave="$hover.show = false">%s</span>`,
		cls, line, col, html.EscapeString(expr), text,
	)
}

// -----------------------------------------------------------------------------
// HTML body rendering
// -----------------------------------------------------------------------------

// renderBodyChunk emits the template body. HTML tags get chroma's
// html-lexer colouring; `{{ ... }}` actions are walked manually so
// any `.Ident` reference becomes a hover span. We split the chunk
// at `{{` / `}}` boundaries and run chroma only on the HTML slices,
// avoiding a fight with the html lexer over template syntax it
// doesn't understand.
//
// originLine is the 0-indexed line in the full source where chunk
// starts; identifier hover spans inside `{{ }}` actions are emitted
// with file-absolute coordinates by adding originLine to the
// chunk-local line counter.
func renderBodyChunk(chunk string, originLine int) string {
	var out strings.Builder
	line, col := 0, 0
	i := 0
	segStart := 0
	flushHTML := func(endExclusive int) {
		if segStart >= endExclusive {
			return
		}
		out.WriteString(highlightHTML(chunk[segStart:endExclusive]))
		segStart = endExclusive
	}
	for i < len(chunk) {
		if i+1 < len(chunk) && chunk[i] == '{' && chunk[i+1] == '{' {
			flushHTML(i)
			end := strings.Index(chunk[i:], "}}")
			if end < 0 {
				// Unmatched — emit the rest as plain text.
				out.WriteString(html.EscapeString(chunk[i:]))
				return out.String()
			}
			actionEnd := i + end + 2 // include closing }}
			action := chunk[i:actionEnd]
			emitTemplateAction(&out, action, line+originLine, col)
			line, col = advance(line, col, action)
			i = actionEnd
			segStart = i
			continue
		}

		ch := chunk[i]
		if ch == '\n' {
			line++
			col = 0
		} else {
			col++
		}
		i++
	}
	flushHTML(len(chunk))
	return out.String()
}

// highlightHTML runs chroma's html lexer over src and returns the
// chroma-classed HTML. No identifier wrapping happens here — HTML
// tag/attribute names aren't hover targets in this demo. The
// chroma lexer emits class names like `nt` (tag name), `na`
// (attribute), `p` (punctuation), already styled by tailwind.css's
// `.chroma` ruleset.
func highlightHTML(src string) string {
	lexer := lexers.Get("html")
	if lexer == nil {
		return html.EscapeString(src)
	}
	lexer = chroma.Coalesce(lexer)
	it, err := lexer.Tokenise(nil, src)
	if err != nil {
		return html.EscapeString(src)
	}
	var out strings.Builder
	for tok := it(); tok != chroma.EOF; tok = it() {
		cls := chromaClass(tok.Type)
		escaped := html.EscapeString(tok.Value)
		if cls == "" {
			out.WriteString(escaped)
			continue
		}
		fmt.Fprintf(&out, `<span class="%s">%s</span>`, cls, escaped)
	}
	return out.String()
}

// emitTemplateAction emits the `{{ ... }}` block, finding any
// `.Identifier` references within it and wrapping them in hover
// spans. originLine/originCol is the (line, col) at which the action
// starts in the original file (0-indexed LSP coords).
func emitTemplateAction(out *strings.Builder, action string, originLine, originCol int) {
	// Emit the opening `{{` with no decoration.
	out.WriteString(html.EscapeString(action[:2]))
	line, col := originLine, originCol+2

	body := action[2 : len(action)-2]

	j := 0
	for j < len(body) {
		ch := body[j]
		// A `.Ident` reference starts with `.` followed by a letter
		// or underscore. We keep this restrictive — the demo only
		// references top-level template variables (`.Greeting`,
		// `.Name`); pipelines / methods aren't part of v1.
		if ch == '.' && j+1 < len(body) && isIdentStart(body[j+1]) {
			// Consume the identifier.
			k := j + 1
			for k < len(body) && isIdentPart(body[k]) {
				k++
			}
			ident := body[j:k] // includes the leading `.`
			// The LSP hover position for a template field reference
			// should land ON the identifier letter, not the dot —
			// gastro's template hover treats the cursor on the first
			// letter of the field as the field. Bump col by 1 to
			// skip the `.`.
			out.WriteString(hoverableSpan(html.EscapeString(ident), "", line, col+1))
			line, col = advance(line, col, ident)
			j = k
			continue
		}
		out.WriteString(html.EscapeString(string(ch)))
		if ch == '\n' {
			line++
			col = 0
		} else {
			col++
		}
		j++
	}

	// Emit the closing `}}`.
	out.WriteString(html.EscapeString(action[len(action)-2:]))
}

func isIdentStart(b byte) bool {
	return (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z') || b == '_'
}

func isIdentPart(b byte) bool {
	return isIdentStart(b) || (b >= '0' && b <= '9')
}
