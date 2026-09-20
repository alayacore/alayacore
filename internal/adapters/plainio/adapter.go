package plainio

// Package plainio provides a plain stdin/stdout adapter for AlayaCore.
// It reads prompts from stdin (one per line) and prints messages to stdout.
// Rendering uses no terminal features — just plain IO (stdin's TTY-ness is
// consulted only for the MCP OAuth fallback policy; see doc.go).
//
// There is no task queue: only one prompt is processed per invocation.
// If stdin contains multiple prompts, only the first is executed;
// subsequent prompts are rejected while a task is running.

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"sync/atomic"

	"golang.org/x/term"

	"github.com/alayacore/alayacore/internal/app"
	"github.com/alayacore/alayacore/internal/mcpauth"
)

// Compile-time check: Adapter satisfies app.Adapter.
var _ app.Adapter = (*Adapter)(nil)

// errSessionClosed is returned by the input gate when the session ends
// before it signals readiness, so the stdin reader stops instead of blocking
// forever.
var errSessionClosed = errors.New("session closed")

// Adapter reads prompts from stdin and prints assistant output to stdout.
type Adapter struct {
	Config *app.Config
}

// NewAdapter creates a new plainio adapter.
func NewAdapter(cfg *app.Config) *Adapter {
	return &Adapter{Config: cfg}
}

// stdinIsTerminal reports whether stdin is an interactive terminal. It is
// used only to choose the MCP OAuth fallback policy: a terminal can still
// receive a typed :mcp_confirm, a pipe cannot. Output rendering stays free
// of any terminal detection.
func stdinIsTerminal() bool {
	return term.IsTerminal(int(os.Stdin.Fd()))
}

// unresolvedPolicy builds plainio's fallback policy for a server whose
// automatic OAuth authorization did not complete. On a terminal the manual
// :mcp_confirm/:mcp_decline commands are printed and the flow keeps
// waiting; with piped (non-interactive) stdin there is nowhere to type
// them, so the server is declined and MCP init can settle.
func unresolvedPolicy(out *stdoutOutput, interactive bool) mcpauth.Unresolved {
	return func(server, redirectURI, reason string) mcpauth.Action {
		if !interactive {
			out.printLine("\n[mcp: %s — declining %q]\n", reason, server)
			return mcpauth.Decline
		}
		out.printManualFallback(server, redirectURI, reason)
		return mcpauth.WaitManual
	}
}

// Start runs the plainio adapter. It blocks until the session finishes.
// Returns 0 on clean exit (:quit/:q or EOF), 1 on startup failure or a
// stdin read error. Ctrl-C (SIGINT) cancels the current task through the
// session (the session aborts any running task and continues) — it never
// terminates the process.
//
// plainio is an interactive mode: task errors are reported and the session
// continues — the user can keep typing prompts. The exit code reflects
// process-level state only, never session content:
//   - 0: the user typed :quit / :q, or stdin reached EOF (Ctrl-D) and all
//     tasks have finished — regardless of whether any task errored.
//   - 1: startup failure or a stdin read error.
//
// Scripts that need a failure signal (0/1 on task errors) should use
// --terseio instead.
//
// MCP initialization runs asynchronously — the session manages it
// internally via MCPInit. No adapter-side goroutine is needed.
func (a *Adapter) Start() int {
	output := newStdoutOutput()

	// stdin decides the OAuth fallback policy and whether prompts are gated:
	// a terminal can still receive a typed :mcp_confirm and a retry, a pipe
	// can do neither.
	interactive := stdinIsTerminal()

	// MCP OAuth flow. The TLV input writer is attached after StartSession
	// returns it; the atomic pointer lets the flow's send closure pick it
	// up without a data race. Wiring the hooks before StartSession means no
	// "auth_required" event can slip through — though in practice the
	// writer is published immediately, long before any MCP network round
	// trip.
	var inputPtr atomic.Pointer[app.LockedWriter]
	flow := mcpauth.New(func(cmd string) error {
		w := inputPtr.Load()
		if w == nil {
			return errors.New("input stream not ready")
		}
		return writeCommand(w, cmd)
	}, output.printLine)
	flow.SetUnresolved(unresolvedPolicy(output, interactive))
	output.mcpAuthRequired = flow.Start
	output.onMCPConnected = flow.Connected
	output.onMCPDone = flow.Abort

	// Load session
	session, inputWriter, err := app.StartSession(a.Config, output, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	// input serializes writes to the session's TLV input stream: the
	// stdin goroutine and the MCP OAuth flow both write to the same
	// pipe, so TLV frames must never interleave. (The SIGINT handler
	// cancels through the session directly — see below.)
	input := app.NewLockedWriter(inputWriter)
	inputPtr.Store(input)

	// Ctrl-C (SIGINT) cancels the current task instead of killing the
	// process. Killing would orphan running tool processes: shell tools
	// start with setsid (own session, no controlling terminal), so they
	// never receive the terminal's SIGINT. Cancellation goes through the
	// session's CancelTask (not a :cancel CI frame on the TLV pipe): the
	// pipe may already be closed at EOF, after which a frame could never
	// reach the session. The cancel is always attempted, matching the
	// terminal adapter's Ctrl-G/:cancel — when idle, the session reports
	// "nothing to cancel" and the session continues. The process exits
	// only via :quit/:q or EOF (Ctrl-D).
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	defer func() {
		signal.Stop(sigCh)
		close(sigCh)
	}()
	go app.WatchSignals(sigCh, session.Done(), func() {
		if !session.CancelTask() {
			// Nothing was running — keep the same feedback the
			// :cancel CI frame used to produce via its CO error.
			output.printLine("\n[error: nothing to cancel]\n")
		}
	})

	readyCh := output.Ready()
	exitCh := make(chan int, 1)

	// readStdin reads prompts from stdin and emits TLV messages.
	// Only this goroutine and the MCP OAuth flow write to the input
	// stream (both through the lockedWriter, so writes are safe).
	readStdin := func() {
		// Wait for the session's ready frame before the first prompt —
		// but only when stdin is not a terminal. A piped prompt is
		// available immediately and would race MCP init, and the pipe's
		// EOF then ends the session before it can retry, so it must wait.
		// An interactive user can retry (an early prompt is rejected with
		// MCP_NOT_READY and the session continues), and gating it would
		// deadlock the manual :mcp_confirm fallback: the reader would be
		// parked at the gate and could no longer type the code that lets
		// MCP init settle. The pipe stays open throughout init either way,
		// so a running OAuth callback can submit its :mcp_confirm.
		var gate func() error
		if !interactive {
			gate = func() error {
				select {
				case <-readyCh:
					return nil
				case <-session.Done():
					return errSessionClosed
				}
			}
		}
		err := readPrompts(input, os.Stdin, gate)
		// Close signals EOF regardless, unblocking the session.
		inputWriter.Close()
		code := 0
		// :quit/:q and EOF are both clean exits (code 0); only a stdin
		// read error is a process-level failure.
		if err != nil && !errors.Is(err, errQuitPrompt) {
			code = 1
		}
		select {
		case exitCh <- code:
		default:
		}
	}
	go readStdin()

	// Wait for EOF (Ctrl-D), :quit, or session completion. Task errors
	// are reported by the output and never terminate the session.
	code := 0
	select {
	case code = <-exitCh:
	case <-session.Done():
	}

	// Wait for the session to finish processing.
	<-session.Done()

	return code
}
