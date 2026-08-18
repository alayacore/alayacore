//go:build !windows

package terminal

// Unix input loop (module 2/5): reads bytes from the TTY and feeds them to
// the parser. The loop polls the TTY with a short timeout instead of
// blocking in a read so it can notice the pause flag (exec_unix.go) between
// polls: when paused it parks and stops reading entirely, guaranteeing a
// foreground child (editor) gets every keystroke.

import (
	"time"

	"golang.org/x/sys/unix"
)

// readInput reads bytes from the TTY and feeds them to the parser. When a
// read ends with an incomplete escape sequence, it waits briefly for more
// bytes before resolving it (so a lone ESC becomes the Escape key).
func (p *Program) readInput(ctxDone <-chan struct{}) {
	buf := make([]byte, 256)
	fds := []unix.PollFd{{Fd: int32(p.tty.In().Fd()), Events: unix.POLLIN}}

	for {
		if p.parkIfSuspended(ctxDone) {
			return
		}

		// Poll with a short timeout: lets the loop re-check the pause flag
		// (and ctxDone) without blocking in a read for long.
		n, err := unix.Poll(fds, 100) // poll timeout in ms
		if err != nil {
			if err == unix.EINTR {
				continue
			}
			return
		}
		if n == 0 {
			continue // timeout: loop back and re-check the pause flag
		}

		cnt, err := p.tty.Read(buf)
		if cnt > 0 && p.deliverParsed(buf[:cnt], ctxDone) {
			return
		}
		if err != nil {
			return
		}
	}
}

// parkIfSuspended parks the input loop while the program is released (editor
// handoff, Ctrl-Z): it stops reading so a foreground child gets every
// keystroke, and returns true when ctxDone fired while parked. The park is
// acknowledged via parkedCh so releaseTerminal knows no read is in flight.
func (p *Program) parkIfSuspended(ctxDone <-chan struct{}) bool {
	if !p.inputPaused.Load() {
		return false
	}
	select {
	case p.parkedCh <- struct{}{}:
	default:
	}
	select {
	case <-p.resumeCh:
		return false
	case <-ctxDone:
		return true
	}
}

// deliverParsed parses data and delivers the resulting messages, waiting
// briefly for the rest of a split escape sequence. It returns true when
// ctxDone fired mid-delivery.
func (p *Program) deliverParsed(data []byte, ctxDone <-chan struct{}) bool {
	msgs := p.parser.Parse(data)
	for _, msg := range msgs {
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
