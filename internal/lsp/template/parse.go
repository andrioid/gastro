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
	stubFuncs := buildStubFuncMap(uses)

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

	for _, u := range uses {
		stubFuncs["__gastro_"+u.Name] = ""
	}
	stubFuncs["__gastro_render_children"] = ""

	return stubFuncs
}
