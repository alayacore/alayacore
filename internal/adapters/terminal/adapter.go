package terminal

// Package terminal implements the terminal UI adapter for AlayaCore.
// This file handles application startup, asynchronous session loading,
// and error handling.

import (
	"fmt"
	"io"
	"os"

	"golang.org/x/term"

	"github.com/alayacore/alayacore/internal/app"
	"github.com/alayacore/alayacore/internal/theme"
)

// Compile-time check: Adapter satisfies app.Adapter.
var _ app.Adapter = (*Adapter)(nil)

const defaultThemeName = "theme-dark"

// Adapter starts the TUI; use from main/app.
type Adapter struct {
	Config *app.Config
}

// NewAdapter creates a new Terminal adapter.
func NewAdapter(cfg *app.Config) *Adapter {
	return &Adapter{Config: cfg}
}

// Start runs the Terminal program. Returns exit code.
//
// The TUI starts immediately on slow machines, showing a loading spinner
// while the session is loaded asynchronously. This avoids the long
// "blank terminal" delay between pressing Enter and seeing the TUI.
func (a *Adapter) Start() int {
	// Note: OAuth MCP authorization is handled via the :mcp_confirm command
	// in the TUI, not synchronously before startup.

	// Create theme manager
	themeManager := NewThemeManager(a.Config.Cfg.ThemesFolder)

	terminalOutput := NewTerminalOutput(NewStyles(theme.DefaultTheme()))

	// Get terminal size before creating the TUI (so we can size the loading screen)
	initialWidth, initialHeight := getTerminalSize()
	terminalOutput.SetWindowWidth(initialWidth)

	// Create the input pipe BEFORE starting the TUI. The TUI gets the
	// write end (pipeWriter) immediately; the session will read from
	// the read end (pipeReader) once loading completes.
	pipeReader, pipeWriter := io.Pipe()

	// Create Terminal model in loading mode. The loading screen uses the
	// embedded default theme (theme.DefaultTheme()) — no file I/O needed
	// for the brief startup phase. The session will load all theme files
	// exactly once via sendThemeListMsg() and broadcast the full data,
	// which the adapter caches and applies when loading completes.
	t := NewTerminalWithTheme(
		terminalOutput, pipeWriter, a.Config,
		initialWidth, initialHeight,
		theme.DefaultTheme(), themeManager, defaultThemeName,
	)
	t.loading = true // enter async loading mode
	t.pipeReader = pipeReader

	// Create and run the program.
	// Run automatically opens the real TTY when stdin is piped
	// (Unix: /dev/tty, Windows: CONIN$ + CONOUT$).
	finalModel, err := Run(t)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error running terminal UI: %v\n", err)
		return 1
	}

	// If the session failed to load asynchronously, report the error now
	// that the terminal is back in cooked mode.
	if term, ok := finalModel.(Terminal); ok && term.loadingError != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", term.loadingError)
		return 1
	}

	return 0
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
