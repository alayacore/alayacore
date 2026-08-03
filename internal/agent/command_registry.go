package agent

// Command registry: define, register, and dispatch commands.
//
// Previously a flat package-level slice + lookupCommand function,
// now wrapped in a commandRegistry struct for cleaner encapsulation.
// The package-level lookupCommand still works via the default registry.

import (
	"context"

	"github.com/alayacore/alayacore/internal/commands"
)

// cmdPolicy specifies how and when a command is dispatched.
type cmdPolicy int

const (
	// cmdImmediate runs synchronously in the run() goroutine.
	// Safe to execute even while a task is streaming (e.g. :cancel, :save).
	cmdImmediate cmdPolicy = iota

	// cmdIdle runs synchronously but is rejected when a task is
	// in progress (e.g. :model_set changes state the task reads).
	cmdIdle
)

// commandHandler is a function that handles a command.
// args is everything after the first space (empty string if no args).
// It returns the structured result (serialized into CO output on success)
// and an error (serialized into a CmdError object on failure). The command
// call ID is transport-level state — handlers never see it.
type commandHandler func(s *Session, ctx context.Context, args string) (any, error)

// cmdErr is an error carrying a machine-readable code for the CO error
// object. Handlers may return plain errors (code defaults to "ERROR").
type cmdErr struct {
	Code    string
	Message string
}

func (e cmdErr) Error() string { return e.Message }

// command describes a user-facing command with its handler and metadata.
type command struct {
	Name        string
	Description string
	Usage       string
	Policy      cmdPolicy
	Handler     commandHandler
}

// commandRegistry manages the set of available commands.
type commandRegistry struct {
	commands map[string]command
}

// newCommandRegistry creates a registry pre-populated with all built-in commands.
func newCommandRegistry() *commandRegistry {
	cr := &commandRegistry{
		commands: make(map[string]command, len(defaultCommandDefs)),
	}
	for _, cmd := range defaultCommandDefs {
		cr.commands[cmd.Name] = cmd
	}
	return cr
}

// Lookup returns the command metadata for name, or (nil, false).
func (cr *commandRegistry) Lookup(name string) (*command, bool) {
	cmd, ok := cr.commands[name]
	if !ok {
		return nil, false
	}
	return &cmd, true
}

// lookupCommand is a package-level shorthand for the default registry.
func lookupCommand(name string) (*command, bool) {
	return defaultCommandRegistry.Lookup(name)
}

// defaultCommandRegistry is the package-level singleton.
var defaultCommandRegistry = newCommandRegistry()

// defaultCommandDefs is the list of all built-in commands. command names
// come from the shared commands package (single source of truth for the
// agent registry and adapter-side rendering).
var defaultCommandDefs = []command{
	{commands.CommandNameCancel, "Cancel the current task", "", cmdImmediate,
		func(s *Session, _ context.Context, _ string) (any, error) { return s.cancelTask() }},
	{commands.CommandNameSave, "Save the current session", "[filename]", cmdImmediate,
		func(s *Session, _ context.Context, args string) (any, error) { return s.saveSession(args) }},
	{commands.CommandNameModelSet, "Switch to a different model", "<id>", cmdIdle,
		func(s *Session, _ context.Context, args string) (any, error) { return s.handleModelSet(args) }},
	{commands.CommandNameModelLoad, "Reload models from configuration file", "", cmdIdle,
		func(s *Session, _ context.Context, _ string) (any, error) { return s.handleModelLoad() }},
	{commands.CommandNameModelSync, "Replace all models with edited content", "<content>", cmdIdle,
		func(s *Session, _ context.Context, args string) (any, error) { return s.handleModelSync(args) }},
	{commands.CommandNameReason, "Set reasoning level (0=off, 1=normal, 2=max)", "[0|1|2]", cmdIdle,
		func(s *Session, _ context.Context, args string) (any, error) { return s.handleReason(args) }},
	{commands.CommandNameThemeSet, "Set the active theme", "<name>", cmdImmediate,
		func(s *Session, _ context.Context, args string) (any, error) { return s.handleThemeSet(args) }},
	{commands.CommandNameToolConfirm, "Confirm a pending tool execution", "<id>", cmdImmediate,
		func(s *Session, _ context.Context, args string) (any, error) { return s.handleToolConfirmCmd(args) }},
	{commands.CommandNameToolDecline, "Decline a pending tool execution", "<id>", cmdImmediate,
		func(s *Session, _ context.Context, args string) (any, error) { return s.handleToolDeclineCmd(args) }},
	{commands.CommandNameFork, "Fork session up to content ID and save to file", "<id> <filename>", cmdImmediate,
		func(s *Session, _ context.Context, args string) (any, error) { return s.handleFork(args) }},
	{commands.CommandNameVideoConfig, "Set video FPS and resolution", "<fps> <0|1>", cmdIdle,
		func(s *Session, _ context.Context, args string) (any, error) { return s.handleVideoConfig(args) }},
	{commands.CommandNameMCPConfirm, "Confirm MCP OAuth authorization with auth code", "<server> <code> <redirect_uri>", cmdIdle,
		func(s *Session, ctx context.Context, args string) (any, error) { return s.handleMCPConfirm(ctx, args) }},
	{commands.CommandNameMCPDecline, "Decline MCP OAuth authorization", "<server>", cmdIdle,
		func(s *Session, _ context.Context, args string) (any, error) { return s.handleMCPDecline(args) }},
	{commands.CommandNameMCPSkip, "Cancel MCP initialization", "", cmdImmediate,
		func(s *Session, _ context.Context, _ string) (any, error) { return s.handleMCPCancel() }},
}
