package debug

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// stderrWritable reports whether fd 2 still accepts writes. Used to prove the
// historical bug — NewDebugWriter handing out os.Stderr, which callers then
// Close()d, killing the process's stderr for good — cannot come back.
func stderrWritable() error {
	_, err := fmt.Fprintf(os.Stderr, "")
	return err
}

func TestNewDebugWriter_CreatesScopedFile(t *testing.T) {
	dir := t.TempDir()

	w, err := NewDebugWriter(dir, "alayacore-debug-api")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if w == nil {
		t.Fatal("nil writer with nil error")
	}

	if _, err := fmt.Fprintf(w, "hello\n"); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "alayacore-debug-api-0.log"))
	if err != nil {
		t.Fatalf("expected slot 0 to be created: %v", err)
	}
	if !strings.Contains(string(data), "hello") {
		t.Errorf("log content = %q, want it to contain %q", data, "hello")
	}
}

// A failed call must never hand out os.Stderr: callers own what they get and
// close it, so returning the process's stderr fd would break all later error
// reporting (and panic traces) for the rest of the program's life.
func TestNewDebugWriter_UnwritableDirErrorsWithoutTouchingStderr(t *testing.T) {
	if err := stderrWritable(); err != nil {
		t.Fatalf("test precondition: stderr not writable: %v", err)
	}

	// A path whose parent is an ordinary file fails MkdirAll with ENOTDIR.
	// Unlike a chmod-based approach this fails for root too, so the test is
	// not sensitive to who runs it.
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, nil, 0644); err != nil {
		t.Fatal(err)
	}
	unwritable := filepath.Join(blocker, "deep")

	w, err := NewDebugWriter(unwritable, "alayacore-debug-mcp")
	if err == nil {
		t.Fatalf("expected error for unwritable dir %q", unwritable)
	}
	if w != nil {
		t.Fatalf("expected nil writer alongside error, got %T", w)
	}
	if w == interface{}(os.Stderr) {
		t.Fatal("regression: returned os.Stderr, which callers Close()")
	}
	if !strings.Contains(err.Error(), "debug log") {
		t.Errorf("error should name the subsystem, got %q", err)
	}

	// The clincher: stderr must still work after the failed call.
	if err := stderrWritable(); err != nil {
		t.Fatalf("regression: stderr broke after failed NewDebugWriter: %v", err)
	}
}

func TestNewDebugWriter_PicksNextFreeSlot(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "alayacore-debug-mcp-0.log"), nil, 0644); err != nil {
		t.Fatal(err)
	}

	w, err := NewDebugWriter(dir, "alayacore-debug-mcp")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer w.Close()

	if _, err := os.Stat(filepath.Join(dir, "alayacore-debug-mcp-1.log")); err != nil {
		t.Errorf("expected slot 1 to be used, stat failed: %v", err)
	}
}

// A full slot range is not a reason to refuse to run. Logging is the diagnostic
// the user asked for; a debugging habit of 1000 runs used to turn the next start
// into app.Setup's error, which is a debugging aid stopping the program it was
// meant to diagnose.
//
// The oldest log pays for the new one, decided by modification time rather than
// by slot number: the number only records when a file was first created, and a
// slot-3 log from last year is exactly the one nobody needs.
func TestNewDebugWriter_ReusesTheOldestSlotWhenAllAreTaken(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	oldest := 999

	for i := range maxSlots {
		name := filepath.Join(dir, fmt.Sprintf("alayacore-debug-api-%d.log", i))
		if err := os.WriteFile(name, []byte("previous run\n"), 0644); err != nil {
			t.Fatal(err)
		}
		mod := now.Add(time.Duration(i) * time.Minute)
		if i == oldest {
			mod = now.Add(-time.Hour)
		}
		if err := os.Chtimes(name, mod, mod); err != nil {
			t.Fatal(err)
		}
	}

	w, err := NewDebugWriter(dir, "alayacore-debug-api")
	if err != nil {
		t.Fatalf("every slot taken must not be an error: %v", err)
	}
	defer w.Close()

	data, err := os.ReadFile(filepath.Join(dir, fmt.Sprintf("alayacore-debug-api-%d.log", oldest)))
	if err != nil {
		t.Fatalf("the freed slot should hold the new log: %v", err)
	}
	if !strings.Contains(string(data), "Debug log started") {
		t.Errorf("oldest slot = %q, want the new log's header", data)
	}

	// Nothing else may be destroyed to make room, and the directory must not
	// grow past the ceiling the reuse exists to respect.
	if data, err := os.ReadFile(filepath.Join(dir, "alayacore-debug-api-0.log")); err != nil || !strings.Contains(string(data), "previous run") {
		t.Errorf("slot 0 was touched: %q (err %v)", data, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != maxSlots {
		t.Errorf("%d logs after reuse, want %d", len(entries), maxSlots)
	}

	if err := stderrWritable(); err != nil {
		t.Fatalf("stderr must be untouched: %v", err)
	}
}

// Rotation only ever considers this base name's slots: a shared directory
// belongs to whatever else writes there too, and "the oldest file" in it may be
// someone's data.
func TestRemoveOldestLog_IgnoresFilesItDidNotWrite(t *testing.T) {
	dir := t.TempDir()
	foreign := []string{
		"other-debug-api-0.log",        // another base name
		"alayacore-debug-api-notes.md", // wrong suffix
		"alayacore-debug-api-x.log",    // slot is not a number
		"alayacore-debug-api.log",      // no slot at all
	}
	for _, name := range foreign {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0644); err != nil {
			t.Fatal(err)
		}
	}

	err := removeOldestLog(dir, "alayacore-debug-api")
	if !errors.Is(err, ErrNoSlot) {
		t.Fatalf("removeOldestLog() = %v, want ErrNoSlot", err)
	}
	for _, name := range foreign {
		if _, statErr := os.Stat(filepath.Join(dir, name)); statErr != nil {
			t.Errorf("rotation removed %q, which this package did not write: %v", name, statErr)
		}
	}
}
