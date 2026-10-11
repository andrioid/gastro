package codegen_test

import (
	"strings"
	"testing"

	"github.com/andrioid/gastro/internal/codegen"
	"github.com/andrioid/gastro/internal/parser"
)

type pos struct{ line, col int }

func positions(diags []codegen.TemplateDiagnostic) []pos {
	out := make([]pos, 0, len(diags))
	for _, d := range diags {
		out = append(out, pos{d.StartLine, d.StartCol})
	}
	return out
}

func layoutAndCardUses() []parser.UseDeclaration {
	return []parser.UseDeclaration{
		{Name: "Layout", Path: "components/layout.gastro"},
		{Name: "Card", Path: "components/card.gastro"},
	}
}

func layoutAndCardSchema() map[string][]codegen.StructField {
	s := cardSchema()
	s["components/layout.gastro"] = []codegen.StructField{{Name: "Title", Type: "string"}}
	return s
}

func TestValidateTemplate_PositionsSurviveWrapBlocks(t *testing.T) {
	// The wrap transform moves children to a trailing {{define}}; the
	// diagnostics must still point at the keys as written.
	body := "{{ wrap Layout (dict \"Title\" \"x\") }}\n" +
		"<p>\n" +
		"  {{ Card (dict \"Tite\" \"Hi\") }}\n" +
		"</p>\n" +
		"{{ end }}\n" +
		"{{ Card (dict \"Bdy\" \"x\") }}\n"

	diags := codegen.ValidateTemplate(body, layoutAndCardUses(), layoutAndCardSchema(), codegen.ValidateDictKeysOptions{})

	want := []pos{{3, 17}, {6, 15}}
	if got := positions(diags); !equalPositions(got, want) {
		t.Fatalf("positions = %v, want %v (diags: %+v)", got, want, diags)
	}
	for _, d := range diags {
		if d.Severity != codegen.SeverityError {
			t.Errorf("unknown prop should be an error: %+v", d)
		}
	}
}

func TestValidateTemplate_WrapWithoutDictIsNotABareCall(t *testing.T) {
	body := "{{ wrap Layout }}<p>hi</p>{{ end }}"
	if diags := codegen.ValidateTemplate(body, layoutAndCardUses(), layoutAndCardSchema(), codegen.ValidateDictKeysOptions{}); len(diags) != 0 {
		t.Fatalf("expected no diagnostics, got %+v", diags)
	}
}

func TestValidateTemplate_ReportsEveryUnknownComponent(t *testing.T) {
	body := "<div>\n" +
		"  {{ Widget (dict) }}\n" +
		"  {{ Card (dict \"Tite\" 1) }} {{ Gadget }}\n" +
		"</div>"

	diags := codegen.ValidateTemplate(body, cardUses(), cardSchema(), codegen.ValidateDictKeysOptions{})
	want := []pos{{2, 6}, {3, 33}, {3, 17}}
	if got := positions(diags); !samePositionSet(got, want) {
		t.Fatalf("positions = %v, want %v (diags: %+v)", got, want, diags)
	}
	var widget, gadget bool
	for _, d := range diags {
		if strings.Contains(d.Message, `"Widget"`) && strings.Contains(d.Message, "not imported") {
			widget = true
		}
		if strings.Contains(d.Message, `"Gadget"`) && strings.Contains(d.Message, "not imported") {
			gadget = true
		}
	}
	if !widget || !gadget {
		t.Errorf("expected not-imported errors for Widget and Gadget, got %+v", diags)
	}
}

func TestValidateTemplate_AppFuncsDoNotHideDiagnostics(t *testing.T) {
	// formatDate is registered by the app at runtime (WithFuncs); it is
	// unknown at generate time and must not stop validation.
	body := `{{ formatDate .When }} {{ Card (dict "Tite" 1) }}`
	diags := codegen.ValidateTemplate(body, cardUses(), cardSchema(), codegen.ValidateDictKeysOptions{})
	if len(diags) != 1 || !strings.Contains(diags[0].Message, `unknown prop "Tite"`) {
		t.Fatalf("expected one unknown-prop diagnostic, got %+v", diags)
	}
}

func TestValidateTemplate_RawBlocksAreNotValidated(t *testing.T) {
	body := "{{ raw }}\n{{ Card (dict \"Nope\" 1) }} {{ Widget }}\n{{ endraw }}\n{{ Card (dict \"Tite\" 1) }}"
	diags := codegen.ValidateTemplate(body, cardUses(), cardSchema(), codegen.ValidateDictKeysOptions{})
	want := []pos{{4, 15}}
	if got := positions(diags); !equalPositions(got, want) {
		t.Fatalf("positions = %v, want %v (diags: %+v)", got, want, diags)
	}
}

func equalPositions(a, b []pos) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func samePositionSet(a, b []pos) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[pos]int{}
	for _, p := range a {
		seen[p]++
	}
	for _, p := range b {
		seen[p]--
	}
	for _, n := range seen {
		if n != 0 {
			return false
		}
	}
	return true
}
