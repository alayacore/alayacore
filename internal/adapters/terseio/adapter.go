package terseio

// Package terseio provides a minimal stdin/stdout adapter for AlayaCore:
// read ALL of stdin as a single prompt (or command, if it starts with ":"),
// print ONLY the final answer.

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync/atomic"

	"github.com/alayacore/alayacore/internal/app"
)

// Compile-time check: Adapter satisfies app.Adapter.
var _ app.Adapter = (*Adapter)(nil)

// Adapter reads all of stdin as a single prompt — or a single command
// (":continue", ":save /tmp/x", ...) — and prints only the final
// assistant text answer to stdout.
type Adapter struct {
	Config *app.Config
}

// NewAdapter creates a new terseio adapter.
func NewAdapter(cfg *app.Config) *Adapter {
	return &Adapter{Config: cfg}
}

// Start runs the terseio adapter. It blocks until the session finishes.
// Returns 0 on success, 1 on errors, 130 (128+SIGINT) when Ctrl-C was
// pressed. Ctrl-C sends a :cancel command — the task is aborted cleanly
// and the buffered answer is discarded — but the conventional SIGINT exit
// code is preserved so scripts still see the interruption.
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

	// input serializes writes to the session's TLV input stream: the
	// stdin goroutine and the SIGINT handler write to the same pipe, so
	// TLV frames must never interleave.
	input := app.NewLockedWriter(inputWriter)

	// Ctrl-C (SIGINT) sends a :cancel command instead of killing the
	// process. Killing would orphan running tool processes: shell tools
	// start with setsid (own session, no controlling terminal), so they
	// never receive the terminal's SIGINT — only :cancel propagates the
	// abort through the session's cancel machinery. The session aborts
	// the task, its error path discards the buffered answer, and the
	// adapter exits 130 (128+SIGINT) to preserve scripting conventions.
	// SIGINT during the stdin read phase (interactive misuse without
	// EOF) also closes stdin to abort the read; SIGINT after the task
	// finished only forces the exit code.
	var sigint atomic.Bool
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	defer func() {
		signal.Stop(sigCh)
		close(sigCh)
	}()
	go func() {
		for {
			select {
			case _, ok := <-sigCh:
				if !ok {
					return
				}
				sigint.Store(true)
				handleInterrupt(input)
				// Unblock a pending io.ReadAll on stdin.
				os.Stdin.Close()
			case <-session.Done():
				// Session is gone: the input pipe has no reader, so
				// writing a cancel frame would block forever.
				return
			}
		}
	}()

	exitCh := make(chan int, 1)

	// Read all of stdin as one prompt or one command, then close input
	// (EOF). terseio never needs further input — tool confirmations are
	// impossible (the --tool-confirm conflict is rejected in main.go) —
	// so closing early is safe and lets the session's run() loop finish.
	go func() {
		err := readAllPrompt(input, os.Stdin)
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
	// no text). On SIGINT the buffered answer is never flushed: the
	// session's cancel error already discarded it, and skipping the
	// fallback covers the race where no error was emitted.
	if sigint.Load() {
		return 130
	}
	output.FlushFinal()
	return code
}

// handleInterrupt reacts to a SIGINT (Ctrl-C): it sends a :cancel command
// so the session aborts the running task (and its tool processes) cleanly.
// The cancel is always sent, matching the terminal and plainio adapters —
// when no task is running the session replies "nothing to cancel" on
// stderr. Returns true if a cancel frame was written.
func handleInterrupt(input io.Writer) bool {
	return sendCancel(input) == nil
}
