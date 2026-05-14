package template

import (
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

	trees, err := parse.Parse("template", body, "{{", "}}", stubFuncs)
	if err != nil {
		return nil, err
	}

	return trees["template"], nil
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
