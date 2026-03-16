// Package terminal provides the terminal user interface for AlayaCore.
//
// The terminal package implements a Bubble Tea-based TUI that serves as the
// primary interface for interacting with the AI assistant. It handles:
//
//   - User input (text prompts and commands)
//   - Display of assistant responses with styling
//   - Model selection and switching
//   - Task queue management
//   - Focus management between input and display windows
//
// Architecture Overview:
//
//	The terminal UI follows the Bubble Tea architecture (Elm-style):
//	  - Terminal: The main model that composes all components
//	  - DisplayModel: Renders assistant output with virtual scrolling
//	  - InputModel: Handles user text input with external editor support
//	  - StatusModel: Shows session status (tokens, queue, model info)
//	  - ModelSelector: Modal for switching between AI models
//	  - QueueManager: Modal for managing the task queue
//
// Communication with the session layer uses TLV (Tag-Length-Value) protocol:
//   - Input: ChanInput receives TLV messages from user actions
//   - Output: OutputWriter parses TLV and renders styled content
//
// Key Files:
//
//   - terminal.go: Main Terminal model and message routing
//   - keys.go: Keyboard handling and focus management
//   - commands.go: Command processing (:quit, :model_set, etc.)
//   - output.go: TLV parsing and styled rendering
//   - window_*.go: Virtual scrolling and diff display
//   - constants.go: Layout and timing constants, color palette
//   - styles.go: Lipgloss style definitions
//   - keybinds.go: Declarative key binding configuration
//
// Usage:
//
//	terminal := NewTerminal(session, output, input, config, width, height)
//	p := tea.NewProgram(terminal, tea.WithOutput(os.Stderr))
//	p.Run()
package terminal
