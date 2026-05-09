package server

import (
	"reflect"
	"testing"
)

// TestDetectChainPrefix is the unit test for the chain-detection
// helper that drives Phase 4.5's chained-field completion path. The
// helper is package-internal (no exported wrapper) so the test lives
// in package server, not server_test.
func TestDetectChainPrefix(t *testing.T) {
	tests := []struct {
		name    string
		content string
		line    int
		dotChar int // position of the dot the user just typed
		want    []string
	}{
		{
			// Canonical case: user typed `.Agent.|`. dotChar points
			// at the second dot. Chain prefix is the segment before
			// it: ["Agent"].
			name:    "single-segment chain at top level",
			content: "{{ .Agent. }}",
			line:    0,
			dotChar: 9,
			want:    []string{"Agent"},
		},
		{
			// Two-segment chain: `.Foo.Bar.|`. Returns both.
			name:    "two-segment chain",
			content: "{{ .Foo.Bar. }}",
			line:    0,
			dotChar: 11,
			want:    []string{"Foo", "Bar"},
		},
		{
			// Bare leading dot (.|) is the canonical top-level case.
			// detectChainPrefix returns nil so the caller falls back
			// to the existing top-level-vars completion path.
			name:    "leading dot has no chain",
			content: "{{ . }}",
			line:    0,
			dotChar: 3,
			want:    nil,
		},
		{
			// Mid-stream dot in arbitrary text: nothing useful, the
			// leading whitespace breaks the chain.
			name:    "dot after text but no chain segment",
			content: "<p>hello {{ . }}</p>",
			line:    0,
			dotChar: 12,
			want:    nil,
		},
		{
			// dotChar at column 0 — nothing to scan back into.
			name:    "dot at start of line",
			content: ".Foo",
			line:    0,
			dotChar: 0,
			want:    nil,
		},
		{
			// Inside a range body, the lexical chain is unchanged.
			// chainedFieldCompletions handles the scope rooting; the
			// detector only sees the chain segments.
			name:    "chain inside range body",
			content: "{{ range .Posts }}{{ .Author. }}{{ end }}",
			line:    0,
			dotChar: 28,
			want:    []string{"Author"},
		},
		{
			// Underscored ident — valid Go, must round-trip.
			name:    "underscored segment",
			content: "{{ .my_var. }}",
			line:    0,
			dotChar: 10,
			want:    []string{"my_var"},
		},
		{
			// Line out of range — defensive return.
			name:    "line out of range",
			content: "hello",
			line:    5,
			dotChar: 1,
			want:    nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := detectChainPrefix(tt.content, tt.line, tt.dotChar)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("detectChainPrefix(%q, %d, %d) = %v, want %v", tt.content, tt.line, tt.dotChar, got, tt.want)
			}
		})
	}
}
