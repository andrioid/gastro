package codegen_test

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/andrioid/gastro/internal/codegen"
	lsptemplate "github.com/andrioid/gastro/internal/lsp/template"
	"github.com/andrioid/gastro/internal/parser"
)

// TestDictKeyDriftCorpus is the structural enforcement of Phase 2's
// "single source of truth" decision. Every fixture under
// testdata/drift/ is run through both:
//
//  1. codegen.ValidateDictKeysFromAST (the canonical validator), with
//     EmitMissingProps off — matching the behaviour `gastro generate`
//     gets today.
//  2. lsptemplate.Diagnose (the LSP entry point), which delegates to
//     the same codegen function with EmitMissingProps on.
//
// Both paths are then normalised to (line, severity-class, message
// prefix) tuples and asserted equivalent on the *intersection* — i.e.
// every diagnostic the codegen path emits MUST also appear in the LSP
// output (and vice versa), modulo the LSP-only missing-prop warnings
// that codegen explicitly opts out of.
//
// If a future change adds a new validation rule to one path without
// the other (the exact drift class that bit git-pm before Phase 2),
// this test fails immediately, flagging the asymmetry.
//
// Adding a fixture: drop a `<name>.gastro` (and optional
// `<name>.layout.gastro`) into testdata/drift/. The test
// auto-discovers files; no registration step is needed.
func TestDictKeyDriftCorpus(t *testing.T) {
	dir := filepath.Join("testdata", "drift")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading corpus dir %s: %v", dir, err)
	}

	pages := make([]string, 0)
	layouts := make(map[string]string) // base name → layout file path
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".gastro") {
			continue
		}
		base := strings.TrimSuffix(e.Name(), ".gastro")
		if strings.HasSuffix(base, ".layout") {
			pageBase := strings.TrimSuffix(base, ".layout")
			layouts[pageBase] = filepath.Join(dir, e.Name())
			continue
		}
		pages = append(pages, e.Name())
	}
	sort.Strings(pages)

	if len(pages) == 0 {
		t.Fatalf("corpus is empty: add fixtures under %s", dir)
	}

	for _, page := range pages {
		t.Run(strings.TrimSuffix(page, ".gastro"), func(t *testing.T) {
			pagePath := filepath.Join(dir, page)
			pageBytes, err := os.ReadFile(pagePath)
			if err != nil {
				t.Fatalf("read page: %v", err)
			}
			parsedPage, err := parser.Parse(page, string(pageBytes))
			if err != nil {
				t.Fatalf("parse page: %v", err)
			}

			// Build the path → fields schema map. The fixture's
			// layout (if any) lives at <name>.layout.gastro and is
			// imported by the page via `Layout
			// "components/<name>.layout.gastro"`.
			propsByPath := map[string][]codegen.StructField{}
			propsByAlias := map[string][]codegen.StructField{}
			pageBase := strings.TrimSuffix(page, ".gastro")
			if layoutPath, ok := layouts[pageBase]; ok {
				layoutBytes, err := os.ReadFile(layoutPath)
				if err != nil {
					t.Fatalf("read layout: %v", err)
				}
				parsedLayout, err := parser.Parse(filepath.Base(layoutPath), string(layoutBytes))
				if err != nil {
					t.Fatalf("parse layout: %v", err)
				}
				_, hoisted := codegen.HoistTypeDeclarations(parsedLayout.Frontmatter)
				fields := codegen.ParseStructFields(hoisted)
				// Match the page's import alias path.
				for _, u := range parsedPage.Uses {
					propsByPath[u.Path] = fields
					propsByAlias[u.Name] = fields
				}
			}

			// Build the LSP tree (lenient stub FuncMap) so both
			// paths see the same parse output.
			tree, err := lsptemplate.ParseTemplateBody(parsedPage.TemplateBody, parsedPage.Uses)
			if err != nil {
				t.Fatalf("parse template body: %v", err)
			}

			// Codegen path: position-rich diagnostics with EmitMissingProps off.
			codegenDiags := codegen.ValidateDictKeysFromAST(
				parsedPage.TemplateBody,
				tree,
				parsedPage.Uses,
				propsByPath,
				codegen.ValidateDictKeysOptions{},
			)

			// LSP path: thin wrapper, alias-keyed propsMap.
			lspDiags := lsptemplate.DiagnoseComponentProps(
				parsedPage.TemplateBody,
				tree,
				parsedPage.Uses,
				propsByAlias,
			)

			// Normalise. The LSP path emits missing-prop warnings
			// that codegen opts out of; filter them so we compare
			// the intersection. Everything else MUST match.
			codegenSet := normaliseCodegen(codegenDiags)
			lspSet := normaliseLSPSkippingMissingProp(lspDiags)

			if !equalNormalised(codegenSet, lspSet) {
				t.Errorf("DRIFT detected on fixture %s\n"+
					"  codegen verdicts: %v\n"+
					"  lsp     verdicts: %v\n"+
					"  (missing-prop warnings filtered from LSP set; codegen opts out by default)",
					page, codegenSet, lspSet)
			}
		})
	}
}

type normalisedDiag struct {
	line     int
	severity int
	prefix   string // first 32 bytes of message — message text may diverge in wording but kind must match
}

func normaliseCodegen(diags []codegen.DictKeyDiagnostic) []normalisedDiag {
	out := make([]normalisedDiag, 0, len(diags))
	for _, d := range diags {
		out = append(out, normalisedDiag{
			line:     d.StartLine,
			severity: int(d.Severity),
			prefix:   messagePrefix(d.Message),
		})
	}
	sortNorm(out)
	return out
}

func normaliseLSPSkippingMissingProp(diags []lsptemplate.Diagnostic) []normalisedDiag {
	out := make([]normalisedDiag, 0, len(diags))
	for _, d := range diags {
		if strings.HasPrefix(d.Message, "missing prop") {
			// Codegen-side opts out; comparing on this would make
			// the parity test require a permanent asymmetry list.
			// Instead: filter LSP missing-prop warnings out of the
			// drift set entirely; they're tested separately in
			// internal/lsp/template/completions_test.go.
			continue
		}
		out = append(out, normalisedDiag{
			line:     d.StartLine + 1, // LSP uses 0-indexed lines; codegen uses 1-indexed
			severity: d.Severity,
			prefix:   messagePrefix(d.Message),
		})
	}
	sortNorm(out)
	return out
}

func messagePrefix(msg string) string {
	const n = 32
	if len(msg) <= n {
		return msg
	}
	return msg[:n]
}

func sortNorm(s []normalisedDiag) {
	sort.Slice(s, func(i, j int) bool {
		if s[i].line != s[j].line {
			return s[i].line < s[j].line
		}
		if s[i].severity != s[j].severity {
			return s[i].severity < s[j].severity
		}
		return s[i].prefix < s[j].prefix
	})
}

func equalNormalised(a, b []normalisedDiag) bool {
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
