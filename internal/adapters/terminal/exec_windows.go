//go:build windows

package terminal

// Windows suspend support (module 5, S2): Ctrl-Z suspension is not
// supported (matching bubbletea's suspendSupported=false) and the input
// loop cannot be parked without console-level polling, so pauseInput and
// resumeInput are no-ops. The editor handoff still works: the terminal is
// restored to cooked mode while the child runs, with the known limitation
// that the (blocking) input reader may race the child for keystrokes.

// suspendProcess is a no-op on Windows: Ctrl-Z does not suspend the program.
func suspendProcess() {}

// pauseInput is a no-op on Windows (see file comment).
func (p *Program) pauseInput() {}

// resumeInput is a no-op on Windows (see file comment).
func (p *Program) resumeInput() {}
