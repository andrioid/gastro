package codegen_test

import (
	"strings"
	"testing"

	"github.com/andrioid/gastro/internal/codegen"
	"github.com/andrioid/gastro/internal/parser"
)

// helper: a tiny Card schema.
func cardSchema() map[string][]codegen.StructField {
	return map[string][]codegen.StructField{
		"components/card.gastro": {
			{Name: "Title", Type: "string"},
			{Name: "Body", Type: "string"},
		},
	}
}

func cardUses() []parser.UseDeclaration {
	return []parser.UseDeclaration{{Name: "Card", Path: "components/card.gastro"}}
}

func TestValidateDictKeys_AllKeysKnown(t *testing.T) {
	body := `{{ Card (dict "Title" "Hi" "Body" "Hello") }}`
	warnings := codegen.ValidateDictKeys(body, cardUses(), cardSchema())
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got: %v", warnings)
	}
}

func TestValidateDictKeys_TypoFlagged(t *testing.T) {
	body := `{{ Card (dict "Tite" "Hi" "Body" "Hello") }}`
	warnings := codegen.ValidateDictKeys(body, cardUses(), cardSchema())
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d: %v", len(warnings), warnings)
	}
	w := warnings[0]
	if !strings.Contains(w.Message, `unknown prop "Tite"`) {
		t.Errorf("warning message missing typo: %q", w.Message)
	}
	if !strings.Contains(w.Message, "Card") {
		t.Errorf("warning message missing component name: %q", w.Message)
	}
	if !strings.Contains(w.Message, "Body, Title") {
		t.Errorf("warning message missing valid-fields hint (sorted): %q", w.Message)
	}
}

func TestValidateDictKeys_MultipleTypos(t *testing.T) {
	body := `{{ Card (dict "Tite" "Hi" "Bdy" "Hello") }}`
	warnings := codegen.ValidateDictKeys(body, cardUses(), cardSchema())
	if len(warnings) != 2 {
		t.Fatalf("expected 2 warnings, got %d: %v", len(warnings), warnings)
	}
}

func TestValidateDictKeys_ChildrenIsNotFlagged(t *testing.T) {
	// TransformTemplate injects "Children" for {{ wrap X ... }} blocks (A5).
	// The validator must not warn about it even though it isn't a user-defined
	// Props field — it's plumbed through to {{ .Children }}.
	body := `{{ Card (dict "Title" "Hi" "Children" (__gastro_render_children "x" .)) }}`
	warnings := codegen.ValidateDictKeys(body, cardUses(), cardSchema())
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings (Children should be skipped), got: %v", warnings)
	}
}

// A5: user-authored "__children" (the pre-A5 sentinel name) should produce a
// targeted warning suggesting the new "Children" key, rather than a generic
// unknown-prop message. This catches old hand-written code that survived the
// migration without breaking silently.
func TestValidateDictKeys_OldChildrenSentinelHinted(t *testing.T) {
	body := `{{ Card (dict "Title" "Hi" "__children" "<p>old</p>") }}`
	warnings := codegen.ValidateDictKeys(body, cardUses(), cardSchema())
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning for __children sentinel, got %d: %v", len(warnings), warnings)
	}
	w := warnings[0]
	if !strings.Contains(w.Message, `"__children"`) {
		t.Errorf("warning should mention the bad key: %q", w.Message)
	}
	if !strings.Contains(w.Message, `"Children"`) {
		t.Errorf("warning should suggest the new key name: %q", w.Message)
	}
	if !strings.Contains(w.Message, "Card") {
		t.Errorf("warning should name the component: %q", w.Message)
	}
}

func TestValidateDictKeys_DynamicKeysSkipped(t *testing.T) {
	// If any odd-arg key isn't a string literal, the dict is dynamic and
	// we skip validation rather than false-positiving. Here .DynamicKey
	// resolves at render time.
	body := `{{ Card (dict .DynamicKey "value" "Title" "Hi") }}`
	warnings := codegen.ValidateDictKeys(body, cardUses(), cardSchema())
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings for dynamic-key dict, got: %v", warnings)
	}
}

