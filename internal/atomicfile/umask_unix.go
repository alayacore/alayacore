//go:build !windows

package atomicfile

import (
	"os"
	"sync"
	"syscall"
)

// umaskOnce caches the process umask after reading it exactly once.
//
// On Unix the umask can only be read by *setting* it (syscall.Umask returns the
// previous value), which is a process-global mutation. Reading it per write
// would be unsafe: two interleaved read-modify-write cycles can leave the
// process umask at 0, making every subsequently created file group- and
// world-writable. sync.Once runs the read-and-restore exactly once and to
// completion, and nothing else in alayacore ever touches the umask, so the
// cached value stays true.
var (
	umaskOnce  sync.Once
	umaskValue os.FileMode
)

// currentUmask returns the process umask, permission bits only.
func currentUmask() os.FileMode {
	umaskOnce.Do(func() {
		old := syscall.Umask(0)
		syscall.Umask(old)
		// mode_t is a 32-bit value on every supported platform, so this
		// int -> os.FileMode (uint32-backed) conversion cannot overflow.
		umaskValue = os.FileMode(old) & os.ModePerm //nolint:gosec // G115: mode_t is 32-bit
	})
	return umaskValue
}
