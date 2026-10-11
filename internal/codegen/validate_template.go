package codegen

import (
	"fmt"
	"slices"
	"strings"
	"text/template/parse"

	gastroParser "github.com/andrioid/gastro/internal/parser"
)

// ValidateTemplate checks a template body exactly as written in the
// .gastro file (before TransformTemplate) and returns every finding,
// positioned in that body (1-indexed line and byte column):
//
//   - SeverityError for each call to a PascalCase component that is not
//     imported in the frontmatter;
//   - every dict-key finding from ValidateDictKeysFromAST.
//
// Validating the source rather than the transformed body keeps positions
// exact: TransformTemplate moves {{ wrap }} children into a trailing
// {{ define }} block, which shifts every line after it.
//
// Functions the app registers at runtime (WithFuncs) are unknown here, so
// function names are not resolved while parsing. A body that does not
// parse yields no findings: template syntax errors are reported by the
// runtime parse, which owns them.
func ValidateTemplate(body string, uses []gastroParser.UseDeclaration, propsByPath map[string][]StructField) []TemplateDiagnostic {
	src := maskForValidation(body)

	treeSet := map[string]*parse.Tree{}
	t := parse.New("template")
	t.Mode = parse.SkipFuncCheck
	if _, err := t.Parse(src, "{{", "}}", treeSet); err != nil {
		return nil
	}

	known := dictValidationStubFuncs(uses)
	known["wrap"] = ""

	names := make([]string, 0, len(treeSet))
	for name := range treeSet {
		names = append(names, name)
	}
	slices.Sort(names)

	var diags []TemplateDiagnostic
	for _, name := range names {
		tree := treeSet[name]
		if tree == nil || tree.Root == nil {
			continue
		}
		walkParseNodes(tree.Root.Nodes, func(n parse.Node) {
			id, ok := n.(*parse.IdentifierNode)
			if !ok || !isPascalCase(id.Ident) {
				return
			}
			if _, isKnown := known[id.Ident]; isKnown {
				return
			}
			start := nodeLineCol(src, int(id.Position()))
			end := nodeLineCol(src, int(id.Position())+len(id.Ident))
			diags = append(diags, TemplateDiagnostic{
				StartLine: start.Line,
				StartCol:  start.Col,
				EndLine:   end.Line,
				EndCol:    end.Col,
				Severity:  SeverityError,
				Message: fmt.Sprintf("unknown component %q: not imported (add `import %s \"components/<file>.gastro\"` to the frontmatter)",
					id.Ident, id.Ident),
			})
		})
		diags = append(diags, ValidateDictKeysFromAST(src, tree, uses, propsByPath, ValidateDictKeysOptions{})...)
	}
	slices.SortStableFunc(diags, func(a, b TemplateDiagnostic) int {
		if a.StartLine != b.StartLine {
			return a.StartLine - b.StartLine
		}
		return a.StartCol - b.StartCol
	})
	return diags
}

// maskForValidation returns body with every byte offset unchanged but in a
// form text/template/parse accepts. Raw blocks (markers and content) and the
// {{ end }} closing each {{ wrap }} become spaces; newlines are kept so
// line numbers do not move. The {{ wrap X ... }} opener stays and parses
// as a call, which ValidateDictKeysFromAST recognises as the wrap form.
func maskForValidation(body string) string {
	b := []byte(body)
	blank := func(from, to int) {
		for i := from; i < to; i++ {
			if b[i] != '\n' {
				b[i] = ' '
			}
		}
	}

	// Raw blocks first: their content is literal text and may contain
	// anything, including unbalanced actions. Scanning body (not b) is
	// equivalent because the scan never revisits a blanked range.
	for i := 0; i < len(body); {
		start := findNextAction(body, i)
		if start == -1 {
			break
		}
		if strings.HasPrefix(body[start:], "{{/*") {
			end := strings.Index(body[start:], "*/}}")
			if end == -1 {
				break
			}
			i = start + end + 4
			continue
		}
		keyword, actionEnd := readActionKeyword(body, start)
		if keyword != "raw" {
			i = actionEnd
			continue
		}
		_, endrawEnd, err := findEndraw(body, actionEnd)
		if err != nil {
			// Unclosed raw block: TransformTemplate reports it.
			break
		}
		blank(start, endrawEnd)
		i = endrawEnd
	}

	// Then the {{ end }} of every wrap. Ends are located before any is
	// blanked so nested wraps each find their own closer.
	masked := string(b)
	var ends [][2]int
	for _, loc := range wrapRegex.FindAllStringIndex(masked, -1) {
		openClose := findActionClose(masked, loc[0])
		if openClose == -1 {
			continue
		}
		endStart, endClose, err := findMatchingEnd(masked, openClose+2)
		if err != nil {
			// Unbalanced wrap: TransformTemplate reports it.
			continue
		}
		ends = append(ends, [2]int{endStart, endClose})
	}
	for _, e := range ends {
		blank(e[0], e[1])
	}
	return string(b)
}

func isPascalCase(name string) bool {
	return name != "" && name[0] >= 'A' && name[0] <= 'Z'
}
