package tools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/tools/shell"
)

func TestExecuteCommandNormalCompletion(t *testing.T) {
	content, err := executeCommand(context.Background(), ExecuteCommandInput{
		Command: "echo hello",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := extractText(content)
	if text != "hello\n" {
		t.Errorf("expected %q, got %q", "hello\n", text)
	}
}

func TestExecuteCommandExitError(t *testing.T) {
	_, err := executeCommand(context.Background(), ExecuteCommandInput{
		Command: "exit 42",
	})
	if err == nil {
		t.Fatal("expected error for exit 42")
	}
	if err.Error() == "" {
		t.Error("expected non-empty error message")
	}
}

func TestExecuteCommandCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct {
		content []llm.ContentPart
		err     error
	}, 1)
	go func() {
		content, err := executeCommand(ctx, ExecuteCommandInput{
			Command: "sleep 60",
		})
		done <- struct {
			content []llm.ContentPart
			err     error
		}{content, err}
	}()

	time.Sleep(500 * time.Millisecond)
	cancel()

	select {
	case res := <-done:
		if res.err == nil {
			t.Fatal("expected error for canceled command")
		}
		if !strings.HasPrefix(res.err.Error(), "canceled") {
			t.Errorf("expected message to start with 'canceled', got %q", res.err.Error())
		}
		// The reason has to be in the result text, not only in the error: the
		// error is what callers classify, the text is what the model reads.
		if text := extractText(res.content); !strings.Contains(text, "Command canceled.") {
			t.Errorf("the model is not told the command was canceled: %q", text)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("command was not canceled within timeout")
	}
}

func TestExecuteCommandTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		_, err := executeCommand(ctx, ExecuteCommandInput{
			Command: "sleep 60",
		})
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected error for timed out command")
		}
		msg := err.Error()
		if !strings.HasPrefix(msg, "canceled") && !strings.HasPrefix(msg, "timed out") {
			t.Errorf("expected message to start with 'canceled' or 'timed out', got %q", msg)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("command was not terminated within timeout")
	}
}

func TestExecuteCommandConfiguredTimeoutIsErrTimeout(t *testing.T) {
	// The timeout a user actually configures is --command-timeout, which lands
	// on the per-command context inside runCommand — not on the caller's ctx.
	// It must still be classified as ErrTimeout: the exec error alone is a
	// signal kill ("signal: killed"), indistinguishable from an outward kill.
	orig := shell.DefaultCommandTimeout
	defer func() { shell.DefaultCommandTimeout = orig }()
	shell.DefaultCommandTimeout = 300 * time.Millisecond

	content, err := executeCommand(context.Background(), ExecuteCommandInput{
		Command: "sleep 60",
	})
	if err == nil {
		t.Fatal("expected error for a command that outlives --command-timeout")
	}
	if !errors.Is(err, ErrTimeout) {
		t.Errorf("error = %v, want errors.Is(err, ErrTimeout)", err)
	}
	if !strings.HasPrefix(err.Error(), "timed out") {
		t.Errorf("message = %q, want it to start with 'timed out'", err.Error())
	}
	// The classification is not only for Go callers. In real life the killed
	// command leaves an exit status of its own (130 here), so the header the
	// model reads has to carry the reason — the status cannot.
	text := extractText(content)
	if !strings.Contains(text, "Command timed out.") {
		t.Errorf("the model is not told the command timed out: %q", text)
	}
	// The status is whatever the signal-killed process left behind (130 here,
	// 137 if it had to be killed harder), so only its presence is pinned: the
	// reason is added, not substituted.
	if !strings.Contains(text, "Exit Code: ") {
		t.Errorf("the exit status was dropped from the result: %q", text)
	}
}

func TestExecuteCommandNoTimeoutByDefault(t *testing.T) {
	// DefaultCommandTimeout = 0 (no limit) must not impose a zero-duration
	// deadline; a 1s command should complete normally.
	orig := shell.DefaultCommandTimeout
	defer func() { shell.DefaultCommandTimeout = orig }()
	shell.DefaultCommandTimeout = 0

	content, err := executeCommand(context.Background(), ExecuteCommandInput{
		Command: "sleep 1 && echo done",
	})
	if err != nil {
		t.Fatalf("unexpected error with no timeout: %v", err)
	}
	text := extractText(content)
	if text != "done\n" {
		t.Errorf("expected %q, got %q", "done\n", text)
	}
}

