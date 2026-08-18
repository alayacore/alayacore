//go:build !windows

package terminal

// Unix suspend support (module 5, S2): SIGTSTP/SIGCONT for Ctrl-Z and the
// input-parking protocol that guarantees a foreground child (editor) gets
// every keystroke while the program is released.
//
// The input loop (program_input_unix.go) polls the TTY with a short timeout
// instead of blocking in a read, so it can notice the pause flag between
// reads. pauseInput waits for the loop to acknowledge (parked) before the
// terminal is released — after that no read is pending, so nothing can steal
// input from the child.

import (
	"os"
	"os/signal"
	"syscall"
	"time"
)

// suspendProcess sends SIGTSTP to the entire process group (stopping the
// program like a regular Ctrl-Z does in cooked mode) and blocks until
// SIGCONT arrives (the user runs `fg`).
func suspendProcess() {
	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGCONT)
	defer signal.Stop(c)
	_ = syscall.Kill(0, syscall.SIGTSTP)
	<-c // blocks until a CONT happens
}

// pauseInput asks the input loop to park: it stops reading the TTY so a
// foreground child gets every keystroke. It waits up to 500ms for the input
// loop to acknowledge (mirroring bubbletea's waitForReadLoop timeout).
func (p *Program) pauseInput() {
	p.inputPaused.Store(true)
	select {
	case <-p.parkedCh:
	case <-time.After(500 * time.Millisecond): // read-loop ack timeout
	}
}

// resumeInput wakes a parked input loop and lets it read the TTY again. It
// also drains any stale park signal left over from a timed-out pause, so a
// later pauseInput cannot mistake it for a fresh acknowledgement.
func (p *Program) resumeInput() {
	p.inputPaused.Store(false)
	select {
	case p.resumeCh <- struct{}{}:
	default:
	}
	select {
	case <-p.parkedCh:
	default:
	}
}
