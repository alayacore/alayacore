package terseio

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/alayacore/alayacore/internal/commands"
	"github.com/alayacore/alayacore/internal/protocol"
	"github.com/alayacore/alayacore/internal/tlv"
)

// errQuitPrompt is returned by readAllPrompt when stdin is ":quit" or ":q".
// The adapter sends the session's quit command first and then stops reading;
// the exit code stays 0 (a clean exit, like EOF).
var errQuitPrompt = errors.New("quit")

// commandSeq generates unique command call IDs for CI frames.
var commandSeq atomic.Uint64

// commandNames maps command call IDs to command names, mirroring how tool
// names are tracked from AF frames: the request carries the name, the
// result (CO) carries only the ID, and the output adapter correlates them.
var commandNames sync.Map // id → command name

// writeCommand sends a colon-command (e.g. ":save /tmp/x") as a CI frame.
// The adapter translates the human-facing text into {id, name, input}.
// The name/args split happens at the FIRST whitespace (space, tab, or
// newline) — unlike plainio (line-based, splits at " "), terseio's input
// can span multiple lines, so the whole stdin after ":" is the command
// and a newline is just another separator.
func writeCommand(input io.Writer, cmd string) error {
	name, args := commands.SplitCommand(cmd)
	id := fmt.Sprintf("terse-%d", commandSeq.Add(1))
	payload, err := json.Marshal(protocol.CmdMsg{
		ID:    id,
		Name:  name,
		Input: args,
	})
	if err != nil {
		return err
	}
	commandNames.Store(id, name)
	return tlv.WriteTLV(input, tlv.TagCommandIn, string(payload))
}

// readAllPrompt reads the entire reader and emits it as ONE TLV message:
//   - stdin starting with ":" (after trimming trailing newlines) is sent
//     as a single CI command frame — the WHOLE input is the command,
//     including newlines in the argument text (":continue" works, so do
//     ":save", ":cancel", ...).
//   - ":quit" / ":q" ask the session to end (the quit command is sent
//     first) and stop reading: clean exit, code 0.
//   - anything else is sent as one UT + UE prompt pair.
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
			_ = writeCommand(input, "quit") // best effort: the session may be gone
			return errQuitPrompt
		}
		return writeCommand(input, strings.TrimPrefix(text, ":"))
	}
	if err := tlv.WriteTLV(input, tlv.TagUserT, text); err != nil {
		return err
	}
	return tlv.WriteTLV(input, tlv.TagUserEnd, "")
}
