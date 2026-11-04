// Package debug provides opt-in log files for --debug-log.
package debug

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// ErrNoSlot is returned when no log could be created even after the oldest one
// was rotated out: another process is claiming slots as fast as they are freed,
// or the directory cannot be read.
var ErrNoSlot = errors.New("all debug log slots are in use")

const (
	// maxSlots bounds how many logs one base name may have in a directory.
	//
	// It is a ceiling, not an interval: a slot is reused only when every one is
	// taken, so an ordinary machine keeps the handful it has written and the
	// bound only matters to a long-lived debug directory.
	maxSlots = 1000

	// logSuffix is what makes a file one of ours (see removeOldestLog).
	logSuffix = ".log"
)

// NewDebugWriter creates a new debug log file in the given directory.
// It tries <baseName>-0.log, -1.log, ... with O_EXCL so that concurrent
// processes never collide, and when every slot is taken it rotates out the
// oldest log rather than failing.
//
// Rotation is the second half of the same promise. Refusing to log was a startup
// failure — NewHTTPClient turns this error into app.Setup's, so the program
// would not run at all — reached by nothing more than a debugging habit: 1000
// runs with --debug-log, no cleanup in between, and the next start reported that
// every slot was in use. A diagnostic the user asked for must not be able to stop
// the program it diagnoses; the oldest log is scratch by definition, and its slot
// is the one reused.
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
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("debug log: cannot create directory %q: %w", dir, err)
	}

	// Two passes over the range: the first takes the lowest free slot, which is
	// what a directory holding a few logs gets; the second frees the oldest log
	// and looks again.
	for pass := 0; pass < 2; pass++ {
		if pass == 1 {
			if err := removeOldestLog(dir, baseName); err != nil {
				return nil, fmt.Errorf("debug log: cannot make room for a new log in %q: %w", dir, err)
			}
		}
		for i := 0; i < maxSlots; i++ {
			logName := filepath.Join(dir, fmt.Sprintf("%s-%d%s", baseName, i, logSuffix))
			f, err := os.OpenFile(logName, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
			if err == nil {
				fmt.Fprintf(f, "Debug log started: %s\n", logName)
				return f, nil
			}
			if !errors.Is(err, os.ErrExist) {
				return nil, fmt.Errorf("debug log: cannot create %q: %w", logName, err)
			}
		}
	}

	return nil, fmt.Errorf("debug log: %w in %q", ErrNoSlot, dir)
}

// removeOldestLog deletes this base name's least recently modified log, freeing
// its slot for the caller.
//
// Only a file with the exact "<baseName>-<digits>.log" shape is eligible, so a
// directory shared with logs from another program is never mined for candidates.
// The modification time decides, not the slot number: slot 3 written a year ago
// is older than slot 900 written this morning, and the number alone says nothing
// about which one the user still needs.
func removeOldestLog(dir, baseName string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}

	prefix, suffix := baseName+"-", logSuffix
	oldest := ""
	var oldestMod time.Time
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, suffix) {
			continue
		}
		if _, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(name, prefix), suffix)); err != nil {
			continue // not a slot this package wrote
		}
		info, err := entry.Info()
		if err != nil {
			continue // vanished between ReadDir and Info; nothing to rotate
		}
		if oldest == "" || info.ModTime().Before(oldestMod) {
			oldest, oldestMod = name, info.ModTime()
		}
	}

	if oldest == "" {
		return ErrNoSlot
	}
	return os.Remove(filepath.Join(dir, oldest))
}
