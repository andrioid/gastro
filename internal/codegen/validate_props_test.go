package codegen_test

import (
	"strings"
	"testing"

	"github.com/andrioid/gastro/internal/codegen"
	"github.com/andrioid/gastro/internal/parser"
)

// requiredCardSchema: Title is gastro:"required", Body is optional.
func requiredCardSchema() map[string][]codegen.StructField {
	return map[string][]codegen.StructField{
		"components/card.gastro": {
			{Name: "Title", Type: "string", Required: true},
			{Name: "Body", Type: "string"},
		},
	}
}

func TestParseStructFields_RequiredTag(t *testing.T) {
	hoisted := "type Props struct {\n" +
		"\tTitle string `gastro:\"required\"`\n" +
		"\tCount int    `json:\"count\" gastro:\"required\"`\n" +
		"\tBody  string `json:\"body\"`\n" +
		"}"
	fields := codegen.ParseStructFields(hoisted)
	want := []codegen.StructField{
		{Name: "Title", Type: "string", Required: true},
		{Name: "Count", Type: "int", Required: true},
		{Name: "Body", Type: "string"},
	}
	if len(fields) != len(want) {
		t.Fatalf("got %+v, want %+v", fields, want)
	}
	for i := range want {
		if fields[i] != want[i] {
			t.Errorf("field %d = %+v, want %+v", i, fields[i], want[i])
		}
	}
}

func TestValidateTemplate_OmittedRequiredPropIsError(t *testing.T) {
	body := "<div>\n  {{ Card (dict \"Body\" \"x\") }}\n</div>"
	diags := codegen.ValidateTemplate(body, cardUses(), requiredCardSchema(), codegen.ValidateDictKeysOptions{})
	if len(diags) != 1 {
		t.Fatalf("expected one diagnostic, got %+v", diags)
	}
	d := diags[0]
	if d.Severity != codegen.SeverityError {
		t.Errorf("severity = %d, want error", d.Severity)
	}
	if !strings.Contains(d.Message, `missing required prop "Title" on component Card`) {
		t.Errorf("message = %q", d.Message)
	}
	// Anchored on the component name.
	if d.StartLine != 2 || d.StartCol != 6 {
		t.Errorf("position = %d:%d, want 2:6", d.StartLine, d.StartCol)
	}
}

func TestValidateTemplate_RequiredPropPresentPasses(t *testing.T) {
	body := `{{ Card (dict "Title" "") }}`
	if diags := codegen.ValidateTemplate(body, cardUses(), requiredCardSchema(), codegen.ValidateDictKeysOptions{}); len(diags) != 0 {
		t.Fatalf("a present required key passes generate (emptiness is a render-time check), got %+v", diags)
	}
}

func TestValidateTemplate_OmittedOptionalPropNeverWarns(t *testing.T) {
	body := `{{ Card (dict "Title" "x") }}`
	if diags := codegen.ValidateTemplate(body, cardUses(), requiredCardSchema(), codegen.ValidateDictKeysOptions{}); len(diags) != 0 {
		t.Fatalf("optional Body omitted must not warn, got %+v", diags)
	}
}

func TestValidateTemplate_DynamicDictSkipsRequiredCheck(t *testing.T) {
	body := `{{ Card (dict .Key "x") }}`
	if diags := codegen.ValidateTemplate(body, cardUses(), requiredCardSchema(), codegen.ValidateDictKeysOptions{}); len(diags) != 0 {
		t.Fatalf("dynamic dict keys cannot be checked, got %+v", diags)
	}
}

func TestValidateTemplate_WrapProvidesRequiredChildren(t *testing.T) {
	schema := map[string][]codegen.StructField{
		"components/layout.gastro": {
			{Name: "Title", Type: "string", Required: true},
			{Name: "Children", Type: "template.HTML", Required: true},
		},
	}
	uses := []parser.UseDeclaration{{Name: "Layout", Path: "components/layout.gastro"}}
	body := "{{ wrap Layout (dict \"Title\" \"x\") }}<p>hi</p>{{ end }}"
	if diags := codegen.ValidateTemplate(body, uses, schema, codegen.ValidateDictKeysOptions{}); len(diags) != 0 {
		t.Fatalf("wrap supplies Children, got %+v", diags)
	}
}

