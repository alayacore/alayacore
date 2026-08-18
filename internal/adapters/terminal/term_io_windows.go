//go:build windows

package terminal

// Windows implementation of openTTY: use stdin/stdout when they are
// consoles, otherwise fall back to CONIN$/CONOUT$.

import (
	"fmt"
	"os"

	"golang.org/x/term"
)

func openTTY() (*TTY, error) {
	in := os.Stdin
	out := os.Stdout

	if !term.IsTerminal(int(in.Fd())) || !term.IsTerminal(int(out.Fd())) {
		inFile, err := os.OpenFile("CONIN$", os.O_RDWR, 0)
		if err != nil {
			return nil, fmt.Errorf("terminal: could not open CONIN$: %w", err)
		}
		outFile, err := os.OpenFile("CONOUT$", os.O_RDWR, 0)
		if err != nil {
			inFile.Close()
			return nil, fmt.Errorf("terminal: could not open CONOUT$: %w", err)
		}
		if !term.IsTerminal(int(in.Fd())) {
			in = inFile
		} else {
			inFile.Close()
		}
		if !term.IsTerminal(int(out.Fd())) {
			out = outFile
		} else {
			outFile.Close()
		}
	}

	return &TTY{in: in, out: out}, nil
}
