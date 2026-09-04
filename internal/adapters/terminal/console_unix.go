//go:build !windows

package terminal

// Console-mode hooks for platforms without a console mode to negotiate.
//
// On Unix there is nothing to ask: whether a byte stream is a terminal is
// decided by the ioctl x/term already performs (TTY.MakeRaw would have failed
// if it were not one), and whether the thing on the other end interprets ANSI
// is the emulator's business — no process can request it. termios is saved and
// restored by TTY.MakeRaw / TTY.Restore.

// vtState is empty here; see console_windows.go for the version that carries
// a saved console mode.
type vtState struct{}

// enterVT always succeeds: no mode to enable.
func enterVT(_, _ uintptr) (vtState, error) { return vtState{}, nil }

// exitVT is a no-op.
func exitVT(_ uintptr, _ vtState) {}
