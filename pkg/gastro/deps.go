package gastro

import (
	"context"
	"fmt"
	"reflect"
)

// depsKey is the unexported context key used to attach the deps registry to
// an *http.Request's context. A struct key avoids collisions with any
// string-based keys placed in the same context by user code.
type depsKey struct{}

// depsMap is the per-request registry of typed dependency values. It is keyed
// by reflect.Type, so each Go type can have at most one instance in scope per
// router. Use FromContext / From to retrieve values.
type depsMap map[reflect.Type]any

// AttachDeps returns a derived context carrying the supplied deps map.
// It is intended to be called by the gastro-generated router as it dispatches
// a request; user code typically does not need to call it directly.
//
// If the parent context already carries a deps map, the returned context
// carries a merged copy (child entries win on key collision). A nil or empty
// deps argument returns the parent context unchanged.
//
// The supplied map is treated as immutable: when the parent context carries
// no existing deps, the same map reference is reused without copying. Callers
// that intend to mutate the map after attaching must clone it themselves.
func AttachDeps(parent context.Context, deps map[reflect.Type]any) context.Context {
	if len(deps) == 0 {
		return parent
	}
	if existing, ok := parent.Value(depsKey{}).(depsMap); ok {
		merged := make(depsMap, len(existing)+len(deps))
		for k, v := range existing {
			merged[k] = v
		}
		for k, v := range deps {
			merged[k] = v
		}
		return context.WithValue(parent, depsKey{}, merged)
	}
	return context.WithValue(parent, depsKey{}, depsMap(deps))
}

// FromContext returns the dependency value of type T attached to ctx.
// It panics with a descriptive message if no value of that type is registered.
//
// Use FromContextOK if a missing value should be a recoverable condition.
//
// FromContext is the lower-level form of From: prefer From when you have a
// *gastro.Context (page handlers); use FromContext from SSE handlers,
// middleware, or any code that only has a context.Context.
func FromContext[T any](ctx context.Context) T {
	v, ok := FromContextOK[T](ctx)
	if !ok {
		var zero T
		panic(fmt.Sprintf(
			"gastro: no dependency of type %s registered "+
				"(did you forget gastro.WithDeps in New()?)",
			reflect.TypeOf(zero),
		))
	}
	return v
}

// FromContextOK is the safe variant of FromContext. It returns the dependency
// of type T and true if registered, or the zero value and false otherwise.
func FromContextOK[T any](ctx context.Context) (T, bool) {
	var zero T
	m, ok := ctx.Value(depsKey{}).(depsMap)
	if !ok {
		return zero, false
	}
	raw, ok := m[reflect.TypeOf(zero)]
	if !ok {
		return zero, false
	}
	v, ok := raw.(T)
	if !ok {
		// Should be impossible when registration uses the type key, but
		// keep the assertion for defence in depth.
		return zero, false
	}
	return v, true
}
