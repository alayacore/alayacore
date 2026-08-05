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

// errQuitPrompt is returned by readAllPrompt when stdin is ":quit" or
// ":q". Like plainio, these are transport-level controls (there is no
// quit command in the session registry) and mean a clean exit (code 0).
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
	name, args := cmd, ""
	if i := strings.IndexAny(cmd, " \t\r\n"); i >= 0 {
		name = cmd[:i]
		args = strings.TrimLeft(cmd[i:], " \t\r\n")
	}
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

// sendCancel writes a CI cancel command frame — the same command a user
// would type as ":cancel". It is used by the SIGINT handler (adapter.go)
// so Ctrl-C cancels the running task (and its tool processes) instead of
// killing the process. Unlike plainio's writeCommand, terseio's expects
// the name without the ":" prefix (the caller strips it).
func sendCancel(input io.Writer) error {
	return writeCommand(input, commands.CommandNameCancel)
}

// readAllPrompt reads the entire reader and emits it as ONE TLV message:
//   - stdin starting with ":" (after trimming trailing newlines) is sent
//     as a single CI command frame — the WHOLE input is the command,
//     including newlines in the argument text (":continue" works, so do
//     ":save", ":cancel", ...).
//   - ":quit" / ":q" are intercepted locally (clean exit, code 0).
//   - anything else is sent as one UT + UE prompt pair.
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
			return errQuitPrompt
		}
		return writeCommand(input, strings.TrimPrefix(text, ":"))
	}
	if err := tlv.WriteTLV(input, tlv.TagUserT, text); err != nil {
		return err
	}
	return tlv.WriteTLV(input, tlv.TagUserEnd, "")
}
