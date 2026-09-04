package terminal

// The input loop: read from the terminal, parse the bytes into messages, deliver
// them to the event loop — and the park protocol that lets the program stop
// reading while a foreground child owns the terminal (the editor handoff and
// Ctrl-Z, both in exec.go).
//
// The protocol is the reason a child can be handed the keyboard at all. The
// terminal is a shared object, and input goes to whoever asked for it first: a
// read this program leaves pending across the handoff would swallow the child's
// keystrokes, and — because os.File.Close waits for a read in flight — an unread
// one at exit would hold up the process while the shell waited for its prompt.
// So releasing the terminal always means waiting for the loop to report that it
// is between reads, and taking the terminal back always means waking it up.
//
// What makes the wait safe is the other half of the design: an inputSource's
// next() returns in bounded time on its own, on every platform
// (program_input_unix.go polls with a timeout; program_input_windows.go only
// reads events it has already counted). A loop that could sit in an unbounded
// read could not be parked at all, which is exactly what the Windows input
// reader used to be.

import "time"

// inputSource is where the loop gets its bytes. The platform files provide it: a
// byte stream on a Unix terminal, decoded console events on Windows.
type inputSource interface {
	// next returns the bytes the terminal has delivered, or an empty slice when
	// nothing arrived before its wait ended. The slice belongs to the source and
	// is valid until the next call — the loop parses it before calling again,
	// and the parser copies anything it needs to keep across calls.
	next() ([]byte, error)
}

const (
	// readLoopTimeout bounds how long the program waits for the input loop to
	// report that it has stopped reading. The loop re-checks the park request
	// once per wait of its own — inputPollTimeout on Unix,
	// consoleInputPollInterval on Windows — so reaching this timeout means the
	// loop is wedged somewhere else, and the alternative to giving up after a
	// bounded wait is never starting the editor, or never handing the terminal
	// back.
	readLoopTimeout = 500 * time.Millisecond
)

// readInput runs the loop. It exits when ctxDone is closed, when the source
// reports an error, or when a delivery is abandoned because the program is
// finishing — and it announces that it is finished either way, because the
// teardown may not hand the terminal back before then (stopInput).
func (p *Program) readInput(ctxDone <-chan struct{}) {
	defer close(p.inputStopped)

	for {
		if p.parkIfSuspended(ctxDone) {
			return
		}
		data, err := p.input.next()
		if len(data) > 0 && p.deliverParsed(data, ctxDone) {
			return
		}
		if err != nil {
			return
		}
	}
}

// parkIfSuspended parks the input loop while the program has released the
// terminal: it stops reading so a foreground child gets every keystroke, and
// returns true when ctxDone fired while parked. The park is acknowledged on
// parkedCh, and releaseTerminal waits for that acknowledgement before touching
// the terminal — so when it returns, no read of ours is in flight.
//
// The flag is the truth and the wake token is only a hint, which is why the
// request is re-read after every wake. A token can be left behind: a pause that
// timed out (a loop blocked delivering into a full queue) is resumed while the
// loop is nowhere near the park check, and the token then sits in the channel.
// Treating that as a release would leave the loop reading the terminal for the
// rest of the session — the original bug, returning after one rare timeout.
func (p *Program) parkIfSuspended(ctxDone <-chan struct{}) bool {
	for p.inputPaused.Load() {
		select {
		case p.parkedCh <- struct{}{}:
		default:
		}
		select {
		case <-p.resumeCh:
		case <-ctxDone:
			return true
		}
	}
	return false
}

// deliverParsed parses data and delivers the resulting messages, waiting briefly
// for the rest of a split escape sequence. It returns true when ctxDone fired
// mid-delivery.
func (p *Program) deliverParsed(data []byte, ctxDone <-chan struct{}) bool {
	for _, msg := range p.parser.Parse(data) {
		if p.sendInput(msg, ctxDone) {
			return true
		}
	}
	if !p.parser.HasPending() {
		return false
	}
	// Wait briefly for the rest of a split sequence.
	select {
	case <-time.After(escSequenceTimeout):
		for _, msg := range p.parser.Flush() {
			if p.sendInput(msg, ctxDone) {
				return true
			}
		}
	case <-ctxDone:
		return true
	}
	return false
}

// sendInput delivers one input message, returning true when ctxDone fired.
func (p *Program) sendInput(msg Msg, ctxDone <-chan struct{}) bool {
	select {
	case p.msgs <- msg:
		return false
	case <-ctxDone:
		return true
	}
}

// pauseInput asks the input loop to park and waits for it to acknowledge, so a
// foreground child can be given the terminal. With no input source (a program
// with no TTY, which is every test that drives the loop by hand) there is no loop
// to wait for.
//
// A stale acknowledgement is dropped before the request, not after it: the loop
// only ever answers a flag it has seen set, so an acknowledgement already sitting
// in the channel at this moment belongs to an earlier pause — typically one that
// timed out while the loop was blocked delivering into a full queue, and parked
// afterwards. Waiting on that would return while the loop is still reading.
func (p *Program) pauseInput() {
	if p.input == nil {
		return
	}
	select {
	case <-p.parkedCh:
	default:
	}
	p.inputPaused.Store(true)
	select {
	case <-p.parkedCh:
	case <-p.inputStopped: // the loop already finished; nothing can read now
	case <-time.After(readLoopTimeout):
	}
}

// resumeInput wakes a parked input loop and lets it read the terminal again. The
// wake is a hint (see parkIfSuspended): a token left over because the loop was
// not parked when this ran cannot release a later park.
func (p *Program) resumeInput() {
	if p.input == nil {
		return
	}
	p.inputPaused.Store(false)
	select {
	case p.resumeCh <- struct{}{}:
	default:
	}
}

// stopInput ends the input loop for good and waits for it to be gone. The
// teardown calls it before restoring the terminal and closing the files: an
// abandoned read is not merely untidy — os.File.Close waits for a read in flight,
// and a console read waits for input, so closing the terminal this program still
// reads from is how quitting ends up waiting for a keystroke before the shell
// prints its prompt.
//
// It does not need a park request: the caller has already closed ctxDone (run
// returns first, and its defer closes it), which is what the loop returns on. The
// park flag is set anyway so that the loop is guaranteed not to start another read
// even if the ordering above ever changes.
func (p *Program) stopInput() {
	if p.input == nil {
		return
	}
	p.inputPaused.Store(true)
	select {
	case <-p.inputStopped:
	case <-time.After(readLoopTimeout):
	}
}
