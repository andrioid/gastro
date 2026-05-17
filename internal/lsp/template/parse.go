package template

import (
	"regexp"
	"text/template/parse"

	gastroparser "github.com/andrioid/gastro/internal/parser"
	"github.com/andrioid/gastro/pkg/gastro"
)

// ParseTemplateBody attempts to parse the template body using Go's
// text/template/parse package. Returns the parse tree or an error
// (e.g. when the template is syntactically incomplete during editing).
//
// A lenient stub FuncMap is built from gastro's default functions and
// any component functions derived from use declarations. The parser
// only checks that function names exist — stub values are sufficient.
func ParseTemplateBody(body string, uses []gastroparser.UseDeclaration) (*parse.Tree, error) {
	return ParseTemplateBodyWithRequestFuncs(body, uses, nil)
}

// ParseTemplateBodyWithRequestFuncs is the WithRequestFuncs-aware
// variant of ParseTemplateBody. requestFuncNames is the set of helper
// names discovered from the project's gastro.WithRequestFuncs binders;
// they are added as stubs so {{ t "..." }} and similar request-aware
// invocations parse without spurious "function not defined" errors.
func ParseTemplateBodyWithRequestFuncs(body string, uses []gastroparser.UseDeclaration, requestFuncNames []string) (*parse.Tree, error) {
	stubFuncs := buildStubFuncMap(uses)
	for _, name := range requestFuncNames {
		if _, exists := stubFuncs[name]; !exists {
			stubFuncs[name] = ""
		}
	}

	// Gastro's {{ wrap X ... }}...{{ end }} extension is the only
	// construct text/template/parse genuinely cannot accept: with `wrap`
	// registered as a function (not a block keyword), the parser sees
	// the trailing {{ end }} as "unexpected" and bails on the whole
	// body. Rewrite wrap to if at the parser level so the body is
	// parseable; the substitution is byte-for-byte preserving so AST
	// positions still map directly to the original source. See
	// rewriteWrapForParsing for the position-preservation contract.
	parseBody := rewriteWrapForParsing(body)

	trees, err := parse.Parse("template", parseBody, "{{", "}}", stubFuncs)
	if err != nil {
		return nil, err
	}

	return trees["template"], nil
}

// wrapKeywordRegex matches the `wrap` keyword at the start of a Gastro
// action opener. The two capture groups bracket the keyword so the
// substitution can splice in `if  ` (four characters, like `wrap`)
// without disturbing surrounding whitespace.
//
// Anchored on `{{` so a literal `wrap` inside a quoted string
// argument (e.g. `{{ X (dict "K" "wrap me") }}`) is not matched.
var wrapKeywordRegex = regexp.MustCompile(`(\{\{-?\s*)wrap(\s)`)

// rewriteWrapForParsing replaces every `{{ wrap X ` action opener with
// `{{ if   X ` so Go's text/template/parse accepts the body. The
// rewrite is purely a parsing aid: it never reaches codegen and does
// not alter semantics. `gastro generate` runs the full
// codegen.TransformTemplate against the original body to produce the
// real, semantic-preserving rewrite.
//
// Position-preservation contract: `wrap` and `if  ` are both four
// characters. The regex captures the opening `{{`, optional `-`,
// whitespace, and the single whitespace character following the
// keyword — all preserved verbatim. Only the four bytes of the
// keyword itself are substituted. Every byte from the component
// identifier onward keeps its original offset, so (line, col)
// calculations against the original body remain valid for any
// diagnostic emitted off the parsed tree.
//
// After the rewrite, `{{ wrap X (dict ...) }}...{{ end }}` becomes
// `{{ if   X (dict ...) }}...{{ end }}`. In the AST, this surfaces as
// an IfNode whose pipeline's first command starts with the component
// identifier — the same shape codegen.ValidateDictKeysFromAST already
// handles via its bare-call branch, just nested one level deeper
// inside an IfNode. walkParseNodes recurses through IfNode.Pipe and
// IfNode.List, so the CommandNode and its body are reached normally.
//
// Choice of `if` over `with`/`range`/`block`/`define`:
//   - `if` does not change dot inside its body, matching wrap's
//     pass-through scoping. `with` and `range` change dot and would
//     break scope-aware variable diagnostics inside wrap bodies.
//   - `block` and `define` require a string argument, breaking length
//     preservation.
func rewriteWrapForParsing(body string) string {
	return wrapKeywordRegex.ReplaceAllString(body, "${1}if  ${2}")
}

// Go template builtins that text/template/parse requires in the FuncMap.
// These are automatically available at runtime via template.New().Funcs()
// but parse.Parse() needs explicit registration.
var goTemplateBuiltins = []string{
	"and", "or", "not",
	"eq", "ne", "lt", "le", "gt", "ge",
	"print", "printf", "println",
	"len", "index", "slice", "call",
	"html", "js", "urlquery",
}

// buildStubFuncMap creates a FuncMap with string stubs for all template
// functions that may appear in a gastro template. The parser only checks
// key existence — the values are never called.
func buildStubFuncMap(uses []gastroparser.UseDeclaration) map[string]any {
	defaultFuncs := gastro.DefaultFuncs()
	stubFuncs := make(map[string]any, len(defaultFuncs)+len(uses)+len(goTemplateBuiltins)+2)

	for _, name := range goTemplateBuiltins {
		stubFuncs[name] = ""
	}

	for name := range defaultFuncs {
		stubFuncs[name] = ""
	}

	// Components are registered under their PascalCase names (bare function calls).
	for _, u := range uses {
		stubFuncs[u.Name] = ""
	}
	stubFuncs["__gastro_render_children"] = ""

	// wrap, raw, and endraw are compile-time keywords that appear
	// in untransformed templates. The LSP parses raw templates, so they
	// must be in the FuncMap for parsing.
	stubFuncs["wrap"] = ""
	stubFuncs["raw"] = ""
	stubFuncs["endraw"] = ""

	return stubFuncs
}
