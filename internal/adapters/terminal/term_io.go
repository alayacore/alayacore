package terminal

// Terminal I/O layer: TTY opening, raw mode (including the platform's
// sequence-processing mode — console_unix.go / console_windows.go), and byte
// reading. This is module 1 of the self-built TUI stack
// (see docs/tui-architecture.md).
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
	in    *os.File
	out   *os.File
	state *term.State

	// vt is the console-mode state enterVT returned (see console_windows.go /
	// console_unix.go). On Unix it carries nothing; the field exists so the
	// lifecycle below is written once for every platform.
	vt vtState
}

// OpenTTY opens the terminal's input and output files (see file comment).
func OpenTTY() (*TTY, error) {
	return openTTY()
}

// In returns the input file.
func (t *TTY) In() *os.File { return t.in }

// Out returns the output file.
func (t *TTY) Out() *os.File { return t.out }

// MakeRaw puts the terminal into raw mode and into the state the renderer
// needs to write ANSI sequences (console_windows.go → enterVT). Calling it
// twice is a no-op.
//
// On error the input mode it already changed is put back, so a terminal this
// program cannot drive is left exactly as it was found: an unusable console
// plus a dead TUI is far worse than a clean startup failure.
func (t *TTY) MakeRaw() error {
	// The guard is what keeps enterVT/exitVT paired: no second enter can
	// happen before the exit, so exitVT always restores a mode that was
	// captured before any of our bits were set. Nesting them would save an
	// already-modified console mode and leak the VT bits to the shell.
	if t.state != nil {
		return nil
	}
	st, err := term.MakeRaw(int(t.in.Fd()))
	if err != nil {
		return err
	}
	t.state = st

	vt, err := enterVT(t.in.Fd(), t.out.Fd())
	if err != nil {
		_ = term.Restore(int(t.in.Fd()), st)
		t.state = nil
		return err
	}
	t.vt = vt
	return nil
}

// Restore puts the terminal back into cooked mode, including the console mode
// MakeRaw changed. It is safe to call multiple times; the second and later
// calls are no-ops.
func (t *TTY) Restore() error {
	if t.state == nil {
		return nil
	}
	err := term.Restore(int(t.in.Fd()), t.state)
	t.state = nil

	// Not gated on the error above: the output mode has to go back whether or
	// not restoring the input worked. vt is cleared too, so a later MakeRaw
	// (the editor handoff re-acquires the terminal) negotiates the mode again
	// from scratch instead of trusting a stale copy.
	exitVT(t.out.Fd(), t.vt)
	t.vt = vtState{}

	return err
}

// Read reads bytes from the terminal.
func (t *TTY) Read(p []byte) (int, error) {
	return t.in.Read(p)
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
