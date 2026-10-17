// Package commands defines the command-plane vocabulary shared between
// the session core (internal/agent) and its UI adapters (terminal,
// plainio, terseio): the canonical command names carried in CI/CO frames.
// rawio clients use the same names when constructing CI frames.
//
// The agent owns command semantics and handlers (internal/agent/
// command_registry.go); adapters use these names to build CI frames and
// render CO results. Keeping the names here — instead of in protocol —
// reflects that commands are session-domain vocabulary, not wire format:
// the protocol only says a CI frame carries a name string, not which
// names are valid.
package commands

import "strings"

// Command name constants. These are the canonical values for the
// protocol.CmdMsg.Name field and the human-facing ":name" text (minus
// the colon). They are the single source of truth for both the agent
// registry and adapter-side rendering/sending.
const (
	// CommandNameQuit ends the session: no new work is accepted and the
	// session returns as soon as nothing is in flight, so a task already
	// running still finishes. ":q" is accepted as an alias (see Canonical).
	CommandNameQuit = "quit"

	CommandNameSummarize = "summarize"
	CommandNameCancel    = "cancel"
	CommandNameContinue  = "continue"
	CommandNameSave      = "save"
	CommandNameModelSet  = "model_set"
	CommandNameModelLoad = "model_load"
	CommandNameModelSync = "model_sync"
	// The setting's name plus the verb: the setters here are scope+verb
	// (model_set, theme_set), and the scope is reasoning_level — the name it has
	// in the frontmatter, on --reasoning-level, and in the SM that broadcasts it.
	// A shorter thing to type is an adapter's choice; it does not reach the wire.
	CommandNameReasoningLevelSet = "reasoning_level_set"
	CommandNameThemeSet          = "theme_set"
	CommandNameToolConfirm       = "tool_confirm"
	CommandNameToolDecline       = "tool_decline"
	CommandNameFork              = "fork"
	CommandNameVideoConfig       = "video_config"
	// The MCP auth trio: confirm one server's authorization, decline it, or
	// skip MCP initialization altogether. "skip" and not "cancel": :cancel
	// already means stopping the running task, and this name says which of the
	// two things is being given up.
	CommandNameMCPConfirm = "mcp_confirm"
	CommandNameMCPDecline = "mcp_decline"
	CommandNameMCPSkip    = "mcp_skip"
)

// shortForms maps what a user may type to the command it means. It is an
// adapter's table, resolved at the input boundary — where a typed line becomes
// a frame — and never on the wire: a CI frame carries the canonical name only,
// so an out-of-process client speaks one vocabulary and cannot depend on a
// nickname. Which words name a command is this package's business, so the table
// lives here and every adapter resolves from it rather than keeping its own.
//
// A short form is worth having when a command is typed often and its name is
// long (r for reasoning_level_set), or when a keystroke has no other way to say
// it (q for quit). A command with a keybinding does not earn one.
var shortForms = map[string]string{
	"q": CommandNameQuit,
	"r": CommandNameReasoningLevelSet,
}

// Canonical resolves a short form to its command name; anything else is
// returned unchanged, so applying it to every name is safe.
func Canonical(name string) string {
	if canonical, ok := shortForms[name]; ok {
		return canonical
	}
	return name
}

// ShortForms returns a copy of the table, for a check that every short form
// resolves to a command the session dispatches.
func ShortForms() map[string]string {
	out := make(map[string]string, len(shortForms))
	for short, name := range shortForms {
		out[short] = name
	}
	return out
}

// SplitCommand splits a command string into its name and argument tail at
// the FIRST whitespace (space, tab, CR, LF), trimming the separator from
// the args. "save" → ("save", ""); "save\t/tmp/x" → ("save", "/tmp/x").
// Whitespace INSIDE the arguments (e.g. a multi-line command argument in
// terseio) is preserved — only the first separator matters.
func SplitCommand(cmd string) (name, args string) {
	name = cmd
	if i := strings.IndexAny(cmd, " \t\r\n"); i >= 0 {
		name = cmd[:i]
		args = strings.TrimLeft(cmd[i:], " \t\r\n")
	}
	return name, args
}
