//go:build !windows

package tools

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// TestWriteFileNewFileMode verifies a brand-new file is created with 0644
// narrowed by the process umask — not a fixed 0600, which made every new
// file unreadable to other users and never executable.
func TestWriteFileNewFileMode(t *testing.T) {
	// Capture the process umask (briefly setting it to 0 to read it, then
	// restoring) so the expected mode can be computed deterministically.
	old := syscall.Umask(0)
	syscall.Umask(old)
	umask := os.FileMode(old)

	path := filepath.Join(t.TempDir(), "new.txt")
	if _, err := executeWriteFile(context.Background(), WriteFileInput{Path: path, Content: "hello"}); err != nil {
		t.Fatalf("executeWriteFile: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	want := os.FileMode(0644) &^ umask
	if perm := info.Mode().Perm(); perm != want {
		t.Fatalf("new file mode = %o, want %o (0644 narrowed by umask %o)", perm, want, umask)
	}
}
