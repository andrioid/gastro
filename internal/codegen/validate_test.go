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
	// TransformTemplate injects "__children" for {{ wrap X ... }} blocks.
	// The validator must not warn about it even though it isn't a Props field.
	body := `{{ Card (dict "Title" "Hi" "__children" (__gastro_render_children "x" .)) }}`
	warnings := codegen.ValidateDictKeys(body, cardUses(), cardSchema())
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings (compile-time __children should be skipped), got: %v", warnings)
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

func TestValidateDictKeys_BareCallNoDict(t *testing.T) {
	body := `{{ Card }}`
	warnings := codegen.ValidateDictKeys(body, cardUses(), cardSchema())
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings on bare call, got: %v", warnings)
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
