//go:build !windows

package terminal

import (
	"os"
	"testing"
	"time"
)

// TestProgramInputParksWhileSuspended verifies the Unix input-parking
// protocol: while pauseInput is active the input loop stops reading (a
// foreground child would get every keystroke), and resumeInput lets it read
// again.
func TestProgramInputParksWhileSuspended(t *testing.T) {
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer pr.Close()
	defer pw.Close()

	p := &Program{
		tty:      &TTY{in: pr, out: pr},
		parser:   &InputParser{},
		msgs:     make(chan Msg, 16),
		parkedCh: make(chan struct{}, 1),
		resumeCh: make(chan struct{}, 1),
	}
	ctxDone := make(chan struct{})
	defer close(ctxDone)
	go p.readInput(ctxDone)

	// A key while running is delivered.
	if _, err := pw.Write([]byte{'j'}); err != nil {
		t.Fatal(err)
	}
	select {
	case msg := <-p.msgs:
		if _, ok := msg.(KeyPressMsg); !ok {
			t.Fatalf("msg = %T, want KeyPressMsg", msg)
		}
	case <-time.After(time.Second):
		t.Fatal("key not delivered while running")
	}

	// While parked, bytes are not read from the TTY.
	p.pauseInput()
	if _, err := pw.Write([]byte{'k'}); err != nil {
		t.Fatal(err)
	}
	select {
	case msg := <-p.msgs:
		t.Fatalf("msg delivered while parked: %#v", msg)
	case <-time.After(200 * time.Millisecond):
	}

	// After resume the buffered byte is delivered.
	p.resumeInput()
	select {
	case msg := <-p.msgs:
		if _, ok := msg.(KeyPressMsg); !ok {
			t.Fatalf("msg = %T, want KeyPressMsg", msg)
		}
	case <-time.After(time.Second):
		t.Fatal("key not delivered after resume")
	}
}
