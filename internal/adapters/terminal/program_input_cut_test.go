package terminal

// Tests for how the input loop resolves an escape sequence that a read boundary
// cuts in half. This is the reported bug's home: the loop is the only reader, so
// a sequence's tail has to be fetched by the next read, and a flush taken at the
// end of the read that was cut drops the head and delivers the tail as typing.
//
// A read boundary is a real cut, not a theoretical one: program_input_unix.go
// stops at inputReadSize bytes and program_input_windows.go at
// consoleEventsPerRead events. The reported case was a mouse report on Windows
// Terminal, which the host synthesizes as characters; what was typed into the
// prompt was the report's `Cb;Cx;CyM` parameters.

import (
	"testing"
	"time"
)

// inputWindow bounds how long a test waits for the loop to deliver, or to not
// deliver. It is several times the sequence timeout, so a tail that was going to
// be typed has been typed by the end of it — the failure this file exists for
// arrives one flush interval after the head.
const inputWindow = 200 * time.Millisecond

// describeMsg renders a message the way a test can compare it, without caring
// which concrete type it is.
func describeMsg(m Msg) string {
	switch v := m.(type) {
	case KeyPressMsg:
		return v.String()
	case PasteMsg:
		return "paste:" + v.Content
	case FocusMsg:
		return "focus"
	case BlurMsg:
		return "blur"
	default:
		return ""
	}
}

// collectInput runs the loop against a fake source fed the given reads, and
// returns every message delivered within inputWindow.
func collectInput(t *testing.T, reads ...[]byte) []string {
	t.Helper()
	msgs := make(chan Msg, 16)
	input := newFakeInput()
	p := newParkedProgram(msgs, input)
	ctxDone := make(chan struct{})
	defer close(ctxDone)
	go p.readInput(ctxDone)

	for _, r := range reads {
		input.tokens <- r
	}
	// Ending the source releases the loop once the window has passed: it only
	// returns on an error or a park request, and closing this one is the error.
	// It is closed here rather than with the reads so the loop cannot see EOF
	// before the window the assertions below are measured over.
	defer close(input.tokens)

	var got []string
	deadline := time.After(inputWindow)
	for {
		select {
		case msg := <-msgs:
			got = append(got, describeMsg(msg))
		case <-deadline:
			return got
		}
	}
}

// TestCutSequenceCompletesOnTheNextRead is the regression. Each case is one
// sequence whose bytes are split across two reads at a different offset; the
// second read must complete the first, never arrive as typing.
//
// The mouse rows are the reported shape, at three cut points; the others show
// that the cut is not a mouse bug — any sequence a boundary lands inside is
// affected the same way.
func TestCutSequenceCompletesOnTheNextRead(t *testing.T) {
	tests := []struct {
		name string
		read [][]byte
		want []string
	}{
		{
			name: "sgr mouse cut after the private marker",
			read: [][]byte{[]byte("\x1b[<"), []byte("35;60;10M")},
			want: nil,
		},
		{
			name: "sgr mouse cut inside the parameters",
			read: [][]byte{[]byte("\x1b[<12;1"), []byte("35;1M")},
			want: nil,
		},
		{
			name: "sgr mouse cut before the final byte",
			read: [][]byte{[]byte("\x1b[<12;135;"), []byte("1M\x1b[<12;176;1M")},
			want: nil,
		},
		{
			// An arrow cut after `ESC [` must still be the arrow, not a stray
			// capital A.
			name: "arrow key",
			read: [][]byte{[]byte("\x1b["), []byte("A")},
			want: []string{"up"},
		},
		{
			name: "focus report",
			read: [][]byte{[]byte("\x1b["), []byte("I")},
			want: []string{"focus"},
		},
		{
			name: "blur report",
			read: [][]byte{[]byte("\x1b["), []byte("O")},
			want: []string{"blur"},
		},
		{
			name: "bracketed paste markers",
			read: [][]byte{[]byte("\x1b[200~hel"), []byte("lo\x1b[201~")},
			want: []string{"paste:hello"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := collectInput(t, tt.read...)
			if len(got) != len(tt.want) {
				t.Fatalf("reads %q delivered %v, want %v: a cut sequence must be completed, not typed",
					tt.read, got, tt.want)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Errorf("message %d = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// TestLoneEscapeStillResolves: reading while a sequence is outstanding must not
// mean never resolving one. A real Escape keypress is still `esc` — and only
// after the timeout, since that window is where the sequence an ESC may begin
// can still arrive (Alt+a, an arrow, a mouse report).
func TestLoneEscapeStillResolves(t *testing.T) {
	msgs := make(chan Msg, 4)
	input := newFakeInput()
	p := newParkedProgram(msgs, input)
	ctxDone := make(chan struct{})
	defer close(ctxDone)
	go p.readInput(ctxDone)

	// Released at the end of the test, so the loop returns instead of polling
	// an empty source for the rest of the run.
	defer close(input.tokens)

	start := time.Now()
	input.tokens <- []byte("\x1b")

	select {
	case msg := <-msgs:
		if got := describeMsg(msg); got != "esc" {
			t.Fatalf("delivered %q, want esc", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the lone ESC never resolved to esc")
	}
	// Measured rather than raced: asserting "no message within 25 ms" with a
	// select would fail on a loaded machine, where both the timer and the
	// message can be ready and the select picks either. Elapsed time cannot
	// flake that way — a stall only makes the number larger — and it still
	// catches any path that resolves the ESC without waiting.
	if elapsed := time.Since(start); elapsed < escSequenceTimeout {
		t.Errorf("esc resolved after %v, before the %v timeout", elapsed, escSequenceTimeout)
	}
}

// TestIncompleteSequenceIsDropped: an introducer with nothing behind it, and
// then silence, is dropped rather than typed — and is resolved rather than held
// forever.
func TestIncompleteSequenceIsDropped(t *testing.T) {
	if got := collectInput(t, []byte("\x1b[")); len(got) != 0 {
		t.Errorf("loop delivered %v for an introducer with nothing behind it", got)
	}
}
