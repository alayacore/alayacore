package tools

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/tools/shell"
)

// defaultCommandTimeout is the maximum time a command may run before being
// terminated.  This prevents interactive programs (vim, nano, etc.) from
// hanging indefinitely when run without a TTY — they start successfully but
// never exit because they're waiting for terminal input that never arrives.
const defaultCommandTimeout = 2 * time.Minute

// executeCommandInput represents the input for the execute_command tool
type executeCommandInput struct {
	Command string `json:"command" jsonschema:"required,description=The command to execute"`
}

// NewExecuteCommandTool creates a tool for executing commands in the
// detected shell (bash/zsh/sh on Unix, PowerShell/cmd on Windows).
func NewExecuteCommandTool() llm.Tool {
	return llm.NewTool(
		"execute_command",
		shell.Detect().PromptFragment,
	).
		WithSchema(llm.GenerateSchema(executeCommandInput{})).
		WithExecute(llm.TypedExecute(executeCommand)).
		Build()
}

func executeCommand(ctx context.Context, args executeCommandInput) (llm.ToolResultOutput, error) {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "." // fallback to current directory
	}

	detectedShell := shell.Detect()

	// Build the command using the detected shell's invocation style.
	//nolint:gosec // G204: Command from user input is intentional for execute_command tool
	cmd := detectedShell.BuildCmd(detectedShell.ResolvedBinary(), args.Command)
	cmd.Dir = cwd

	// Close stdin so commands that read stdin see EOF immediately.
	devNull, err := shell.OpenDevNull()
	if err != nil {
		return llm.NewTextErrorResponse("failed to open null device: " + err.Error()), nil
	}
	defer devNull.Close()
	cmd.Stdin = devNull

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// Set OS-specific detach flags (setsid on Unix, CREATE_NEW_CONSOLE on Windows).
	shell.SetDetachFlags(cmd)

	if err := cmd.Start(); err != nil {
		return llm.NewTextErrorResponse("failed to start command: " + err.Error()), nil
	}

	// Apply a timeout so commands that hang (e.g. interactive programs like
	// vim that start successfully but never exit without a TTY) are eventually
	// killed.  The parent context cancellation is still honored.
	timeoutCtx, timeoutCancel := context.WithTimeout(ctx, defaultCommandTimeout)
	defer timeoutCancel()

	// Wait for command to complete, handling cancellation and timeout
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case <-timeoutCtx.Done():
		if ctx.Err() != nil {
			// Parent context was canceled (user sent :cancel)
			return handleCommandCancellation(cmd, done, &stdout, &stderr), nil
		}
		// Timeout expired — kill the hung process
		return handleCommandTimeout(cmd, done, &stdout, &stderr), nil
	case execErr := <-done:
		return handleCommandCompletion(execErr, &stdout, &stderr), nil
	}
}

func handleCommandCancellation(cmd *exec.Cmd, done chan error, stdout, stderr *bytes.Buffer) llm.ToolResultOutput {
	process := cmd.Process
	if process != nil {
		shell.TerminateProcessGroup(process, done)
	}
	output := formatCommandOutput(stdout, stderr, -1) // canceled, always show labels
	if output != "" {
		return llm.NewTextErrorResponse("Canceled\n" + output)
	}
	return llm.NewTextErrorResponse("Canceled")
}

func handleCommandTimeout(cmd *exec.Cmd, done chan error, stdout, stderr *bytes.Buffer) llm.ToolResultOutput {
	process := cmd.Process
	if process != nil {
		shell.TerminateProcessGroup(process, done)
	}
	output := formatCommandOutput(stdout, stderr, -1)
	if output != "" {
		return llm.NewTextErrorResponse("Timed out\n" + output)
	}
	return llm.NewTextErrorResponse("Timed out")
}

func handleCommandCompletion(execErr error, stdout, stderr *bytes.Buffer) llm.ToolResultOutput {
	exitCode := 0
	if execErr != nil {
		if exitErr, ok := execErr.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		}
	}

	output := formatCommandOutput(stdout, stderr, exitCode)

	if execErr != nil {
		if exitErr, ok := execErr.(*exec.ExitError); ok {
			code := exitErr.ExitCode()
			if code != 0 {
				return llm.NewTextErrorResponse(fmt.Sprintf("Exit Code: %d\n%s", code, output))
			}
			return llm.NewTextErrorResponse(output)
		}
		return llm.NewTextErrorResponse(execErr.Error())
	}

	return llm.NewTextResponse(output)
}

func formatCommandOutput(stdout, stderr *bytes.Buffer, exitCode int) string {
	if exitCode == 0 && stderr.Len() == 0 {
		return stdout.String()
	}

	var output string
	if stdout.Len() > 0 {
		output = "STDOUT:\n" + stdout.String() + "\n"
	}
	if stderr.Len() > 0 {
		output += "STDERR:\n" + stderr.String()
	}
	return output
}
