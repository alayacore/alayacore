//go:build !windows

package shell

import (
	"os"
	"syscall"
	"time"
)

// Job is a no-op type on Unix, returned as nil by AssignJob.
type Job struct{}

// Close is a no-op on Unix.
func (j *Job) Close() error { return nil }

// TerminateProcessGroup sends SIGINT to the process group, waits briefly,
// then sends SIGKILL if the group hasn't exited.
// pid must be the session leader PID (same as process group ID when
// created with Setsid).
func TerminateProcessGroup(process *os.Process, done <-chan error) {
	pid := process.Pid

	//nolint:errcheck // Best effort signal, errors ignored
	_ = syscall.Kill(-pid, syscall.SIGINT)

	// Give the process group 2 seconds to clean up
	select {
	case <-done:
		// Process exited cleanly after SIGINT
	case <-time.After(2 * time.Second):
		// Force kill if still running
		//nolint:errcheck // Best effort kill, errors ignored
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		<-done
	}
}
