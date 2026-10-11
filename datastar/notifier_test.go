package datastar_test

import (
	"sync"
	"testing"
	"time"

	"github.com/andrioid/gastro/datastar"
)

func TestNotifier_WakesEverySubscriber(t *testing.T) {
	var n datastar.Notifier
	a, cancelA := n.Subscribe()
	defer cancelA()
	b, cancelB := n.Subscribe()
	defer cancelB()

	n.Notify()
	for name, ch := range map[string]<-chan struct{}{"a": a, "b": b} {
		select {
		case <-ch:
		case <-time.After(time.Second):
			t.Fatalf("subscriber %s was not woken", name)
		}
	}
}

func TestNotifier_CoalescesBursts(t *testing.T) {
	var n datastar.Notifier
	ch, cancel := n.Subscribe()
	defer cancel()

	for range 10 {
		n.Notify() // never blocks, even though nobody is reading
	}
	<-ch
	select {
	case <-ch:
		t.Fatal("a burst of notifications left more than one pending signal")
	default:
	}
}

func TestNotifier_CancelRemovesWithoutClosing(t *testing.T) {
	var n datastar.Notifier
	ch, cancel := n.Subscribe()
	if got := n.Subscribers(); got != 1 {
		t.Fatalf("Subscribers = %d, want 1", got)
	}
	cancel()
	cancel() // idempotent
	if got := n.Subscribers(); got != 0 {
		t.Fatalf("Subscribers after cancel = %d, want 0", got)
	}
	n.Notify()
	select {
	case _, ok := <-ch:
		if !ok {
			t.Fatal("cancel closed the channel")
		}
		t.Fatal("a cancelled subscriber was notified")
	default:
	}
}

// TestNotifier_ConcurrentSubscribeCancel hammers Subscribe, cancel and Notify
// from many goroutines; run under -race. Cancel never closes the channel, so
// a Notify racing a cancel cannot panic on a closed channel.
func TestNotifier_ConcurrentSubscribeCancel(t *testing.T) {
	var n datastar.Notifier
	const subscribers = 1000

	stop := make(chan struct{})
	var notifiers sync.WaitGroup
	for range 4 {
		notifiers.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
					n.Notify()
				}
			}
		})
	}

	var subs sync.WaitGroup
	for range subscribers {
		subs.Go(func() {
			ch, cancel := n.Subscribe()
			select {
			case <-ch:
			case <-time.After(time.Millisecond):
			}
			cancel()
		})
	}
	subs.Wait()
	close(stop)
	notifiers.Wait()

	if got := n.Subscribers(); got != 0 {
		t.Errorf("Subscribers after every cancel = %d, want 0", got)
	}
}
