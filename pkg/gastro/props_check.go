package gastro

import (
	"fmt"
	"log/slog"
	"math"
	"reflect"
	"strings"
	"sync"
)

// RequiredTag is the struct tag that marks a Props field as required:
//
//	type Props struct {
//		Title string `gastro:"required"`
//	}
//
// Props are optional by default. A required field must be passed by every
// template `dict` call (checked by `gastro generate`) and must not hold its
// zero value at render time (checked by CheckRequiredProps).
const RequiredTag = "required"

// RequiredPropsError lists the gastro:"required" fields that hold their
// zero value, in declaration order.
type RequiredPropsError struct {
	Fields []string
}

func (e *RequiredPropsError) Error() string {
	quoted := make([]string, len(e.Fields))
	for i, f := range e.Fields {
		quoted[i] = fmt.Sprintf("%q", f)
	}
	noun := "prop"
	if len(e.Fields) > 1 {
		noun = "props"
	}
	return fmt.Sprintf("required %s %s not set (zero value)", noun, strings.Join(quoted, ", "))
}

// requiredFieldIndex caches, per struct type, the indices of its
// gastro:"required" fields so the per-render check does not re-parse tags.
var requiredFieldIndex sync.Map // reflect.Type -> []int

func requiredFields(t reflect.Type) []int {
	if cached, ok := requiredFieldIndex.Load(t); ok {
		return cached.([]int)
	}
	var idx []int
	for i := range t.NumField() {
		if IsRequiredTag(t.Field(i).Tag) {
			idx = append(idx, i)
		}
	}
	requiredFieldIndex.Store(t, idx)
	return idx
}

// IsRequiredTag reports whether a struct tag carries gastro:"required".
// The gastro tag value is a comma-separated option list.
func IsRequiredTag(tag reflect.StructTag) bool {
	value, ok := tag.Lookup("gastro")
	if !ok {
		return false
	}
	for opt := range strings.SplitSeq(value, ",") {
		if strings.TrimSpace(opt) == RequiredTag {
			return true
		}
	}
	return false
}

