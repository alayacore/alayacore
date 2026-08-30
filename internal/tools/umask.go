package tools

import (
	"os"
	"sync"
)

// defaultFileMode is the mode a newly created file asks for before the process
// umask narrows it — the same 0644 os.WriteFile used.
const defaultFileMode = os.FileMode(0644)

// umaskOnce caches the process umask after reading it exactly once.
//
// On Unix the umask can only be read by *setting* it (syscall.Umask returns the
// previous value), which is a process-global mutation. Doing that per call
// would be unsafe here because tools run concurrently on agent goroutines: two
// interleaved read-modify-write cycles can leave the process umask at 0,
// making every subsequently created file group- and world-writable. sync.Once
// runs the dance exactly once and to completion, and nothing else in alayacore
// ever touches the umask, so the cached value stays true.
var (
	umaskOnce  sync.Once
	umaskValue os.FileMode
)

// currentUmask returns the process umask, permission bits only. It is 0 on
// Windows, where the concept does not exist.
func currentUmask() os.FileMode {
	umaskOnce.Do(func() {
		umaskValue = readUmask() & os.ModePerm
	})
	return umaskValue
}

// newFilePerm computes the mode for a file that does not exist yet.
//
// The atomic write sets the mode with chmod, which — unlike open(2) — is not
// filtered by the umask. Narrowing here restores that: a literal 0644 would
// hand group and world read access to files whose owner asked for
// private-by-default, exactly backwards for a tool that creates .env files.
func newFilePerm(umask os.FileMode) os.FileMode {
	return defaultFileMode &^ (umask & os.ModePerm)
}
