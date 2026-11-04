package agent

// The task → run() event channel: what a send waits for, and what it refuses to
// wait for.

import (
	"testing"
	"time"
)

// The channel is how a task reports its progress to run(), so a send must wait
// while the loop is busy. It must not wait for a loop that has exited.
//
// run()'s last act cancels the session's context, and nothing drains the channel
// afterwards. A task goroutine parked on a send would then hold its tool
// goroutine — and whatever result it had already computed — for the life of the
// process, which is the one shape of leak a canceled session must not have: the
// work is over, and the cancellation is what says so.
func TestSendEventGivesUpWhenTheSessionIsOver(t *testing.T) {
	done := make(chan struct{})
	close(done) // run() has returned
	s := &Session{
		// Unbuffered and unread: with a live session this send would block
		// forever, which is exactly the state the test distinguishes.
		runState:  runState{taskEventCh: make(chan taskEvent)},
		runDoneCh: done,
	}

	sent := make(chan struct{})
	go func() {
		defer close(sent)
		s.sendEvent(stepStartEvent{Step: 1})
	}()

	select {
	case <-sent:
	case <-time.After(2 * time.Second):
		t.Fatal("sendEvent blocked after the session ended")
	}
}

// Giving up early would be its own bug: while the session lives, every event
// reaches run(). A step boundary or a step's parts dropped here would leave the
// adapter showing a task the session does not have.
func TestSendEventDeliversWhileTheSessionLives(t *testing.T) {
	ch := make(chan taskEvent, 1)
	s := &Session{
		runState:  runState{taskEventCh: ch},
		runDoneCh: make(chan struct{}), // run() is still there
	}

	s.sendEvent(stepStartEvent{Step: 2})

	select {
	case ev := <-ch:
		start, ok := ev.(stepStartEvent)
		if !ok || start.Step != 2 {
			t.Errorf("received %#v, want stepStartEvent{Step: 2}", ev)
		}
	default:
		t.Fatal("event was not delivered")
	}
}
