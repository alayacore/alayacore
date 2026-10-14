package agent

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// How a persistence failure reads, and the one behavior that depends on its
// shape.
//
// The rule: a layer adds only what the layer above it cannot know. The command
// layer names the operation, the OS error names the path and the reason, and a
// layer in between that repeats the operation's name produces noise that grows
// with the call depth — one failure here used to read
// "save: failed to save session: save: save: open …".

func TestSaveSessionErrorNamesTheOperationOnce(t *testing.T) {
	s := newSaveSession()
	// A path whose directory does not exist: the write is what fails.
	path := filepath.Join(t.TempDir(), "nodir", "session.alaya")

	_, err := s.saveSession(path)
	if err == nil {
		t.Fatal("saveSession() succeeded for a path whose directory does not exist")
	}

	msg := err.Error()
	if !strings.HasPrefix(msg, "failed to save session: ") {
		t.Errorf("message = %q, want it to name the operation", msg)
	}
	if got := strings.Count(msg, "save"); got != 1 {
		t.Errorf("message = %q names the operation %d times, want once", msg, got)
	}
	if !strings.Contains(msg, path) {
		t.Errorf("message = %q, want it to carry the failure's own account of %s", msg, path)
	}
}

// TestSaveErrorNamesNoOperation covers the other caller of the same code: the
// auto-save reports "Auto-save failed: %v", so an operation name baked in below
// it would arrive twice.
func TestSaveErrorNamesNoOperation(t *testing.T) {
	s := newSaveSession()
	path := filepath.Join(t.TempDir(), "nodir", "session.alaya")

	err := s.saveContentToFile(path, nil)
	if err == nil {
		t.Fatal("saveContentToFile() succeeded for a path whose directory does not exist")
	}
	if msg := err.Error(); strings.Contains(msg, "save") {
		t.Errorf("message = %q, want no operation name — its caller supplies one", msg)
	}
}

// TestLoadSessionErrorNamesNoFile is the load side of the same rule: the caller
// says which file it asked for ("could not load session file %q"), so the error
// itself should carry only what went wrong with it.
func TestLoadSessionErrorNamesNoFile(t *testing.T) {
	path := writeSessionFile(t, "bad") // a valid frontmatter over a body that is not a frame

	_, err := loadSession(path)
	if err == nil {
		t.Fatal("loadSession() accepted a body that is not a frame")
	}

	msg := err.Error()
	if strings.Contains(msg, "load: ") {
		t.Errorf("message = %q, want no \"load: \" — the caller names the file", msg)
	}
	if !strings.Contains(msg, "at frame 1") {
		t.Errorf("message = %q, want the position of the frame that failed", msg)
	}
}

// TestLoadSessionMissingFileIsRecognisable pins what the start-fresh path depends
// on: a session file that is simply not there is told apart by errors.Is, so a
// layer that wraps this error must keep the chain (and must not lose it by
// formatting with %v instead of %w).
func TestLoadSessionMissingFileIsRecognisable(t *testing.T) {
	_, err := loadSession(filepath.Join(t.TempDir(), "gone.alaya"))
	if err == nil {
		t.Fatal("loadSession() succeeded for a file that is not there")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("error = %v, want errors.Is(err, os.ErrNotExist) — the silent start-fresh case depends on it", err)
	}
}
