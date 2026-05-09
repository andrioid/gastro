package server

import (
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"github.com/andrioid/gastro/internal/lsp/shadow"
)

// TestCodegenLSPContract_AnchorAndProbeStaysParseable locks down the
// structural contract between codegen.GenerateHandler (the source of the
// virtual .go file the LSP consumes) and the LSP probe / suppression-line
// helpers (insertProbe, findSuppressionLine, queryVariableTypes' "_ = "
// scan).
//
// Background: the LSP synthesises completion / hover / go-to-definition
// answers by mutating the shadow Go source on the fly — most importantly,
// by injecting a probe assignment of the form
//
//	_ = <ChainExpr>.<Field>
//
// at a line determined by insertProbe. That helper used to look for a
// closing `}` immediately preceded by a `_ = VarName` line. When the
// codegen evolved (the page model gained a BodyWritten check, __data
// construction, and template execution between the suppression block and
// the function close), the heuristic stopped matching for every page and
// component — silently breaking field validation in range/with scopes.
//
// This test re-runs the codegen for canonical page and component
// frontmatter, exercises both anchor helpers, applies a representative
// probe, and re-parses the modified source with go/parser. A green run
// means insertProbe still finds a valid anchor AND the probe insertion
// produces syntactically valid Go. A future codegen reshuffle that breaks
// either invariant fails this test instead of silently degrading the LSP.
func TestCodegenLSPContract_AnchorAndProbeStaysParseable(t *testing.T) {
	cases := []struct {
		name, filename, content, knownVar string
		// probeChain is the chain expression that exercises the same
		// insertion path the resolver uses (Rows[0] for a slice, Foo
		// for a non-slice top-level var).
		probeChain string
	}{
		{
			name:     "page model with slice export",
			filename: "pages/index.gastro",
			content: `---
type RowData struct {
	Y     int
	Agent string
}

rows := []RowData{{Y: 0, Agent: "x"}}
Rows := rows
---
<svg>{{ range .Rows }}<text>{{ .Agent }}</text>{{ end }}</svg>
`,
			knownVar:   "Rows",
			probeChain: "Rows[0]",
		},
		{
			name:     "component model with Props",
			filename: "components/dashboard.gastro",
			content: `---
type Props struct {
	Items []string
}

Items := gastro.Props().Items
---
<ul>{{ range .Items }}<li>{{ . }}</li>{{ end }}</ul>
`,
			knownVar:   "Items",
			probeChain: "Items[0]",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			vf, err := shadow.GenerateVirtualFile(tc.filename, tc.content)
			if err != nil {
				t.Fatalf("GenerateVirtualFile: %v", err)
			}
			if vf == nil || vf.GoSource == "" {
				t.Fatalf("expected non-empty shadow source")
			}

			// 1. Suppression-line scanner (queryVariableTypes,
			// headFieldDefinition) must still find "_ = <Var>" anchors
			// for every exported frontmatter var. Without this, the
			// LSP can't ask gopls for the variable's type.
			line, char, found := findSuppressionLine(vf.GoSource, tc.knownVar)
			if !found {
				t.Fatalf("findSuppressionLine(%q) returned not-found.\nshadow source:\n%s",
					tc.knownVar, vf.GoSource)
			}
			if line < 0 || char <= 0 {
				t.Errorf("findSuppressionLine returned implausible position line=%d char=%d", line, char)
			}

			// 2. insertProbe must find a stable anchor. Without this,
			// chain-based probes (resolveFieldsViaChain,
			// chainedFieldDefinition) silently return nil and the
			// walker / definition handler fall through to no-op.
			probeText := "\t_ = " + tc.probeChain + ".X"
			probeLine, ok := insertProbe(vf.GoSource, probeText)
			if !ok {
				t.Fatalf("insertProbe returned not-found for chain %q.\nshadow source:\n%s",
					tc.probeChain, vf.GoSource)
			}

			// 3. The injected probe must produce SYNTACTICALLY valid
			// Go. gopls is lenient about partial code (it accepts
			// `_ = X.` for completion) but a structural break in the
			// shadow — e.g. probe inserted outside any function body
			// — would silently degrade real gopls features. Use a
			// complete probe expression so go/parser is strict.
			probed := applyInsertProbe(vf.GoSource, probeText, probeLine)
			fset := token.NewFileSet()
			if _, err := parser.ParseFile(fset, tc.filename+".go", probed, parser.AllErrors); err != nil {
				t.Fatalf("probe insertion produced unparseable Go: %v\n--- probed source (probeLine=%d) ---\n%s",
					err, probeLine, probed)
			}

			// 4. The probe must land INSIDE the handler function so
			// the chain expression sees the right lexical scope. We
			// approximate this by checking the line above the probe
			// is also indented (suppression lines are tab-indented by
			// the codegen), which is true exactly when the probe is
			// inside a function body and not at package scope.
			lines := strings.Split(vf.GoSource, "\n")
			if probeLine == 0 || probeLine > len(lines) {
				t.Fatalf("probe line %d out of range (have %d lines)", probeLine, len(lines))
			}
			prev := lines[probeLine-1]
			if !strings.HasPrefix(prev, "\t") && !strings.HasPrefix(prev, " ") {
				t.Errorf("probe inserted at package scope (prev line not indented): %q", prev)
			}
		})
	}
}
