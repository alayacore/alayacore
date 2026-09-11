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
//
// An incomplete escape sequence is resolved here, on *silence*, and not on the
// read that happened to stop in the middle of it. The difference is the whole of
// a bug: this loop is the only reader, so a wait that does not read cannot
// receive the rest of a sequence — the rest stays in the source, and a flush
// taken at the end of the wait drops the head and leaves the body to be
// delivered on the next read as typing. The read boundaries make it reachable
// rather than theoretical: a Unix read stops at inputReadSize bytes and a
// Windows one at consoleEventsPerRead events, so a burst that is not a multiple
// of the boundary (the characters a host synthesizes for a mouse report are the
// reported case) is cut mid-sequence routinely. The cut is meant to be invisible
// — program_input_unix.go says as much of the parser ("it keeps an incomplete
// sequence across calls") — and it becomes invisible here, where the next read
// is asked for the rest before any timer is allowed to resolve it.
func (p *Program) readInput(ctxDone <-chan struct{}) {
	defer close(p.inputStopped)

	// pendingSince is when the parser last received bytes and was still
	// holding an incomplete sequence; the zero value means nothing is
	// outstanding, so it is also the gate on the timer below.
	//
	// The residue is growth: a sequence that keeps arriving without a 50 ms gap
	// is allowed to keep growing, where the reader this replaces gave up after
	// one read. What bounds it is input the user is already sending — a paste is
	// unbounded the same way, and the prompt's own value is uncapped — and the
	// alternative is dropping a sequence that is still on its way, which is the
	// bug this loop exists to fix.
	var pendingSince time.Time

	for {
		if p.parkIfSuspended(ctxDone) {
			return
		}
		data, err := p.input.next()
		if len(data) > 0 {
			if p.deliverParsed(data, ctxDone) {
				return
			}
			if p.parser.MidSequence() {
				pendingSince = time.Now()
			} else {
				pendingSince = time.Time{}
			}
		} else if !pendingSince.IsZero() && time.Since(pendingSince) >= escSequenceTimeout {
			// The terminal has been quiet for the whole timeout with a
			// sequence still incomplete, so it is not coming: Flush resolves a
			// lone ESC to the Escape key and drops anything else that cannot
			// be completed.
			pendingSince = time.Time{}
			for _, msg := range p.parser.Flush() {
				if p.sendInput(msg, ctxDone) {
					return
				}
			}
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

// deliverParsed parses data and delivers the resulting messages. It returns true
// when ctxDone fired mid-delivery.
//
// Resolving an incomplete sequence is deliberately not done here: this function
// is called with the bytes of one read, and the reader that can fetch the rest
// is the loop (readInput), not this call.
func (p *Program) deliverParsed(data []byte, ctxDone <-chan struct{}) bool {
	for _, msg := range p.parser.Parse(data) {
		if p.sendInput(msg, ctxDone) {
			return true
		}
	}
	return false
}

// sendInput delivers one input message, returning true when ctxDone fired. It
// goes to the dedicated input channel, which the event loop drains before
// anything else (program.go → run): input must not wait behind a backlog of
// command results, ticks and display writes.
func (p *Program) sendInput(msg Msg, ctxDone <-chan struct{}) bool {
	select {
	case p.inputMsgs <- msg:
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
