package gastro_test

import (
	"html/template"
	"strings"
	"testing"

	"github.com/andrioid/gastro/pkg/gastro"
)

// attrsFn returns the wired "attrs" template func, asserting it was
// registered in DefaultFuncs with the expected signature.
func attrsFn(t *testing.T) func(any, ...map[string]any) template.HTMLAttr {
	t.Helper()
	fn, ok := gastro.DefaultFuncs()["attrs"].(func(any, ...map[string]any) template.HTMLAttr)
	if !ok {
		t.Fatalf("attrs func not registered with expected signature")
	}
	return fn
}

func TestAttrs_SortedAndEscaped(t *testing.T) {
	attrs := attrsFn(t)
	got := string(attrs(gastro.Attrs{
		"type":        "text",
		"data-label":  `a & "b"`,
		"aria-hidden": "false",
	}))
	want := ` aria-hidden="false" data-label="a &amp; &#34;b&#34;" type="text"`
	if got != want {
		t.Errorf("attrs:\n got %q\nwant %q", got, want)
	}
}

func TestAttrs_BoolBareAndOmitted(t *testing.T) {
	attrs := attrsFn(t)
	got := string(attrs(gastro.Attrs{
		"disabled": true,
		"checked":  false,
		"name":     "x",
	}))
	// disabled -> bare; checked -> omitted; name -> value. Sorted.
	want := ` disabled name="x"`
	if got != want {
		t.Errorf("attrs bool:\n got %q\nwant %q", got, want)
	}
}

func TestAttrs_SafeTypedPassthrough(t *testing.T) {
	attrs := attrsFn(t)
	got := string(attrs(gastro.Attrs{
		"data-on:click": template.JS("@post('/x')"),
	}))
	want := ` data-on:click="@post('/x')"`
	if got != want {
		t.Errorf("attrs safe passthrough:\n got %q\nwant %q", got, want)
	}
	// A plain string in the same slot must be escaped, proving the
	// passthrough is type-driven, not key-driven.
	got = string(attrs(gastro.Attrs{"data-on:click": "@post('/x')"}))
	if !strings.Contains(got, "&#39;") {
		t.Errorf("plain string should be escaped, got %q", got)
	}
}

func TestAttrs_InvalidNameSkipped(t *testing.T) {
	attrs := attrsFn(t)
	got := string(attrs(gastro.Attrs{
		"bad name":            "x", // space
		`evil"=y onmouseover`: "1", // quote/eq
		"ok-name":             "y",
	}))
	want := ` ok-name="y"`
	if got != want {
		t.Errorf("attrs invalid-name skip:\n got %q\nwant %q", got, want)
	}
}

func TestAttrs_BaseDefaultsOverriddenByBag(t *testing.T) {
	attrs := attrsFn(t)
	// type defaults to "button" but the bag forwards "submit".
	got := string(attrs(
		gastro.Attrs{"type": "submit"},
		map[string]any{"type": "button"},
	))
	want := ` type="submit"`
	if got != want {
		t.Errorf("attrs base override:\n got %q\nwant %q", got, want)
	}
}

func TestAttrs_ClassMergedWithDefaultMerger(t *testing.T) {
	attrs := attrsFn(t)
	// Default merger only concatenates base + bag (no conflict resolution).
	got := string(attrs(
		gastro.Attrs{"class": "px-2"},
		map[string]any{"class": "btn px-4"},
	))
	want := ` class="btn px-4 px-2"`
	if got != want {
		t.Errorf("attrs class concat:\n got %q\nwant %q", got, want)
	}
}

func TestAttrs_AcceptsInlineMap(t *testing.T) {
	attrs := attrsFn(t)
	// Called with a bare dict (map[string]any), not a typed Attrs value.
	got := string(attrs(map[string]any{"id": "main"}))
	if got != ` id="main"` {
		t.Errorf("attrs inline map: got %q", got)
	}
}

func TestBuildAttrsFunc_CustomMerger(t *testing.T) {
	// A conflict-resolving stub: last token wins per utility prefix.
	merger := func(classes ...string) string {
		seen := map[string]string{}
		var order []string
		for _, group := range classes {
			for _, tok := range strings.Fields(group) {
				prefix := tok
				if i := strings.LastIndex(tok, "-"); i >= 0 {
					prefix = tok[:i]
				}
				if _, ok := seen[prefix]; !ok {
					order = append(order, prefix)
				}
				seen[prefix] = tok
			}
		}
		out := make([]string, len(order))
		for i, p := range order {
			out[i] = seen[p]
		}
		return strings.Join(out, " ")
	}
	attrs := gastro.BuildAttrsFunc(merger)
	got := string(attrs(
		gastro.Attrs{"class": "px-2"},
		map[string]any{"class": "btn px-4"},
	))
	want := ` class="btn px-2"`
	if got != want {
		t.Errorf("custom merger:\n got %q\nwant %q", got, want)
	}
}

func TestDefaultClassMerger(t *testing.T) {
	got := gastro.DefaultClassMerger("btn  px-4", "", "px-2")
	want := "btn px-4 px-2"
	if got != want {
		t.Errorf("DefaultClassMerger: got %q, want %q", got, want)
	}
	if gastro.DefaultClassMerger() != "" {
		t.Errorf("DefaultClassMerger() empty: got %q", gastro.DefaultClassMerger())
	}
}
