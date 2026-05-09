package codegen_test

import (
	"testing"

	"github.com/andrioid/gastro/internal/codegen"
)

func TestTemplateRendersChildren(t *testing.T) {
	tests := []struct {
		name string
		body string
		want bool
	}{
		// Canonical forms — these MUST match (parity with the legacy
		// strings.Contains(body, "{{ .Children }}") check used by both
		// compiler.go:366 and shadow/workspace.go:513 prior to the
		// extraction).
		{"canonical with spaces", `<main>{{ .Children }}</main>`, true},
		{"no spaces", `<main>{{.Children}}</main>`, true},

		// Whitespace-trim variants — the old strings.Contains check
		// missed these. Keeping them in scope is intentional: a
		// component that writes `{{- .Children -}}` clearly intends
		// to render children, and silently dropping the synthetic
		// field on the XProps stub would be a worse failure mode
		// than the previous false-negative.
		{"left trim", `{{- .Children }}`, true},
		{"right trim", `{{ .Children -}}`, true},
		{"both trim", `{{- .Children -}}`, true},
		{"trim no spaces", `{{-.Children-}}`, true},

		// Negatives.
		{"empty body", ``, false},
		{"plain text", `<p>hello</p>`, false},
		{"unrelated action", `{{ .Title }}`, false},
		{"comment containing pattern", `{{/* {{ .Children }} */}}`, true /* still matches inside comments — codegen and shadow both behave this way today, comment-stripping happens later */},
		{"sub-field on Children", `{{ .Children.Foo }}`, false},
		{"different field name", `{{ .ChildrenList }}`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := codegen.TemplateRendersChildren(tt.body); got != tt.want {
				t.Errorf("TemplateRendersChildren(%q) = %v, want %v", tt.body, got, tt.want)
			}
		})
	}
}

// TestTemplateRendersChildren_LegacyParity asserts the new helper
// returns true for every body where the legacy `strings.Contains(body,
// "{{ .Children }}")` check returned true. This guarantees Phase 2's
// rewiring of compiler.go and shadow/workspace.go is a strict superset
// of today's behaviour — no body that used to be detected as
// children-rendering will silently stop being detected.
func TestTemplateRendersChildren_LegacyParity(t *testing.T) {
	legacyTrue := []string{
		`{{ .Children }}`,
		`<main>{{ .Children }}</main>`,
		`<a>{{ .Children }}</a><b>{{ .Children }}</b>`, // multiple occurrences
		"\n{{ .Children }}\n",                          // surrounded by newlines
	}
	for _, body := range legacyTrue {
		if !codegen.TemplateRendersChildren(body) {
			t.Errorf("legacy parity broken: TemplateRendersChildren(%q) = false, legacy strings.Contains was true", body)
		}
	}
}
