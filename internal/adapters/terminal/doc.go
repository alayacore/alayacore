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
// Glyph and Emoji Notes:
//
//	Drawn symbols follow the glyph policy in constants.go (East-Asian
//	width, single codepoint, one cell per fixed-width row).
//
//	Use only single-codepoint emoji. The old justification for this —
//	that a variation selector makes the width model miscount and a
//	truncation eats the neighboring character ("Image" → "Imag") — no
//	longer holds: both width libraries measure a camera emoji followed by
//	U+FE0F, and a ZWJ sequence, as one cluster of two cells. The rule
//	stands for a different reason: this package measures a string with
//	one library (ansi/displaywidth) and slices it with the other
//	(uniseg), and the two disagree on roughly 3100 codepoints — U+2713
//	followed by U+FE0F is 2 cells to the first and 1 to the second. Any
//	glyph whose width depends on a variation selector or ZWJ is
//	therefore a layout shift waiting to happen, and no host can detect
//	it at runtime.
//
// Key Files:
//
//   - tui.go: Terminal model, overlay components, overlay rendering, MCP overlay
//     state machine, and overlay action type
//   - tui_focus.go: Focus management (input/display switching, blur/focus)
//   - tui_status.go: Status bar rendering (tokens, steps, switches)
//   - keybinds.go: Declarative key binding configuration
//   - program_input.go: The input loop and the parking protocol that hands the
//     keyboard to a foreground child (per-platform sources:
//     program_input_unix.go, program_input_windows.go)
//   - key_parser.go: Byte-stream → key message parser (VT100/SS3/URxvt)
//   - console_events.go: Windows console input events → the same byte stream
//     (built for every platform so the mapping is testable without a console)
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
//   - exec.go: External process execution and terminal suspension (module 5)
//   - editor.go: External editor support ($EDITOR handoff)
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
