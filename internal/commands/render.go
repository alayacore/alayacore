package commands

import (
	"encoding/json"
	"fmt"
)

// RenderResult renders a CO (command output) result as the one line an adapter
// shows for it, or "" when the command's effect is its own feedback.
//
// The command name comes from the adapter's own CI tracking — a CO carries only
// the id — and the fields come from the structured result, never from display
// text: how a result reads is the adapter's business, which is why the core
// sends a path and not a sentence. All three built-in adapters printed the same
// sentence for each command, so it is written once here. A result that grows a
// field is then one change, and no adapter can disagree with the others about
// what "saved" says.
//
// A command with no entry renders nothing: its effect (a task stopped, a setting
// changed) is visible on its own, and a generic confirmation would put a line on
// screen that says less than the absence of one.
func RenderResult(name string, result json.RawMessage) string {
	var data struct {
		Path      string `json:"path"`
		HistoryID uint64 `json:"history_id"`
		Server    string `json:"server"`
	}
	_ = json.Unmarshal(result, &data) // best-effort: a zero field renders as it is
	switch name {
	case CommandNameSave:
		return "Session saved to " + data.Path
	case CommandNameFork:
		return fmt.Sprintf("Session forked to %s (up to content ID %d)", data.Path, data.HistoryID)
	case CommandNameMCPConfirm:
		return fmt.Sprintf("MCP auth code received for %q.", data.Server)
	case CommandNameMCPDecline:
		return fmt.Sprintf("MCP authorization for %q declined.", data.Server)
	}
	return ""
}
