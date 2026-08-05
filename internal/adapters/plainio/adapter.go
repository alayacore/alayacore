package plainio

// Package plainio provides a plain stdin/stdout adapter for AlayaCore.
// It reads prompts from stdin (one per line) and prints messages to stdout.
// No terminal features are used — just plain IO.
//
// There is no task queue: only one prompt is processed per invocation.
// If stdin contains multiple prompts, only the first is executed;
// subsequent prompts are rejected while a task is running.

import (
	"errors"
	"fmt"
	"os"
	"os/signal"

	"github.com/alayacore/alayacore/internal/app"
)

// Compile-time check: Adapter satisfies app.Adapter.
var _ app.Adapter = (*Adapter)(nil)

// Adapter reads prompts from stdin and prints assistant output to stdout.
type Adapter struct {
	Config *app.Config
}

// NewAdapter creates a new plainio adapter.
func NewAdapter(cfg *app.Config) *Adapter {
	return &Adapter{Config: cfg}
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

	// Wire the MCP OAuth flow before the session starts so no
	// "auth_required" event can slip through. The TLV input writer is
	// attached after StartSession returns it (flow.setInput below).
	flow := newMCPAuthFlow(output)
	output.mcpAuthRequired = flow.start
	output.onMCPConnected = flow.connected
	output.onMCPDone = flow.abort

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
	flow.setInput(input)

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
	go func() {
		for {
			select {
			case _, ok := <-sigCh:
				if !ok {
					return
				}
				if !session.CancelTask() {
					// Nothing was running — keep the same feedback the
					// :cancel CI frame used to produce via its CO error.
					output.printLine("\n[error: nothing to cancel]\n")
				}
			case <-session.Done():
				// Session is gone; nothing left to cancel.
				return
			}
		}
	}()

	exitCh := make(chan int, 1)

	// readStdin reads prompts from stdin and emits TLV messages.
	// Only this goroutine and the MCP OAuth flow write to the input
	// stream (both through the lockedWriter, so writes are safe).
	readStdin := func() {
		err := readPrompts(input, os.Stdin)
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
