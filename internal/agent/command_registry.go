package agent

import (
	"context"
	"strings"

	domainerrors "github.com/alayacore/alayacore/internal/errors"
)

// CommandHandler is the function signature for command handlers
type CommandHandler func(ctx context.Context, args []string)

// Command represents a registered command
type Command struct {
	Name        string         // Command name (without colon)
	Description string         // Short description for help
	Usage       string         // Usage example (e.g., "<id>")
	Handler     CommandHandler // The handler function
}

// CommandRegistry holds all registered commands
type CommandRegistry struct {
	commands map[string]*Command
}

// NewCommandRegistry creates a new command registry
func NewCommandRegistry() *CommandRegistry {
	return &CommandRegistry{
		commands: make(map[string]*Command),
	}
}

// Register adds a command to the registry
func (r *CommandRegistry) Register(cmd *Command) {
	r.commands[cmd.Name] = cmd
}

// Get retrieves a command by name
func (r *CommandRegistry) Get(name string) (*Command, bool) {
	cmd, ok := r.commands[name]
	return cmd, ok
}

// List returns all registered commands
func (r *CommandRegistry) List() []*Command {
	cmds := make([]*Command, 0, len(r.commands))
	for _, cmd := range r.commands {
		cmds = append(cmds, cmd)
	}
	return cmds
}

// commandRegistry is the global command registry for the session
var commandRegistry = NewCommandRegistry()

// init registers all commands declaratively
//
//nolint:gochecknoinits // global command registry requires init-time registration
func init() {
	// Session management commands
	commandRegistry.Register(&Command{
		Name:        "summarize",
		Description: "Summarize the conversation to reduce context",
		Usage:       "",
		Handler: func(_ context.Context, _ []string) {
			// Handler is resolved at runtime via Session method
		},
	})

	commandRegistry.Register(&Command{
		Name:        "cancel",
		Description: "Cancel the current task",
		Usage:       "",
		Handler: func(_ context.Context, _ []string) {
			// Handler is resolved at runtime via Session method
		},
	})

	commandRegistry.Register(&Command{
		Name:        "cancel_all",
		Description: "Cancel current task and clear the task queue",
		Usage:       "",
		Handler: func(_ context.Context, _ []string) {
			// Handler is resolved at runtime via Session method
		},
	})

	commandRegistry.Register(&Command{
		Name:        "save",
		Description: "Save the current session",
		Usage:       "[filename]",
		Handler: func(_ context.Context, _ []string) {
			// Handler is resolved at runtime via Session method
		},
	})

	// Model commands
	commandRegistry.Register(&Command{
		Name:        "model_set",
		Description: "Switch to a different model",
		Usage:       "<id>",
		Handler: func(_ context.Context, _ []string) {
			// Handler is resolved at runtime via Session method
		},
	})

	commandRegistry.Register(&Command{
		Name:        "model_load",
		Description: "Reload models from configuration file",
		Usage:       "",
		Handler: func(_ context.Context, _ []string) {
			// Handler is resolved at runtime via Session method
		},
	})

	// Task queue commands
	commandRegistry.Register(&Command{
		Name:        "taskqueue_get_all",
		Description: "List all queued tasks",
		Usage:       "",
		Handler: func(_ context.Context, _ []string) {
			// Handler is resolved at runtime via Session method
		},
	})

	commandRegistry.Register(&Command{
		Name:        "taskqueue_del",
		Description: "Delete a queued task",
		Usage:       "<queue_id>",
		Handler: func(_ context.Context, _ []string) {
			// Handler is resolved at runtime via Session method
		},
	})

	// Retry command
	commandRegistry.Register(&Command{
		Name:        "retry",
		Description: "Retry the last prompt (re-send if the latest message is from user)",
		Usage:       "",
		Handler: func(_ context.Context, _ []string) {
			// Handler is resolved at runtime via Session method
		},
	})
}

// GetCommandRegistry returns the global command registry
func GetCommandRegistry() *CommandRegistry {
	return commandRegistry
}

// DispatchCommand dispatches a command to the appropriate handler
// This is called by Session.handleCommandSync
func (s *Session) dispatchCommand(ctx context.Context, cmd string) bool {
	parts := strings.Fields(cmd)
	if len(parts) == 0 {
		s.writeError(domainerrors.ErrEmptyCommand.Error())
		return false
	}

	commandName := parts[0]
	args := parts[1:]

	// Check if command exists in registry
	if _, ok := commandRegistry.Get(commandName); !ok {
		return false
	}

	// Dispatch to the handler methods (defined in session.go)
	switch commandName {
	case "summarize":
		s.summarize(ctx)
	case "cancel":
		s.cancelTask()
	case "cancel_all":
		s.cancelAllTasks()
	case "save":
		s.saveSession(args)
	case "model_set":
		s.handleModelSet(args)
	case "model_load":
		s.handleModelLoad()
	case "taskqueue_get_all":
		s.handleTaskQueueGetAll()
	case "taskqueue_del":
		s.handleTaskQueueDel(args)
	case "retry":
		s.executeRetry(ctx)
	}

	return true
}
