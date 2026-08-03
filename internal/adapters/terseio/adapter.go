package terseio

// Package terseio provides a minimal stdin/stdout adapter for AlayaCore:
// read ALL of stdin as a single prompt, print ONLY the final answer.

import (
	"errors"
	"fmt"
	"os"

	"github.com/alayacore/alayacore/internal/app"
)

// Compile-time check: Adapter satisfies app.Adapter.
var _ app.Adapter = (*Adapter)(nil)

// Adapter reads all of stdin as a single prompt and prints only the
// final assistant text answer to stdout.
type Adapter struct {
	Config *app.Config
}

// NewAdapter creates a new terseio adapter.
func NewAdapter(cfg *app.Config) *Adapter {
	return &Adapter{Config: cfg}
}

// Start runs the terseio adapter. It blocks until the session finishes.
// Returns 0 on success, 1 on errors. Ctrl-C (SIGINT) terminates immediately
// with default signal handling (exit code 130).
//
// stdin is read in full (until EOF) and treated as a single prompt — or,
// if it starts with ":", as a single command (":continue", ":save", ...;
// see input.go). Command errors go to stderr and set exit code 1, just
// like session errors.
// stdout receives ONLY the final assistant text; errors and notifications
// go to stderr. --tool-confirm is rejected at startup (see main.go), so no
// tool_confirm frames can arrive and no interactive channel is needed.
func (a *Adapter) Start() int {
	output := newAnswerOutput(os.Stdout, os.Stderr)

	// Load session.
	session, inputWriter, err := app.StartSession(a.Config, output, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	exitCh := make(chan int, 1)

	// Read all of stdin as one prompt or one command, then close input
	// (EOF). terseio never needs further input — tool confirmations are
	// impossible (the --tool-confirm conflict is rejected in main.go) —
	// so closing early is safe and lets the session's run() loop finish.
	go func() {
		err := readAllPrompt(inputWriter, os.Stdin)
		inputWriter.Close()
		code := 0
		if err != nil && !errors.Is(err, errQuitPrompt) {
			code = 1
		}
		select {
		case exitCh <- code:
		default:
		}
	}()

	// Wait for EOF (Ctrl-D), error, or session completion.
	code := 0
	select {
	case code = <-exitCh:
	case <-output.ErrorChannel():
		code = 1
		// Unblock a pending io.ReadAll on stdin (interactive misuse where
		// the user has not sent EOF yet).
		os.Stdin.Close()
	case <-session.Done():
	}

	// Wait for the session to finish processing.
	<-session.Done()

	// Final check: even on a clean EOF path the session may have written
	// errors (network failures, API errors, etc.) that arrived after the
	// stdin goroutine finished. Override the exit code.
	if code == 0 && output.HasError() {
		code = 1
	}

	// Safety net: if the task-completion system message never arrived,
	// flush the buffered final answer now (no-op if already flushed, if
	// the answer was discarded by an error, or if the final message had
	// no text).
	output.FlushFinal()

	return code
}
