//go:build !windows

package terminal

// Unix suspend support (module 5, S2): SIGTSTP/SIGCONT for Ctrl-Z.
//
// The other half of a suspension — stopping the input loop so the foreground
// process gets every keystroke — is the same on every platform and lives in
// program_input.go. On Unix it works because the loop polls the TTY with a
// bounded wait (program_input_unix.go) and so is always between reads within
// inputPollTimeout.

import (
	"os"
	"os/signal"
	"syscall"
)

// suspendProcess sends SIGTSTP to the entire process group (stopping the
// program like a regular Ctrl-Z does in cooked mode) and blocks until SIGCONT
// arrives (the user runs `fg`).
func suspendProcess() {
	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGCONT)
	defer signal.Stop(c)
	_ = syscall.Kill(0, syscall.SIGTSTP)
	<-c // blocks until a CONT happens
}