// CheckRequiredProps reports every gastro:"required" field of v that holds
// its zero value, as a *RequiredPropsError. v must be a struct or a pointer
// to one. It returns nil when every required field is set.
//
// Generated component code calls it on the decoded Props of every render
// (template dict calls and typed Go rendering alike) and hands a non-nil
// result to PropsViolation.
func CheckRequiredProps(v any) error {
	rv := reflect.ValueOf(v)
	for rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return fmt.Errorf("CheckRequiredProps: nil %s", rv.Type())
		}
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return fmt.Errorf("CheckRequiredProps: want a struct, got %T", v)
	}
	var missing []string
	for _, i := range requiredFields(rv.Type()) {
		if rv.Field(i).IsZero() {
			missing = append(missing, rv.Type().Field(i).Name)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return &RequiredPropsError{Fields: missing}
}

// PropTypeMismatch is one dict value that is not assignable to its field.
type PropTypeMismatch struct {
	Field string
	Got   string // dynamic type of the value, "nil" for a nil value
	Want  string // field type
}

// PropTypeError lists every dict value CheckPropTypes rejected, in field
// declaration order.
type PropTypeError struct {
	Mismatches []PropTypeMismatch
}

func (e *PropTypeError) Error() string {
	parts := make([]string, len(e.Mismatches))
	for i, m := range e.Mismatches {
		parts[i] = fmt.Sprintf("prop %q: cannot use %s as %s", m.Field, m.Got, m.Want)
	}
	return strings.Join(parts, "; ")
}

// CheckPropTypes checks a template dict against the Props struct T without
// coercion (gastro --typecheck-props). A value is accepted when it is
// assignable to the field type, or when it is a value of a predeclared
// basic type (what template literals evaluate to) of the same kind as the
// field and converts exactly: "x" into a string-kinded type such as
// template.HTML, 3 into int64, 2 into float64. Rejected, among others:
// "3" into int, 3 into string, "true" into bool, 2.5 into int, and nil
// into a type that cannot be nil.
//
// Keys that are not fields of T, and the gastro.Attrs bag field, are not
// checked: the bag captures them. It returns a *PropTypeError or nil.
func CheckPropTypes[T any](m map[string]any) error {
	rt := reflect.TypeFor[T]()
	if rt.Kind() != reflect.Struct {
		return fmt.Errorf("CheckPropTypes: T must be a struct, got %s", rt)
	}
	var mismatches []PropTypeMismatch
	for i := range rt.NumField() {
		field := rt.Field(i)
		if field.Type == attrsType {
			continue
		}
		val, ok := m[field.Name]
		if !ok || strictAssignable(val, field.Type) {
			continue
		}
		got := "nil"
		if val != nil {
			got = reflect.TypeOf(val).String()
		}
		mismatches = append(mismatches, PropTypeMismatch{Field: field.Name, Got: got, Want: field.Type.String()})
	}
	if len(mismatches) == 0 {
		return nil
	}
	return &PropTypeError{Mismatches: mismatches}
}

func strictAssignable(val any, ft reflect.Type) bool {
	if val == nil {
		switch ft.Kind() {
		case reflect.Pointer, reflect.Map, reflect.Slice, reflect.Interface, reflect.Func, reflect.Chan, reflect.UnsafePointer:
			return true
		}
		return false
	}
	v := reflect.ValueOf(val)
	vt := v.Type()
	if vt.AssignableTo(ft) {
		return true
	}
	// Only predeclared basic types stand in for untyped template literals;
	// a value of a named type must be assignable as-is.
	if vt.PkgPath() != "" || vt.Name() == "" {
		return false
	}
	switch {
	case vt.Kind() == reflect.String && ft.Kind() == reflect.String,
		vt.Kind() == reflect.Bool && ft.Kind() == reflect.Bool:
		return true
	case isNumeric(vt.Kind()) && isNumeric(ft.Kind()):
		return convertsExactly(v, ft)
	}
	return false
}

func isNumeric(k reflect.Kind) bool {
	return isInt(k) || isUint(k) || k == reflect.Float32 || k == reflect.Float64
}

func isInt(k reflect.Kind) bool {
	return k >= reflect.Int && k <= reflect.Int64
}

func isUint(k reflect.Kind) bool {
	return k >= reflect.Uint && k <= reflect.Uintptr
}

// convertsExactly reports whether the numeric value v converts to ft
// without overflow, truncation or sign change.
func convertsExactly(v reflect.Value, ft reflect.Type) bool {
	target := reflect.New(ft).Elem()
	switch k := v.Kind(); {
	case isInt(k):
		i := v.Int()
		switch {
		case isInt(ft.Kind()):
			return !target.OverflowInt(i)
		case isUint(ft.Kind()):
			return i >= 0 && !target.OverflowUint(uint64(i))
		default:
			f := float64(i)
			return int64(f) == i && !target.OverflowFloat(f)
		}
	case isUint(k):
		u := v.Uint()
		switch {
		case isInt(ft.Kind()):
			return u <= math.MaxInt64 && !target.OverflowInt(int64(u))
		case isUint(ft.Kind()):
			return !target.OverflowUint(u)
		default:
			f := float64(u)
			return uint64(f) == u && !target.OverflowFloat(f)
		}
	default:
		f := v.Float()
		switch {
		case isInt(ft.Kind()):
			return f == math.Trunc(f) && f >= math.MinInt64 && f < math.MaxInt64 && !target.OverflowInt(int64(f))
		case isUint(ft.Kind()):
			return f == math.Trunc(f) && f >= 0 && f < math.MaxUint64 && !target.OverflowUint(uint64(f))
		default:
			return !target.OverflowFloat(f)
		}
	}
}

// PropsViolation applies gastro's policy for a broken props contract (a
// required prop holding its zero value, or a --typecheck-props mismatch).
// In dev mode it returns the violation as an error, which fails the
// render. In production it logs a warning through log/slog and returns
// nil, so the render proceeds with the props as decoded. A nil err is a
// no-op.
func PropsViolation(dev bool, component string, err error) error {
	if err == nil {
		return nil
	}
	err = fmt.Errorf("component %s: %w", component, err)
	if dev {
		return err
	}
	slog.Warn("gastro: props contract violated; rendering anyway", "component", component, "error", err.Error())
	return nil
}