func TestExecuteCommandWorkingDir(t *testing.T) {
	tmpDir := t.TempDir()

	// Both halves are checked, and the reason is the pair: Getwd's error was
	// already discarded, so a failure there left originalWd empty and the
	// restore below would have "succeeded" at chdir("") — leaving every later
	// test in this binary running out of tmpDir, which t.TempDir() then deletes.
	// A test that fails for the wrong reason is bad; one that passes from the
	// wrong directory is worse.
	originalWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.Chdir(originalWd); err != nil {
			t.Errorf("restore working directory: %v — later tests in this run inherit it", err)
		}
	}()

	content, err := executeCommand(context.Background(), ExecuteCommandInput{
		Command: "pwd",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := extractText(content)
	if text != tmpDir+"\n" {
		t.Errorf("expected %q, got %q", tmpDir+"\n", text)
	}
}

func TestHandleCommandOutput(t *testing.T) {
	tests := []struct {
		name        string
		stdout      string
		stderr      string
		exitCode    int
		execErr     error
		wantText    string
		wantErr     bool
		wantErrText string // if non-empty, error.Error() must contain this
	}{
		{
			name:     "success with output",
			stdout:   "hello",
			stderr:   "",
			exitCode: 0,
			execErr:  nil,
			wantText: "hello",
			wantErr:  false,
		},
		{
			name:     "success no output",
			stdout:   "",
			stderr:   "",
			exitCode: 0,
			execErr:  nil,
			wantText: "Command completed successfully (no output)",
			wantErr:  false,
		},
		{
			name:     "success with stderr only",
			stdout:   "",
			stderr:   "warning",
			exitCode: 0,
			execErr:  nil,
			wantText: "STDERR:\nwarning",
			wantErr:  false,
		},
		{
			name:     "error exit 1 with stderr",
			stdout:   "",
			stderr:   "not found",
			exitCode: 1,
			execErr:  fmt.Errorf("exit status 1"),
			wantText: "Exit Code: 1\nSTDERR:\nnot found",
			wantErr:  true,
		},
		{
			name:     "error exit 42 no output",
			stdout:   "",
			stderr:   "",
			exitCode: 42,
			execErr:  fmt.Errorf("exit status 42"),
			wantText: "Exit Code: 42\n",
			wantErr:  true,
		},
		{
			name:     "error exit 1 with stdout",
			stdout:   "result",
			stderr:   "",
			exitCode: 1,
			execErr:  fmt.Errorf("exit status 1"),
			wantText: "Exit Code: 1\nSTDOUT:\nresult\n",
			wantErr:  true,
		},
		{
			name:     "canceled with exit code 130",
			stdout:   "",
			stderr:   "",
			exitCode: 130,
			execErr:  fmt.Errorf("%w: %w", ErrCanceled, context.Canceled),
			wantText: "Exit Code: 130\nCommand canceled.\n",
			wantErr:  true,
		},
		{
			name:     "canceled with partial output",
			stdout:   "partial",
			stderr:   "",
			exitCode: 130,
			execErr:  fmt.Errorf("%w: %w", ErrCanceled, context.Canceled),
			wantText: "Exit Code: 130\nCommand canceled.\nSTDOUT:\npartial\n",
			wantErr:  true,
		},
		{
			name:     "timed out no exit code no output",
			stdout:   "",
			stderr:   "",
			exitCode: -1,
			execErr:  fmt.Errorf("%w: %w", ErrTimeout, context.DeadlineExceeded),
			wantText: "Command timed out.\n",
			wantErr:  true,
		},
		{
			name:     "timed out with partial output",
			stdout:   "partial",
			stderr:   "",
			exitCode: -1,
			execErr:  fmt.Errorf("%w: %w", ErrTimeout, context.DeadlineExceeded),
			wantText: "Command timed out.\nSTDOUT:\npartial\n",
			wantErr:  true,
		},
		{
			// The reason must not depend on the exit code: Windows reports a
			// killed command as 1, which on its own is indistinguishable from
			// an ordinary failure. This is the shape a real --command-timeout
			// expiry takes there.
			name:     "timed out with the platform's own exit code",
			stdout:   "",
			stderr:   "",
			exitCode: 1,
			execErr:  fmt.Errorf("%w: %w", ErrTimeout, context.DeadlineExceeded),
			wantText: "Exit Code: 1\nCommand timed out.\n",
			wantErr:  true,
		},
		{
			name:     "unknown error no output",
			stdout:   "",
			stderr:   "",
			exitCode: -1,
			execErr:  fmt.Errorf("something went wrong"),
			wantText: "something went wrong",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdout := newTestCapture(t, tt.stdout)
			stderr := newTestCapture(t, tt.stderr)

			content, err := handleCommandOutput(stdout, stderr, tt.exitCode, tt.execErr)

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tt.wantErrText != "" && !strings.Contains(err.Error(), tt.wantErrText) {
					t.Errorf("error %q does not contain %q", err.Error(), tt.wantErrText)
				}
			} else if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			text := extractText(content)
			if text != tt.wantText {
				t.Errorf("text:\n  expected: %q\n  got:      %q", tt.wantText, text)
			}
		})
	}
}

