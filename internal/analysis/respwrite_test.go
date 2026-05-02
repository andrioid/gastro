package analysis_test

import (
	"strings"
	"testing"

	"github.com/andrioid/gastro/internal/analysis"
)

func TestFindMissingReturns_FlagsWriteWithoutReturn(t *testing.T) {
	t.Parallel()
	// Frontmatter as the analyser receives it — imports are stripped
	// upstream by the parser before analysis runs.
	frontmatter := `if r.Method == "POST" {
    http.Error(w, "nope", http.StatusBadRequest)
}
Title := "fall-through"`

	got := analysis.FindMissingReturns(frontmatter)
	if len(got) != 1 {
		t.Fatalf("got %d findings, want 1: %#v", len(got), got)
	}
	if got[0].Line != 2 {
		t.Errorf("line = %d, want 2", got[0].Line)
	}
	if !strings.Contains(got[0].Snippet, "http.Error(w") {
		t.Errorf("snippet = %q, want to contain http.Error(w", got[0].Snippet)
	}
}

func TestFindMissingReturns_AcceptsWriteFollowedByReturn(t *testing.T) {
	t.Parallel()
	frontmatter := `if r.Method == "POST" {
    http.Error(w, "nope", http.StatusBadRequest)
    return
}
Title := "ok"`

	got := analysis.FindMissingReturns(frontmatter)
	if len(got) != 0 {
		t.Errorf("expected no findings, got %#v", got)
	}
}

func TestFindMissingReturns_AcceptsWriteAsLastStatement(t *testing.T) {
	t.Parallel()
	frontmatter := `http.Error(w, "always 500", http.StatusInternalServerError)`

	// The write is the last statement of the function body; the
	// codegen-wrapped function returns naturally.
	got := analysis.FindMissingReturns(frontmatter)
	if len(got) != 0 {
		t.Errorf("expected no findings, got %#v", got)
	}
}

func TestFindMissingReturns_DetectsMethodCallsOnW(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		src  string
	}{
		{"Write", `w.Write([]byte("hi"))
Title := "x"`},
		{"WriteHeader", `w.WriteHeader(201)
Title := "x"`},
		{"HeaderSet", `w.Header().Set("X-Foo", "bar")
Title := "x"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := analysis.FindMissingReturns(tc.src)
			if len(got) != 1 {
				t.Fatalf("got %d findings, want 1: %#v", len(got), got)
			}
		})
	}
}

func TestFindMissingReturns_DetectsRedirect(t *testing.T) {
	t.Parallel()
	frontmatter := `if needsLogin {
    http.Redirect(w, r, "/login", http.StatusSeeOther)
}
Title := "fall-through"`

	got := analysis.FindMissingReturns(frontmatter)
	if len(got) != 1 {
		t.Fatalf("got %d findings, want 1", len(got))
	}
}

func TestFindMissingReturns_IgnoresNonWriteCallsTakingW(t *testing.T) {
	t.Parallel()
	// Conservative: a function whose first param is `w` but that doesn't
	// actually write is still flagged (known false-positive class per
	// §4.9). Accept that as documented behaviour.
	frontmatter := `helperThatTakesW(w, r)
Title := "x"`

	got := analysis.FindMissingReturns(frontmatter)
	if len(got) != 1 {
		t.Errorf("expected 1 finding (documented false-positive class), got %d", len(got))
	}
}

func TestFindMissingReturns_IgnoresCallsThatDoNotTakeWOrR(t *testing.T) {
	t.Parallel()
	frontmatter := `fmt.Println("debug")
Title := "x"`

	got := analysis.FindMissingReturns(frontmatter)
	if len(got) != 0 {
		t.Errorf("expected no findings, got %#v", got)
	}
}

func TestFindMissingReturns_IgnoresMethodOnR(t *testing.T) {
	t.Parallel()
	// r.URL.Query() and friends are reads, not writes.
	frontmatter := `q := r.URL.Query().Get("filter")
Title := q`

	got := analysis.FindMissingReturns(frontmatter)
	if len(got) != 0 {
		t.Errorf("expected no findings, got %#v", got)
	}
}

func TestFindMissingReturns_HandlesUnparseableInput(t *testing.T) {
	t.Parallel()
	// Mid-edit garbage; analyser must bail without panicking.
	got := analysis.FindMissingReturns(`if r.Method ==`)
	if got != nil {
		t.Errorf("expected nil on parse error, got %#v", got)
	}
}

func TestFindMissingReturns_FlagsBothBranchesIndependently(t *testing.T) {
	t.Parallel()
	frontmatter := `if cond {
    http.Error(w, "a", 400)
} else {
    http.Error(w, "b", 500)
}
Title := "after"`

	got := analysis.FindMissingReturns(frontmatter)
	if len(got) != 2 {
		t.Errorf("expected 2 findings (one per branch), got %d: %#v", len(got), got)
	}
}

func TestFindMissingReturns_SSEPattern(t *testing.T) {
	t.Parallel()
	// The headline Track B pattern (§4.10) — POST writes an SSE patch
	// then returns. Should be clean.
	frontmatter := `if r.Method == "POST" {
    sse := datastar.NewSSE(w, r)
    sse.PatchElements(html)
    return
}
Title := "GET render"`

	got := analysis.FindMissingReturns(frontmatter)
	if len(got) != 0 {
		t.Errorf("expected no findings, got %#v", got)
	}
}
