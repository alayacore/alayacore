package app

// The latch is the one-shot broadcast an adapter's frame parser uses to tell
// Start's goroutines that the session is over. Its two properties: waiting
// before the mark blocks until it, and marking twice is harmless (the session
// sends one terminal frame, but a latch that panicked on a repeat would make
// that a correctness requirement of the wire).

import (
	"testing"
	"time"
)

func TestLatch(t *testing.T) {
	l := NewLatch()

	select {
	case <-l.Done():
		t.Fatal("an unmarked latch should not be done")
	default:
	}

	l.Mark()
	select {
	case <-l.Done():
	default:
		t.Fatal("a marked latch should be done")
	}

	l.Mark() // idempotent: a second mark must not close a closed channel
	select {
	case <-l.Done():
	default:
		t.Fatal("a re-marked latch should still be done")
	}
}

func TestLatchWakesWaiters(t *testing.T) {
	l := NewLatch()
	woken := make(chan struct{})

	go func() {
		<-l.Done()
		close(woken)
	}()

	select {
	case <-woken:
		t.Fatal("the waiter returned before the latch was marked")
	case <-time.After(50 * time.Millisecond):
	}

	l.Mark()
	select {
	case <-woken:
	case <-time.After(2 * time.Second):
		t.Fatal("marking the latch did not wake the waiter")
	}
}
