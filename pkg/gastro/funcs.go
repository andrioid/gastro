package gastro

import (
	"encoding/json"
	"html/template"
	"reflect"
	"strings"
	"time"
)

// DefaultFuncs returns the built-in template functions available in all
// .gastro templates.
func DefaultFuncs() template.FuncMap {
	return template.FuncMap{
		"upper":    strings.ToUpper,
		"lower":    strings.ToLower,
		"trim":     strings.TrimSpace,
		"contains": strings.Contains,
		"replace":  strings.ReplaceAll,
		"join":     strings.Join,
		"split":    strings.Split,
		"safeHTML": func(s string) template.HTML { return template.HTML(s) },
		"safeAttr": func(s string) template.HTMLAttr { return template.HTMLAttr(s) },
		"safeURL":  func(s string) template.URL { return template.URL(s) },
		"safeCSS":  func(s string) template.CSS { return template.CSS(s) },
		"safeJS":   func(s string) template.JS { return template.JS(s) },
		"dict":     dictFunc,
		"list":     func(args ...any) []any { return args },
		"default":  defaultFunc,
		"json":     jsonFunc,
		// timeFormat takes layout first so it works with pipes:
		// {{ .CreatedAt | timeFormat "Jan 2, 2006" }}
		"timeFormat": func(layout string, t time.Time) string {
			return t.Format(layout)
		},
		// Membership / lookup helpers. The audit's git-pm consumer kept
		// reinventing `activeSet := map[string]bool{...}` in frontmatter
		// because templates couldn't ask "is this thing in that thing?".
		"has":    hasFunc,
		"hasKey": hasKeyFunc,
		"set":    setFunc,
	}
}

// dictFunc builds a map[string]any from alternating key/value pairs.
func dictFunc(pairs ...any) map[string]any {
	m := make(map[string]any, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		key, ok := pairs[i].(string)
		if !ok {
			continue
		}
		m[key] = pairs[i+1]
	}
	return m
}

// defaultFunc returns fallback if val is empty/zero, otherwise val.
func defaultFunc(fallback, val any) any {
	if val == nil {
		return fallback
	}
	if s, ok := val.(string); ok && s == "" {
		return fallback
	}
	if i, ok := val.(int); ok && i == 0 {
		return fallback
	}
	return val
}

func jsonFunc(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

// hasFunc reports whether haystack contains needle. If haystack is a single
// argument it must be a slice/array; for ergonomics it can also be passed
// as variadic arguments:
//
//	{{ if has .Tag .ActiveTags }}...{{ end }}
//	{{ if has .Status "open" "in_progress" }}...{{ end }}
//
// Comparison uses reflect.DeepEqual so basic scalar types and equal-by-value
// composites both work.
func hasFunc(needle any, haystack ...any) bool {
	// Single-argument slice/array form: has needle haystack.
	if len(haystack) == 1 {
		rv := reflect.ValueOf(haystack[0])
		if rv.IsValid() && (rv.Kind() == reflect.Slice || rv.Kind() == reflect.Array) {
			for i := 0; i < rv.Len(); i++ {
				if reflect.DeepEqual(rv.Index(i).Interface(), needle) {
					return true
				}
			}
			return false
		}
	}
	// Variadic form: has needle a b c ...
	for _, h := range haystack {
		if reflect.DeepEqual(h, needle) {
			return true
		}
	}
	return false
}

// hasKeyFunc reports whether the given map (typically a map[string]any
// produced by dict, a frontmatter expression, or a set call) contains key.
//
// Works against maps with any key type. The key argument is converted to
// the map's key type via reflect.Value if assignable; otherwise the lookup
// returns false. Non-map values return false rather than panicking,
// matching the safe-by-default ergonomic of html/template's other helpers.
func hasKeyFunc(key any, m any) bool {
	rv := reflect.ValueOf(m)
	if !rv.IsValid() || rv.Kind() != reflect.Map {
		return false
	}
	kv := reflect.ValueOf(key)
	keyType := rv.Type().Key()
	// Direct match: key already has the map's key type.
	if kv.IsValid() && kv.Type().AssignableTo(keyType) {
		return rv.MapIndex(kv).IsValid()
	}
	// Boxed match: map key type is interface{} (e.g. set's map[any]bool).
	if keyType.Kind() == reflect.Interface {
		boxed := reflect.New(keyType).Elem()
		if kv.IsValid() {
			boxed.Set(kv)
		}
		return rv.MapIndex(boxed).IsValid()
	}
	return false
}

// setFunc builds a set-like map[any]bool from its arguments. Combined with
// hasKey it lets templates do efficient membership tests without round-
// tripping through frontmatter:
//
//	{{ $active := set "open" "in_progress" }}
//	{{ if hasKey .Status $active }}...{{ end }}
//
// All arguments must be hashable; non-hashable arguments are skipped.
func setFunc(items ...any) map[any]bool {
	m := make(map[any]bool, len(items))
	for _, it := range items {
		// Map keys must be hashable; skip unhashable types (slices, maps,
		// functions) rather than panicking. Strings, numbers, and bools
		// are the overwhelmingly common case.
		insertSetItem(m, it)
	}
	return m
}

func insertSetItem(m map[any]bool, item any) {
	defer func() { _ = recover() }()
	m[item] = true
}
