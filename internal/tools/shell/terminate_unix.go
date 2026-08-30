//go:build !windows

package shell

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// Job is a no-op type on Unix, returned as nil by AssignJob.
type Job struct{}

// Close is a no-op on Unix.
func (j *Job) Close() error { return nil }

// AssignJob is a no-op on Unix.  Process groups are managed via
// SetDetachFlags (setsid) and SignalProcessGroup (SIGINT) with
// exec.Cmd.WaitDelay for the follow-up SIGKILL.
func AssignJob(_ *os.Process) *Job {
	return nil
}

// ClearJob is a no-op on Unix.
func ClearJob() {}

// SignalProcessGroup sends SIGINT to the process group and returns
// immediately. The caller should follow up with a stronger signal
// (e.g. SIGKILL) after a grace period if the process hasn't exited.
//
// The wait-and-kill cycle is handled by exec.Cmd.Cancel + WaitDelay:
// SignalProcessGroup sends the initial graceful signal, and Go kills
// the process after WaitDelay if it hasn't exited.
func SignalProcessGroup(process *os.Process) error {
	pid := process.Pid
	return syscall.Kill(-pid, syscall.SIGINT)
}

// ExitCodeFromError extracts the exit code from a cmd.Wait error.
// Returns 0 for nil, 128+signal for processes killed by a signal,
// the exit status for normal non-zero exits, or -1 for
// unrecognized errors.
func ExitCodeFromError(err error) int {
	if err == nil {
		return 0
	}
	// errors.As rather than a type assertion: a wrapped *exec.ExitError must
	// still report the child's exit code instead of falling through to -1.
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if ws, ok := exitErr.ProcessState.Sys().(syscall.WaitStatus); ok {
			if ws.Signaled() {
				return 128 + int(ws.Signal())
			}
			return ws.ExitStatus()
		}
	}
	return -1
}

// ExitCodeFromProcessState extracts the exit code from an OS process state.
// Unlike ProcessState.ExitCode() (which returns -1 for signal-killed
// processes on Unix), this returns the conventional 128+signal value.
// Returns -1 if ps is nil.
func ExitCodeFromProcessState(ps *os.ProcessState) int {
	if ps == nil {
		return -1
	}
	if ws, ok := ps.Sys().(syscall.WaitStatus); ok {
		if ws.Signaled() {
			return 128 + int(ws.Signal())
		}
		return ws.ExitStatus()
	}
	return ps.ExitCode()
}
