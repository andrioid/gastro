package lspdemo

import (
	"strings"
	"testing"

	"gastro-website/lspclient"
)

func TestRenderer_FileContainsExpectedIdents(t *testing.T) {
	r, err := NewRenderer(Source(), nil)
	if err != nil {
		t.Fatal(err)
	}

	out := string(r.Render())

	// Title bar appears exactly once (single-window layout).
	if got := strings.Count(out, "greeting.gastro"); got != 1 {
		t.Errorf("filename should appear exactly once, got %d occurrences in: %s", got, out)
	}

	// Both `---` delimiters made it into the rendered window.
	if got := strings.Count(out, `<span class="cp">---</span>`); got != 2 {
		t.Errorf("expected exactly 2 `---` delimiter spans, got %d in: %s", got, out)
	}

	// Frontmatter Name-tokens become hoverable spans.
	for _, want := range []string{">Props<", ">Greeting<", ">gastro<", ">NAme<"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing token %q in span output", want)
		}
	}
	if !strings.Contains(out, `class="lsp-ident`) {
		t.Errorf("output has no lsp-ident wrapping; got: %s", out)
	}

	// Body `.Greeting` and `.Name` inside `{{ }}` are hoverable.
	if !strings.Contains(out, `>.Greeting<`) {
		t.Errorf("body should render `.Greeting` literally inside a span; got: %s", out)
	}
	if !strings.Contains(out, `>.Name<`) {
		t.Errorf("body should render `.Name` literally inside a span; got: %s", out)
	}
}

func TestRenderer_CoordsAreFileAbsolute(t *testing.T) {
	r, err := NewRenderer(Source(), nil)
	if err != nil {
		t.Fatal(err)
	}

	out := string(r.Render())

	// `Greeting` (the frontmatter assignment LHS, RHS use of the same
	// name from `Greeting := "Hi, " + p.NAme`) is on file line 7
	// (0-indexed: line 0 = `---`, line 1 = `type Props struct {`,
	// line 7 = `Greeting := "Hi, " + p.NAme`).
	if !containsCoord(out, `data-l="7"`, `>Greeting<`) {
		t.Errorf("Greeting should carry data-l=\"7\"; got: %s", out)
	}

	// `.Greeting` in the body lives on file line 10
	// (`\t<h1>{{ .Greeting }}</h1>`), unchanged from the panel layout
	// because the body chunk's origin line was already file-absolute.
	if !containsCoord(out, `data-l="10"`, `>.Greeting<`) {
		t.Errorf("body .Greeting should carry data-l=\"10\"; got: %s", out)
	}
}

func TestRenderer_SquiggleOverlay(t *testing.T) {
	// Diagnostic on the `NAme` identifier: file line 7, char 23-27.
	diags := []lspclient.Diagnostic{{
		Range: lspclient.Range{
			Start: lspclient.Position{Line: 7, Character: 23},
			End:   lspclient.Position{Line: 7, Character: 27},
		},
		Severity: 1,
		Source:   "gopls",
		Message:  "p.NAme undefined (type Props has no field or method NAme, but does have field Name)",
	}}
	r, err := NewRenderer(Source(), diags)
	if err != nil {
		t.Fatal(err)
	}

	out := string(r.Render())
	if !strings.Contains(out, `class="lsp-squiggle"`) {
		t.Errorf("expected squiggle overlay; got: %s", out)
	}
	// Coordinates are now file-absolute. The pre starts at file
	// line 0 (the opening `---`) so --lsp-line equals d.Range.Start.Line.
	if !strings.Contains(out, `--lsp-line:7`) {
		t.Errorf("squiggle --lsp-line should be 7 (file-absolute); got: %s", out)
	}
	if !strings.Contains(out, `--lsp-col:23`) {
		t.Errorf("squiggle --lsp-col should be 23; got: %s", out)
	}
	if !strings.Contains(out, `--lsp-width:4`) {
		t.Errorf("squiggle --lsp-width should be 4; got: %s", out)
	}
}

// containsCoord verifies both substrings appear AND the data-l one
// comes before the identifier text within ~1200 chars (i.e. they're
// part of the same span tag — the inline mouseenter handler is ~330
// chars on its own, so the window has to be generous). Cheap check,
// avoids parsing HTML.
func containsCoord(haystack, dataAttr, identText string) bool {
	idx := strings.Index(haystack, identText)
	if idx < 0 {
		return false
	}
	window := haystack[max(0, idx-1200):idx]
	// Make sure dataAttr is the LAST data-l before identText — not a
	// stale one from an earlier span. Find the last `data-l="` in
	// the window and require it to equal dataAttr.
	last := strings.LastIndex(window, `data-l="`)
	if last < 0 {
		return false
	}
	return strings.HasPrefix(window[last:], dataAttr)
}
