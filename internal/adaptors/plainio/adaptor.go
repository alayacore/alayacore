package plainio

// Package plainio provides a plain stdin/stdout adaptor for AlayaCore.
// It reads prompts from stdin (one per line) and prints messages to stdout.
// No terminal features are used — just plain IO.

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	agentpkg "github.com/alayacore/alayacore/internal/agent"
	"github.com/alayacore/alayacore/internal/app"
	"github.com/alayacore/alayacore/internal/stream"
)

// Adaptor reads prompts from stdin and prints assistant output to stdout.
type Adaptor struct {
	Config   *app.Config
	TextOnly bool
}

// NewAdaptor creates a new plainio adaptor.
func NewAdaptor(cfg *app.Config, textOnly bool) *Adaptor {
	return &Adaptor{Config: cfg, TextOnly: textOnly}
}

// Start runs the plainio adaptor. It blocks until the session finishes.
// Returns the exit code: 0 for graceful exit, 1 for Ctrl-C, negative for errors.
func (a *Adaptor) Start() int {
	input := stream.NewChanInput(100)
	output := newStdoutOutput(a.TextOnly)

	// Load session
	sess, _ := agentpkg.LoadOrNewSession(
		a.Config.AgentTools,
		a.Config.SystemPrompt,
		a.Config.ExtraSystemPrompt,
		a.Config.MaxSteps,
		input,
		output,
		a.Config.Cfg.Session,
		a.Config.Cfg.ModelConfig,
		a.Config.Cfg.RuntimeConfig,
		a.Config.Cfg.DebugAPI,
		a.Config.Cfg.AutoSummarize,
		a.Config.Cfg.AutoSave,
		a.Config.Cfg.Proxy,
	)

	// Channel to communicate the result from goroutines
	resultCh := make(chan int, 2)

	// Goroutine: read stdin and emit TLV messages
	go func() {
		if err := readPromptsFromStdin(input); err != nil {
			resultCh <- -1
			return
		}
		// EOF: close input so session finishes queued tasks
		input.Close()
	}()

	// Goroutine: handle SIGINT (Ctrl-C)
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT)
		<-sigCh
		// Send cancel_all command
		_ = input.EmitTLV(stream.TagTextUser, ":cancel_all") //nolint:errcheck // best effort on signal
		input.Close()
		resultCh <- 1
	}()

	// Wait for the session to finish processing all queued tasks
	sess.WaitDone()

	// Give the output a final flush
	fmt.Fprintln(output.writer)

	// Determine exit code
	select {
	case result := <-resultCh:
		return result
	default:
		return 0
	}
}
