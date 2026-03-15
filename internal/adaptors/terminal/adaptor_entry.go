package terminal

// This file contains the outer TerminalAdaptor used by main/app to
// start the Bubble Tea TUI. It wires the session, TLV streams, and
// terminal program together, leaving the rest of the package focused
// on the Tea model and view logic.

import (
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"
	"golang.org/x/term"

	agentpkg "github.com/alayacore/alayacore/internal/agent"
	"github.com/alayacore/alayacore/internal/app"
	"github.com/alayacore/alayacore/internal/stream"
)

// TerminalAdaptor starts the TUI; use from main/app.
type TerminalAdaptor struct {
	Config *app.Config
}

// NewTerminalAdaptor creates a new Terminal adaptor.
func NewTerminalAdaptor(cfg *app.Config) *TerminalAdaptor {
	return &TerminalAdaptor{
		Config: cfg,
	}
}

// getTerminalSize returns the current terminal size, or defaults if not a TTY.
func getTerminalSize() (width, height int) {
	if term.IsTerminal(int(os.Stdout.Fd())) {
		w, h, err := term.GetSize(int(os.Stdout.Fd()))
		if err == nil {
			return w, h
		}
	}
	return DefaultWidth, DefaultHeight
}

// Start runs the Terminal program.
func (a *TerminalAdaptor) Start() {
	inputStream := stream.NewChanInput(10)
	terminalOutput := NewTerminalOutput()

	// Get terminal size before loading session (so session loads with correct dimensions)
	initialWidth, initialHeight := getTerminalSize()
	terminalOutput.SetWindowWidth(initialWidth)

	// Load session synchronously before starting the UI
	session, _ := agentpkg.LoadOrNewSession(
		a.Config.Model,
		a.Config.AgentTools,
		a.Config.SystemPrompt,
		inputStream,
		terminalOutput,
		a.Config.Cfg.Session,
		a.Config.Cfg.ContextLimit,
		a.Config.Cfg.ModelConfig,
		a.Config.Cfg.RuntimeConfig,
		a.Config.Cfg.DebugAPI,
		a.Config.Cfg.Proxy,
	)

	// Check if we have any models available.
	if !terminalOutput.HasModels() {
		// Print error to stderr and exit
		modelPath := terminalOutput.GetModelConfigPath()
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Error: No models configured.")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Please edit the model config file:")
		fmt.Fprintf(os.Stderr, "  %s\n", modelPath)
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Example format:")
		fmt.Fprintln(os.Stderr, `name: "OpenAI GPT-4o"
protocol_type: "openai"
base_url: "https://api.openai.com/v1"
api_key: "your-api-key"
model_name: "gpt-4o"
context_limit: 128000
---
name: "Ollama GPT-OSS:20B"
protocol_type: "anthropic"
base_url: "https://127.0.0.1:11434"
api_key: "your-api-key"
model_name: "gpt-oss:20b"
context_limit: 32768`)
		fmt.Fprintln(os.Stderr, "")
		os.Exit(1)
	}

	// Create terminal with loaded session and initial window size
	t := NewTerminal(session, terminalOutput, inputStream, a.Config, initialWidth, initialHeight)

	// Create and run the program
	p := tea.NewProgram(t, tea.WithInput(os.Stdin), tea.WithOutput(os.Stdout))
	p.Run()
}