// TestCommandHeader pins what the header says: the child's own exit status, and
// — because a number cannot say why this tool ended the command — the reason
// the sentinels classify, rendered from the sentinels themselves so that no
// caller has to invent the wording.
func TestCommandHeader(t *testing.T) {
	tests := []struct {
		name     string
		exitCode int
		execErr  error
		want     string
	}{
		{
			name:     "clean exit says nothing",
			exitCode: 0,
			execErr:  nil,
			want:     "",
		},
		{
			name:     "ordinary failure is the exit status alone",
			exitCode: 1,
			execErr:  fmt.Errorf("exit status 1"),
			want:     "Exit Code: 1\n",
		},
		{
			name:     "timeout names the timeout",
			exitCode: 130,
			execErr:  fmt.Errorf("%w: %w", ErrTimeout, context.DeadlineExceeded),
			want:     "Exit Code: 130\nCommand timed out.\n",
		},
		{
			name:     "cancellation names the cancellation",
			exitCode: 130,
			execErr:  fmt.Errorf("%w: %w", ErrCanceled, context.Canceled),
			want:     "Exit Code: 130\nCommand canceled.\n",
		},
		{
			// A signal-killed process has no exit status of its own, but the
			// reason still has to be stated.
			name:     "timeout with no exit status still names the timeout",
			exitCode: -1,
			execErr:  fmt.Errorf("%w: %w", ErrTimeout, context.DeadlineExceeded),
			want:     "Command timed out.\n",
		},
		{
			name:     "an unclassified failure is left to the error text",
			exitCode: -1,
			execErr:  fmt.Errorf("failed to start command: %w", os.ErrNotExist),
			want:     "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := commandHeader(tt.exitCode, tt.execErr); got != tt.want {
				t.Errorf("commandHeader(%d, %v):\n  expected: %q\n  got:      %q", tt.exitCode, tt.execErr, tt.want, got)
			}
		})
	}
}

func TestFormatCommandOutput(t *testing.T) {
	tests := []struct {
		name   string
		stdout string
		stderr string
		header string
		want   string
	}{
		{
			name:   "no header, stdout only",
			stdout: "hello\n",
			stderr: "",
			header: "",
			want:   "hello\n",
		},
		{
			name:   "no header, nothing",
			stdout: "",
			stderr: "",
			header: "",
			want:   "",
		},
		{
			name:   "exit status header with stderr",
			stdout: "",
			stderr: "error\n",
			header: "Exit Code: 1\n",
			want:   "Exit Code: 1\nSTDERR:\nerror\n",
		},
		{
			name:   "exit status header with both streams",
			stdout: "out\n",
			stderr: "err\n",
			header: "Exit Code: 1\n",
			want:   "Exit Code: 1\nSTDOUT:\nout\n\nSTDERR:\nerr\n",
		},
		{
			name:   "no header, both streams",
			stdout: "out\n",
			stderr: "warn\n",
			header: "",
			want:   "STDOUT:\nout\n\nSTDERR:\nwarn\n",
		},
		{
			// No header and no output: the caller falls back to the error text.
			name:   "no header, no output",
			stdout: "",
			stderr: "",
			header: "",
			want:   "",
		},
		{
			// A stop with no header is a stop with nothing to declare — the
			// exit code no longer selects the layout, so the single stream is
			// rendered as it is on success. The failure travels as IsError and
			// as the returned error, which is where a caller looks for it.
			name:   "no header, stdout only, labeled by nothing",
			stdout: "partial\n",
			stderr: "",
			header: "",
			want:   "partial\n",
		},
		{
			// The reason belongs with the exit status, above the streams: the
			// header is the one place that answers "why did it stop".
			name:   "reason header precedes the streams",
			stdout: "partial\n",
			stderr: "",
			header: "Exit Code: 130\nCommand timed out.\n",
			want:   "Exit Code: 130\nCommand timed out.\nSTDOUT:\npartial\n\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdout := newTestCapture(t, tt.stdout)
			stderr := newTestCapture(t, tt.stderr)
			got := formatCommandOutput(stdout, stderr, tt.header)
			if got != tt.want {
				t.Errorf("formatCommandOutput:\n  expected: %q\n  got:      %q", tt.want, got)
			}
		})
	}
}

// Output too large to return inline is read back by the model through
// read_file, so the saved file has to open with the same reason the message
// gives — otherwise the explanation exists only for commands small enough not
// to need the file, which is the wrong way round.
func TestLargeCommandOutputFileCarriesTheStopReason(t *testing.T) {
	stdout := newTestCapture(t, strings.Repeat("L\n", 40000)) // 80KB > 64KB budget
	defer stdout.Close()
	stderr := newTestCapture(t, "")
	defer stderr.Close()

	execErr := fmt.Errorf("%w: %w", ErrTimeout, context.DeadlineExceeded)
	content, err := handleCommandOutput(stdout, stderr, 130, execErr)
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("error = %v, want the stop to stay classified for callers", err)
	}

	msg := extractText(content)
	if !strings.Contains(msg, "Command timed out.") {
		t.Errorf("the message does not say why the command stopped: %q", msg)
	}

	data, readErr := os.ReadFile(savedPathFromMessage(t, msg))
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !strings.HasPrefix(string(data), "Exit Code: 130\nCommand timed out.\n") {
		t.Errorf("the saved file does not open with the header the message stated: %.80q", data)
	}
}

// extractText is a test helper to get the text from a ContentPart slice.
func extractText(content []llm.ContentPart) string {
	for _, p := range content {
		if tp, ok := p.(*llm.TextPart); ok {
			return tp.Text
		}
	}
	return ""
}
