// Package terminal provides the TUI adaptor for AlayaCore using Bubble Tea.
//
// # Architecture Overview
//
// The terminal adaptor is organized into focused modules with clear responsibilities:
//
//   - terminal.go: Main Bubble Tea model that coordinates all components
//   - keys.go: Keyboard input handling and routing
//   - commands.go: Command processing (:quit, :cancel, :model_set, etc.)
//   - output.go: TLV parsing from session, styling, and WindowBuffer updates
//   - window.go: WindowBuffer (windows with borders, wrap, diff rendering)
//   - display.go: Viewport, scroll state, and window cursor navigation
//   - input_component.go: Text input with editor integration
//   - model_selector.go: Model selection UI with search and navigation
//   - status.go: Token usage and status bar
//   - styles.go: Lipgloss styles for all UI elements
//   - adaptor_entry.go: TerminalAdaptor entry point used by main/app
//
// # Message Flow
//
// User Input Flow:
//
//	User types → Terminal.Update() → keys.go handlers → commands.go or direct actions
//
// Display Update Flow:
//
//	Session writes TLV → outputWriter.Write() → parses tags, styles, appends to WindowBuffer
//	→ throttled updateChan signal → Terminal.handleTick() → DisplayModel.updateContent()
//
// # Key Bindings
//
// Global:
//   - Tab: Toggle focus between display and input
//   - Ctrl+L: Open model selector
//   - Ctrl+O: Open external editor
//   - Ctrl+S: Save session
//   - Ctrl+G: Cancel current request
//   - Enter: Submit prompt/command
//
// Display-focused:
//   - j/k: Move window cursor down/up
//   - J/K: Scroll down/up by one line
//   - H/L/M: Jump to top/bottom/center
//   - g/G: Go to top/bottom
//   - Space: Toggle window wrap mode
//   - :: Switch to input with command prefix
//
// Model Selector:
//   - Tab: Toggle search/list focus
//   - j/k or up/down: Navigate list
//   - e: Edit model config file
//   - r: Reload models
//   - Enter: Select model
//   - Esc/q: Close selector
//
// # Components
//
// Terminal (terminal.go):
// The main coordinator that owns all sub-components and routes messages.
// Delegates keyboard handling to keys.go and command processing to commands.go.
//
// DisplayModel (display.go):
// Manages the viewport over WindowBuffer content. Handles scrolling and
// window cursor navigation (vim-style j/k, H/L/M, g/G).
//
// InputModel (input_component.go):
// Wraps textinput.Model with editor integration. Supports opening an
// external editor (Ctrl+O) for multi-line input.
//
// ModelSelector (model_selector.go):
// Provides a searchable list of available models. Supports filtering
// and keyboard navigation.
//
// outputWriter (output.go):
// Implements io.Writer for the session output stream. Parses TLV tags
// and routes content to appropriate handlers (text, reasoning, functions).
//
// WindowBuffer (window.go):
// Holds the sequence of display windows. Supports efficient incremental
// updates and virtual rendering for large outputs.
package terminal
