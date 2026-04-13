package tools

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/alayacore/alayacore/internal/llm"
)

// shellInput represents the input for the shell tool
type shellInput struct {
	Command string `json:"command" jsonschema:"required,description=The shell command to execute"`
}

// shellPath is the resolved path to the shell binary (bash if available, sh otherwise).
// Resolved once at init time.
var shellPath string

var shellOnce sync.Once

func resolveShellPath() string {
	shellOnce.Do(func() {
		// Prefer bash — LLMs naturally write bash syntax (brace expansion,
		// [[ ]], arrays, etc.) and this avoids compatibility surprises.
		for _, candidate := range []string{"/bin/bash", "/usr/bin/bash", "/bin/sh"} {
			if _, err := os.Stat(candidate); err == nil {
				shellPath = candidate
				return
			}
		}
		// Last resort — the OS should always have /bin/sh, but if not,
		// "sh" relies on $PATH which is better than nothing.
		shellPath = "sh"
	})
	return shellPath
}

// NewShellTool creates a shell tool for executing commands via bash (or sh as fallback).
func NewShellTool() llm.Tool {
	return llm.NewTool(
		"shell",
		`Execute a shell command.

Rules:
- Bash syntax is available (brace expansion, [[ ]], arrays, etc.)
- Prefer simple, standard commands over complex pipelines
- Quote filenames with spaces or special characters
- Check command output for errors before proceeding
- Clean up temporary files when done
- Commands run in a detached session with no controlling terminal and stdin closed. Interactive programs (sudo, ssh, etc.) that require a TTY or terminal input will fail immediately.`,
	).
		WithSchema(llm.GenerateSchema(shellInput{})).
		WithExecute(llm.TypedExecute(executeShell)).
		Build()
}

func executeShell(ctx context.Context, args shellInput) (llm.ToolResultOutput, error) {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "." // fallback to current directory
	}

	//nolint:gosec // G204: Command from user input is intentional for shell tool
	cmd := exec.CommandContext(ctx, resolveShellPath(), "-c", args.Command)
	cmd.Dir = cwd

	// Close stdin so commands that read stdin see EOF immediately.
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		return llm.NewTextErrorResponse("failed to open /dev/null: " + err.Error()), nil
	}
	defer devNull.Close()
	cmd.Stdin = devNull

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// Create a new session and detach from the controlling terminal.
	// This prevents child processes from accessing /dev/tty, so programs
	// like sudo that open the terminal device directly for password input
	// cannot scribble on the user's display or hang waiting for input.
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true,
	}

	if err := cmd.Start(); err != nil {
		return llm.NewTextErrorResponse("failed to start command: " + err.Error()), nil
	}

	// Wait for command to complete, handling cancellation
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case <-ctx.Done():
		return handleShellCancellation(cmd, done, &stdout, &stderr), nil
	case execErr := <-done:
		return handleShellCompletion(execErr, &stdout, &stderr), nil
	}
}

func handleShellCancellation(cmd *exec.Cmd, done chan error, stdout, stderr *bytes.Buffer) llm.ToolResultOutput {
	process := cmd.Process
	if process != nil {
		terminateProcessGroup(process, done)
	}
	output := combineShellOutput(stdout, stderr)
	if output != "" {
		return llm.NewTextErrorResponse("canceled: " + output)
	}
	return llm.NewTextErrorResponse("canceled")
}

func terminateProcessGroup(process *os.Process, done chan error) {
	// With Setsid: true, the child is a session leader and its PID equals
	// the session ID. Sending a signal to -PID targets every process in
	// that session (the shell and all its descendants).
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

func handleShellCompletion(execErr error, stdout, stderr *bytes.Buffer) llm.ToolResultOutput {
	output := combineShellOutput(stdout, stderr)

	if execErr != nil {
		if exitErr, ok := execErr.(*exec.ExitError); ok {
			return llm.NewTextErrorResponse(fmt.Sprintf("[%d] %s", exitErr.ExitCode(), output))
		}
		return llm.NewTextErrorResponse(execErr.Error())
	}

	return llm.NewTextResponse(output)
}

func combineShellOutput(stdout, stderr *bytes.Buffer) string {
	output := stdout.String()
	if stderr.Len() > 0 {
		if output != "" {
			output += "\n"
		}
		output += stderr.String()
	}
	return output
}
