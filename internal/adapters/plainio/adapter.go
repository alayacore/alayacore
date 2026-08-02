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
// stdin read error. Ctrl-C (SIGINT) terminates the process with exit code
// 130 (default signal handling).
//
// plainio is an interactive mode: task errors are reported and the session
// continues — the user can keep typing prompts. The exit code reflects
// process-level state only, never session content:
//   - 0: the user typed :quit / :q, or stdin reached EOF (Ctrl-D) and all
//     tasks have finished — regardless of whether any task errored.
//   - 1: startup failure or a stdin read error.
//   - 130: SIGINT.
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
	output.onMCPDone = flow.abort

	// Load session
	session, inputWriter, err := app.StartSession(a.Config, output, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	flow.setInput(inputWriter)

	exitCh := make(chan int, 1)

	// readStdin reads prompts from stdin and emits TLV messages.
	// Only this goroutine touches inputWriter.
	readStdin := func() {
		err := readPrompts(inputWriter, os.Stdin)
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
