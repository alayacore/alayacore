package terminal

// Tests for the input loop and its parking protocol (program_input.go).
//
// The protocol is why a foreground child (the editor) can be handed the keyboard
// at all, and why the program can exit without waiting for a keystroke: the
// release path does not touch the terminal until the loop reports that it is
// between reads, and the teardown does not hand the terminal back until the loop
// has stopped existing.
//
// Both are asserted against a source the test controls, because the property is
// about the loop rather than about any one terminal — and the loop is shared, so
// these run on Windows too, where the byte-reading loop they replace could not be
// parked at all. program_input_unix_test.go adds the one thing a fake cannot
// prove: that a real source keeps the promise the protocol is built on.

import (
	"io"
	"sync"
	"testing"
	"time"
)

// fakeInput is an inputSource a test drives. It answers like the real sources do
// — bytes when it has them, an empty slice when its wait ends — and keeps the one
// fact the protocol depends on observable: whether a read is in flight right now.
type fakeInput struct {
	tokens chan []byte   // what next() hands out
	wait   time.Duration // how long an empty round waits, as a poll timeout does

	// entered and release, when set, turn a round that finds input into a read
	// the test is inside of: next() announces itself on entered and then blocks
	// until release is closed. That is how a test asks the program to park while
	// a read is certainly in flight, rather than hoping to catch it there.
	entered chan struct{}
	release chan struct{}

	mu     sync.Mutex
	inRead bool
	reads  int
}

func newFakeInput() *fakeInput {
	return &fakeInput{
		tokens: make(chan []byte, 8),
		wait:   2 * time.Millisecond,
	}
}

func (f *fakeInput) next() ([]byte, error) {
	f.mu.Lock()
	f.inRead = true
	f.reads++
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		f.inRead = false
		f.mu.Unlock()
	}()

	data, ok, err := f.round()
	if err != nil || !ok {
		return nil, err
	}
	if f.entered != nil {
		f.entered <- struct{}{}
		<-f.release
	}
	return data, nil
}

// round is the wait itself: a queued token, or nothing once the timeout ends.
func (f *fakeInput) round() ([]byte, bool, error) {
	select {
	case data, ok := <-f.tokens:
		if !ok {
			return nil, false, io.EOF
		}
		return data, true, nil
	case <-time.After(f.wait):
		return nil, false, nil
	}
}

// inARead reports whether a read is in flight.
func (f *fakeInput) inARead() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.inRead
}

// readCount reports how many reads the loop has started.
func (f *fakeInput) readCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reads
}

// newParkedProgram builds a Program wired for the parking tests: a message
// channel, the loop's signaling channels, and the source to read.
func newParkedProgram(msgs chan Msg, input inputSource) *Program {
	return &Program{
		parser:       &InputParser{},
		msgs:         msgs,
		input:        input,
		parkedCh:     make(chan struct{}, 1),
		resumeCh:     make(chan struct{}, 1),
		inputStopped: make(chan struct{}),
	}
}

// TestPauseInputWaitsForAnInFlightRead is the guarantee the editor handoff
// depends on: pauseInput does not return while a read is still in flight, so the
// terminal it goes on to release cannot still be answering this program's request
// for input. A pause that set the flag and walked away — which is what the Windows
// implementation used to do — fails here at the first select.
func TestPauseInputWaitsForAnInFlightRead(t *testing.T) {
	input := newFakeInput()
	input.entered = make(chan struct{})
	input.release = make(chan struct{})
	msgs := make(chan Msg, 16)
	p := newParkedProgram(msgs, input)
	ctxDone := make(chan struct{})
	defer close(ctxDone)
	go p.readInput(ctxDone)

	input.tokens <- []byte{'j'}
	<-input.entered // the loop is inside a read, and staying there until told

	paused := make(chan struct{})
	go func() {
		p.pauseInput()
		close(paused)
	}()
	select {
	case <-paused:
		t.Fatal("pauseInput returned while a read was still in flight")
	case <-time.After(50 * time.Millisecond):
	}

	close(input.release)
	select {
	case <-paused:
	case <-time.After(2 * time.Second):
		t.Fatal("pauseInput did not return after the read finished")
	}
	if input.inARead() {
		t.Error("pauseInput returned with a read in flight")
	}
}

