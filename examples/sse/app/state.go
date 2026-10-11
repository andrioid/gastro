// Package app holds the SSE example's application-level state.
//
// Pages and WithHandler handlers retrieve it as a typed dependency
// (gastro.From[T] in frontmatter, gastroRuntime.FromContext[T] in Go). The
// type must live in a package both the entry point (main.go) and the
// generated handler (.gastro) can import; main is unreachable from generated
// code, so this small package sits in between.
package app

import "sync"

// State holds the demo's counter and the subscribers waiting for it to
// change. Commands write through Increment; page streams Subscribe and
// re-render from Count whenever they are notified.
type State struct {
	mu    sync.Mutex
	count int
	subs  map[chan struct{}]struct{}
}

// New constructs a zero-valued State suitable for registration via
// gastro.WithDeps in main.go.
func New() *State {
	return &State{subs: map[chan struct{}]struct{}{}}
}

// Count returns the current counter value.
func (s *State) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.count
}

// Increment adds step to the counter and notifies every subscriber.
func (s *State) Increment(step int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.count += step
	for ch := range s.subs {
		// Coalesce: a subscriber with a pending notification re-renders
		// from current state anyway.
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// Subscribe returns a channel that receives a value after every change, and
// a function that unsubscribes it.
func (s *State) Subscribe() (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	s.mu.Lock()
	s.subs[ch] = struct{}{}
	s.mu.Unlock()
	return ch, func() {
		s.mu.Lock()
		delete(s.subs, ch)
		s.mu.Unlock()
	}
}
