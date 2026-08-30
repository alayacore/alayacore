//go:build !windows

package tools

import (
	"os"
	"syscall"
)

// readUmask reads the process umask by setting it to 0 and immediately
// restoring the previous value, which the call returns. Callers must invoke
// this at most once — see the caching in currentUmask for why.
func readUmask() os.FileMode {
	old := syscall.Umask(0)
	syscall.Umask(old)
	// mode_t is a 32-bit value on every supported platform, so this
	// int -> os.FileMode (uint32-backed) conversion cannot overflow.
	return os.FileMode(old) //nolint:gosec // G115: mode_t is 32-bit
}
