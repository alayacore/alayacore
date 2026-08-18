//go:build !windows

package terminal

// Unix signal watcher: SIGINT/SIGTERM quit the program, SIGWINCH resizes.

import (
	"os"
	"os/signal"
	"syscall"
)

// signalChannel returns a channel with the program's lifecycle signals
// registered: SIGINT/SIGTERM quit, SIGWINCH resizes.
func (p *Program) signalChannel() chan os.Signal {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM, syscall.SIGWINCH)
	return sig
}

// resizeSignal returns the signal that indicates a terminal resize.
func (p *Program) resizeSignal() os.Signal { return syscall.SIGWINCH }
