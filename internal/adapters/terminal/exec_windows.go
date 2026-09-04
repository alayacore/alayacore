//go:build windows

package terminal

// Windows suspend support (module 5, S2): Ctrl-Z does not suspend, because
// there is no process-group stop signal to send (matching Bubble Tea's
// suspendSupported = false). The terminal dance around it still runs, so
// Ctrl-Z and :suspend cost a release/re-acquire and change nothing else.
//
// The input loop, which used to be the Windows limitation here, is not: it
// parks on request like every other platform's, because it now reads console
// events, which cannot block once they have been counted
// (program_input_windows.go, program_input.go).

// suspendProcess is a no-op on Windows: see the file comment.
func suspendProcess() {}
