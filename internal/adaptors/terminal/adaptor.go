package terminal

// Entry point for the terminal UI adaptor.
// This file handles application startup, session loading, and error handling.

import (
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"
	"golang.org/x/term"

	agentpkg "github.com/alayacore/alayacore/internal/agent"
	"github.com/alayacore/alayacore/internal/app"
	"github.com/alayacore/alayacore/internal/stream"
)

// Adaptor starts the TUI; use from main/app.
type Adaptor struct {
	Config    *app.Config
	ThemePath string
}

// NewAdaptor creates a new Terminal adaptor.
func NewAdaptor(cfg *app.Config) *Adaptor {
	return &Adaptor{
		Config: cfg,
	}
}

// NewAdaptorWithTheme creates a new Terminal adaptor with a custom theme path.
func NewAdaptorWithTheme(cfg *app.Config, themePath string) *Adaptor {
	return &Adaptor{
		Config:    cfg,
		ThemePath: themePath,
	}
}

// Start runs the Terminal program.
func (a *Adaptor) Start() {
	// Load theme from config paths
	theme := LoadThemeFromPaths(a.ThemePath)
	styles := NewStyles(theme)

	inputStream := stream.NewChanInput(10)
	terminalOutput := NewTerminalOutput(styles)

	// Get terminal size before loading session (so session loads with correct dimensions)
	initialWidth, initialHeight := getTerminalSize()
	terminalOutput.SetWindowWidth(initialWidth)

	// Load session synchronously before starting the UI
	session, _ := agentpkg.LoadOrNewSession(
		a.Config.AgentTools,
		a.Config.SystemPrompt,
		a.Config.ExtraSystemPrompt,
		a.Config.MaxSteps,
		inputStream,
		terminalOutput,
		a.Config.Cfg.Session,
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
		fmt.Fprintln(os.Stderr, `---
name: "Ollama (127.0.0.1) / GPT OSS 20B"
protocol_type: "anthropic"
base_url: "http://127.0.0.1:11434"
api_key: "no-key-by-default"
model_name: "gpt-oss:20b"
context_limit: 128000
---
name: "OpenAI GPT-4o"
protocol_type: "openai"
base_url: "https://api.openai.com/v1"
api_key: "your-api-key"
model_name: "gpt-4o"
context_limit: 128000`)
		fmt.Fprintln(os.Stderr, "")
		os.Exit(1)
	}

	// Create terminal with loaded session, initial window size, and theme
	t := NewTerminalWithTheme(session, terminalOutput, inputStream, a.Config, initialWidth, initialHeight, theme)

	// Create and run the program
	p := tea.NewProgram(t, tea.WithInput(os.Stdin), tea.WithOutput(os.Stdout))
	_, _ = p.Run() //nolint:errcheck // terminal program run, error not critical
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
