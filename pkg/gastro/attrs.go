package gastro

import (
	"fmt"
	"html"
	"html/template"
	"sort"
	"strings"
)

// Attrs is the sentinel type for a component's attribute-forwarding bag.
// A Props struct field of type gastro.Attrs collects every dict key the
// caller passes that does not match a declared Props field (implicit
// "rest" capture), letting a component forward arbitrary HTML attributes —
// type, href, data-*, aria-*, data-on:* — without a named field for each:
//
//	type Props struct {
//	    Label string
//	    Attrs gastro.Attrs
//	}
//
// Render the bag in a component template with the attrs func:
//
//	<button {{ attrs .Attrs (dict "class" "btn" "type" "button") }}>{{ .Label }}</button>
//
// Values are HTML-attribute-escaped by default. To bypass escaping (e.g. a
// Datastar expression), pass a value already typed template.JS / HTMLAttr /
// URL / CSS — it is emitted verbatim, the same contract as the safe* funcs.
type Attrs map[string]any

// ClassMerger combines class token lists into a single class attribute
// value. The built-in DefaultClassMerger only concatenates; plug a
// Tailwind-aware merger (e.g. tailwind-merge-go) via WithClassMerger to get
// real conflict resolution. The dependency lives in your module — gastro
// core never imports it.
type ClassMerger func(classes ...string) string

// DefaultClassMerger is the built-in twJoin: it trims, drops empties, and
// space-joins the given class lists in order. It performs NO Tailwind
// conflict resolution — merging "px-4" and "px-2" yields "px-4 px-2", not
// "px-2". Override via WithClassMerger for conflict-aware merging.
func DefaultClassMerger(classes ...string) string {
	var b strings.Builder
	for _, group := range classes {
		for _, tok := range strings.Fields(group) {
			if b.Len() > 0 {
				b.WriteByte(' ')
			}
			b.WriteString(tok)
		}
	}
	return b.String()
}

// BuildAttrsFunc returns the template "attrs" func bound to merge. It
// renders an Attrs bag (plus optional base/default key-values) into a
// template.HTMLAttr fragment with a leading space:
//
//	{{ attrs .Attrs (dict "class" "btn px-4" "type" "button") }}
//
// The optional base dict supplies defaults: every base key is used unless
// the bag overrides it, EXCEPT "class", whose base and bag values are
// combined through merge (base first, so a conflict-aware merger lets the
// caller win). Keys are emitted in sorted order for stable output. A bool
// value renders as a bare attribute when true and is omitted when false;
// safe typed values (template.JS/HTMLAttr/URL/CSS/HTML) bypass escaping;
// every other value is fmt.Sprint'd and HTML-attribute-escaped. Names that
// are not [A-Za-z0-9_:.-] are skipped — we build the attribute string
// ourselves (html/template has no attribute-name context), so this guards
// against a crafted key breaking out of the tag.
func BuildAttrsFunc(merge ClassMerger) func(bag any, base ...map[string]any) template.HTMLAttr {
	if merge == nil {
		merge = DefaultClassMerger
	}
	return func(bag any, base ...map[string]any) template.HTMLAttr {
		merged := make(map[string]any, len(base)+4)
		var classParts []string
		addClass := func(v any) {
			if v == nil {
				return
			}
			if s := fmt.Sprint(v); s != "" {
				classParts = append(classParts, s)
			}
		}

		for _, b := range base {
			for k, v := range b {
				if k == "class" {
					addClass(v)
				} else {
					merged[k] = v
				}
			}
		}
		for k, v := range toAttrs(bag) {
			if k == "class" {
				addClass(v)
			} else {
				merged[k] = v
			}
		}
		if len(classParts) > 0 {
			merged["class"] = merge(classParts...)
		}

		keys := make([]string, 0, len(merged))
		for k := range merged {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		var b strings.Builder
		for _, k := range keys {
			if !validAttrName(k) {
				continue
			}
			v := merged[k]
			if v == nil {
				continue
			}
			if bv, ok := v.(bool); ok {
				if bv {
					b.WriteByte(' ')
					b.WriteString(k)
				}
				continue
			}
			b.WriteByte(' ')
			b.WriteString(k)
			b.WriteString(`="`)
			if s, safe := attrSafeString(v); safe {
				b.WriteString(s)
			} else {
				b.WriteString(html.EscapeString(fmt.Sprint(v)))
			}
			b.WriteByte('"')
		}
		return template.HTMLAttr(b.String())
	}
}

// toAttrs normalises the first attrs argument. It accepts the typed bag
// (gastro.Attrs, the common case from a component's .Attrs field) and a
// bare map[string]any (from an inline dict call), returning nil otherwise.
func toAttrs(v any) Attrs {
	switch m := v.(type) {
	case Attrs:
		return m
	case map[string]any:
		return Attrs(m)
	default:
		return nil
	}
}

// attrSafeString reports whether v is one of html/template's typed safe
// strings that should be emitted into an attribute value verbatim, and
// returns that string. Mirrors the safe* funcs: when the author has marked
// a value safe (e.g. safeJS for a Datastar expression) attrs must not
// re-escape it.
func attrSafeString(v any) (string, bool) {
	switch s := v.(type) {
	case template.HTMLAttr:
		return string(s), true
	case template.JS:
		return string(s), true
	case template.URL:
		return string(s), true
	case template.CSS:
		return string(s), true
	case template.HTML:
		return string(s), true
	default:
		return "", false
	}
}

// validAttrName reports whether name is a safe HTML attribute name. Only
// [A-Za-z0-9_:.-] is allowed, which still covers data-*, aria-*, and
// Datastar's data-on:evt__mod.dur spellings while rejecting whitespace,
// quotes, '=' and '>' that could break out of the tag.
func validAttrName(name string) bool {
	if name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '_' || c == ':' || c == '.' || c == '-':
		default:
			return false
		}
	}
	return true
}
