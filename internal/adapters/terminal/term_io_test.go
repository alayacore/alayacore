package terminal

// TTY.Close's contract: the files openTTY opened are released; the files it did
// not — the process's own stdin and stdout, which is the usual case — are left
// open.
//
// The distinction is not tidiness. os.File.Close waits for a read still in flight
// on that file, so closing the stream the input loop reads from is a shutdown that
// waits for a keystroke: the process does not exit, and the shell behind it does
// not get its prompt back (docs/internal/windows-console.md records the report).
// Program.stopInput is the fix that removes the in-flight read; not closing what
// this program never opened is the one that keeps the shell's own streams intact,
// and it is what these tests pin.

import (
	"errors"
	"os"
	"testing"
)

// openDevNullPair returns two stand-ins for the terminal's streams. A file on
// /dev/null reads and writes like a stream and, unlike os.Stdin, belongs to this
// test alone.
func openDevNullPair(t *testing.T) (in, out *os.File) {
	t.Helper()
	in, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("open in: %v", err)
	}
	out, err = os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		in.Close()
		t.Fatalf("open out: %v", err)
	}
	t.Cleanup(func() {
		in.Close()
		out.Close()
	})
	return in, out
}

// mustStillUsable asserts the file has not been closed, by writing to it: a closed
// os.File reports ErrClosed, which is the observable difference.
func mustStillUsable(t *testing.T, what string, f *os.File) {
	t.Helper()
	if _, err := f.Write([]byte{'x'}); err != nil {
		t.Errorf("%s was closed (%v); it is not this program's to close", what, err)
	}
}

// mustClosed asserts the file was closed.
func mustClosed(t *testing.T, what string, f *os.File) {
	t.Helper()
	_, err := f.Write([]byte{'x'})
	if !errors.Is(err, os.ErrClosed) {
		t.Errorf("%s: write after Close = %v, want %v", what, err, os.ErrClosed)
	}
}

// TestTTYCloseLeavesUnownedStreamsAlone is the ordinary case: stdin and stdout are
// the process's own, OpenTTY took them as they were, and Close must not touch
// them.
func TestTTYCloseLeavesUnownedStreamsAlone(t *testing.T) {
	in, out := openDevNullPair(t)
	tty := &TTY{in: in, out: out}

	if err := tty.Close(); err != nil {
		t.Errorf("Close() = %v, want nil", err)
	}
	mustStillUsable(t, "stdin", in)
	mustStillUsable(t, "stdout", out)
}

// TestTTYCloseReleasesOwnedFallback is the case the flags exist for: the streams
// were redirected, OpenTTY opened the controlling terminal itself, and that handle
// is the one to give back. When one file serves both, it is closed once — and a
// second Close reports it closed rather than panicking on a double release.
func TestTTYCloseReleasesOwnedFallback(t *testing.T) {
	f, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	tty := &TTY{in: f, out: f, ownsIn: true, ownsOut: true}

	if err := tty.Close(); err != nil {
		t.Errorf("first Close() = %v, want nil", err)
	}
	mustClosed(t, "the controlling terminal", f)
	if err := tty.Close(); !errors.Is(err, os.ErrClosed) {
		t.Errorf("second Close() = %v, want %v", err, os.ErrClosed)
	}
}

// TestTTYCloseReleasesOnlyWhatItOpened is the mixed case both platforms can
// produce — stdout redirected to a file while stdin stays the console (or, on
// Windows, the reverse) — and it is the one where a blanket "close both" is worst:
// it would hand the shell a closed standard input.
func TestTTYCloseReleasesOnlyWhatItOpened(t *testing.T) {
	in, out := openDevNullPair(t)
	tty := &TTY{in: in, out: out, ownsOut: true}

	if err := tty.Close(); err != nil {
		t.Errorf("Close() = %v, want nil", err)
	}
	mustStillUsable(t, "stdin", in)
	mustClosed(t, "the opened stdout", out)
}

// TestTTYCloseZeroValueIsNoOp: the fields are zero before OpenTTY has produced a
// TTY, and the teardown runs on every exit path — including the ones that get
// there without one.
func TestTTYCloseZeroValueIsNoOp(t *testing.T) {
	tty := &TTY{}
	if err := tty.Close(); err != nil {
		t.Errorf("Close() on a zero TTY = %v, want nil", err)
	}
}
