package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/tools/shell"
)

const maxCommandOutput = 64 * 1024 // 64KB

type executeCommandInput struct {
	Command string `json:"command" jsonschema:"required" jsonschema_desc:"Command to execute"`
}

func NewExecuteCommandTool() llm.Tool {
	return llm.NewTool(
		"execute_command",
		shell.Detect().Description(),
	).
		WithSchema(llm.MustGenerateSchema(executeCommandInput{})).
		WithExecute(llm.TypedExecute(executeCommand)).
		WithExecuteStreaming(llm.TypedExecuteStreaming(executeCommandStreaming)).
		Build()
}

// runCommand builds and runs the shell command, writing stdout to the
// provided writer and stderr to the provided buffer. It returns the exit
// code and the error from cmd.Wait (nil on clean exit).
func runCommand(ctx context.Context, args executeCommandInput, stdout io.Writer, stderr *bytes.Buffer) (int, error) {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}

	detectedShell := shell.Detect()
	timeoutCtx, timeoutCancel := context.WithTimeout(ctx, shell.DefaultCommandTimeout)
	defer timeoutCancel()

	baseCmd := detectedShell.BuildCmd(detectedShell.ResolvedBinary(), args.Command)
	//nolint:gosec // G204: Command from user input is intentional
	cmd := exec.CommandContext(timeoutCtx, baseCmd.Path, baseCmd.Args[1:]...)
	cmd.Dir = cwd

	devNull, err := shell.OpenDevNull()
	if err != nil {
		return 0, fmt.Errorf("failed to open null device: %w", err)
	}
	defer devNull.Close()
	cmd.Stdin = devNull

	cmd.Stdout = stdout
	cmd.Stderr = stderr
	shell.SetDetachFlags(cmd)

	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return shell.SignalProcessGroup(cmd.Process)
	}
	cmd.WaitDelay = 2 * time.Second

	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("failed to start command: %w", err)
	}

	job := shell.AssignJob(cmd.Process)
	if job != nil {
		defer func() {
			shell.ClearJob()
			_ = job.Close()
		}()
	}

	execErr := cmd.Wait()

	exitCode := shell.ExitCodeFromError(execErr)
	if exitCode == -1 && cmd.ProcessState != nil {
		exitCode = shell.ExitCodeFromProcessState(cmd.ProcessState)
	}

	return exitCode, execErr
}

func executeCommand(ctx context.Context, args executeCommandInput) ([]llm.ContentPart, error) {
	var stdout, stderr bytes.Buffer
	exitCode, execErr := runCommand(ctx, args, &stdout, &stderr)

	if ctx.Err() != nil {
		return handleCommandOutput(&stdout, &stderr, exitCode, fmt.Errorf("canceled"))
	}
	if errors.Is(execErr, context.DeadlineExceeded) {
		return handleCommandOutput(&stdout, &stderr, exitCode, fmt.Errorf("timed out"))
	}

	return handleCommandOutput(&stdout, &stderr, exitCode, execErr)
}

// ============================================================================
// Streaming execution (ephemeral Uf preview snapshots)
// ============================================================================

const (
	// previewTickInterval is the minimum interval between preview frames.
	previewTickInterval = 100 * time.Millisecond
	// maxPreviewLen caps the current-line preview snapshot length.
	maxPreviewLen = 4096
)

// streamingWriter captures command stdout for the authoritative result
// (UF) while emitting ephemeral preview snapshots via onDelta (Uf).
// Snapshot semantics: the preview is the current line, or the most
// recently completed line when the current line is empty. '\r' rewrites
// collapse to their latest state. All Write calls come from exec's
// stdout-copy goroutine and complete before cmd.Wait() returns, so no
// locking is needed.
type streamingWriter struct {
	buf       bytes.Buffer    // authoritative full output (UF)
	onDelta   func(string)    // preview callback (Uf); may be nil
	tail      strings.Builder // current line (reset on '\n' and on rewrite after '\r')
	lastLine  string          // most recently completed line (from '\n')
	crPending bool            // last byte was '\r': next printable byte starts a rewrite
	lastSent  string
	lastTick  time.Time
	dirty     bool
}

func newStreamingWriter(onDelta func(string)) *streamingWriter {
	return &streamingWriter{onDelta: onDelta}
}

