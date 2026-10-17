package commands

// The command vocabulary: what each command is called, what it does, and what
// arguments it takes. The session owns the handlers and their policy
// (internal/agent/command_registry.go); an adapter that shows a list of commands
// reads this table, so the two cannot disagree and neither keeps a copy.
//
// Defs is the order a command list should show — most-used first, the
// housekeeping last — not the dispatcher's order, which is a lookup and does
// not care.
type Definition struct {
	Name        string
	Description string
	Usage       string // "" when the command takes no arguments
}

// Defs is every command a session dispatches. The two task commands (:continue,
// :summarize) are here and not in the agent's handler table: the dispatcher
// starts them before it reaches the registry, but they are commands an adapter
// sends and a help list shows.
var Defs = []Definition{
	{CommandNameMCPConfirm, "Confirm MCP OAuth authorization", "<server> <code> <redirect_uri> [iss]"},
	{CommandNameMCPDecline, "Decline MCP OAuth authorization", "<server>"},
	{CommandNameMCPSkip, "Skip MCP initialization", ""},
	{CommandNameToolConfirm, "Confirm pending tool execution", "<call_id>"},
	{CommandNameToolDecline, "Decline pending tool execution", "<call_id>"},
	{CommandNameContinue, "Retry last prompt", ""},
	{CommandNameReasoningLevelSet, "Set reasoning level", "<0|1|2>"},
	{CommandNameCancel, "Cancel current task", ""},
	{CommandNameSummarize, "Summarize/compress history", ""},
	{CommandNameThemeSet, "Switch theme by name", "<name>"},
	{CommandNameModelSet, "Switch model by ID", "<id>"},
	{CommandNameModelLoad, "Reload model config", ""},
	{CommandNameModelSync, "Apply edited model config", "<content>"},
	{CommandNameSave, "Save session", "[filename]"},
	{CommandNameFork, "Fork session up to a history ID", "<history_id> <filename>"},
	{CommandNameVideoConfig, "Set video FPS and resolution", "<fps> <res>"},
	{CommandNameQuit, "End the session", ""},
}

// DefinitionOf returns the definition of name, if it is a command.
func DefinitionOf(name string) (Definition, bool) {
	for _, d := range Defs {
		if d.Name == name {
			return d, true
		}
	}
	return Definition{}, false
}

// UsageLine renders the one-line usage a command's INVALID_ARGS message carries.
// The handler does not spell it: the argument shape is Defs', and a handler that
// repeated it drifted (":tool_confirm <id>" against a `<call_id>` in the table).
// A command that explains its values appends the legend to this line.
func UsageLine(name string) string {
	def, ok := DefinitionOf(name)
	if !ok {
		return "usage: :" + name
	}
	if def.Usage == "" {
		return "usage: :" + def.Name
	}
	return "usage: :" + def.Name + " " + def.Usage
}
