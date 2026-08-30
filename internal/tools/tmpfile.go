package tools

import (
	"os"
	"sync"
)

var (
	procTmpDir        string
	procTmpDirOnce    sync.Once
	procTmpDirCreated bool // true when MkdirTemp succeeded; prevents Cleanup from removing the system temp root
)

// procTmpDirInit creates a per-process temporary directory under the
// system temp directory (os.TempDir()). Each process gets its own
// uniquely-named directory so concurrently running alayacore instances
// never collide. Uses os.MkdirTemp for atomic, collision-free creation.
func procTmpDirInit() {
	var err error
	procTmpDir, err = os.MkdirTemp(os.TempDir(), "alayacore-*")
	if err != nil {
		// Fall back to the system temp root if we can't create the scoped dir.
		procTmpDir = os.TempDir()
		return
	}
	procTmpDirCreated = true
}

// createProcTmpFile creates a uniquely-named file under this process's
// temporary directory (e.g. /tmp/alayacore-1234567890/) and returns it open
// for writing. Callers stream into it instead of accumulating large content in
// memory; the returned file's Name() is the absolute path reported to the
// model, which can read it back with read_file.
func createProcTmpFile(prefix string) (*os.File, error) {
	procTmpDirOnce.Do(procTmpDirInit)
	return os.CreateTemp(procTmpDir, prefix)
}

// Cleanup removes this process's temporary directory.
// Safe to call even when the scoped directory could not be created
// (only the scoped dir created by MkdirTemp is removed, never the
// system temp root).
func Cleanup() {
	if procTmpDirCreated && procTmpDir != "" {
		os.RemoveAll(procTmpDir)
	}
}
