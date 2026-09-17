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
//	width, single codepoint, one cell per fixed-width row) — read it there
//	before adding a symbol. The one rule worth repeating at this level:
//	only single-codepoint emoji, because a U+FE0F or a ZWJ sequence gives
//	the host a second opinion about the cell count (constants.go, rule 3).
//
// Source Layout:
//
//	One flat directory. Start at tui.go (the root model) and program.go (the
//	event loop); everything else groups by concern rather than by file, so a
//	new file rarely needs a line here:
//
//	  - input     — bytes → keys (key_parser.go, console_events.go), then
//	                the bindings and the dispatch stack (keys.go,
//	                keybinds.go, input_layers.go)
//	  - rendering — the window model and its pipeline (window.go,
//	                window_renderer.go, window_buffer.go, wrap.go,
//	                display.go, scroll_view.go, live_edge.go) and the
//	                terminal surface under it (screen.go)
//	  - surfaces  — prompt_input.go / input_field.go, overlay.go, the
//	                selectors (model_selector.go, theme_selector.go,
//	                help_window.go, attachment_window.go),
//	                confirm_dialog.go, filtered_list.go
//	  - look      — styles.go / style.go (SGR layer), theme_manager.go,
//	                spinner.go, markdown.go, diff.go, constants.go (glyph
//	                policy)
//	  - I/O       — output.go, tool_render.go / tool_handler.go, exec.go,
//	                editor.go, term_io*.go, program_input*.go
//
//	docs/tui-architecture.md maps the whole stack.
//
// Theme data types (Theme struct, DefaultTheme, LoadTheme) and the core
// Manager live in internal/theme — shared with future GUI adapters.
//
// Usage:
//
//	terminal := NewTerminal(output, input, config, width, height)
//	finalModel, err := Run(terminal)
package terminal
