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
	"sync"
	"time"
	"unicode/utf8"

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

// runCommand builds and runs the shell command, writing stdout and stderr
// to the provided writers. It returns the exit code and the error from
// cmd.Wait (nil on clean exit).
func runCommand(ctx context.Context, args executeCommandInput, stdout, stderr io.Writer) (int, error) {
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
	// maxPreviewLen caps each preview snapshot line length.
	maxPreviewLen = 4096
)

// lineSnapshot maintains a current-line preview snapshot from a byte
// stream, honoring '\n' (line completion) and '\r' (progress-bar rewrite,
// deferred so CRLF line endings are not mistaken for rewrites).
type lineSnapshot struct {
	tail      strings.Builder
	lastLine  string
	crPending bool
}

func (ls *lineSnapshot) write(p []byte) {
	for _, b := range p {
		switch b {
		case '\n':
			ls.lastLine = ls.tail.String()
			if len(ls.lastLine) > maxPreviewLen {
				ls.lastLine = truncateAtRuneBoundary(ls.lastLine, maxPreviewLen)
			}
			ls.tail.Reset()
			ls.crPending = false
		case '\r':
			ls.crPending = true
		default:
			if ls.crPending {
				ls.tail.Reset()
				ls.crPending = false
			}
			ls.tail.WriteByte(b)
		}
	}
	if ls.tail.Len() > maxPreviewLen {
		s := ls.tail.String()
		ls.tail.Reset()
		ls.tail.WriteString(truncateAtRuneBoundary(s, maxPreviewLen))
	}
}

// truncateAtRuneBoundary keeps the last maxLen bytes of s, adjusted to a
// UTF-8 rune boundary so the preview never ends on a broken character
// (e.g. progress-bar block glyphs █ split across the cut).
func truncateAtRuneBoundary(s string, maxLen int) string {
	cut := len(s) - maxLen
	for cut < len(s) && !utf8.RuneStart(s[cut]) {
		cut++
	}
	return s[cut:]
}

// text returns the snapshot: the current line, or the most recently
// completed line when the current line is empty.
func (ls *lineSnapshot) text() string {
	text := ls.tail.String()
	if text == "" {
		return ls.lastLine
	}
	return text
}

// streamingWriter captures command output for the authoritative result
// (UF) while emitting ephemeral preview snapshots via onDelta (Uf).
// The preview is a single line: the current line of the most recently
// written stream (stdout or stderr), falling back to the other stream.
// Write/WriteErr are called concurrently from exec's stdout/stderr copy
// goroutines — the shared flush state (dirty, lastTick, lastSent,
// lastWriter, onDelta) is protected by mu. The authoritative buffers are
// single-writer and need no lock.
type streamingWriter struct {
	mu         sync.Mutex   // guards dirty, lastTick, lastSent, lastWriter, onDelta calls
	buf        bytes.Buffer // authoritative stdout (UF)
	errBuf     bytes.Buffer // authoritative stderr (UF)
	onDelta    func(string) // preview callback (Uf); may be nil
	out        lineSnapshot // stdout preview snapshot
	err        lineSnapshot // stderr preview snapshot
	lastWriter byte         // 0 = none, 1 = stdout, 2 = stderr (most recent)
	lastSent   string
	lastTick   time.Time
	dirty      bool
}

func newStreamingWriter(onDelta func(string)) *streamingWriter {
	return &streamingWriter{onDelta: onDelta}
}

func (w *streamingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n, err := w.buf.Write(p)
	if err != nil {
		return n, err
	}
	w.out.write(p)
	w.lastWriter = 1
	w.dirty = true
	if w.onDelta != nil && time.Since(w.lastTick) >= previewTickInterval {
		w.flushPreviewLocked()
	}
	return n, nil
}

// WriteErr captures stderr: authoritative bytes go to errBuf, while the
// current line feeds the stderr preview snapshot.
func (w *streamingWriter) WriteErr(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n, err := w.errBuf.Write(p)
	if err != nil {
		return n, err
	}
	w.err.write(p)
	w.lastWriter = 2
	w.dirty = true
	if w.onDelta != nil && time.Since(w.lastTick) >= previewTickInterval {
		w.flushPreviewLocked()
	}
	return n, nil
}

// errWriter adapts streamingWriter to io.Writer for cmd.Stderr.
type errWriter struct{ w *streamingWriter }

func (ew errWriter) Write(p []byte) (int, error) { return ew.w.WriteErr(p) }

// flushPreview emits the latest combined snapshot if it changed since
// the last emission. Safe to call after cmd.Wait() (no concurrent writers).
func (w *streamingWriter) flushPreview() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.flushPreviewLocked()
}

// flushPreviewLocked emits the latest combined snapshot. Must be called
// with mu held.
func (w *streamingWriter) flushPreviewLocked() {
	if !w.dirty || w.onDelta == nil {
		return
	}
	w.dirty = false
	w.lastTick = time.Now()
	text := w.combinedPreview()
	if text == w.lastSent {
		return
	}
	w.lastSent = text
	w.onDelta(text)
}

// combinedPreview returns the preview line of the most recently written
// stream (stdout or stderr), falling back to the other stream when the
// preferred one has no content yet.
func (w *streamingWriter) combinedPreview() string {
	if w.lastWriter == 2 {
		if text := w.err.text(); text != "" {
			return text
		}
		return w.out.text()
	}
	if text := w.out.text(); text != "" {
		return text
	}
	return w.err.text()
}

func executeCommandStreaming(ctx context.Context, args executeCommandInput, onDelta func(string)) ([]llm.ContentPart, error) {
	sw := newStreamingWriter(onDelta)
	exitCode, execErr := runCommand(ctx, args, sw, errWriter{sw})
	sw.flushPreview() // command finished — emit the final snapshot

	if ctx.Err() != nil {
		return handleCommandOutput(&sw.buf, &sw.errBuf, exitCode, fmt.Errorf("canceled"))
	}
	if errors.Is(execErr, context.DeadlineExceeded) {
		return handleCommandOutput(&sw.buf, &sw.errBuf, exitCode, fmt.Errorf("timed out"))
	}

	return handleCommandOutput(&sw.buf, &sw.errBuf, exitCode, execErr)
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
