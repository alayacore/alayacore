// Package debug provides opt-in log files for --debug-log.
package debug

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// ErrNoSlot is returned when every numbered log slot is already taken.
var ErrNoSlot = errors.New("all 1000 debug log slots are in use")

// NewDebugWriter creates a new debug log file in the given directory.
// It tries <baseName>-0.log, -1.log, ..., -999.log with O_EXCL so that
// concurrent processes never collide.
//
// On failure it returns a non-nil error and a nil writer. It never falls
// back to os.Stderr: every caller treats the returned writer as something
// it owns and Close()s, so handing out os.Stderr would close the process's
// standard error stream (fd 2) for the rest of the program's life — after
// which every error message and panic trace is silently lost. Writing
// debug output to stderr would also scribble over the TUI, so stderr is
// not a usable fallback even when the caller does not close it. Callers
// must surface the error instead.
func NewDebugWriter(dir, baseName string) (io.WriteCloser, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("debug log: cannot create directory %q: %w", dir, err)
	}

	for i := 0; i < 1000; i++ {
		logName := filepath.Join(dir, fmt.Sprintf("%s-%d.log", baseName, i))
		f, err := os.OpenFile(logName, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		if err == nil {
			fmt.Fprintf(f, "Debug log started: %s\n", logName)
			return f, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("debug log: cannot create %q: %w", logName, err)
		}
	}

	return nil, fmt.Errorf("debug log: %w in %q", ErrNoSlot, dir)
}
