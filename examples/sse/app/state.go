// Package app holds the SSE example's application-level state.
//
// Track B (plans/frictions-plan.md §4.10) lets a page's frontmatter
// retrieve typed dependencies via gastro.From[T](r.Context()). The
// type T must live in a package both the entry point (main.go) and
// the generated handler (.gastro) can import. main is unreachable
// from generated code, so this small package sits in between.
package app

import "sync/atomic"

// State holds the demo's mutable counter. The counter is shared
// across goroutines because page handlers may receive concurrent
// requests; sync/atomic gives lock-free integer increment.
type State struct {
	Count *atomic.Int64
}

// New constructs a zero-valued State suitable for registration via
// gastro.WithDeps in main.go.
func New() *State {
	return &State{Count: &atomic.Int64{}}
}
