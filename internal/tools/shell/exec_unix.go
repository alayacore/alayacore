//go:build !windows

package shell

import (
	"os"
	"os/exec"
	"syscall"
)

// SetDetachFlags sets OS-specific process attributes for detaching from
// the controlling terminal.  On Unix this creates a new session (setsid)
// so that child processes cannot access /dev/tty.
func SetDetachFlags(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true,
	}
}

// OpenDevNull returns a file handle to the null device (/dev/null).
func OpenDevNull() (*os.File, error) {
	return os.Open("/dev/null")
}

// AssignJob is a no-op on Unix.  Process groups are managed via
// SetDetachFlags (setsid) and TerminateProcessGroup (SIGINT/SIGKILL).
func AssignJob(_ *os.Process) *Job {
	return nil
}

// ClearJob is a no-op on Unix.
func ClearJob() {}
