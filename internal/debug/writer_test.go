package debug

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

func TestNewDebugWriter_AllSlotsUsed(t *testing.T) {
	dir := t.TempDir()
	for i := range 1000 {
		name := filepath.Join(dir, fmt.Sprintf("alayacore-debug-api-%d.log", i))
		if err := os.WriteFile(name, nil, 0644); err != nil {
			t.Fatal(err)
		}
	}

	w, err := NewDebugWriter(dir, "alayacore-debug-api")
	if w != nil {
		t.Fatal("expected nil writer when every slot is taken")
	}
	if !errors.Is(err, ErrNoSlot) {
		t.Fatalf("error = %v, want ErrNoSlot", err)
	}
	if err := stderrWritable(); err != nil {
		t.Fatalf("stderr must be untouched: %v", err)
	}
}