// TestNoReadHappensWhileParked is the other half: the parked loop stays out of
// the terminal, and what arrives in the meantime is read only after it is
// resumed. That is what "the editor gets every keystroke" means from inside this
// program.
func TestNoReadHappensWhileParked(t *testing.T) {
	msgs := make(chan Msg, 16)
	input := newFakeInput()
	p := newParkedProgram(msgs, input)
	ctxDone := make(chan struct{})
	defer close(ctxDone)
	go p.readInput(ctxDone)

	p.pauseInput()
	// Let the acknowledgement settle, so the count below is the count at the
	// moment of parking and not one still landing from the round before it.
	time.Sleep(10 * input.wait)
	reads := input.readCount()

	input.tokens <- []byte{'j'}
	select {
	case msg := <-msgs:
		t.Fatalf("message delivered while parked: %#v", msg)
	case <-time.After(200 * time.Millisecond):
	}
	if got := input.readCount(); got != reads {
		t.Errorf("the parked loop started %d more reads; a foreground child would have had its input stolen", got-reads)
	}

	p.resumeInput()
	select {
	case msg := <-msgs:
		if _, ok := msg.(KeyPressMsg); !ok {
			t.Fatalf("msg = %T, want KeyPressMsg", msg)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("input queued during the park was not delivered after resume")
	}
}

// TestResumeAfterParkDeliversEveryQueuedByte covers the shape of a real handoff:
// the child keeps whatever the user typed for it, and the keys typed for the
// program afterwards arrive in order once the terminal comes back.
func TestResumeAfterParkDeliversEveryQueuedByte(t *testing.T) {
	msgs := make(chan Msg, 16)
	input := newFakeInput()
	p := newParkedProgram(msgs, input)
	ctxDone := make(chan struct{})
	defer close(ctxDone)
	go p.readInput(ctxDone)

	p.pauseInput()
	input.tokens <- []byte{':', 'q'}
	input.tokens <- []byte{'\r'}
	p.resumeInput()

	var got []string
	for range 3 {
		select {
		case msg := <-msgs:
			key, ok := msg.(KeyPressMsg)
			if !ok {
				t.Fatalf("msg = %T, want KeyPressMsg", msg)
			}
			got = append(got, key.String())
		case <-time.After(2 * time.Second):
			t.Fatalf("only these keys arrived: %v", got)
		}
	}
	want := []string{":", "q", "enter"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("key %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestStopInputWaitsForTheLoopToLeave is the teardown guarantee: when stopInput
// returns, the loop has finished and is not inside a read, so the terminal can be
// restored, its files closed, and the process exit — without a console read still
// waiting for input. The delayed shell prompt this replaces was exactly that wait:
// os.File.Close does not return until the read in flight on the file does.
func TestStopInputWaitsForTheLoopToLeave(t *testing.T) {
	input := newFakeInput()
	p := newParkedProgram(make(chan Msg, 16), input)
	ctxDone := make(chan struct{})
	loopDone := make(chan struct{})
	go func() {
		p.readInput(ctxDone)
		close(loopDone)
	}()
	mustWaitFor(t, "the first read", func() bool { return input.readCount() > 0 })

	// run() returns first, and its defer closes ctxDone; the teardown waits
	// here, before touching the terminal.
	close(ctxDone)
	start := time.Now()
	p.stopInput()

	select {
	case <-p.inputStopped:
	default:
		t.Error("stopInput returned before the input loop reported itself finished")
	}
	select {
	case <-loopDone:
	default:
		t.Error("stopInput returned while the input loop was still running")
	}
	if input.inARead() {
		t.Error("a read is still in flight after stopInput: the teardown would wait for input")
	}
	// Bounded by the source's own wait, not by the timeout that stands in for a
	// wedged loop: waiting that long on every quit would be its own bug.
	if elapsed := time.Since(start); elapsed > readLoopTimeout/2 {
		t.Errorf("stopInput waited %v for a loop that had nowhere to be", elapsed)
	}
}

// TestStopInputWithoutInputSourceIsImmediate covers the program with no terminal
// (every test that drives the loop by hand): there is no reader to wait for, and
// waiting for one would cost the process a readLoopTimeout on every exit.
func TestStopInputWithoutInputSourceIsImmediate(t *testing.T) {
	p := &Program{msgs: make(chan Msg, 1)}
	start := time.Now()
	p.stopInput()
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Errorf("stopInput with no input source waited %v", elapsed)
	}
}

// mustWaitFor is waitFor (program_test.go) with a failure that stops the test and
// names what it was waiting for.
func mustWaitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	if err := waitFor(t, cond); err != nil {
		t.Fatalf("timed out waiting for %s: %v", what, err)
	}
}
