//go:build windows

package shell

import (
	"os"
	"os/exec"
	"syscall"
)

// SetDetachFlags sets OS-specific process attributes for Windows.
// On Windows we create the process without a visible console window
// and assign it to a Job Object that kills the entire process tree
// when the Job handle is closed or TerminateJobObject is called.
func SetDetachFlags(cmd *exec.Cmd) {
	// CREATE_NO_WINDOW (0x08000000) — the process runs without creating
	// a visible console window.  This prevents a command prompt window
	// from flashing on screen each time a command is executed.
	//
	// CREATE_NEW_PROCESS_GROUP (0x00000200) — the child process is the
	// root of a new process group.  This prevents it from receiving
	// console Ctrl+C events sent to the parent's console.
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: 0x08000000 | 0x00000200,
	}
}

// OpenDevNull returns a file handle to the null device (NUL on Windows).
func OpenDevNull() (*os.File, error) {
	return os.Open("NUL")
}
