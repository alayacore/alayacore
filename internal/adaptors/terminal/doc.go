// Package terminal provides the TUI adaptor for AlayaCore using Bubble Tea.
//
// # Architecture
//
// The terminal adaptor composes several sub-models:
//   - outputWriter: parses TLV from the session, styles content, appends to WindowBuffer
//   - WindowBuffer: holds message windows (reasoning, text, tools); renders on demand
//   - DisplayModel: viewport over WindowBuffer content; handles scroll and window cursor
//   - InputModel: text input for prompts and commands
//   - TodoModel: todo list display (when present)
//   - StatusModel: token usage and status bar
//
// # Message Flow
//
// Session writes TLV bytes → OutputWriter.Write() → parses tags, styles, appends to WindowBuffer
// → throttled updateChan signal → Terminal drains on tick → DisplayModel.updateContent()
//
// # User input → Terminal.handleKeyMsg → InputModel or DisplayModel or session commands
//
// # Key Files
//
//   - terminal.go: main Tea model, message routing, key bindings
//   - output.go: TLV parsing, styling, WindowBuffer updates
//   - window.go: WindowBuffer (windows with borders, wrap, diff), Window struct
//   - display.go: viewport, scroll state, window cursor (j/k navigation)
//   - input_component.go: text input, editor integration
//   - styles.go: lipgloss styles for all UI elements
package terminal
