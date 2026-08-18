package terminal

// Regression tests for TTY lifecycle.
//
// The audit (TERMINAL_AUDIT.md §B-7) flagged a potential double-close
// risk in TTY.Close() when in and out point to the same fd (the
// case where both stdin and stdout are pipes and we open /dev/tty
// for both). This file locks down the actual contract:
//
//   - Close() with in == out returns nil and closes the fd exactly once.
//   - A second Close() call does not panic and is idempotent.
//   - Close() with distinct in/out closes both.
//   - Close() with nil in/out is a no-op.

import (
	"errors"
	"os"
	"testing"
)

func TestTTYCloseSameFD(t *testing.T) {
	f, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("open %s: %v", os.DevNull, err)
	}
	tty := &TTY{in: f, out: f}

	if err := tty.Close(); err != nil {
		t.Errorf("first Close() = %v, want nil", err)
	}
	// Second Close must not panic and must report the file is closed
	// (not a fresh EBADF or similar). The exact error string is not
	// part of the contract — what matters is "no panic, no double-close".
	if err := tty.Close(); !errors.Is(err, os.ErrClosed) {
		t.Errorf("second Close() = %v, want os.ErrClosed", err)
	}
}

func TestTTYCloseDistinctFDs(t *testing.T) {
	in, err := os.OpenFile(os.DevNull, os.O_RDONLY, 0)
	if err != nil {
		t.Fatalf("open in: %v", err)
	}
	out, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		in.Close()
		t.Fatalf("open out: %v", err)
	}
	tty := &TTY{in: in, out: out}

	if err := tty.Close(); err != nil {
		t.Errorf("Close() = %v, want nil", err)
	}
}

func TestTTYCloseNilFields(t *testing.T) {
	tty := &TTY{}
	if err := tty.Close(); err != nil {
		t.Errorf("Close() with nil fields = %v, want nil", err)
	}
}