//go:build windows

package terminal

// Windows signal watcher: SIGINT/SIGTERM quit the program. There is no
// SIGWINCH on Windows — and no resize signal of any kind, including under a
// pseudo console — so terminal-size changes come from Program.refreshSize,
// which re-reads the size on every model tick.

import (
	"os"
	"os/signal"
	"syscall"
)

// signalChannel returns a channel with the program's lifecycle signals
// registered: SIGINT/SIGTERM quit.
func (p *Program) signalChannel() chan os.Signal {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	return sig
}

// resizeSignal returns nil on Windows: no resize signal is registered, so
// every signal on the channel takes the quit path.
func (p *Program) resizeSignal() os.Signal { return nil }
