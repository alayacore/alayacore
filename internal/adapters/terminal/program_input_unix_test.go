//go:build !windows

package terminal

// The Unix input source's share of the parking protocol (program_input.go). The
// protocol itself is tested against a source the test controls; what is asserted
// here is that the real source keeps the promise the protocol depends on: it
// returns from a round with nothing to read within its poll timeout, so the loop
// is between reads often enough to be parked — and once it is, the terminal is
// left alone.
//
// A pipe stands in for the terminal: it is not a tty, but this source asks a
// descriptor only whether it is readable (unix.Poll) and then reads it, and a pipe
// answers both.

import (
	"os"
	"testing"
	"time"
)

func TestUnixInputParksWhileSuspended(t *testing.T) {
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer pr.Close()
	defer pw.Close()

	input, err := newInput(&TTY{in: pr, out: pr})
	if err != nil {
		t.Fatal(err)
	}
	msgs := make(chan Msg, 16)
	p := newParkedProgram(msgs, input)
	ctxDone := make(chan struct{})
	defer close(ctxDone)
	go p.readInput(ctxDone)

	// A keystroke while running reaches the loop.
	if _, err := pw.Write([]byte{'j'}); err != nil {
		t.Fatal(err)
	}
	select {
	case msg := <-msgs:
		if _, ok := msg.(KeyPressMsg); !ok {
			t.Fatalf("msg = %T, want KeyPressMsg", msg)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("key not delivered while running")
	}

	// Parking is noticed within a poll round, not at the end of the
	// readLoopTimeout: if this slips, so does the start of every editor
	// session, and the child waits for the program's read to time out before
	// it can be given the terminal.
	start := time.Now()
	p.pauseInput()
	if elapsed := time.Since(start); elapsed > 4*inputPollTimeout*time.Millisecond {
		t.Errorf("pauseInput waited %v; the loop is meant to be between reads within %v",
			elapsed, time.Duration(inputPollTimeout)*time.Millisecond)
	}

	// While parked the terminal is not read: what is typed stays where a
	// foreground child can reach it.
	if _, err := pw.Write([]byte{'k'}); err != nil {
		t.Fatal(err)
	}
	select {
	case msg := <-msgs:
		t.Fatalf("message delivered while parked: %#v", msg)
	case <-time.After(300 * time.Millisecond):
	}

	// Resuming delivers it.
	p.resumeInput()
	select {
	case msg := <-msgs:
		if _, ok := msg.(KeyPressMsg); !ok {
			t.Fatalf("msg = %T, want KeyPressMsg", msg)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("key not delivered after resume")
	}
}
