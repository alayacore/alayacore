// Package terminal provides the terminal user interface for AlayaCore.
//
// The terminal package implements the TUI on a self-hosted minimal stack
// (own event loop, key parser, screen management, and style layer — no
// Bubble Tea / lipgloss dependency). It serves as the primary interface
// for interacting with the AI assistant. It handles:
//
//   - User input (text prompts and commands)
//   - Display of assistant responses with styling
//   - Model selection and switching
//   - Focus management between input and display windows
//
// Architecture Overview:
//
//	The terminal UI follows the Elm-style architecture:
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
//   - key_parser.go: Byte-stream → key message parser (VT100/SS3/URxvt)
//   - program.go: Self-built event loop (Update/Cmd/Msg dispatch, timers)
//   - screen.go: Alt screen, cursor, and raw passthrough renderer with
//     soft-wrap-aware row diffing
//   - output.go: TLV parsing and styled rendering
//   - display.go: DisplayModel, virtual scrolling, and cursor navigation
//   - window.go: Window struct with polymorphic WindowRendering interface
//   - window_renderer.go: Renderers for text, user, and tool windows
//   - window_buffer.go: WindowBuffer, line tracking, and virtual rendering
//   - wrap.go: Display-width wrapping, truncation, and visual-line splitting
//   - styles.go, style.go: Self-built style layer (SGR byte-compatible)
//   - scroll_view.go: Viewport clipping and scroll clamping
//   - prompt_input.go, input_field.go: Input handling and external editor support
//   - model_selector.go: Model switching UI with fuzzy search
//   - theme_manager.go: Wrapper around theme.Manager with startup init errors
//   - theme_selector.go: Theme selection UI with live preview
//   - init_errors.go: Init error collection for initialization errors
//   - overlay.go: Overlay rendering for selectors
//   - help_window.go: Keybinding and command help overlay
//   - confirm_dialog.go: Confirmation dialogs (quit, cancel, tool, MCP auth, MCP init)
//   - attachment_window.go: File/URL attachment picker
//   - tool_render.go, tool_handler.go: Tool execution display
//   - exec.go, editor.go: External editor / process handoff
//   - session_state.go: Session status/model/queue snapshot state
//   - term_io.go: Raw-mode terminal I/O
//
// Theme data types (Theme struct, DefaultTheme, LoadTheme) and the core
// Manager live in internal/theme — shared with future GUI adapters.
//
// Usage:
//
//	terminal := NewTerminal(output, input, config, width, height)
//	finalModel, err := Run(terminal)
package terminal
