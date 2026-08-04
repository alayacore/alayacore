// Package terminal provides the terminal user interface for AlayaCore.
//
// The terminal package implements a Bubble Tea-based TUI that serves as the
// primary interface for interacting with the AI assistant. It handles:
//
//   - User input (text prompts and commands)
//   - Display of assistant responses with styling
//   - Model selection and switching
//   - Focus management between input and display windows
//
// Architecture Overview:
//
//	The terminal UI follows the Bubble Tea architecture (Elm-style):
//	  - Terminal: The main model that composes all components
//	  - DisplayModel: Renders assistant output with virtual scrolling
//	  - PromptInput: Handles user text input with external editor support
//	  - Status bar: Shows session status (tokens, model info)
//	  - ModelSelector: Modal for switching between AI models
//
// Communication with the session layer uses TLV (Tag-Length-Value) protocol:
//   - Input: io.WriteCloser sends TLV messages to the session
//   - Output: OutputWriter parses TLV and renders styled content
//
// Emoji Notes:
//
//	Use only single-codepoint emoji throughout the TUI. Multi-codepoint
//	emoji with variation selectors (U+FE0F) cause terminal compatibility
//	issues — the width calculation mismatch can truncate adjacent text
//	characters (e.g. "Image" → "Imag"). This affects all emoji used in
//	labels, icons, and status indicators.
//
// Key Files:
//
//   - tui.go: Terminal model, overlay components, overlay rendering, MCP overlay
//     state machine, and overlay action type
//   - tui_focus.go: Focus management (input/display switching, blur/focus)
//   - tui_status.go: Status bar rendering (tokens, steps, switches)
//   - keybinds.go: Declarative key binding configuration
//   - output.go: TLV parsing and styled rendering
//   - display.go: DisplayModel, virtual scrolling, and cursor navigation
//   - window.go: Window struct with polymorphic WindowRendering interface
//   - window_renderer.go: Renderers for text, user, and tool windows
//   - window_buffer.go: WindowBuffer, line tracking, and virtual rendering
//   - styles.go: Lipgloss style derivation from theme.Theme
//   - prompt_input.go: Input handling and external editor support
//   - model_selector.go: Model switching UI with fuzzy search
//   - theme_manager.go: Wrapper around theme.Manager with startup init errors
//   - theme_selector.go: Theme selection UI with live preview
//   - init_errors.go: Init error collection for initialization errors
//   - overlay.go: Overlay rendering for selectors
//   - help_window.go: Keybinding and command help overlay
//   - confirm_dialog.go: Confirmation dialogs (quit, cancel, tool, MCP auth, MCP init)
//   - tool_render.go, tool_handler.go: Tool execution display
//
// Theme data types (Theme struct, DefaultTheme, LoadTheme) and the core
// Manager live in internal/theme — shared with future GUI adapters.
//
// Usage:
//
//	terminal := NewTerminal(output, input, config, width, height)
//	p := tea.NewProgram(terminal)
//	p.Run()
package terminal