func TestValidateDictKeys_BareCallOnPropfulComponent(t *testing.T) {
	// Bare-calling a component that declares a Props struct must surface
	// as a hard diagnostic — the FuncMap wrapper for propful components
	// is non-variadic, so html/template will fail at execute time. We
	// catch it during validation so editors and `gastro build` reject
	// it before deployment.
	body := `{{ Card }}`
	warnings := codegen.ValidateDictKeys(body, cardUses(), cardSchema())
	if len(warnings) != 1 {
		t.Fatalf("expected exactly one warning for bare-call on propful component, got: %v", warnings)
	}
	if !strings.Contains(warnings[0].Message, "requires props") {
		t.Errorf("warning message = %q, want it to mention \"requires props\"", warnings[0].Message)
	}
	if !strings.Contains(warnings[0].Message, "Card") {
		t.Errorf("warning message = %q, want it to mention the component name \"Card\"", warnings[0].Message)
	}
}

func TestValidateDictKeys_BareCallOnPropless(t *testing.T) {
	// A propless component (absent from propsByPath) called bare must
	// not trigger any diagnostic. The compiler emits a variadic FuncMap
	// wrapper for these so `{{ Icon }}` and `{{ Icon (dict) }}` both work.
	body := `{{ Icon }}`
	uses := []parser.UseDeclaration{{Name: "Icon", Path: "components/icon.gastro"}}
	warnings := codegen.ValidateDictKeys(body, uses, map[string][]codegen.StructField{})
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings on bare-call of propless component, got: %v", warnings)
	}
}

func TestValidateDictKeys_NonImportedComponentIgnored(t *testing.T) {
	// {{ NotImported (dict ...) }} is a parse-level error in real builds,
	// but the validator's job is only props \u2014 it must not warn here.
	body := `{{ NotImported (dict "X" 1) }}`
	warnings := codegen.ValidateDictKeys(body, cardUses(), cardSchema())
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings for non-imported component, got: %v", warnings)
	}
}

func TestValidateDictKeys_NoSchemaSkipped(t *testing.T) {
	// Component imported but schema map has no entry (e.g. component
	// without a Props struct). Don't warn.
	body := `{{ Card (dict "Anything" 1) }}`
	uses := []parser.UseDeclaration{{Name: "Card", Path: "components/card.gastro"}}
	warnings := codegen.ValidateDictKeys(body, uses, map[string][]codegen.StructField{})
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings when schema is missing, got: %v", warnings)
	}
}

func TestValidateDictKeys_AliasResolution(t *testing.T) {
	// User imported the same file under a different local alias. Validation
	// must follow the alias \u2192 path \u2192 schema chain.
	body := `{{ MyCard (dict "Tite" "Hi") }}`
	uses := []parser.UseDeclaration{{Name: "MyCard", Path: "components/card.gastro"}}
	warnings := codegen.ValidateDictKeys(body, uses, cardSchema())
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d: %v", len(warnings), warnings)
	}
	if !strings.Contains(warnings[0].Message, "MyCard") {
		t.Errorf("warning should mention the alias the user wrote: %q", warnings[0].Message)
	}
}

func TestValidateDictKeys_NestedInRange(t *testing.T) {
	// Component invocations inside {{ range }} / {{ if }} / {{ with }}
	// must be visited too.
	body := `{{ range .Items }}{{ Card (dict "Tite" .Title) }}{{ end }}`
	warnings := codegen.ValidateDictKeys(body, cardUses(), cardSchema())
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning inside range, got %d: %v", len(warnings), warnings)
	}
}

func TestValidateDictKeys_NestedInIfElse(t *testing.T) {
	body := `{{ if .Show }}{{ Card (dict "Title" "ok") }}{{ else }}{{ Card (dict "Tite" "bad") }}{{ end }}`
	warnings := codegen.ValidateDictKeys(body, cardUses(), cardSchema())
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning in else branch, got %d: %v", len(warnings), warnings)
	}
}

func TestValidateDictKeys_NoUsesNoWarnings(t *testing.T) {
	// Nothing imported \u2192 nothing to validate; no parse cost incurred.
	body := `<p>plain text {{ .X }}</p>`
	warnings := codegen.ValidateDictKeys(body, nil, cardSchema())
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings with empty uses, got: %v", warnings)
	}
}

