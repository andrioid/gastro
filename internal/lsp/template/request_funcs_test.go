package template_test

// Tests that WithRequestFuncs binder helper names feed through
// ParseTemplateBodyWithRequestFuncs and FuncMapCompletionsWithRequestFuncs.

import (
	"strings"
	"testing"

	lsptemplate "github.com/andrioid/gastro/internal/lsp/template"
)

// TestParseTemplateBodyWithRequestFuncs_RecognisesHelpers: a template
// that references a request-aware helper parses without error when the
// helper name is supplied to the request-func-aware variant. The plain
// ParseTemplateBody would reject it as "function not defined".
func TestParseTemplateBodyWithRequestFuncs_RecognisesHelpers(t *testing.T) {
	body := `<p>{{ t "Welcome" }}</p>`

	// Plain variant: helper unknown → parse fails.
	if _, err := lsptemplate.ParseTemplateBody(body, nil); err == nil {
		t.Errorf("plain ParseTemplateBody should reject unknown helper; got nil err")
	}

	// Request-aware variant: helper known → parse succeeds.
	tree, err := lsptemplate.ParseTemplateBodyWithRequestFuncs(body, nil, []string{"t"})
	if err != nil {
		t.Fatalf("request-aware parse should succeed: %v", err)
	}
	if tree == nil {
		t.Fatalf("request-aware parse returned nil tree")
	}
}

// TestFuncMapCompletionsWithRequestFuncs_IncludesHelpers: request-aware
// helper names appear in the completion list with the "request-aware
// helper" detail string so editors can render them distinctly from
// stdlib funcs and component invocations.
func TestFuncMapCompletionsWithRequestFuncs_IncludesHelpers(t *testing.T) {
	items := lsptemplate.FuncMapCompletionsWithRequestFuncs(false, []string{"t", "csrfField"})

	var tFound, csrfFound bool
	for _, it := range items {
		switch it.Label {
		case "t":
			tFound = true
			if !strings.Contains(it.Detail, "request-aware") {
				t.Errorf("t detail should mention request-aware; got %q", it.Detail)
			}
		case "csrfField":
			csrfFound = true
		}
	}
	if !tFound {
		t.Errorf("completion list should include t")
	}
	if !csrfFound {
		t.Errorf("completion list should include csrfField")
	}

	// And the legacy entrypoint still works (no request-aware helpers).
	plain := lsptemplate.FuncMapCompletions(false)
	for _, it := range plain {
		if it.Label == "t" {
			t.Errorf("plain FuncMapCompletions should not include t; got %+v", it)
		}
	}
}