func typedCardSchema() map[string][]codegen.StructField {
	return map[string][]codegen.StructField{
		"components/card.gastro": {
			{Name: "Title", Type: "string"},
			{Name: "Count", Type: "int"},
			{Name: "Size", Type: "uint8"},
			{Name: "Ratio", Type: "float64"},
			{Name: "Active", Type: "bool"},
			{Name: "Body", Type: "template.HTML"},
			{Name: "Tags", Type: "[]string"},
			{Name: "Any", Type: "any"},
			{Name: "Variant", Type: "ButtonVariant"},
			{Name: "When", Type: "time.Time"},
		},
	}
}

func TestValidateTemplate_TypecheckLiteralMismatch(t *testing.T) {
	body := "{{ Card (dict \"Count\" \"three\") }}"
	typecheck := codegen.ValidateDictKeysOptions{TypecheckProps: true}

	if diags := codegen.ValidateTemplate(body, cardUses(), typedCardSchema(), codegen.ValidateDictKeysOptions{}); len(diags) != 0 {
		t.Fatalf("with TypecheckProps off the literal passes, got %+v", diags)
	}

	diags := codegen.ValidateTemplate(body, cardUses(), typedCardSchema(), typecheck)
	if len(diags) != 1 {
		t.Fatalf("expected one diagnostic, got %+v", diags)
	}
	d := diags[0]
	if d.Severity != codegen.SeverityError {
		t.Errorf("severity = %d, want error", d.Severity)
	}
	if !strings.Contains(d.Message, `prop "Count" on component Card`) || !strings.Contains(d.Message, "int") {
		t.Errorf("message = %q", d.Message)
	}
	// Anchored on the value literal "three".
	if d.StartLine != 1 || d.StartCol != 23 {
		t.Errorf("position = %d:%d, want 1:23", d.StartLine, d.StartCol)
	}
}

func TestValidateTemplate_TypecheckLiterals(t *testing.T) {
	typecheck := codegen.ValidateDictKeysOptions{TypecheckProps: true}
	cases := []struct {
		dict    string
		wantErr bool
	}{
		{`"Title" "x"`, false},
		{`"Title" 3`, true},
		{`"Title" true`, true},
		{`"Title" nil`, true},
		{`"Count" 3`, false},
		{`"Count" -3`, false},
		{`"Count" 2.5`, true},
		{`"Count" true`, true},
		{`"Size" 255`, false},
		{`"Size" 256`, true},
		{`"Size" -1`, true},
		{`"Ratio" 2`, false},
		{`"Ratio" 2.5`, false},
		{`"Ratio" "2.5"`, true},
		{`"Active" false`, false},
		{`"Active" "true"`, true},
		{`"Active" 1`, true},
		{`"Body" "<b>x</b>"`, false},
		{`"Body" 1`, true},
		{`"Tags" nil`, false},
		{`"Tags" "a"`, true},
		{`"Any" "x"`, false},
		{`"Any" nil`, false},
		// Types gastro cannot resolve from the type text are skipped.
		{`"Variant" "primary"`, false},
		{`"When" "2024"`, false},
		// Non-literal values are checked at render time only.
		{`"Count" .Count`, false},
	}
	for _, tc := range cases {
		t.Run(tc.dict, func(t *testing.T) {
			body := "{{ Card (dict " + tc.dict + ") }}"
			diags := codegen.ValidateTemplate(body, cardUses(), typedCardSchema(), typecheck)
			if gotErr := len(diags) > 0; gotErr != tc.wantErr {
				t.Fatalf("dict %s: wantErr=%v, got %+v", tc.dict, tc.wantErr, diags)
			}
		})
	}
}
