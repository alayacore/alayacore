//go:build windows

package shell

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Job manages a Windows Job Object used to kill an entire process tree.
// When the Job handle is closed (including on process crash), Windows
// automatically terminates all processes assigned to the Job.
type Job struct {
	handle windows.Handle
	mu     sync.Mutex
	closed bool
}

var (
	kernel32                     = windows.NewLazyDLL("kernel32.dll")
	procCreateJobObjectW         = kernel32.NewProc("CreateJobObjectW")
	procSetInformationJobObject  = kernel32.NewProc("SetInformationJobObject")
	procAssignProcessToJobObject = kernel32.NewProc("AssignProcessToJobObject")
	procTerminateJobObject       = kernel32.NewProc("TerminateJobObject")
)

// NewJob creates a Windows Job Object with KILL_ON_JOB_CLOSE set.
// When the returned Job handle is closed (explicitly or on process crash),
// Windows terminates all processes in the Job.
//
// Returns an error if the Job Object cannot be created (e.g. running
// inside a pre-Win8 job that doesn't allow nested jobs).
func NewJob() (*Job, error) {
	handle, _, err := procCreateJobObjectW.Call(
		0, // lpJobAttributes = NULL (default security)
		0, // lpName = NULL (anonymous)
	)
	if handle == 0 {
		return nil, fmt.Errorf("CreateJobObject: %w", err)
	}

	// Configure the Job to kill all child processes when the handle is closed.
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}

	const jobObjectExtendedLimitInformation = 9

	ret, _, err := procSetInformationJobObject.Call(
		handle,
		jobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		unsafe.Sizeof(info),
	)
	if ret == 0 {
		windows.CloseHandle(windows.Handle(handle))
		return nil, fmt.Errorf("SetInformationJobObject: %w", err)
	}

	return &Job{handle: windows.Handle(handle)}, nil
}

// Assign adds a process to the Job by opening a handle to it via its PID.
// All of the process's future children also belong to the Job automatically.
func (j *Job) Assign(pid int) error {
	j.mu.Lock()
	defer j.mu.Unlock()

	if j.closed {
		return fmt.Errorf("job already closed")
	}

	// Open a process handle with the minimum access rights needed for
	// AssignProcessToJobObject: PROCESS_TERMINATE and PROCESS_SET_QUOTA.
	const access = uint32(syscall.PROCESS_TERMINATE | 0x0100) // PROCESS_SET_QUOTA = 0x0100
	h, err := syscall.OpenProcess(access, false, uint32(pid))
	if err != nil {
		return fmt.Errorf("OpenProcess: %w", err)
	}
	defer syscall.CloseHandle(h)

	ret, _, err := procAssignProcessToJobObject.Call(
		uintptr(j.handle),
		uintptr(h),
	)
	if ret == 0 {
		return fmt.Errorf("AssignProcessToJobObject: %w", err)
	}
	return nil
}

// Terminate kills all processes in the Job.
func (j *Job) Terminate() error {
	j.mu.Lock()
	defer j.mu.Unlock()

	if j.closed {
		return nil
	}

	ret, _, err := procTerminateJobObject.Call(
		uintptr(j.handle),
		1, // exit code
	)
	if ret == 0 {
		return fmt.Errorf("TerminateJobObject: %w", err)
	}
	return nil
}

// Close releases the Job handle.  If KILL_ON_JOB_CLOSE is set, Windows
// automatically kills all processes still in the Job.
func (j *Job) Close() error {
	j.mu.Lock()
	defer j.mu.Unlock()

	if j.closed {
		return nil
	}
	j.closed = true
	return windows.CloseHandle(j.handle)
}

// --- per-command Job tracking ---

// currentJob stores the Job Object associated with the running command.
var currentJob struct {
	mu  sync.Mutex
	job *Job
}

// AssignJob creates a Job Object and assigns the process to it.
// Returns the Job for the caller to close after cmd.Wait() returns.
// Returns nil if the Job Object cannot be created (e.g. nested job
// restriction) — the caller should still proceed, as TerminateProcessGroup
// will fall back to taskkill.
func AssignJob(process *os.Process) *Job {
	job, err := NewJob()
	if err != nil {
		return nil
	}

	if err := job.Assign(process.Pid); err != nil {
		_ = job.Close()
		return nil
	}

	currentJob.mu.Lock()
	currentJob.job = job
	currentJob.mu.Unlock()

	return job
}

// ClearJob removes the job reference.  Called after the Job is closed.
func ClearJob() {
	currentJob.mu.Lock()
	currentJob.job = nil
	currentJob.mu.Unlock()
}

// loadJob returns the current command's Job Object (if any).
func loadJob() *Job {
	currentJob.mu.Lock()
	defer currentJob.mu.Unlock()
	return currentJob.job
}

// --- termination ---

// TerminateProcessGroup kills the process tree on Windows.
//
// It first tries the Job Object (kills the entire tree, including
// grandchildren).  If the Job is unavailable (e.g. nested job restriction
// on pre-Win8), it falls back to taskkill /F /T.
//
// The done channel receives the result of cmd.Wait() and is used to
// detect when the process has actually exited.
func TerminateProcessGroup(process *os.Process, done <-chan error) {
	// Try Job Object first (fast, kernel-level, covers entire tree).
	if job := loadJob(); job != nil {
		if err := job.Terminate(); err == nil {
			waitWithTimeout(done, 3*time.Second)
			return
		}
		// Job termination failed; fall through to taskkill.
	}

	// Fallback: taskkill /F /T kills the process tree.
	if killProcessTree(process.Pid) {
		waitWithTimeout(done, 3*time.Second)
		return
	}

	// Last resort: kill only the direct child.
	_ = process.Kill()
	<-done
}

// killProcessTree runs "taskkill /F /T /PID <pid>" to kill an entire
// process tree.  Returns true if taskkill reported success.
func killProcessTree(pid int) bool {
	cmd := exec.Command("taskkill", "/F", "/T", "/PID", strconv.Itoa(pid))
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: 0x08000000, // CREATE_NO_WINDOW — don't flash a console
	}
	err := cmd.Run()
	return err == nil
}

// waitWithTimeout waits for the process to exit (via done) or for the
// timeout to elapse.  If the timeout fires, it falls back to Kill.
func waitWithTimeout(done <-chan error, timeout time.Duration) {
	select {
	case <-done:
		return
	case <-time.After(timeout):
		// Process didn't exit in time; leave it to KILL_ON_JOB_CLOSE
		// or the caller's WaitDelay to clean up.
		<-done
	}
}
