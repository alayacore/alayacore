package plainio

// Package plainio provides a plain stdin/stdout adaptor for AlayaCore.
// It reads prompts from stdin (one per line) and prints messages to stdout.
// No terminal features are used — just plain IO.

import (
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
	_, _ = agentpkg.LoadOrNewSession(
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

	// Channel to communicate exit code from goroutines
	resultCh := make(chan int, 1)

	// Goroutine: read stdin and emit TLV messages
	go func() {
		if err := readPromptsFromStdin(input); err != nil {
			resultCh <- -1
			return
		}
		// EOF: close input so session finishes
		input.Close()
	}()

	// Goroutine: handle SIGINT (Ctrl-C) - same as :q in terminal
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT)
		<-sigCh
		input.Close()
		resultCh <- 1
	}()

	// Determine exit code
	return <-resultCh
}
