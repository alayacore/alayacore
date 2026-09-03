package tools

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/tools/shell"
)

const maxCommandOutput = 64 * 1024 // 64KB

// ExecuteCommandInput is the input of the execute_command tool. It is exported
// because the terminal adapter formats a tool call from it.
type ExecuteCommandInput struct {
	Command string `json:"command" jsonschema:"required" jsonschema_desc:"Command to execute"`
	WorkDir string `json:"workdir" jsonschema_desc:"Directory to run the command in: absolute, or relative to the current working directory. Omit it to run in the current directory. Each call starts in the current directory again - this does not persist between calls."`
}

func NewExecuteCommandTool() llm.Tool {
	return llm.NewTool(
		"execute_command",
		shell.Detect().Description(),
	).
		WithSchema(llm.MustGenerateSchema(ExecuteCommandInput{})).
		WithExecute(llm.TypedExecute(executeCommand)).
		WithExecuteStreaming(llm.TypedExecuteStreaming(executeCommandStreaming)).
		Build()
}

// resolveWorkDir turns the workdir argument into the directory a command runs
// in.
//
// An empty argument means the process's own directory, which is what this tool
// did before the argument existed and what a command that names no directory
// must keep doing. A relative argument is resolved against that same directory,
// because that is how every other path-taking tool reads a path.
//
// The directory has to exist, and has to be one. Left to exec, a bad directory
// surfaces as a failed command carrying the shell's own complaint, which reads
// to the model as though the command ran and broke; checked here, it says what
// actually went wrong and the command never starts.
func resolveWorkDir(workDir string) (string, error) {
	if workDir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			// No answer for the working directory: let the child inherit
			// whatever the process itself has, which is what cmd.Dir = "" does.
			return "", nil
		}
		return cwd, nil
	}

	abs, err := filepath.Abs(workDir)
	if err != nil {
		return "", fmt.Errorf("workdir %q cannot be resolved: %w", workDir, err)
	}

	info, err := os.Stat(abs)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("workdir %q does not exist", workDir)
		}
		return "", fmt.Errorf("workdir %q cannot be used: %w", workDir, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("workdir %q is not a directory", workDir)
	}
	return abs, nil
}

