package gastro

import (
	"encoding/json"
	"html/template"
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
