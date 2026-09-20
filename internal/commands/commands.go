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

	CommandNameSummarize   = "summarize"
	CommandNameCancel      = "cancel"
	CommandNameContinue    = "continue"
	CommandNameSave        = "save"
	CommandNameModelSet    = "model_set"
	CommandNameModelLoad   = "model_load"
	CommandNameModelSync   = "model_sync"
	CommandNameReason      = "reason"
	CommandNameThemeSet    = "theme_set"
	CommandNameToolConfirm = "tool_confirm"
	CommandNameToolDecline = "tool_decline"
	CommandNameFork        = "fork"
	CommandNameVideoConfig = "video_config"
	CommandNameMCPConfirm  = "mcp_confirm"
	CommandNameMCPDecline  = "mcp_decline"
	CommandNameMCPSkip     = "mcp_cancel"
)

// commandAliases maps the aliases users may type to their canonical command
// name. Which words name a command is this package's business, so the
// resolution lives here: neither the agent's registry nor the adapters have
// to keep a copy of this table.
var commandAliases = map[string]string{
	"q": CommandNameQuit,
}

// Canonical resolves an alias to its canonical command name; a name that is
// not an alias is returned unchanged. Applying it to every command name is
// safe — command names that carry no alias are their own canonical form.
func Canonical(name string) string {
	if canonical, ok := commandAliases[name]; ok {
		return canonical
	}
	return name
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
