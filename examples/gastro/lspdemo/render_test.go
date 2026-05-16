package lspdemo

import (
	"strings"
	"testing"

	"gastro-website/lspclient"
)

func TestRenderer_BothPanelsContainExpectedIdents(t *testing.T) {
	r, err := NewRenderer(Source(), nil)
	if err != nil {
		t.Fatal(err)
	}

	fm := string(r.Frontmatter())
	if !strings.Contains(fm, "greeting.gastro") {
		t.Errorf("frontmatter panel missing title; got: %s", fm)
	}
	if !strings.Contains(fm, "— frontmatter") {
		t.Errorf("frontmatter panel missing suffix; got: %s", fm)
	}
	// Every Name-category token in the frontmatter should be wrapped
	// in an lsp-ident span. `Props`, `Name`, `Greeting`, `gastro`,
	// `p`, `NAme` are all good candidates.
	for _, want := range []string{">Props<", ">Greeting<", ">gastro<", ">NAme<"} {
		if !strings.Contains(fm, want) {
			t.Errorf("frontmatter panel missing token %q in span output", want)
		}
	}
	// And those tokens should appear inside lsp-ident spans.
	if !strings.Contains(fm, `class="lsp-ident`) {
		t.Errorf("frontmatter panel has no lsp-ident wrapping; got: %s", fm)
	}

	body := string(r.Body())
	if !strings.Contains(body, "— body") {
		t.Errorf("body panel missing suffix; got: %s", body)
	}
	// .Greeting and .Name inside `{{ }}` should be hoverable.
	if !strings.Contains(body, `>.Greeting<`) {
		t.Errorf("body panel should render `.Greeting` literally inside a span; got: %s", body)
	}
	if !strings.Contains(body, `>.Name<`) {
		t.Errorf("body panel should render `.Name` literally inside a span; got: %s", body)
	}
}

func TestRenderer_CoordsArePerOriginalFile(t *testing.T) {
	r, err := NewRenderer(Source(), nil)
	if err != nil {
		t.Fatal(err)
	}

	fm := string(r.Frontmatter())
	// `Greeting` (the frontmatter assignment LHS) is on original line
	// 7 (0-indexed: line 1 = `type Props struct {`, line 7 =
	// `Greeting := "Hi, " + p.NAme`).
	if !containsCoord(fm, `data-l="7"`, `>Greeting<`) {
		t.Errorf("Greeting in frontmatter should carry data-l=\"7\"; got: %s", fm)
	}

	body := string(r.Body())
	// .Greeting in the body: source line 10 (0-indexed: line 10) =
	// `\t<h1>{{ .Greeting }}</h1>`. So data-l should be 10.
	if !containsCoord(body, `data-l="10"`, `>.Greeting<`) {
		t.Errorf("body .Greeting should carry data-l=\"10\"; got: %s", body)
	}
}

func TestRenderer_SquiggleOverlay(t *testing.T) {
	// Diagnostic on the `NAme` identifier: 0-indexed line 7, char 23-27.
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

	fm := string(r.Frontmatter())
	if !strings.Contains(fm, `class="lsp-squiggle"`) {
		t.Errorf("expected squiggle overlay in frontmatter panel; got: %s", fm)
	}
	// Frontmatter starts at file line 1 (0-indexed). The diagnostic
	// is at file line 7. Panel-local line = 7 - 1 = 6.
	if !strings.Contains(fm, `--lsp-line:6`) {
		t.Errorf("squiggle --lsp-line should be 6 (panel-local); got: %s", fm)
	}
	if !strings.Contains(fm, `--lsp-col:23`) {
		t.Errorf("squiggle --lsp-col should be 23; got: %s", fm)
	}
	if !strings.Contains(fm, `--lsp-width:4`) {
		t.Errorf("squiggle --lsp-width should be 4; got: %s", fm)
	}

	body := string(r.Body())
	if strings.Contains(body, `class="lsp-squiggle"`) {
		t.Errorf("body panel should have no squiggle (diagnostic is in frontmatter); got: %s", body)
	}
}

// containsCoord verifies both substrings appear AND the data-l one
// comes before the identifier text within ~1200 chars (i.e. they're
// part of the same span tag — the inline mouseenter handler is ~400
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
