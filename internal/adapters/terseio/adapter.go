package terseio

// Package terseio provides a minimal stdin/stdout adapter for AlayaCore:
// read ALL of stdin as a single prompt (or command, if it starts with ":"),
// print ONLY the final answer.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"sync/atomic"

	"github.com/alayacore/alayacore/internal/app"
	"github.com/alayacore/alayacore/internal/mcpauth"
)

// Compile-time check: Adapter satisfies app.Adapter.
var _ app.Adapter = (*Adapter)(nil)

// errSessionClosed is returned by the input gate when the session ends
// before it signals readiness.
var errSessionClosed = errors.New("session closed")

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
// pressed. Ctrl-C cancels the running task through the session — the task
// is aborted cleanly and the buffered answer is discarded — but the
// conventional SIGINT exit code is preserved so scripts still see the
// interruption.
//
// stdin is read in full (until EOF) and treated as a single prompt — or,
// if it starts with ":", as a single command (":continue", ":save", ...;
// see input.go). Command errors go to stderr and set exit code 1, just
// like session errors.
//
// stdout receives ONLY the final assistant text; errors and notifications
// go to stderr. --tool-confirm is rejected at startup (see main.go), so no
// tool_confirm frames can arrive and no interactive channel is needed.
//
// MCP OAuth is handled automatically: the adapter starts the callback
// server, opens the browser, and submits the code itself (mcpauth). A
// server whose automatic authorization cannot complete is declined (there
// is no one to type a code), so MCP init still settles and the prompt runs
// without that server's tools.
func (a *Adapter) Start() int {
	output := newAnswerOutput(os.Stdout, os.Stderr)

	// MCP OAuth flow. The TLV input writer is attached after StartSession
	// returns it; the atomic pointer lets the flow's send closure pick it
	// up without a data race.
	var inputPtr atomic.Pointer[app.LockedWriter]
	flow := mcpauth.New(func(cmd string) error {
		w := inputPtr.Load()
		if w == nil {
			return errors.New("input stream not ready")
		}
		return writeCommand(w, cmd)
	}, output.diagnostic)
	// No interactive channel: leave the flow's default Unresolved policy in
	// place (decline a server whose automatic authorization fails), so a
	// stuck authorization always settles instead of hanging the run.
	output.mcpAuthRequired = flow.Start
	output.onMCPConnected = flow.Connected
	output.onMCPDone = flow.Abort

	// Load session.
	session, inputWriter, err := app.StartSession(a.Config, output, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	// input serializes writes to the session's TLV input stream: the stdin
	// goroutine and the MCP OAuth flow both write to the same pipe, so TLV
	// frames must never interleave.
	input := app.NewLockedWriter(inputWriter)
	inputPtr.Store(input)

	// feedCtx aborts a prompt waiting for the ready frame when Ctrl-C
	// arrives; the session cannot be reached otherwise (there is no task to
	// cancel yet).
	feedCtx, feedCancel := context.WithCancel(context.Background())
	defer feedCancel()

	// Ctrl-C (SIGINT) cancels the running task via the session's
	// CancelTask — NOT by writing a :cancel CI frame to the TLV input
	// pipe. The pipe is already closed by the time the task runs: stdin
	// reached EOF and the adapter closed inputWriter, so inputPump has
	// exited and a late frame could never reach the session (io.Pipe
	// Write after Close fails immediately). Killing the process outright
	// would orphan running tool processes: shell tools start with setsid
	// (own session, no controlling terminal), so they never receive the
	// terminal's SIGINT — only CancelTask propagates the abort through
	// the session's cancel machinery. The session aborts the task, its
	// error path discards the buffered answer, and the adapter exits 130
	// (128+SIGINT) to preserve scripting conventions. SIGINT during the
	// stdin read phase (interactive misuse without EOF) also closes stdin
	// to abort the read; SIGINT after the task finished only forces the
	// exit code.
	var sigint atomic.Bool
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	defer func() {
		signal.Stop(sigCh)
		close(sigCh)
	}()
	go app.WatchSignals(sigCh, session.Done(), func() {
		sigint.Store(true)
		session.CancelTask()
		// Unblock a prompt waiting for the ready frame, and a pending
		// io.ReadAll on stdin.
		feedCancel()
		os.Stdin.Close()
	})

	exitCh := make(chan int, 1)

	// Read all of stdin as one prompt or one command, then close input
	// (EOF). terseio never needs further input — tool confirmations are
	// impossible (the --tool-confirm conflict is rejected in main.go) —
	// so closing early is safe and lets the session's run() loop finish.
	// The prompt waits for the session's ready frame first; closing before
	// then would let run() exit while MCP init is still in flight, and a
	// prompt sent during init is rejected with MCP_NOT_READY.
	go func() {
		err := readAllPrompt(input, os.Stdin, func() error {
			select {
			case <-output.Ready():
				return nil
			case <-feedCtx.Done():
				return feedCtx.Err()
			case <-session.Done():
				return errSessionClosed
			}
		})
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
