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

	old := syscall.Umask(0o077)
	_, err := executeWriteFile(context.Background(), WriteFileInput{Path: path, Content: "#!/bin/sh\necho hi\n"})
	syscall.Umask(old)
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
