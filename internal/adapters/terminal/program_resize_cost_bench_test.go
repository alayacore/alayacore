//go:build !windows

package terminal

// Cost measurements for the size re-read on the model tick
// (program.go → refreshSize). The question these answer: on Unix, where
// SIGWINCH already reports resizes, what does the extra query cost, and is it
// worth never having a platform-specific "poll only if there is no signal"
// branch in front of it.
//
// Benchmarks, not tests: they gate nothing in CI (`go test` does not run them),
// they exist so the number quoted in refreshSize's comment can be reproduced.
//
// Run with: go test -run '^$' -bench 'Size' ./internal/adapters/terminal/

import (
	"os"
	"testing"

	"golang.org/x/sys/unix"
)

// benchTTY returns a Screen reading its size from a freshly allocated pty
// master, with a known window size set. A terminal is the only thing
// term.GetSize answers for, and a CI runner or a piped `go test` has no
// console of its own.
func benchTTY(b *testing.B) *Screen {
	b.Helper()

	// /dev/ptmx needs no helper process: the master is a real terminal fd for
	// ioctl purposes, which is all Size() asks of it.
	ptmx, err := os.OpenFile("/dev/ptmx", os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		b.Skipf("no pty available: %v", err)
	}
	b.Cleanup(func() { ptmx.Close() })

	if err := unix.IoctlSetWinsize(int(ptmx.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Row: 40, Col: 120}); err != nil {
		b.Skipf("TIOCSWINSZ: %v", err)
	}

	return &Screen{out: ptmx, sizeFile: ptmx, width: 120, height: 40}
}

func BenchmarkScreenSizeUnchanged(b *testing.B) {
	s := benchTTY(b)
	p := &Program{screen: s, msgs: make(chan Msg, 1), width: 120, height: 40}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		// The whole call path a quiet tick takes: query, compare, return.
		if w, h := s.Size(); w == p.width && h == p.height {
			continue
		}
		b.Fatal("size changed under the benchmark")
	}
}

func BenchmarkRefreshSizeUnchanged(b *testing.B) {
	s := benchTTY(b)
	p := &Program{screen: s, msgs: make(chan Msg, 1), width: 120, height: 40}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		p.refreshSize()
	}
}

func BenchmarkRefreshSizeChanged(b *testing.B) {
	s := benchTTY(b)
	// Drained each iteration, so the non-blocking send always succeeds and the
	// measured path is the one that runs when the window was actually dragged.
	p := &Program{screen: s, msgs: make(chan Msg, 1), width: 80, height: 24}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		p.refreshSize()
		select {
		case <-p.msgs:
		default:
		}
	}
}
