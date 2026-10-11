package codegen

import (
	"fmt"
	"math"
	"strings"
	"text/template/parse"
)

// literalTypeMismatch reports whether a literal dict value (string,
// number, bool or nil) cannot be assigned to a Props field (prop type
// checks, on unless --no-typecheck-props). The rules mirror Go's
// assignability of untyped constants, which is how
// gastroRuntime.CheckPropTypes treats the values template literals
// evaluate to at render time.
//
// Field types are only known as source text. Builtin types, composite
// type literals, any/interface{}, error and html/template's string types
// are resolved; any other named type (a local `type Variant string`, a
// qualified time.Time) is skipped, leaving it to the render-time check
// rather than risking a false positive.
func literalTypeMismatch(body, compName string, field StructField, value parse.Node) (TemplateDiagnostic, bool) {
	var kind string
	switch value.(type) {
	case *parse.StringNode:
		kind = "untyped string constant"
	case *parse.NumberNode:
		kind = "untyped number constant"
	case *parse.BoolNode:
		kind = "untyped bool constant"
	case *parse.NilNode:
		kind = "untyped nil"
	default:
		return TemplateDiagnostic{}, false
	}
	class := classifyFieldType(field.Type)
	if class.kind == fieldUnknown || literalAssignable(value, class) {
		return TemplateDiagnostic{}, false
	}
	text := value.String()
	start := nodeLineCol(body, int(value.Position()))
	end := nodeLineCol(body, int(value.Position())+len(text))
	if kind != "untyped nil" {
		text += " (" + kind + ")"
	}
	return TemplateDiagnostic{
		StartLine: start.Line,
		StartCol:  start.Col,
		EndLine:   end.Line,
		EndCol:    end.Col,
		Severity:  SeverityError,
		Message:   fmt.Sprintf("prop %q on component %s: cannot use %s as %s value", field.Name, compName, text, field.Type),
	}, true
}

type fieldKind int

const (
	fieldUnknown   fieldKind = iota
	fieldAny                 // any, interface{}: accepts every literal
	fieldNilable             // pointer, slice, map, func, chan, error, interface with methods
	fieldNoLiteral           // array, struct: no literal is assignable
	fieldString
	fieldBool
	fieldInt
	fieldUint
	fieldFloat
	fieldComplex
)

type fieldClass struct {
	kind fieldKind
	bits int // size of numeric kinds
}

// htmlTemplateStrings are html/template's string-kinded types, which a
// string literal converts to.
var htmlTemplateStrings = map[string]bool{
	"template.HTML": true, "template.HTMLAttr": true, "template.URL": true,
	"template.JS": true, "template.JSStr": true, "template.CSS": true,
	"template.Srcset": true,
}

func classifyFieldType(typ string) fieldClass {
	typ = strings.TrimSpace(typ)
	switch typ {
	case "any", "interface{}":
		return fieldClass{kind: fieldAny}
	case "error":
		return fieldClass{kind: fieldNilable}
	case "string":
		return fieldClass{kind: fieldString}
	case "bool":
		return fieldClass{kind: fieldBool}
	case "int", "int64":
		return fieldClass{kind: fieldInt, bits: 64}
	case "int8":
		return fieldClass{kind: fieldInt, bits: 8}
	case "int16":
		return fieldClass{kind: fieldInt, bits: 16}
	case "int32", "rune":
		return fieldClass{kind: fieldInt, bits: 32}
	case "uint", "uint64", "uintptr":
		return fieldClass{kind: fieldUint, bits: 64}
	case "uint8", "byte":
		return fieldClass{kind: fieldUint, bits: 8}
	case "uint16":
		return fieldClass{kind: fieldUint, bits: 16}
	case "uint32":
		return fieldClass{kind: fieldUint, bits: 32}
	case "float32":
		return fieldClass{kind: fieldFloat, bits: 32}
	case "float64":
		return fieldClass{kind: fieldFloat, bits: 64}
	case "complex64", "complex128":
		return fieldClass{kind: fieldComplex}
	}
	if htmlTemplateStrings[typ] {
		return fieldClass{kind: fieldString}
	}
	switch {
	case strings.HasPrefix(typ, "*"), strings.HasPrefix(typ, "[]"),
		strings.HasPrefix(typ, "map["), strings.HasPrefix(typ, "func("),
		strings.HasPrefix(typ, "chan "), strings.HasPrefix(typ, "<-chan "),
		strings.HasPrefix(typ, "interface{"), strings.HasPrefix(typ, "interface {"):
		return fieldClass{kind: fieldNilable}
	case strings.HasPrefix(typ, "["), strings.HasPrefix(typ, "struct{"), strings.HasPrefix(typ, "struct {"):
		return fieldClass{kind: fieldNoLiteral}
	}
	return fieldClass{kind: fieldUnknown}
}

func literalAssignable(value parse.Node, class fieldClass) bool {
	if class.kind == fieldAny {
		return true
	}
	switch v := value.(type) {
	case *parse.NilNode:
		return class.kind == fieldNilable
	case *parse.StringNode:
		return class.kind == fieldString
	case *parse.BoolNode:
		return class.kind == fieldBool
	case *parse.NumberNode:
		switch class.kind {
		case fieldInt:
			lim := int64(1) << (class.bits - 1)
			return v.IsInt && (class.bits == 64 || (v.Int64 >= -lim && v.Int64 < lim))
		case fieldUint:
			return v.IsUint && (class.bits == 64 || v.Uint64 < uint64(1)<<class.bits)
		case fieldFloat:
			return v.IsFloat && (class.bits == 64 || math.Abs(v.Float64) <= math.MaxFloat32)
		case fieldComplex:
			return true
		}
	}
	return false
}
