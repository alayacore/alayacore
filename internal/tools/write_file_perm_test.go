package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// The atomic write sets the mode with chmod, and chmod is NOT filtered by the
// umask the way open(2) is. Applying a literal 0644 would therefore silently
// WIDEN permissions for anyone running under a restrictive umask — backwards
// for a tool that creates .env-style files. These tests are cross-platform:
// currentUmask() is 0 on Windows, where no narrowing applies anyway.

func TestNewFilePermAppliesUmask(t *testing.T) {
	tests := []struct {
		umask os.FileMode
		want  os.FileMode
	}{
		{0o000, 0o644},
		{0o022, 0o644}, // the common default: unchanged
		{0o027, 0o640},
		{0o077, 0o600}, // must NOT become 0644
		{0o007, 0o640},
		{0o777, 0o000},
	}
	for _, tt := range tests {
		got := newFilePerm(tt.umask)
		if got != tt.want {
			t.Errorf("newFilePerm(umask %o) = %o, want %o", tt.umask, got, tt.want)
		}
	}
}

// write_file must actually use that arithmetic, not a literal 0644. Asserted
// against currentUmask() rather than a umask set here: currentUmask caches the
// value seen the first time it is read, so a test cannot assume the process
// umask it sets is the one production will consult. What must hold — and what
// a regression breaks whenever the cached umask is non-zero — is that the
// produced mode equals 0644 narrowed by that umask.
func TestWriteFileNewFileMatchesNarrowedDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "new.txt")
	if _, err := executeWriteFile(context.Background(), WriteFileInput{Path: path, Content: "hello"}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	want := newFilePerm(currentUmask())
	if got := info.Mode().Perm(); got != want {
		t.Errorf("new file mode = %o, want %o (0644 narrowed by cached umask %o)", got, want, currentUmask())
	}
}

// Wiring test: force the cached umask and confirm write_file actually applies
// it. Deterministic because currentUmask caches after the first read, so the
// only honest way to exercise the restrictive-umask path without racing the
// process-global syscall is to set the cached value and assert behavior.
func TestWriteFileAppliesCachedUmaskToNewFiles(t *testing.T) {
	saved := currentUmask() // trigger the real read, then override the cache
	defer func() { umaskValue = saved }()

	tests := []struct {
		umask os.FileMode
		want  os.FileMode
	}{
		{0o022, 0o644},
		{0o077, 0o600},
	}
	for _, tt := range tests {
		umaskValue = tt.umask
		path := filepath.Join(t.TempDir(), "new.txt")
		if _, err := executeWriteFile(context.Background(), WriteFileInput{Path: path, Content: "x"}); err != nil {
			t.Fatalf("umask %o: %v", tt.umask, err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != tt.want {
			t.Errorf("cached umask %o: new file mode = %o, want %o", tt.umask, got, tt.want)
		}
	}
}
