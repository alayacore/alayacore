package terminal

// Terminal I/O layer: TTY opening, raw mode, and byte reading.
// This is module 1 of the self-built TUI stack (see docs/tui-architecture.md).
//
// OpenTTY prefers the real stdin/stdout when they are terminals and falls
// back to the controlling TTY (/dev/tty on Unix, CONIN$/CONOUT$ on Windows)
// when stdin/stdout are piped — mirroring bubbletea's behavior.

import (
	"errors"
	"os"

	"golang.org/x/term"
)

// TTY wraps the terminal's input/output files and raw-mode state.
type TTY struct {
	in     *os.File
	out    *os.File
	state  *term.State
	buffer []byte // internal buffer for reads that produced more than asked
}

// OpenTTY opens the terminal's input and output files (see file comment).
func OpenTTY() (*TTY, error) {
	return openTTY()
}

// In returns the input file.
func (t *TTY) In() *os.File { return t.in }

// Out returns the output file.
func (t *TTY) Out() *os.File { return t.out }

// MakeRaw puts the terminal into raw mode. Calling it twice is a no-op.
func (t *TTY) MakeRaw() error {
	if t.state != nil {
		return nil
	}
	st, err := term.MakeRaw(int(t.in.Fd()))
	if err != nil {
		return err
	}
	t.state = st
	return nil
}

// Restore puts the terminal back into cooked mode. It is safe to call
// multiple times; the second and later calls are no-ops.
func (t *TTY) Restore() error {
	if t.state == nil {
		return nil
	}
	err := term.Restore(int(t.in.Fd()), t.state)
	t.state = nil
	return err
}

// Read reads bytes from the terminal. Bytes buffered by a previous read
// (when the caller's buffer was smaller than the available input) are
// returned first.
func (t *TTY) Read(p []byte) (int, error) {
	if len(t.buffer) > 0 {
		n := copy(p, t.buffer)
		t.buffer = t.buffer[n:]
		return n, nil
	}
	n, err := t.in.Read(p)
	if err != nil {
		return n, err
	}
	return n, nil
}

// Close closes the underlying input and output files.
func (t *TTY) Close() error {
	var errs []error
	if t.in != nil {
		errs = append(errs, t.in.Close())
	}
	if t.out != nil && t.out != t.in {
		errs = append(errs, t.out.Close())
	}
	return errors.Join(errs...)
}
