//go:build windows

package terminal

// Windows console-mode hooks (the TTY lifecycle lives in term_io.go).
//
// A Win32 console buffer owns a mode word, and ANSI/VT sequences are only
// interpreted once the application asks for it: without
// ENABLE_VIRTUAL_TERMINAL_PROCESSING the buffer writes the escape bytes out as
// visible text, so an alternate-screen entry lands on screen as "?1049h". This
// is the piece that the deleted Bubble Tea fork used to do for us
// (third_party/bubbletea/tty_windows.go → initInput, dropped in 4edb5a85);
// golang.org/x/term does the input half — its makeRaw sets
// ENABLE_VIRTUAL_TERMINAL_INPUT — and never touches the output handle.
//
// The mode is per screen buffer, and the shell keeps using that buffer after
// we exit, so everything set here is saved and put back by exitVT. Leaving
// DISABLE_NEWLINE_AUTO_RETURN behind would corrupt the *shell's* output: with
// it on, a bare LF moves the cursor down without returning to column 0.
//
// Both hooks are deliberately thin. The decisions are the two pure functions
// below so that the bit arithmetic can be tested without a console — see
// console_windows_test.go, which runs in the Windows CI job.

import (
	"fmt"

	"golang.org/x/sys/windows"
)

// vtState records the console mode enterVT replaced. The input side is not
// recorded: TTY.MakeRaw calls term.MakeRaw first, and x/term saves the input
// mode before it changes it, so term.Restore (see TTY.Restore) already puts
// back exactly what enterVT later modified — capturing it twice would let the
// two copies disagree about which is original.
type vtState struct {
	out   uint32 // previous output-handle console mode, valid when saved
	saved bool
}

// outputVTMode returns mode with ANSI processing turned on.
//
// ENABLE_VIRTUAL_TERMINAL_PROCESSING is the sequence interpreter.
// DISABLE_NEWLINE_AUTO_RETURN makes LF move down without returning to column
// 0, matching every other terminal the renderer is written for — screen.go
// emits "\r\n" for content newlines precisely because it assumes that
// semantics. The flags are OR'd, never cleared: whatever the host had set
// stays set.
func outputVTMode(mode uint32) uint32 {
	return mode | windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING |
		windows.DISABLE_NEWLINE_AUTO_RETURN
}

// inputNoSelectionMode returns mode with the console's own mouse selection
// turned off.
//
// QuickEdit is on by default in cmd and PowerShell: a single click inside the
// window puts the buffer into select mode, which suspends writes from the
// application — the UI freezes mid-frame and keystrokes are swallowed until
// the user presses Esc. Setting ENABLE_EXTENDED_FLAGS is what makes the
// QUICK_EDIT bit take effect at all; without it the write is accepted and
// silently ignored.
func inputNoSelectionMode(mode uint32) uint32 {
	return mode&^windows.ENABLE_QUICK_EDIT_MODE | windows.ENABLE_EXTENDED_FLAGS
}

// enterVT makes outFd able to process ANSI sequences and stops the console
// from capturing the mouse. An error here means the TUI cannot render at all on
// this host, so the caller must not proceed.
//
// The hooks take fds rather than *os.File: that is what the console API wants,
// and it is how the rest of this file already reaches x/term
// (term.MakeRaw(int(t.in.Fd()))).
func enterVT(inFd, outFd uintptr) (vtState, error) {
	var st vtState

	outHandle := windows.Handle(outFd)
	var prev uint32
	if err := windows.GetConsoleMode(outHandle, &prev); err != nil {
		return st, fmt.Errorf("this stream is not a console window the interface can draw on (%w)", err)
	}
	if err := windows.SetConsoleMode(outHandle, outputVTMode(prev)); err != nil {
		return st, fmt.Errorf("this console will not accept ANSI sequence processing (%w); run in Windows Terminal, or start a plain-text session with --plainio", err)
	}
	st.out, st.saved = prev, true

	// Best effort: a console that rejects this still renders correctly, it
	// just keeps the click-to-select behavior, which is a usability problem
	// rather than a broken frame. Failing the whole startup over it would be
	// the wrong trade.
	inHandle := windows.Handle(inFd)
	var inMode uint32
	if err := windows.GetConsoleMode(inHandle, &inMode); err == nil {
		_ = windows.SetConsoleMode(inHandle, inputNoSelectionMode(inMode))
	}

	return st, nil
}

// exitVT puts the output console mode back. Safe to call on a zero vtState.
func exitVT(outFd uintptr, st vtState) {
	if !st.saved {
		return
	}
	_ = windows.SetConsoleMode(windows.Handle(outFd), st.out)
}
