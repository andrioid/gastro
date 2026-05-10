package md_test

import (
	"strings"
	"testing"

	"gastro-website/md"
)

func TestMustRender_Heading(t *testing.T) {
	out := md.MustRender("# Title")
	if !strings.Contains(string(out), "<h1") {
		t.Errorf("expected <h1>, got: %s", out)
	}
}

func TestMustRender_GFMTable(t *testing.T) {
	src := `| a | b |
|---|---|
| 1 | 2 |`
	out := md.MustRender(src)
	if !strings.Contains(string(out), "<table") {
		t.Errorf("expected <table>, got: %s", out)
	}
}

func TestMustRender_GFMStrikethrough(t *testing.T) {
	out := md.MustRender("hello ~~world~~")
	if !strings.Contains(string(out), "<del>") {
		t.Errorf("expected <del>, got: %s", out)
	}
}

func TestMustRender_CodeFenceHighlight(t *testing.T) {
	src := "```go\nfunc main() {}\n```"
	out := md.MustRender(src)
	s := string(out)
	if !strings.Contains(s, "chroma") {
		t.Errorf("expected chroma class on highlighted block, got: %s", s)
	}
	// Chroma with WithClasses=true emits semantic class names, e.g. "kd"
	// for keyword-declaration. The exact set depends on the lexer; just
	// assert that *some* class-laden span is present.
	if !strings.Contains(s, "<span class=") {
		t.Errorf("expected highlighted spans, got: %s", s)
	}
}

func TestMustRender_Footnote(t *testing.T) {
	src := "Hello[^1]\n\n[^1]: world"
	out := md.MustRender(src)
	s := string(out)
	if !strings.Contains(s, "footnote") {
		t.Errorf("expected footnote markup, got: %s", s)
	}
}

func TestRender_ReturnsErrorRatherThanPanic(t *testing.T) {
	// goldmark is famously hard to make fail; this is mostly a smoke
	// test that the non-panic API path is wired.
	if _, err := md.Render(""); err != nil {
		t.Errorf("empty input should not error, got: %v", err)
	}
}
