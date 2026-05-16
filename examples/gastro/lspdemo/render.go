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

// Renderer turns the embedded demo .gastro source into the two
// side-by-side panels (frontmatter / body) for the homepage live-LSP
// demo. It does NOT talk to the LSP itself — the diagnostics are
// snapshotted at app boot, passed in here, and baked into the SSR
// output as absolute-positioned squiggle overlays.
//
// Each identifier in the rendered source becomes a hoverable span
// carrying its (line, character) position in the ORIGINAL file
// coordinates, even in the body panel where lines come from the
// post-frontmatter region of the file. The hover endpoint then
// queries the LSP at exactly those coordinates without needing to
// translate panel-local positions back to file-local ones.
type Renderer struct {
	source      string
	diagnostics []lspclient.Diagnostic

	// Split coordinates in 0-indexed LSP-style line numbers.
	frontmatterStart int // first line of frontmatter content
	frontmatterEnd   int // exclusive: line of the closing ---
	bodyStart        int // first line of body content
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

// Frontmatter returns the frontmatter panel's HTML, already wrapped
// in macOS-window chrome and titled "<filename> — frontmatter".
func (r *Renderer) Frontmatter() template.HTML {
	return r.renderPanel(panelOpts{
		titleSuffix: "frontmatter",
		panelKey:    "left",
		startLine:   r.frontmatterStart,
		endLine:     r.frontmatterEnd,
		lexerName:   "go",
	})
}

// Body returns the body panel's HTML, already wrapped in macOS-window
// chrome and titled "<filename> — body".
func (r *Renderer) Body() template.HTML {
	return r.renderPanel(panelOpts{
		titleSuffix: "body",
		panelKey:    "right",
		startLine:   r.bodyStart,
		endLine:     r.bodyEnd,
		lexerName:   "html",
	})
}

type panelOpts struct {
	titleSuffix string // "frontmatter" | "body"
	panelKey    string // "left" | "right" — set into $hover.panel on enter
	startLine   int    // 0-indexed inclusive line in the original source
	endLine     int    // 0-indexed exclusive
	lexerName   string // chroma lexer id ("go" | "html")
}

func (r *Renderer) renderPanel(opts panelOpts) template.HTML {
	lines := strings.Split(r.source, "\n")
	chunkLines := lines[opts.startLine:opts.endLine]
	chunk := strings.Join(chunkLines, "\n")

	var code string
	switch opts.lexerName {
	case "go":
		code = renderGoChunk(chunk, opts.startLine, opts.panelKey)
	case "html":
		code = renderBodyChunk(chunk, opts.startLine, opts.panelKey)
	default:
		code = html.EscapeString(chunk)
	}

	// Squiggle overlays for diagnostics whose range falls within this
	// panel's line span. Positioned absolutely via CSS custom props.
	var squiggles strings.Builder
	for _, d := range r.diagnostics {
		if d.Range.Start.Line < opts.startLine || d.Range.Start.Line >= opts.endLine {
			continue
		}
		// Convert to panel-local 0-indexed coordinates for visual placement.
		localLine := d.Range.Start.Line - opts.startLine
		startCol := d.Range.Start.Character
		endCol := d.Range.End.Character
		if d.Range.End.Line != d.Range.Start.Line {
			// Multi-line diagnostic: cap at the end of the start line.
			lineLen := 0
			if localLine < len(chunkLines) {
				lineLen = len(chunkLines[localLine])
			}
			endCol = lineLen
		}
		width := endCol - startCol
		if width <= 0 {
			width = 1
		}
		fmt.Fprintf(&squiggles,
			`<span class="lsp-squiggle" style="--lsp-line:%d;--lsp-col:%d;--lsp-width:%d" title="%s"></span>`,
			localLine, startCol, width, html.EscapeString(d.Message))
	}

	// Panel chrome: traffic-light dots + filename — suffix. Pre is
	// scrollable horizontally on overflow but never wraps so the
	// (line, col)-positioned squiggles stay aligned.
	var b strings.Builder
	fmt.Fprintf(&b, `<div class="lsp-panel">`)
	fmt.Fprintf(&b, `<div class="lsp-panel-bar">`)
	fmt.Fprintf(&b, `<span class="lsp-dot lsp-dot-red"></span>`)
	fmt.Fprintf(&b, `<span class="lsp-dot lsp-dot-yellow"></span>`)
	fmt.Fprintf(&b, `<span class="lsp-dot lsp-dot-green"></span>`)
	fmt.Fprintf(&b, `<span class="lsp-panel-title">%s <span class="lsp-panel-suffix">— %s</span></span>`,
		html.EscapeString(Filename), html.EscapeString(opts.titleSuffix))
	fmt.Fprintf(&b, `</div>`)
	fmt.Fprintf(&b, `<div class="lsp-panel-code">`)
	// `chroma` class on the wrapper so existing per-token color rules
	// (.chroma .kd, .chroma .nx, etc.) in tailwind.css apply without
	// needing a parallel rule set keyed on .lsp-code.
	fmt.Fprintf(&b, `<pre class="lsp-code chroma"><code>%s</code></pre>`, code)
	b.WriteString(squiggles.String())
	fmt.Fprintf(&b, `</div>`)
	fmt.Fprintf(&b, `</div>`)
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
func renderGoChunk(chunk string, originLine int, panelKey string) string {
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
		emitToken(&out, tok, line+originLine, col, panelKey)
		// Advance our (line, col) tracker by the token's value.
		line, col = advance(line, col, tok.Value)
	}
	return out.String()
}

// emitToken writes one chroma token as HTML. If the token is a Name
// (any subcategory), it's wrapped in an .lsp-ident hover span.
func emitToken(out *strings.Builder, tok chroma.Token, line, col int, panelKey string) {
	escaped := html.EscapeString(tok.Value)

	if isHoverable(tok.Type) {
		out.WriteString(hoverableSpan(escaped, chromaClass(tok.Type), line, col, panelKey))
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
func hoverableSpan(text, chromaCls string, line, col int, panelKey string) string {
	// Build space-separated class list: lsp-ident + chroma class (if any).
	cls := "lsp-ident"
	if chromaCls != "" {
		cls += " " + chromaCls
	}

	// The mouseenter expression is intentionally inline rather than a
	// helper script: keeping it next to the data attribute makes the
	// span self-contained, and Datastar v1 doesn't have an event-
	// delegation primitive that would let us factor it onto the
	// wrapper div. Inline cost is ~380 bytes per span; demo has on
	// the order of 30 spans so the markup overhead is bounded.
	//
	// Coordinates are computed relative to the .lsp-demo-wrap (which
	// is the tooltip's offset parent) by subtracting the wrap's rect
	// from the target's. Using offsetLeft/offsetTop directly would
	// give panel-local coords, mismatched against the tooltip's
	// containing block.
	const handler = "const w = evt.target.closest('.lsp-demo-wrap').getBoundingClientRect(); " +
		"const t = evt.target.getBoundingClientRect(); " +
		"$hover.x = t.left - w.left + t.width/2; " +
		"$hover.y = t.top - w.top + t.height; " +
		"$hover.panel = '%s'; " +
		"$hover.show = true; " +
		"@get('/api/lsp-demo/hover?l=' + evt.target.dataset.l + '&c=' + evt.target.dataset.c)"
	expr := fmt.Sprintf(handler, panelKey)

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
func renderBodyChunk(chunk string, originLine int, panelKey string) string {
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
			emitTemplateAction(&out, action, line+originLine, col, panelKey)
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
func emitTemplateAction(out *strings.Builder, action string, originLine, originCol int, panelKey string) {
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
			out.WriteString(hoverableSpan(html.EscapeString(ident), "", line, col+1, panelKey))
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
