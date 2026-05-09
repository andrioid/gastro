package codegen_test

import (
	"testing"

	"github.com/andrioid/gastro/internal/codegen"
)

func TestSyntheticPropKey(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantKind codegen.SyntheticPropKind
		wantOK   bool
	}{
		{"canonical Children", "Children", codegen.SyntheticChildren, true},
		{"deprecated __children", "__children", codegen.SyntheticDeprecatedChildren, true},
		{"user field", "Title", codegen.SyntheticNone, false},
		{"empty string", "", codegen.SyntheticNone, false},
		{"lowercase children", "children", codegen.SyntheticNone, false},
		{"case-sensitive _Children", "_Children", codegen.SyntheticNone, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotKind, gotOK := codegen.SyntheticPropKey(tt.input)
			if gotKind != tt.wantKind {
				t.Errorf("SyntheticPropKey(%q) kind = %v, want %v", tt.input, gotKind, tt.wantKind)
			}
			if gotOK != tt.wantOK {
				t.Errorf("SyntheticPropKey(%q) ok = %v, want %v", tt.input, gotOK, tt.wantOK)
			}
		})
	}
}

// TestSyntheticPropKey_BoolRedundancy documents the (kind, ok) contract:
// ok is true iff kind != SyntheticNone. If this invariant ever breaks,
// callers using the boolean form will silently disagree with callers
// using the kind comparison form.
func TestSyntheticPropKey_BoolRedundancy(t *testing.T) {
	for _, name := range []string{"Children", "__children", "Title", "", "children"} {
		kind, ok := codegen.SyntheticPropKey(name)
		if ok != (kind != codegen.SyntheticNone) {
			t.Errorf("SyntheticPropKey(%q): kind=%v ok=%v — invariant broken", name, kind, ok)
		}
	}
}
