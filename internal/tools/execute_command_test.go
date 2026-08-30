package tools

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/tools/shell"
)

func TestExecuteCommandNormalCompletion(t *testing.T) {
	content, err := executeCommand(context.Background(), executeCommandInput{
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
	_, err := executeCommand(context.Background(), executeCommandInput{
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

	done := make(chan error, 1)
	go func() {
		_, err := executeCommand(ctx, executeCommandInput{
			Command: "sleep 60",
		})
		done <- err
	}()

	time.Sleep(500 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected error for canceled command")
		}
		if !strings.HasPrefix(err.Error(), "canceled") {
			t.Errorf("expected message to start with 'canceled', got %q", err.Error())
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
		_, err := executeCommand(ctx, executeCommandInput{
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

func TestExecuteCommandNoTimeoutByDefault(t *testing.T) {
	// DefaultCommandTimeout = 0 (no limit) must not impose a zero-duration
	// deadline; a 1s command should complete normally.
	orig := shell.DefaultCommandTimeout
	defer func() { shell.DefaultCommandTimeout = orig }()
	shell.DefaultCommandTimeout = 0

	content, err := executeCommand(context.Background(), executeCommandInput{
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

	originalWd, _ := os.Getwd()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(originalWd)

	content, err := executeCommand(context.Background(), executeCommandInput{
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
			execErr:  fmt.Errorf("canceled"),
			wantText: "Exit Code: 130\n",
			wantErr:  true,
		},
		{
			name:     "canceled with partial output",
			stdout:   "partial",
			stderr:   "",
			exitCode: 130,
			execErr:  fmt.Errorf("canceled"),
			wantText: "Exit Code: 130\nSTDOUT:\npartial\n",
			wantErr:  true,
		},
		{
			name:     "timed out no exit code no output",
			stdout:   "",
			stderr:   "",
			exitCode: -1,
			execErr:  fmt.Errorf("timed out"),
			wantText: "timed out",
			wantErr:  true,
		},
		{
			name:     "timed out with partial output",
			stdout:   "partial",
			stderr:   "",
			exitCode: -1,
			execErr:  fmt.Errorf("timed out"),
			wantText: "STDOUT:\npartial\n",
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

func TestFormatCommandOutput(t *testing.T) {
	tests := []struct {
		name     string
		stdout   string
		stderr   string
		exitCode int
		want     string
	}{
		{
			name:     "exit 0 stdout only",
			stdout:   "hello\n",
			stderr:   "",
			exitCode: 0,
			want:     "hello\n",
		},
		{
			name:     "exit 0 empty both",
			stdout:   "",
			stderr:   "",
			exitCode: 0,
			want:     "",
		},
		{
			name:     "exit 1 with stderr",
			stdout:   "",
			stderr:   "error\n",
			exitCode: 1,
			want:     "Exit Code: 1\nSTDERR:\nerror\n",
		},
		{
			name:     "exit 1 stdout and stderr",
			stdout:   "out\n",
			stderr:   "err\n",
			exitCode: 1,
			want:     "Exit Code: 1\nSTDOUT:\nout\n\nSTDERR:\nerr\n",
		},
		{
			name:     "exit 0 with stderr",
			stdout:   "out\n",
			stderr:   "warn\n",
			exitCode: 0,
			want:     "STDOUT:\nout\n\nSTDERR:\nwarn\n",
		},
		{
			name:     "exit -1 no output",
			stdout:   "",
			stderr:   "",
			exitCode: -1,
			want:     "",
		},
		{
			name:     "exit -1 with stdout",
			stdout:   "partial\n",
			stderr:   "",
			exitCode: -1,
			want:     "STDOUT:\npartial\n\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdout := newTestCapture(t, tt.stdout)
			stderr := newTestCapture(t, tt.stderr)
			got := formatCommandOutput(stdout, stderr, tt.exitCode)
			if got != tt.want {
				t.Errorf("formatCommandOutput:\n  expected: %q\n  got:      %q", tt.want, got)
			}
		})
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
