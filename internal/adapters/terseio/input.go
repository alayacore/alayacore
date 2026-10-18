package terseio

import (
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/alayacore/alayacore/internal/commands"
	"github.com/alayacore/alayacore/internal/protocol"
	"github.com/alayacore/alayacore/internal/wire"
)

// errQuitPrompt is returned by readAllPrompt when stdin is ":quit" or ":q".
// The adapter sends the session's quit command first and then stops reading;
// the exit code stays 0 (a clean exit, like EOF).
var errQuitPrompt = errors.New("quit")

// terseCommands is this adapter's CI writer: a typed command becomes a frame
// here, and the output side reads that command's name back when its CO arrives
// (output.go), since a CO carries only the id. See commands.Writer.
//
// The name/args split happens at the FIRST whitespace (space, tab, newline), the
// same rule plainio's line-based reader gets: terseio's input can span several
// lines, so a newline is just another separator rather than the end of a prompt.
var terseCommands = commands.NewWriter("terse")

// readAllPrompt reads the entire reader and emits it as ONE frame:
//   - stdin starting with ":" (after trimming trailing newlines) is sent
//     as a single CI command frame — the WHOLE input is the command,
//     including newlines in the argument text (":continue" works, so do
//     ":save", ":cancel", ...).
//   - ":quit" / ":q" ask the session to end (the quit command is sent
//     first) and stop reading: clean exit, code 0.
//   - anything else is sent as one UX + UE prompt pair.
//
// Nothing here waits for the session: a prompt that arrives before MCP
// initialization settles is accepted and held by the session, which is the only
// side that can tell whether a refusal would leave the client with no way to
// retry (a pipe at EOF cannot type its prompt again).
//
// Trailing newlines are trimmed (a prompt piped from echo/printf or a
// file usually ends with "\n"). Empty input emits nothing.
func readAllPrompt(input io.Writer, reader io.Reader) error {
	data, err := io.ReadAll(reader)
	if err != nil {
		return err
	}
	text := strings.TrimRight(string(data), "\r\n")
	if text == "" {
		return nil
	}
	if strings.HasPrefix(text, ":") {
		if text == ":quit" || text == ":q" {
			_, _ = terseCommands.Write(input, "quit") // best effort: the session may be gone
			return errQuitPrompt
		}
		_, cmdErr := terseCommands.Write(input, text)
		return cmdErr
	}
	body, err := json.Marshal(protocol.ContentData{ContentItem: protocol.ContentItem{Type: protocol.TypeText, Text: text}})
	if err != nil {
		return err
	}
	if err := wire.WriteFrame(input, wire.TagUserX, string(body)); err != nil {
		return err
	}
	return wire.WriteFrame(input, wire.TagUserEnd, "")
}