func (w *streamingWriter) Write(p []byte) (int, error) {
	n, err := w.buf.Write(p)
	if err != nil {
		return n, err
	}

	// Maintain the line snapshot state machine:
	//   '\n'            — line completed; snapshot falls back to lastLine
	//   '\r'            — defer reset: a following '\n' means CRLF (line
	//                     end), otherwise the next printable byte starts
	//                     a progress-bar rewrite (old content discarded)
	for _, b := range p {
		switch b {
		case '\n':
			w.lastLine = w.tail.String()
			if len(w.lastLine) > maxPreviewLen {
				w.lastLine = w.lastLine[len(w.lastLine)-maxPreviewLen:]
			}
			w.tail.Reset()
			w.crPending = false
		case '\r':
			w.crPending = true
		default:
			if w.crPending {
				w.tail.Reset()
				w.crPending = false
			}
			w.tail.WriteByte(b)
		}
	}
	if w.tail.Len() > maxPreviewLen {
		s := w.tail.String()
		w.tail.Reset()
		w.tail.WriteString(s[len(s)-maxPreviewLen:])
	}
	w.dirty = true

	if w.onDelta != nil && time.Since(w.lastTick) >= previewTickInterval {
		w.flushPreview()
	}
	return n, nil
}

// flushPreview emits the latest snapshot if it changed since the last
// emission. Safe to call from the exec goroutine and after cmd.Wait().
func (w *streamingWriter) flushPreview() {
	if !w.dirty || w.onDelta == nil {
		return
	}
	w.dirty = false
	w.lastTick = time.Now()
	text := w.tail.String()
	if text == "" {
		text = w.lastLine
	}
	if text == w.lastSent {
		return
	}
	w.lastSent = text
	w.onDelta(text)
}

func executeCommandStreaming(ctx context.Context, args executeCommandInput, onDelta func(string)) ([]llm.ContentPart, error) {
	var stderr bytes.Buffer
	sw := newStreamingWriter(onDelta)
	exitCode, execErr := runCommand(ctx, args, sw, &stderr)
	sw.flushPreview() // command finished — emit the final snapshot

	if ctx.Err() != nil {
		return handleCommandOutput(&sw.buf, &stderr, exitCode, fmt.Errorf("canceled"))
	}
	if errors.Is(execErr, context.DeadlineExceeded) {
		return handleCommandOutput(&sw.buf, &stderr, exitCode, fmt.Errorf("timed out"))
	}

	return handleCommandOutput(&sw.buf, &stderr, exitCode, execErr)
}

func handleCommandOutput(stdout, stderr *bytes.Buffer, exitCode int, execErr error) ([]llm.ContentPart, error) {
	output := formatCommandOutput(stdout, stderr, exitCode)

	if len(output) > maxCommandOutput {
		return handleLargeCommandOutput(output, exitCode, execErr)
	}

	if execErr != nil {
		if output == "" {
			output = execErr.Error()
		}
		return []llm.ContentPart{&llm.TextPart{Text: output}}, execErr
	}

	if output == "" {
		return []llm.ContentPart{&llm.TextPart{Text: "Command completed successfully (no output)"}}, nil
	}
	return []llm.ContentPart{&llm.TextPart{Text: output}}, nil
}

func handleLargeCommandOutput(output string, exitCode int, execErr error) ([]llm.ContentPart, error) {
	filePath, err := saveToTmpFile(output, "cmd-*.txt")
	if err != nil {
		return nil, fmt.Errorf("failed to save large output: %w", err)
	}

	totalLines := countLines(output)
	totalKB := float64(len(output)) / 1024

	var msg string
	if execErr != nil && exitCode > 0 {
		msg = fmt.Sprintf("Exit Code: %d\n", exitCode)
	}
	msg += fmt.Sprintf(
		"Output (%d lines, %.1fKB) saved to: %s\nUse read_file to access specific sections.",
		totalLines, totalKB, filePath,
	)

	if execErr != nil {
		return []llm.ContentPart{&llm.TextPart{Text: msg}}, execErr
	}
	return []llm.ContentPart{&llm.TextPart{Text: msg}}, nil
}

func countLines(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}

func formatCommandOutput(stdout, stderr *bytes.Buffer, exitCode int) string {
	if exitCode == 0 && stderr.Len() == 0 {
		return stdout.String()
	}

	var output string
	if exitCode > 0 {
		output = fmt.Sprintf("Exit Code: %d\n", exitCode)
	}
	if stdout.Len() > 0 {
		output += "STDOUT:\n" + stdout.String() + "\n"
	}
	if stderr.Len() > 0 {
		output += "STDERR:\n" + stderr.String()
	}
	return output
}
