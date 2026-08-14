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