// runCommand builds and runs the shell command, writing stdout and stderr
// to the provided writers. dir is the working directory returned by
// resolveWorkDir. It returns the exit code and the error from cmd.Wait (nil on
// clean exit).
func runCommand(ctx context.Context, args ExecuteCommandInput, dir string, stdout, stderr io.Writer) (int, error) {
	detectedShell := shell.Detect()

	// Only wrap the context with a deadline when a timeout is configured;
	// 0 = no limit (commands run until they finish or the surrounding
	// context is canceled).
	execCtx := ctx
	if shell.DefaultCommandTimeout > 0 {
		var cancel context.CancelFunc
		execCtx, cancel = context.WithTimeout(ctx, shell.DefaultCommandTimeout)
		defer cancel()
	}

	baseCmd := detectedShell.BuildCmd(detectedShell.ResolvedBinary(), args.Command)
	//nolint:gosec // G204: Command from user input is intentional
	cmd := exec.CommandContext(execCtx, baseCmd.Path, baseCmd.Args[1:]...)
	cmd.Dir = dir

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

func executeCommand(ctx context.Context, args ExecuteCommandInput) ([]llm.ContentPart, error) {
	dir, err := resolveWorkDir(args.WorkDir)
	if err != nil {
		return nil, err
	}

	stdout, stderr := newCapture(maxCommandOutput), newCapture(maxCommandOutput)
	defer stdout.Close()
	defer stderr.Close()

	exitCode, execErr := runCommand(ctx, args, dir, stdout, stderr)
	return commandResult(ctx, stdout, stderr, exitCode, execErr)
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

// streamingWriter captures tool output for the authoritative result
// (UF) while emitting ephemeral preview snapshots via onDelta (Uf).
// Shared by execute_command and search_content.
// The preview is a single line: the current line of the most recently
// written stream (stdout or stderr), falling back to the other stream.
// Write/WriteErr are called concurrently from exec's stdout/stderr copy
// goroutines — the shared flush state (dirty, lastTick, lastSent,
// lastWriter, onDelta) is protected by mu. Each authoritative capture is
// written by exactly one of those goroutines, so it needs no lock of its
// own (capture is not safe against two concurrent writers).
type streamingWriter struct {
	mu      sync.Mutex   // guards dirty, lastTick, lastSent, lastWriter, onDelta calls
	buf     *capture     // authoritative stdout (UF), bounded memory
	errBuf  *capture     // authoritative stderr (UF), bounded memory
	onDelta func(string) // preview callback (Uf); may be nil
	out     lineSnapshot // stdout preview snapshot
	err     lineSnapshot // stderr preview snapshot

	lastWriter byte // 0 = none, 1 = stdout, 2 = stderr (most recent)
	lastSent   string
	lastTick   time.Time
	dirty      bool
}

func newStreamingWriter(onDelta func(string)) *streamingWriter {
	return newStreamingWriterWith(onDelta, maxCommandOutput)
}

// newStreamingWriterWith takes the memory budget explicitly. execute_command
// and search_content share this writer but own separate caps
// (maxCommandOutput / maxSearchContentSize); hardcoding one here would let the
// capture spill at a different threshold than the formatter later compares
// against the moment either constant changes.
func newStreamingWriterWith(onDelta func(string), maxBytes int) *streamingWriter {
	return &streamingWriter{
		onDelta: onDelta,
		buf:     newCapture(maxBytes),
		errBuf:  newCapture(maxBytes),
	}
}

// Close releases any spill files the captures created.
func (w *streamingWriter) Close() {
	w.buf.Close()
	w.errBuf.Close()
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

func executeCommandStreaming(ctx context.Context, args ExecuteCommandInput, onDelta func(string)) ([]llm.ContentPart, error) {
	dir, err := resolveWorkDir(args.WorkDir)
	if err != nil {
		return nil, err
	}

	sw := newStreamingWriter(onDelta)
	defer sw.Close()
	exitCode, execErr := runCommand(ctx, args, dir, sw, errWriter{sw})
	sw.flushPreview() // command finished — emit the final snapshot

	return commandResult(ctx, sw.buf, sw.errBuf, exitCode, execErr)
}

// ErrCanceled and ErrTimeout classify why a tool stopped early. Tools used to
// return bare prose ("Canceled", "timed out"), which callers could only match
// with string comparison — and the capitalized variant was already dead
// weight, kept only because history once keyed off it. Sentinels let the agent
// layer branch with errors.Is.
var (
	ErrCanceled = errors.New("canceled")
	ErrTimeout  = errors.New("timed out")
)

// commandResult is the shared tail of the buffered and streaming command
// paths: it classifies a cancellation/timeout, then renders the output.
func commandResult(ctx context.Context, stdout, stderr *capture, exitCode int, execErr error) ([]llm.ContentPart, error) {
	switch {
	case ctx.Err() != nil:
		execErr = fmt.Errorf("%w: %w", ErrCanceled, ctx.Err())
	case errors.Is(execErr, context.DeadlineExceeded):
		execErr = fmt.Errorf("%w: %w", ErrTimeout, context.DeadlineExceeded)
	}
	return handleCommandOutput(stdout, stderr, exitCode, execErr)
}

func handleCommandOutput(stdout, stderr *capture, exitCode int, execErr error) ([]llm.ContentPart, error) {
	// The common case: nothing spilled to disk and the formatted output still
	// fits the budget, so it goes back inline.
	if !stdout.spilled() && !stderr.spilled() {
		if output := formatCommandOutput(stdout, stderr, exitCode); len(output) <= maxCommandOutput {
			return inlineCommandOutput(output, execErr)
		}
	}
	return handleLargeCommandOutput(stdout, stderr, exitCode, execErr)
}

func inlineCommandOutput(output string, execErr error) ([]llm.ContentPart, error) {
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

// handleLargeCommandOutput saves the full output to a file and reports the
// path. Unlike the previous implementation it never materializes the whole
// output: sizes and line counts come from the captures, and the file is
// written by streaming the in-memory prefix followed by the spill.
func handleLargeCommandOutput(stdout, stderr *capture, exitCode int, execErr error) ([]llm.ContentPart, error) {
	filePath, err := saveCommandOutput(stdout, stderr, exitCode)
	if err != nil {
		// No file: fall back to the in-memory prefix rather than discarding a
		// whole command's output because the temp directory is unusable.
		note := fmt.Sprintf("\n\n[Output was %s and could not be saved to a file (%v); the text above is only its first %s.]",
			describeSize(stdout.size()+stderr.size()), err, describeSize(int64(maxCommandOutput)))
		return inlineCommandOutput(formatCommandOutput(stdout, stderr, exitCode)+note, execErr)
	}

	totalLines := stdout.lineTotal() + stderr.lineTotal()

	var msg string
	if execErr != nil && exitCode > 0 {
		msg = fmt.Sprintf("Exit Code: %d\n", exitCode)
	}
	msg += fmt.Sprintf(
		"Output (%d lines, %s) saved to: %s\nUse read_file to access specific sections.",
		totalLines, describeSize(stdout.size()+stderr.size()), filePath,
	)
	if stdout.truncated() || stderr.truncated() {
		msg += "\n[Warning: part of the output could not be written to disk and was dropped.]"
	}

	if execErr != nil {
		return []llm.ContentPart{&llm.TextPart{Text: msg}}, execErr
	}
	return []llm.ContentPart{&llm.TextPart{Text: msg}}, nil
}

// writeCommandOutput is the single definition of the command output layout.
// formatCommandOutput and the saved file must never disagree, so both go
// through here.
//
// includeSpill selects the full stream (for the file) or the in-memory part
// only (for an inline string). Rendering the inline form straight from
// writeOut would copy a spilled stream back into a strings.Builder — on the
// path where saving to disk just failed, which is exactly the disk-full case
// where the spill is largest. That would reintroduce the unbounded allocation
// this code exists to prevent.
func writeCommandOutput(w io.Writer, stdout, stderr *capture, exitCode int, includeSpill bool) error {
	if exitCode == 0 && stderr.size() == 0 {
		return stdout.emit(w, includeSpill)
	}

	if exitCode > 0 {
		if _, err := fmt.Fprintf(w, "Exit Code: %d\n", exitCode); err != nil {
			return err
		}
	}
	if stdout.size() > 0 {
		if _, err := io.WriteString(w, "STDOUT:\n"); err != nil {
			return err
		}
		if err := stdout.emit(w, includeSpill); err != nil {
			return err
		}
		if _, err := io.WriteString(w, "\n"); err != nil {
			return err
		}
	}
	if stderr.size() > 0 {
		if _, err := io.WriteString(w, "STDERR:\n"); err != nil {
			return err
		}
		return stderr.emit(w, includeSpill)
	}
	return nil
}

// saveCommandOutput streams the formatted command output to a file in this
// process's temp directory and returns its path.
func saveCommandOutput(stdout, stderr *capture, exitCode int) (string, error) {
	f, err := createProcTmpFile("cmd-*.txt")
	if err != nil {
		return "", err
	}
	path := f.Name()

	if werr := writeCommandOutput(f, stdout, stderr, exitCode, true); werr != nil {
		f.Close()
		os.Remove(path)
		return "", werr
	}
	if cerr := f.Close(); cerr != nil {
		os.Remove(path)
		return "", cerr
	}
	return path, nil
}

// formatCommandOutput renders the inline (memory-bounded) form of the output.
func formatCommandOutput(stdout, stderr *capture, exitCode int) string {
	var b strings.Builder
	_ = writeCommandOutput(&b, stdout, stderr, exitCode, false) // strings.Builder never fails
	return b.String()
}

// describeSize renders a byte count for tool messages.
func describeSize(b int64) string {
	if b >= 1024*1024 {
		return fmt.Sprintf("%.1fMB", float64(b)/(1024*1024))
	}
	return fmt.Sprintf("%.1fKB", float64(b)/1024)
}
