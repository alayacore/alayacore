package plainio

import (
	"bufio"
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

// errQuitPrompt is returned by readPrompts when the user types :quit or :q.
// The adapter sends the session's quit command first and then treats it as a
// clean exit request (code 0), like EOF — task errors never affect the exit
// code — unlike a stdin read error (code 1).
var errQuitPrompt = errors.New("quit")

// commandSeq generates unique command call IDs for CI frames.
var commandSeq atomic.Uint64

// commandNames maps command call IDs to command names, mirroring how tool
// names are tracked from AF frames: the request carries the name, the
// result (CO) carries only the ID, and the adapter correlates them.
var commandNames sync.Map // id → command name

// writeCommand sends a colon-command (e.g. ":save /tmp/x") as a CI frame.
// The adapter translates the human-facing text into {id, name, input}.
func writeCommand(input io.Writer, cmd string) error {
	name, args := commands.SplitCommand(strings.TrimPrefix(cmd, ":"))
	id := fmt.Sprintf("plain-%d", commandSeq.Add(1))
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

// readPrompts reads lines from stdin and emits them as TLV messages.
// Lines ending with `\` are continued on the next line (backslash-escaped newline).
// Returns nil on EOF (Ctrl-D), errQuitPrompt on :quit/:q (which is sent to the
// session as a quit command first), or a read/write error.
//
// gate, when non-nil, is consulted immediately before each *prompt* frame is
// written and blocks until the session is ready to accept one. Commands
// bypass it, so a user can still steer a still-initializing session
// (`:mcp_cancel`, `:quit`, …). A gate error stops the feed.
func readPrompts(input io.Writer, reader io.Reader, gate func() error) error {
	scanner := bufio.NewReader(reader)
	var prompt strings.Builder

	for {
		line, err := scanner.ReadString('\n')

		if err != nil {
			if err == io.EOF {
				if prompt.Len() > 0 || len(line) > 0 {
					prompt.WriteString(line)
					text := strings.TrimRight(prompt.String(), "\r\n")
					if text != "" {
						if err = sendPrompt(input, text, gate); err != nil {
							return err
						}
					}
				}
				return nil
			}
			return err
		}

		// Check if line ends with backslash (escaped newline)
		trimmed := strings.TrimRight(line, "\r\n")
		if strings.HasSuffix(trimmed, "\\") {
			prompt.WriteString(trimmed[:len(trimmed)-1])
			prompt.WriteString("\n")
			continue
		}

		// Complete prompt: accumulated + current line
		prompt.WriteString(trimmed)
		text := prompt.String()
		prompt.Reset()

		if text == "" {
			continue
		}

		// :quit/:q stop reading and ask the session to end. Ending the
		// session is the session's decision (it knows whether a task is
		// still running); the exit code stays the adapter's (0).
		if text == ":quit" || text == ":q" {
			_ = writeCommand(input, "quit") // best effort: the session may be gone
			return errQuitPrompt
		}

		if err := sendPrompt(input, text, gate); err != nil {
			return err
		}
	}
}

// sendPrompt writes a prompt to the TLV stream, followed by UE to flush.
// Commands (starting with ':') are sent as CI frames without UE and bypass
// gate — they do not start a task that needs MCP tools, and some of them
// exist precisely to steer MCP init. Returns the first write error, if any.
func sendPrompt(input io.Writer, text string, gate func() error) error {
	if strings.HasPrefix(text, ":") {
		return writeCommand(input, text)
	}
	if gate != nil {
		if err := gate(); err != nil {
			return err
		}
	}
	if err := tlv.WriteTLV(input, tlv.TagUserT, text); err != nil {
		return err
	}
	return tlv.WriteTLV(input, tlv.TagUserEnd, "")
}
