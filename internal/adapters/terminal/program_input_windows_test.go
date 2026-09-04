//go:build windows

package terminal

// The one decision program_input_windows.go makes that a CI runner can observe.
//
// A runner gives `go test` pipes, never a console window, and a pipe is exactly
// the wrong kind of stream for this source: the console event API refuses it. That
// is the failure the program must report at startup rather than discover as a
// TUI that reads no keys, so the refusal is the thing under test — not a stand-in
// for the reading itself, which no runner can see (the real-machine list in
// docs/internal/windows-console.md owns that).

import (
	"os"
	"strings"
	"testing"
)

func TestNewInputRefusesANonConsole(t *testing.T) {
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer pr.Close()
	defer pw.Close()

	_, err = newInput(&TTY{in: pr, out: pr})
	if err == nil {
		t.Fatal("newInput accepted a pipe as a console input buffer")
	}
	// The error has to say what could not be read, because it is the whole
	// explanation a user gets for a startup that produced no TUI at all.
	if !strings.Contains(err.Error(), "console keyboard") {
		t.Errorf("newInput error = %q, want it to name the console keyboard", err)
	}
}

// TestReadConsoleEventsRejectsAnEmptyBuffer pins the guard that keeps the call
// from passing a nil pointer as the record array. The handle is zero on purpose:
// an empty batch must be refused before the console is asked, because the
// alternative is a panic on records[0] in the caller.
func TestReadConsoleEventsRejectsAnEmptyBuffer(t *testing.T) {
	read, err := readConsoleEvents(0, nil)
	if err != nil {
		t.Errorf("empty batch = (%d, %v), want (0, nil) without reaching the console", read, err)
	}
	if read != 0 {
		t.Errorf("empty batch read %d events, want 0", read)
	}
}
