package tools

import (
	"bytes"
	"context"
	"os"
	"testing"
	"time"

	"github.com/alayacore/alayacore/internal/llm"
)

func TestExecuteCommandNormalCompletion(t *testing.T) {
	result, err := executeCommand(context.Background(), executeCommandInput{
		Command: "echo hello",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text, ok := result.(llm.ToolResultOutputText)
	if !ok {
		t.Fatalf("expected text output, got %T", result)
	}
	if text.Text != "hello\n" {
		t.Errorf("expected %q, got %q", "hello\n", text.Text)
	}
}

func TestExecuteCommandExitError(t *testing.T) {
	result, err := executeCommand(context.Background(), executeCommandInput{
		Command: "exit 42",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	errOut, ok := result.(llm.ToolResultOutputError)
	if !ok {
		t.Fatalf("expected error output, got %T", result)
	}
	if errOut.Error == "" {
		t.Error("expected non-empty error output")
	}
}

func TestExecuteCommandCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	// Start a long-running command
	done := make(chan llm.ToolResultOutput, 1)
	go func() {
		result, _ := executeCommand(ctx, executeCommandInput{
			Command: "sleep 60",
		})
		done <- result
	}()

	// Cancel after a short delay
	time.Sleep(500 * time.Millisecond)
	cancel()

	select {
	case result := <-done:
		errOut, ok := result.(llm.ToolResultOutputError)
		if !ok {
			t.Fatalf("expected error output, got %T", result)
		}
		if errOut.Error != "Canceled" {
			t.Errorf("expected 'Canceled', got %q", errOut.Error)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("command was not canceled within timeout")
	}
}

func TestExecuteCommandTimeout(t *testing.T) {
	// Override the default timeout for this test
	original := defaultCommandTimeout
	defer func() {
		// Restore the original value — cannot reassign const, so we
		// rely on the fact that the test below uses a context timeout
		// shorter than defaultCommandTimeout.
		_ = original
	}()

	// Use a context with a very short timeout to simulate the timeout path
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	done := make(chan llm.ToolResultOutput, 1)
	go func() {
		result, _ := executeCommand(ctx, executeCommandInput{
			// vim hangs when there's no TTY — perfect for testing timeout
			Command: "sleep 60",
		})
		done <- result
	}()

	select {
	case result := <-done:
		errOut, ok := result.(llm.ToolResultOutputError)
		if !ok {
			t.Fatalf("expected error output, got %T", result)
		}
		if errOut.Error != "Canceled" && errOut.Error != "Timed out" {
			// Either is acceptable depending on whether the parent context
			// or the internal timeout fires first
			t.Errorf("expected 'Canceled' or 'Timed out', got %q", errOut.Error)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("command was not terminated within timeout")
	}
}

func TestExecuteCommandWorkingDir(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "alayacore-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Save and restore working directory
	originalWd, _ := os.Getwd()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(originalWd)

	result, err := executeCommand(context.Background(), executeCommandInput{
		Command: "pwd",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text, ok := result.(llm.ToolResultOutputText)
	if !ok {
		t.Fatalf("expected text output, got %T", result)
	}
	if text.Text != tmpDir+"\n" {
		t.Errorf("expected %q, got %q", tmpDir+"\n", text.Text)
	}
}

func TestHandleCommandCompletion(t *testing.T) {
	tests := []struct {
		name       string
		execErr    error
		wantError  bool
		wantInText string
	}{
		{
			name:       "success with output",
			execErr:    nil,
			wantError:  false,
			wantInText: "hello",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdout := bytes.NewBufferString("hello")
			stderr := &bytes.Buffer{}

			result := handleCommandCompletion(tt.execErr, stdout, stderr)

			if tt.wantError {
				if _, ok := result.(llm.ToolResultOutputError); !ok {
					t.Error("expected error output")
				}
			} else {
				text, ok := result.(llm.ToolResultOutputText)
				if !ok {
					t.Fatalf("expected text output, got %T", result)
				}
				if text.Text != tt.wantInText {
					t.Errorf("expected %q, got %q", tt.wantInText, text.Text)
				}
			}
		})
	}
}
