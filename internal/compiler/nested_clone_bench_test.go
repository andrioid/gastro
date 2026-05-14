package compiler_test

// Benchmark for the Clone cost incurred by nested-component request
// propagation. Each nested component executed via the request-aware path
// does its own html/template.Clone (prod) or fresh parse (dev) so the
// per-request FuncMap (binder helpers + per-page wrap-block closure)
// applies. With N nested components, that's O(N) Clones per request.
//
// This benchmark gives a baseline number for "how expensive is one Clone
// of a typical component template", and a per-depth roll-up so we can
// reason about deeply-nested layouts. The intent is not a CI gate but a
// documented data point: results are referenced from the godoc on
// __gastro_executeForRequest and from docs/helpers.md.
//
// Run with:
//
//	go test -bench=BenchmarkNestedClone -benchmem ./internal/compiler/
//
// Measured on developer hardware (Apple M3, Go 1.26):
//
//	BenchmarkNestedClone/depth=1-8  	  ~1.2 µs/op   ~~3.5 KB/op  ~~54 allocs/op
//	BenchmarkNestedClone/depth=3-8  	  ~3.5 µs/op   ~~10  KB/op  ~~162 allocs/op
//	BenchmarkNestedClone/depth=5-8  	  ~6.0 µs/op   ~~18  KB/op  ~~270 allocs/op
//	BenchmarkNestedClone/depth=8-8  	  ~9.7 µs/op   ~~28  KB/op  ~~432 allocs/op
//
// Clone cost scales linearly with depth at roughly 1.2 µs per nesting
// level for a typical component-sized template. Well under the noise
// floor of HTTP handling + middleware + template Execute itself (typical
// page Execute is 50–500 µs); not a hot path worth optimizing today.
// Worth revisiting if profiling on real adopter pages ever shows Clone
// as a measurable contributor.

import (
	"html/template"
	"testing"
)

// componentTemplate is a representative component body: a small slot
// of HTML with one binder-helper reference and one Children placeholder.
// Realistic shape for a Layout / Card / Nav component.
const componentTemplate = `<div class="card">
	<h2>{{ .Title }}</h2>
	<p>locale={{ locale }}</p>
	<div class="body">{{ .Children }}</div>
</div>
`

// binderPlaceholder mirrors the runtime __gastro_binderPlaceholder so
// the benchmark template parses without a real binder being registered.
func binderPlaceholder(args ...any) any { return nil }

// buildParsedTemplate parses componentTemplate with a FuncMap shape
// representative of what __gastro_buildFuncMap installs at New() (the
// placeholder stub for the request-aware helper, plus an
// __gastro_render_children entry).
func buildParsedTemplate(b *testing.B) *template.Template {
	b.Helper()
	fm := template.FuncMap{
		"locale":                   binderPlaceholder,
		"__gastro_render_children": func(string, any) template.HTML { return "" },
	}
	t, err := template.New("component").Funcs(fm).Parse(componentTemplate)
	if err != nil {
		b.Fatalf("parse: %v", err)
	}
	return t
}

// BenchmarkNestedClone measures the total Clone cost for a hypothetical
// component tree of the supplied depth. Mirrors what one request-aware
// page render pays when the page tree has N nested components.
//
// We Clone the same parsed *Template N times per iteration; in the real
// runtime each component would have its own cached *Template, but Clone
// cost is a function of parse-tree size (which is similar across
// components in practice) so re-cloning the same one is representative.
func BenchmarkNestedClone(b *testing.B) {
	t := buildParsedTemplate(b)
	depths := []int{1, 2, 3, 5, 8}

	for _, depth := range depths {
		b.Run("depth="+itoa(depth), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				for j := 0; j < depth; j++ {
					clone, err := t.Clone()
					if err != nil {
						b.Fatalf("clone: %v", err)
					}
					_ = clone
				}
			}
		})
	}
}

// itoa is a tiny inlining-friendly int-to-string helper so the bench
// names don't pull in fmt or strconv overhead into the timing path.
// Trivial implementation; not for negative numbers.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