func TestValidateDictKeys_LineNumberPointsAtKey(t *testing.T) {
	// Verify the warning's line number points at the key, not at line 1.
	body := "<div>\n  {{ Card (dict \"Tite\" \"Hi\") }}\n</div>"
	warnings := codegen.ValidateDictKeys(body, cardUses(), cardSchema())
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d: %v", len(warnings), warnings)
	}
	if warnings[0].Line != 2 {
		t.Errorf("expected line 2, got %d", warnings[0].Line)
	}
}

func TestValidateDictKeys_ParseErrorBailsSilently(t *testing.T) {
	// Garbage template body. Validator must not panic or invent warnings.
	body := `{{ Card (dict "Title" `
	warnings := codegen.ValidateDictKeys(body, cardUses(), cardSchema())
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings on parse error, got: %v", warnings)
	}
}

// buttonBagSchema is a component with an attribute-forwarding bag
// (a gastro.Attrs field) alongside one typed prop.
func buttonBagSchema() map[string][]codegen.StructField {
	return map[string][]codegen.StructField{
		"components/button.gastro": {
			{Name: "Label", Type: "string"},
			{Name: "Attrs", Type: "gastro.Attrs"},
		},
	}
}

func buttonUses() []parser.UseDeclaration {
	return []parser.UseDeclaration{{Name: "Button", Path: "components/button.gastro"}}
}

func TestValidateDictKeys_BagComponentAcceptsArbitraryKeys(t *testing.T) {
	// A gastro.Attrs field opens the schema: forwarded keys that match no
	// declared field are HTML attributes, not typos, so no warning fires.
	body := `{{ Button (dict "Label" "Save" "type" "submit" "data-on:click" "@post('/x')") }}`
	warnings := codegen.ValidateDictKeys(body, buttonUses(), buttonBagSchema())
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings for bag component, got: %v", warnings)
	}
}

func TestValidateDictKeys_NonBagComponentStillFlagsUnknown(t *testing.T) {
	// Contrast with the bag case: a component without gastro.Attrs must
	// still flag an unknown key (preserves the #36 dict-key typo guard).
	body := `{{ Card (dict "Title" "Hi" "type" "submit") }}`
	warnings := codegen.ValidateDictKeys(body, cardUses(), cardSchema())
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning for non-bag component, got %d: %v", len(warnings), warnings)
	}
	if !strings.Contains(warnings[0].Message, `unknown prop "type"`) {
		t.Errorf("warning message missing key: %q", warnings[0].Message)
	}
}

func TestValidateImportedComponents(t *testing.T) {
	uses := cardUses() // Card -> components/card.gastro

	// Imported component: no error.
	if err := codegen.ValidateImportedComponents(`<div>{{ Card (dict "Title" "x") }}</div>`, uses); err != nil {
		t.Fatalf("imported component should not error: %v", err)
	}

	// Bare un-imported PascalCase call: hard error, parity with {{ wrap }}.
	err := codegen.ValidateImportedComponents("<div>\n{{ Widget (dict) }}</div>", uses)
	if err == nil {
		t.Fatal("expected error for un-imported component Widget")
	}
	uc, ok := err.(*codegen.UnknownComponentError)
	if !ok {
		t.Fatalf("expected *UnknownComponentError, got %T: %v", err, err)
	}
	if uc.Name != "Widget" {
		t.Errorf("Name = %q, want Widget", uc.Name)
	}
	if uc.Line != 2 {
		t.Errorf("Line = %d, want 2", uc.Line)
	}
	if !strings.Contains(uc.Error(), "not imported") {
		t.Errorf("message %q should mention 'not imported'", uc.Error())
	}

	// Lowercase builtins and gastro helpers must not be flagged as
	// components (they are registered in the stub FuncMap).
	for _, body := range []string{
		`{{ len .Items }}`,
		`<a {{ attrs .Attrs }}>{{ .Label }}</a>`,
		`{{ if .X }}ok{{ end }}`,
	} {
		if err := codegen.ValidateImportedComponents(body, uses); err != nil {
			t.Errorf("body %q should not error: %v", body, err)
		}
	}
}
