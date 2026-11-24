package agent

// The command table: what the session can dispatch.
//
// One table, because a session has one. A registry object wrapping it bought
// nothing — there is no second set of commands to keep apart, and no state to
// own — so the definitions are a slice and the lookup is a map built from it.
// What a command is *called* and what arguments it takes is not here: that is
// commands.Defs, which an adapter shows too (see command's own comment).

import (
	"context"

	"github.com/alayacore/alayacore/internal/commands"
)

// Every command here runs synchronously in the run() goroutine. There is no
// second policy: the one thing a command could not do mid-task — replace the
// model — is now safe, because the replacement is published as a snapshot and a
// task holds the one it started with (see modelRuntime and session_model.go).
// A user switching models while an answer streams is not a race the dispatcher
// has to refuse; it is a switch that lands on the next prompt, and the session
// says so in a notify.
//
// commandHandler is a function that handles a command.
// args is everything after the first space (empty string if no args).
// It returns the structured result (serialized into CO result on success)
// and an error (serialized into a CmdError object on failure). The command
// ID is transport-level state — handlers never see it.
type commandHandler func(s *Session, ctx context.Context, args string) (any, error)

// cmdErr is an error carrying a machine-readable code for the CO error
// object. Handlers may return plain errors (code defaults to
// protocol.ErrCodeInternalUnclassified — every refusal in this tree names its
// cause, so that fallback is a bug, not a code to branch on).
type cmdErr struct {
	Code    string
	Message string
}

func (e cmdErr) Error() string { return e.Message }

// command is the session's half of a command: its handler and when that handler
// may run. The name, description and usage are vocabulary
// (commands.Defs) — an adapter shows the same table, and one of the two keeping
// a copy is how the help list drifted from the registry.
type command struct {
	Name    string
	Handler commandHandler
}

// lookupCommand returns the handler for name, or false when the name is not a
// command this session dispatches.
func lookupCommand(name string) (command, bool) {
	cmd, ok := commandHandlers[name]
	return cmd, ok
}

// commandHandlers is commandDefs indexed by name, built once. The two cannot
// disagree: the map is derived from the slice, and the slice is the one list.
var commandHandlers = func() map[string]command {
	handlers := make(map[string]command, len(commandDefs))
	for _, cmd := range commandDefs {
		handlers[cmd.Name] = cmd
	}
	return handlers
}()

// commandDefs is every command the session dispatches, with its handler. The
// names come from the shared commands package — the same table an
// adapter renders — so neither side can introduce a spelling of its own.
var commandDefs = []command{
	{commands.CommandNameQuit,
		func(s *Session, _ context.Context, args string) (any, error) { return s.handleQuit(args) }},
	{commands.CommandNameCancel,
		func(s *Session, _ context.Context, args string) (any, error) {
			fate, err := cancelSteeringFate(args)
			if err != nil {
				return nil, err
			}
			return s.cancelTask(fate)
		}},
	{commands.CommandNameSave,
		func(s *Session, _ context.Context, args string) (any, error) { return s.saveSession(args) }},
	{commands.CommandNameModelSet,
		func(s *Session, _ context.Context, args string) (any, error) { return s.handleModelSet(args) }},
	{commands.CommandNameModelLoad,
		func(s *Session, _ context.Context, _ string) (any, error) { return s.handleModelLoad() }},
	{commands.CommandNameReasoningLevelSet,
		func(s *Session, _ context.Context, args string) (any, error) { return s.handleReasoningLevelSet(args) }},
	{commands.CommandNameThemeSet,
		func(s *Session, _ context.Context, args string) (any, error) { return s.handleThemeSet(args) }},
	{commands.CommandNameToolConfirm,
		func(s *Session, _ context.Context, args string) (any, error) { return s.handleToolConfirmCmd(args) }},
	{commands.CommandNameToolDecline,
		func(s *Session, _ context.Context, args string) (any, error) { return s.handleToolDeclineCmd(args) }},
	{commands.CommandNameFork,
		func(s *Session, _ context.Context, args string) (any, error) { return s.handleFork(args) }},
	{commands.CommandNameVideoConfig,
		func(s *Session, _ context.Context, args string) (any, error) { return s.handleVideoConfig(args) }},
	{commands.CommandNameMCPConfirm,
		func(s *Session, ctx context.Context, args string) (any, error) { return s.handleMCPConfirm(ctx, args) }},
	{commands.CommandNameMCPDecline,
		func(s *Session, _ context.Context, args string) (any, error) { return s.handleMCPDecline(args) }},
	{commands.CommandNameMCPSkip,
		func(s *Session, _ context.Context, _ string) (any, error) { return s.handleMCPSkip() }},
}
