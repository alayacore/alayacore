package terminal

// The grammar of a submitted input line. A line is either a prompt or a
// ":"-command — that is the whole language — and deciding which, and splitting a
// command into a name and its arguments, is one function instead of a CutPrefix
// in the submit path and a second split in the command path.

import (
	"strings"

	"github.com/alayacore/alayacore/internal/commands"
)

// Submission is what a submitted line is. Sealed: only this package defines
// implementations.
type Submission interface{ isSubmission() }

// PromptSubmission is text to send to the model.
type PromptSubmission struct{ Text string }

func (PromptSubmission) isSubmission() {}

// CommandSubmission is a ":"-command. Raw is the text after the colon, trimmed —
// what the session receives when the command is not one the adapter handles
// itself. Name and Args are its split form, for matching the adapter-local
// commands.
type CommandSubmission struct {
	Raw  string
	Name string
	Args string
}

func (CommandSubmission) isSubmission() {}

// parseSubmission classifies one input line. The leading ':' (after trimming)
// makes it a command; anything else is prompt text. The command's name is its
// first whitespace-separated token and the rest is the arguments.
func parseSubmission(line string) Submission {
	trimmed := strings.TrimSpace(line)
	rest, found := strings.CutPrefix(trimmed, ":")
	if !found {
		return PromptSubmission{Text: trimmed}
	}
	rest = strings.TrimSpace(rest)
	name, args := commands.SplitCommand(rest)
	return CommandSubmission{Raw: rest, Name: name, Args: args}
}
