//go:build !windows

package terminal

// Unix implementation of openTTY: use stdin/stdout when they are terminals,
// otherwise fall back to the controlling terminal (/dev/tty) — the same
// strategy bubbletea uses (uv.OpenTTY).

import (
	"fmt"
	"os"

	"golang.org/x/term"
)

func openTTY() (*TTY, error) {
	in := os.Stdin
	out := os.Stdout

	if !term.IsTerminal(int(in.Fd())) || !term.IsTerminal(int(out.Fd())) {
		tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
		if err != nil {
			return nil, fmt.Errorf("terminal: could not open controlling TTY: %w", err)
		}
		if !term.IsTerminal(int(in.Fd())) {
			in = tty
		}
		if !term.IsTerminal(int(out.Fd())) {
			out = tty
		}
	}

	return &TTY{in: in, out: out}, nil
}
