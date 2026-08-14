package mcp

import (
	"context"
	"testing"
	"time"
)

// TestInitDoneNotDroppedWhenChannelFull is a regression test for the
// lossy sendEvent path: when the events channel was full, the terminal
// InitDone event could be dropped, making the session treat a successful
// init as aborted (MCP tools never loaded, misleading "canceled" error).
// deliverEvent must block until the session receives the event instead.
func TestInitDoneNotDroppedWhenChannelFull(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	in := NewInitializer(nil) // no servers: InitDone is the only event run() sends

	// Fill the events channel past capacity so the lossy send path would
	// drop the terminal event.
	for i := 0; i < cap(in.events); i++ {
		in.events <- InitEvent{Type: InitConnecting, Server: "filler"}
	}

	in.Start(ctx)

	// Wait for run() to reach the send while the channel is still full:
	// with the lossy path it completes immediately (Done closes, InitDone
	// dropped); with deliverEvent it blocks on the full channel until we
	// drain below.
	select {
	case <-in.Done():
	case <-time.After(200 * time.Millisecond):
	}

	// Drain everything; the terminal InitDone must still arrive.
	gotDone := false
	for !gotDone {
		select {
		case evt, ok := <-in.events:
			if !ok {
				t.Fatal("events channel closed before InitDone was received")
			}
			if evt.Type == InitDone {
				gotDone = true
			}
		case <-time.After(2 * time.Second):
			t.Fatal("timed out draining events")
		}
	}
}

// TestDeliverEventUnblocksOnCancel verifies that a blocked deliverEvent
// does not hang run() forever: once the init context is canceled (which
// the session does on shutdown), the blocked send is aborted and run()
// exits cleanly.
func TestDeliverEventUnblocksOnCancel(t *testing.T) {
	in := NewInitializer(nil)

	// Fill the channel so deliverEvent blocks on the send.
	for i := 0; i < cap(in.events); i++ {
		in.events <- InitEvent{Type: InitConnecting, Server: "filler"}
	}

	ctx, cancel := context.WithCancel(context.Background())
	in.Start(ctx)

	// Give run() a chance to reach the blocked deliverEvent, then cancel.
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-in.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("run() did not exit after cancel — deliverEvent blocked forever")
	}
}
