package commands

import "strings"

// Arg is one argument a command declares. It is a value rather than a word in a
// sentence, and it has no default kind: declaring an argument means saying
// whether it is one token or the rest of the line. That is what makes the shape
// enforceable — a command cannot be added with an argument whose reading nobody
// decided, which is how `:fork <history_id> <filename>` came to truncate
// filenames at the first space while every document agreed with it.
type Arg struct {
	label    string
	rest     bool
	optional bool
}

// Token declares an argument that ends at the first whitespace.
func Token(label string) Arg { return Arg{label: label} }

// Rest declares an argument that takes the remainder of the line, spaces
// included: a filename, a theme name, a JSON blob. Only the last argument may
// be one, which Split enforces.
func Rest(label string) Arg { return Arg{label: label, rest: true} }

// Optional declares an argument that may be absent. Optional arguments come
// last, which Split enforces.
func Optional(a Arg) Arg { a.optional = true; return a }

// Definition is one command's vocabulary: what it is called, what it does, and
// what arguments it takes. The session owns the handler and its policy
// (internal/agent/command_registry.go); an adapter that shows a list of commands
// reads this table, and the dispatcher refuses a request whose tail does not
// match it, so the three cannot disagree and none keeps a copy.
type Definition struct {
	Name        string
	Description string
	Args        []Arg
	// Hint explains what a value means, appended to the usage line a refusal
	// carries ("(0=off, 1=normal, 2=max)"). It is not part of the shape: an
	// adapter's command list shows Signature instead.
	Hint string
}

// def builds a table entry whose argument shape is the reading of the call.
func def(name, description string, args ...Arg) Definition {
	return Definition{Name: name, Description: description, Args: args}
}

// withHint adds the legend a refusal carries after the usage line.
func (d Definition) withHint(hint string) Definition {
	d.Hint = hint
	return d
}

// Signature renders the declared shape the way a usage line shows it: "<a> [b]",
// or "" when the command takes no arguments. It is derived from Args, so the
// prose a user reads cannot drift from the shape the dispatcher enforces.
func (d Definition) Signature() string {
	if len(d.Args) == 0 {
		return ""
	}
	parts := make([]string, len(d.Args))
	for i, a := range d.Args {
		if a.optional {
			parts[i] = "[" + a.label + "]"
		} else {
			parts[i] = "<" + a.label + ">"
		}
	}
	return strings.Join(parts, " ")
}

// Min is the fewest arguments the shape allows — the count of required ones.
func (d Definition) Min() int {
	required := 0
	for _, a := range d.Args {
		if !a.optional {
			required++
		}
	}
	return required
}

// Max is the most arguments the shape allows. A rest argument counts as one:
// it takes the remainder of the line, not a number of words.
func (d Definition) Max() int { return len(d.Args) }

// EndsWithRest reports whether the last argument takes the remainder of the
// line. Such a command has no "too many words" case: the extra words are its
// last argument.
func (d Definition) EndsWithRest() bool {
	return len(d.Args) > 0 && d.Args[len(d.Args)-1].rest
}

// Split reads args into one value per declared argument, reporting whether the
// tail has the declared shape. Absent optional arguments come back as "" — a
// declared value is never empty (a token is a non-empty run, and an empty rest
// counts as absent).
//
// It is the one place the shape is interpreted: the dispatcher asks it whether
// to refuse, and a handler that wants its arguments split asks it for the
// values, so neither invents its own reading.
func (d Definition) Split(args string) ([]string, bool) {
	values := make([]string, len(d.Args))
	rest := strings.Trim(args, cmdSpace)
	for i, a := range d.Args {
		if rest == "" {
			if a.optional {
				return values, true
			}
			return nil, false
		}
		if a.rest {
			if i != len(d.Args)-1 {
				return nil, false // only the last argument may take the remainder
			}
			values[i] = rest
			return values, true
		}
		head, tail := splitFirst(rest)
		values[i] = head
		rest = tail
	}
	if rest != "" {
		return nil, false // more words than the command declares
	}
	return values, true
}

// Values reads a command's arguments by name, one per declared argument. It is
// for a handler whose request the dispatcher has already vetted — reading, not
// checking. A value the shape does not provide comes back empty, which the
// handler's own value checks then reject; it never returns a short slice, so a
// handler can index the arguments it declared.
func Values(name, args string) []string {
	d, ok := DefinitionOf(name)
	if !ok {
		return nil
	}
	values, _ := d.Split(args)
	if values == nil {
		values = make([]string, len(d.Args))
	}
	return values
}

// Defs is every command a session dispatches, in the order a command list should
// show — most-used first, the housekeeping last — which is not the dispatcher's
// order: that is a lookup and does not care.
//
// The two task commands (:continue, :summarize) are here and not in the agent's
// handler table: the dispatcher starts them before it reaches the registry, but
// they are commands an adapter sends and a help list shows.
var Defs = []Definition{
	def(CommandNameMCPConfirm, "Confirm MCP OAuth authorization",
		Token("server"), Token("code"), Token("redirect_uri"), Optional(Token("iss"))),
	def(CommandNameMCPDecline, "Decline MCP OAuth authorization", Token("server")),
	def(CommandNameMCPSkip, "Skip MCP initialization"),
	def(CommandNameToolConfirm, "Confirm pending tool execution", Token("call_id")),
	def(CommandNameToolDecline, "Decline pending tool execution", Token("call_id")),
	def(CommandNameContinue, "Retry last prompt"),
	def(CommandNameReasoningLevelSet, "Set reasoning level", Token("0|1|2")).
		withHint("(0=off, 1=normal, 2=max)"),
	def(CommandNameCancel, "Cancel current task"),
	def(CommandNameSummarize, "Summarize/compress history"),
	def(CommandNameThemeSet, "Switch theme by name", Rest("name")),
	def(CommandNameModelSet, "Switch model by ID", Token("id")),
	def(CommandNameModelLoad, "Reload model config"),
	def(CommandNameModelSync, "Apply edited model config", Rest("content")),
	def(CommandNameSave, "Save session", Optional(Rest("filename"))),
	def(CommandNameFork, "Fork session up to a history ID", Token("history_id"), Rest("filename")),
	def(CommandNameVideoConfig, "Set video FPS and resolution", Token("fps"), Token("res")).
		withHint("(fps: positive integer, res: 0=default, 1=max)"),
	def(CommandNameQuit, "End the session"),
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

// UsageLine renders the one-line usage a command's REQUEST_INVALID_ARGS message
// carries. The handler does not spell it: the argument shape is Defs', and a
// handler that repeated it drifted (":tool_confirm <id>" against a `<call_id>`
// in the table).
func UsageLine(name string) string {
	def, ok := DefinitionOf(name)
	if !ok {
		return "usage: :" + name
	}
	line := "usage: :" + def.Name
	if sig := def.Signature(); sig != "" {
		line += " " + sig
	}
	if def.Hint != "" {
		line += "  " + def.Hint
	}
	return line
}
