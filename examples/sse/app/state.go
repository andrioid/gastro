// Package app holds the SSE example's application-level state.
//
// Pages and WithHandler handlers retrieve it as a typed dependency
// (gastro.From[T] in frontmatter, gastroRuntime.FromContext[T] in Go). The
// type must live in a package both the entry point (main.go) and the
// generated handler (.gastro) can import; main is unreachable from generated
// code, so this small package sits in between.
package app

import (
	"sync"

	"github.com/andrioid/gastro/datastar"
)

// State holds the demo's counter. Commands write through Increment; the
// live pages subscribe to Changes and re-render after every change.
type State struct {
	// Changes signals every change to the state.
	Changes datastar.Notifier

	mu    sync.Mutex
	count int
}

// New constructs a zero-valued State suitable for registration via
// gastro.WithDeps in main.go.
func New() *State {
	return &State{}
}

// Count returns the current counter value.
func (s *State) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.count
}

// Increment adds step to the counter and signals the change.
func (s *State) Increment(step int) {
	s.mu.Lock()
	s.count += step
	s.mu.Unlock()
	s.Changes.Notify()
}
