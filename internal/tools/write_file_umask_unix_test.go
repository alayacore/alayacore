//go:build !windows

package tools

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// An existing file keeps its exact mode regardless of the umask: re-narrowing
// would strip bits the user deliberately set (a 0755 script going to 0700
// under a 0077 umask would stop running).
func TestWriteFileExistingKeepsExactModeUnderUmask(t *testing.T) {
	path := filepath.Join(t.TempDir(), "script.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Pin the cached umask BEFORE changing the process-global one. currentUmask
	// reads the real value exactly once (sync.Once), and a read that happened
	// inside this window would latch 0077 for the rest of this test binary —
	// every later "a new file is 0644 narrowed by the umask" assertion would
	// then see 0600, depending on test order.
	_ = currentUmask()

	old := syscall.Umask(0o077)
	defer syscall.Umask(old) // defer, not a bare call: a Fatal or panic must not leave 0077 behind
	_, err := executeWriteFile(context.Background(), WriteFileInput{Path: path, Content: "#!/bin/sh\necho hi\n"})
	if err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o755 {
		t.Errorf("existing file mode = %o, want 0755 (exact preservation, no umask narrowing)", got)
	}
}
