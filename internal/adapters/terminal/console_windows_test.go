//go:build windows

package terminal

// Tests for the console-mode bit arithmetic in console_windows.go.
//
// These run only on Windows (the whole file is build-tagged: it imports
// golang.org/x/sys/windows for the flag names). That is not a gap in coverage
// — the logic under test is the layout of a Win32 console mode word — but it
// does mean they only ever execute in the Windows CI job. The syscalls
// themselves are not covered here on purpose: `go test` on a runner has pipes
// for stdout, not a console, so anything that needs a real buffer would have to
// fake the API, and a fake SetConsoleMode proves nothing about a host we cannot
// observe. The real-machine list in this project's docs owns that part.

import (
	"testing"

	"golang.org/x/sys/windows"
)

func TestOutputVTMode(t *testing.T) {
	tests := []struct {
		name string
		in   uint32
		want uint32
	}{
		{
			name: "from a bare default console",
			in:   windows.ENABLE_PROCESSED_OUTPUT | windows.ENABLE_WRAP_AT_EOL_OUTPUT,
			want: windows.ENABLE_PROCESSED_OUTPUT | windows.ENABLE_WRAP_AT_EOL_OUTPUT |
				windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING | windows.DISABLE_NEWLINE_AUTO_RETURN,
		},
		{
			name: "from zero",
			in:   0,
			want: windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING | windows.DISABLE_NEWLINE_AUTO_RETURN,
		},
		{
			name: "already enabled (a child shell that left it on)",
			in:   windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING | windows.DISABLE_NEWLINE_AUTO_RETURN,
			want: windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING | windows.DISABLE_NEWLINE_AUTO_RETURN,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := outputVTMode(tt.in)
			if got != tt.want {
				t.Errorf("outputVTMode(%#x) = %#x, want %#x", tt.in, got, tt.want)
			}
		})
	}
}

// TestOutputVTModePreservesEverythingElse is the property that makes it safe to
// hand this mode to SetConsoleMode: the two bits are added and nothing else is
// touched, so a host configured with the grid font, or with wrap-at-EOL off, or
// with processed output disabled, keeps its configuration.
func TestOutputVTModePreservesEverythingElse(t *testing.T) {
	const preserved uint32 = windows.ENABLE_PROCESSED_OUTPUT |
		windows.ENABLE_WRAP_AT_EOL_OUTPUT |
		windows.ENABLE_LVB_GRID_WORLDWIDE

	got := outputVTMode(preserved)

	if got&preserved != preserved {
		t.Errorf("outputVTMode(%#x) = %#x: dropped pre-existing bits (%#x wanted)", preserved, got, preserved)
	}
	if extra := got & ^(preserved | windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING | windows.DISABLE_NEWLINE_AUTO_RETURN); extra != 0 {
		t.Errorf("outputVTMode set %#x beyond the VT bits and the preserved ones", extra)
	}
}

// TestOutputVTModeIsIdempotent matters because MakeRaw can run more than once
// in a session (the editor handoff releases and re-acquires the terminal), and
// each pass reads back what the previous one wrote.
func TestOutputVTModeIsIdempotent(t *testing.T) {
	once := outputVTMode(windows.ENABLE_PROCESSED_OUTPUT)
	twice := outputVTMode(once)
	if once != twice {
		t.Errorf("second application changed the result: %#x then %#x", once, twice)
	}
}

// TestOutputVTModeAddsExactlyTwoBits pins what the helper contributes: the VT
// interpreter and the newline flag, nothing else.
//
// The numeric aliasing is the reason the two helpers in console_windows.go are
// separate functions rather than one "enable" value used on both handles:
// 0x0004 reads as ENABLE_ECHO_INPUT and 0x0008 as ENABLE_WINDOW_INPUT when the
// word belongs to an input handle, and SetConsoleMode accepts that mismatch
// without complaint. Only the call site keeps the two apart.
func TestOutputVTModeAddsExactlyTwoBits(t *testing.T) {
	const want = windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING |
		windows.DISABLE_NEWLINE_AUTO_RETURN

	if got := outputVTMode(0); got != want {
		t.Errorf("outputVTMode(0) = %#x, want %#x", got, want)
	}
}

func TestInputNoSelectionMode(t *testing.T) {
	tests := []struct {
		name string
		in   uint32
		want uint32
	}{
		{
			name: "default console input (QuickEdit on, extended flags off)",
			in:   windows.ENABLE_PROCESSED_INPUT | windows.ENABLE_LINE_INPUT | windows.ENABLE_ECHO_INPUT | windows.ENABLE_QUICK_EDIT_MODE,
			want: windows.ENABLE_PROCESSED_INPUT | windows.ENABLE_LINE_INPUT | windows.ENABLE_ECHO_INPUT | windows.ENABLE_EXTENDED_FLAGS,
		},
		{
			name: "raw input as x/term leaves it, with VT input on",
			in:   windows.ENABLE_VIRTUAL_TERMINAL_INPUT,
			want: windows.ENABLE_VIRTUAL_TERMINAL_INPUT | windows.ENABLE_EXTENDED_FLAGS,
		},
		{
			name: "already disabled",
			in:   windows.ENABLE_EXTENDED_FLAGS,
			want: windows.ENABLE_EXTENDED_FLAGS,
		},
		{
			name: "mouse input left alone",
			in:   windows.ENABLE_MOUSE_INPUT | windows.ENABLE_QUICK_EDIT_MODE,
			want: windows.ENABLE_MOUSE_INPUT | windows.ENABLE_EXTENDED_FLAGS,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := inputNoSelectionMode(tt.in)
			if got != tt.want {
				t.Errorf("inputNoSelectionMode(%#x) = %#x, want %#x", tt.in, got, tt.want)
			}
			if got&windows.ENABLE_QUICK_EDIT_MODE != 0 {
				t.Error("QuickEdit still enabled: a click would enter select mode and freeze rendering")
			}
			if got&windows.ENABLE_EXTENDED_FLAGS == 0 {
				t.Error("ENABLE_EXTENDED_FLAGS not set: without it the console ignores the QuickEdit bit entirely")
			}
		})
	}
}

// TestExitVTZeroStateIsSafe: the field is zero before the first MakeRaw, and
// Restore runs on every exit path — including the one where MakeRaw never got
// as far as enterVT. The saved flag must be what decides, never the fd.
func TestExitVTZeroStateIsSafe(t *testing.T) {
	exitVT(0, vtState{})
	exitVT(0, vtState{out: windows.ENABLE_PROCESSED_OUTPUT}) // saved == false
}
