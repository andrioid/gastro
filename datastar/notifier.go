package datastar

import "sync"

// Signal is a change signal: every Subscribe returns a channel that
// receives a value after state changes, and a cancel function that stops
// the subscription. [Notifier] implements it; so can an application's own
// state type.
//
// Implementations must never block the sender on a slow subscriber, and
// cancel must be idempotent. Receivers stop on their own context, so a
// channel need not (and should not) be closed by cancel.
type Signal interface {
	Subscribe() (<-chan struct{}, func())
}

// Notifier is a protocol-neutral, coalescing change signal for live pages
// and other subscribers that re-read state on every change.
//
// Notifications carry no payload and coalesce: each subscriber owns a
// 1-buffered channel, so a burst of changes leaves at most one pending
// signal and the next read sees the latest state anyway. Notify never
// blocks on a slow subscriber.
//
// Cancel only removes the subscriber; channels are never closed, so a
// Notify racing a cancel cannot send on a closed channel. Subscribers stop
// on their own context (select on ctx.Done() next to the channel).
//
// The zero value is ready to use. A Notifier must not be copied after
// first use.
type Notifier struct {
	mu   sync.Mutex
	subs map[chan struct{}]struct{}
}

// Subscribe registers a subscriber. The returned cancel is idempotent and
// safe to call concurrently with Notify.
func (n *Notifier) Subscribe() (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	n.mu.Lock()
	if n.subs == nil {
		n.subs = make(map[chan struct{}]struct{})
	}
	n.subs[ch] = struct{}{}
	n.mu.Unlock()
	return ch, func() {
		n.mu.Lock()
		delete(n.subs, ch)
		n.mu.Unlock()
	}
}

// Notify wakes every subscriber. A subscriber that already has a pending
// signal keeps exactly one.
func (n *Notifier) Notify() {
	n.mu.Lock()
	defer n.mu.Unlock()
	for ch := range n.subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// Subscribers reports the number of live subscribers.
func (n *Notifier) Subscribers() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.subs)
}
