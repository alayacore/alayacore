package terminal

// The exit sequence, pinned by order rather than by membership.
//
// screen_test.go → TestScreenStartStop already checks that Start/Stop emit the
// alt-screen and mode sequences — and it stayed green when the teardown was
// rewritten, which is the gap this file closes: containment cannot express the
// two properties that actually matter here.
//
//  1. Everything that moves or erases happens while the alternate buffer is
//     still current. The same bytes sent after the switch would wipe the
//     shell's screen instead of ours.
//  2. The cursor restore is last. Nothing may be written to this stream after
//     it — the next byte belongs to the shell.
//
// Status: written while chasing the delayed-prompt report on Windows, and that
// report is NOT cured by this sequence — the prompt was waiting on an abandoned
// console read, which is what docs/internal/windows-console.md → "What the two
// reports were" now explains. The ordering still earns its keep on its own terms:
// property 1 is the difference between clearing our screen and clearing the user's,
// and property 2 is the guarantee that nothing of ours lands after the shell's
// first byte. Nothing here should be read as the test of an explained cause.

import (
	"bytes"
	"strings"
	"testing"
)

const (
	seqSaveCursor   = "\x1b[s"
	seqRestoreCur   = "\x1b[u"
	seqEraseAll     = "\x1b[2J"
	seqHomeLastRow  = "\x1b[24;1H" // CursorPosition(1, 24)
	seqEnterAlt     = "\x1b[?1049h"
	seqLeaveAlt     = "\x1b[?1049l"
	seqPasteOff     = "\x1b[?2004l"
	seqFocusOff     = "\x1b[?1004l"
	seqCursorShown  = "\x1b[?25h"
	seqCursorHidden = "\x1b[?25l"
)

func TestScreenStartSavesCursorBeforeEnteringAlt(t *testing.T) {
	var buf bytes.Buffer
	s := &Screen{out: &buf}
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	out := buf.String()

	save, enter := strings.Index(out, seqSaveCursor), strings.Index(out, seqEnterAlt)
	if save < 0 || enter < 0 {
		t.Fatalf("Start() = %q, want both %q and %q", out, seqSaveCursor, seqEnterAlt)
	}
	if save > enter {
		t.Errorf("cursor saved after entering the alt screen (pos %d vs %d): the position captured would be the alt buffer's", save, enter)
	}
	if !strings.HasSuffix(out, seqCursorHidden) {
		t.Errorf("Start() should end by hiding the cursor, got %q", out)
	}
}

// TestScreenStopOrder is the whole contract of the teardown.
func TestScreenStopOrder(t *testing.T) {
	var buf bytes.Buffer
	s := &Screen{out: &buf, width: 80, height: 24}
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	if err := s.Stop(); err != nil {
		t.Fatal(err)
	}
	out := buf.String()

	ordered := []string{
		seqHomeLastRow, // 1. park the caret on the bottom row
		seqEraseAll,    // 2. blank the screen we are leaving
		seqPasteOff,    // 3. turn the modes off
		seqFocusOff,
		seqCursorShown,
		seqLeaveAlt, // 4. only then switch back
		seqRestoreCur,
	}
	prev := -1
	for _, want := range ordered {
		at := strings.Index(out, want)
		if at < 0 {
			t.Fatalf("Stop() missing %q, got %q", want, out)
		}
		if at < prev {
			t.Errorf("%q is out of order in Stop(): %q", want, out)
			prev = at
		} else {
			prev = at
		}
	}

	// The restore must be the last byte on the stream: after it, the console
	// belongs to the shell again.
	if !strings.HasSuffix(out, seqRestoreCur) {
		t.Errorf("Stop() wrote %q after the cursor restore", out[strings.LastIndex(out, seqRestoreCur)+len(seqRestoreCur):])
	}

	// And the destructive steps must sit on the near side of the switch.
	if strings.Index(out, seqEraseAll) > strings.Index(out, seqLeaveAlt) {
		t.Error("Stop() erased the screen after leaving the alt screen — that is the shell's screen")
	}
	if strings.Index(out, seqHomeLastRow) > strings.Index(out, seqLeaveAlt) {
		t.Error("Stop() moved the cursor after leaving the alt screen")
	}
}

// TestScreenStopWithoutSizeStillLeavesCleanly: a Screen that never learned its
// size (no TTY, or a Start before the first WindowSizeMsg) cannot target a
// bottom row, so it must skip the move and still hand the terminal back.
func TestScreenStopWithoutSizeStillLeavesCleanly(t *testing.T) {
	var buf bytes.Buffer
	s := &Screen{out: &buf}
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	if err := s.Stop(); err != nil {
		t.Fatal(err)
	}
	out := buf.String()

	if strings.Contains(out, ";1H") {
		t.Errorf("Stop() emitted a cursor move with no known height: %q", out)
	}
	for _, want := range []string{seqEraseAll, seqLeaveAlt, seqRestoreCur} {
		if !strings.Contains(out, want) {
			t.Errorf("Stop() missing %q, got %q", want, out)
		}
	}
	if !strings.HasSuffix(out, seqRestoreCur) {
		t.Errorf("Stop() should end on the cursor restore, got %q", out)
	}
}

func TestScreenStopIsIdempotent(t *testing.T) {
	var buf bytes.Buffer
	s := &Screen{out: &buf, width: 80, height: 24}
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	if err := s.Stop(); err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	if err := s.Stop(); err != nil {
		t.Fatal(err)
	}
	if buf.Len() != 0 {
		t.Errorf("second Stop() wrote %q; the shell's screen must not be touched twice", buf.String())
	}

	// Restart after a stop (the editor handoff does exactly this): the cursor
	// is saved again, inside the same single write as the alt-screen entry.
	buf.Reset()
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if strings.Index(out, seqSaveCursor) > strings.Index(out, seqEnterAlt) {
		t.Errorf("re-Start() did not save the cursor before entering alt: %q", out)
	}
}
