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
// The adapter treats it as a clean exit request (code 0), like EOF — task
// errors never affect the exit code — unlike a stdin read error (code 1).
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
	name, args, _ := strings.Cut(strings.TrimPrefix(cmd, ":"), " ")
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
// Returns nil on EOF (Ctrl-D), errQuitPrompt on :quit/:q, or a read/write error.
func readPrompts(input io.Writer, reader io.Reader) error {
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
						if err = sendPrompt(input, text); err != nil {
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

		// Intercept :quit/:q — handled locally, not by the session
		if text == ":quit" || text == ":q" {
			return errQuitPrompt
		}

		if err := sendPrompt(input, text); err != nil {
			return err
		}
	}
}

// sendCancel writes a CI cancel command frame — the same command a user
// would type as ":cancel". It is used by the SIGINT handler (adapter.go)
// so Ctrl-C cancels the running task (and its tool processes) instead of
// killing the process.
func sendCancel(input io.Writer) error {
	return writeCommand(input, ":"+commands.CommandNameCancel)
}

// sendPrompt writes a prompt to the TLV stream, followed by UE to flush.
// Commands (starting with ':') are sent as CI frames without UE.
// Returns the first write error, if any.
func sendPrompt(input io.Writer, text string) error {
	if strings.HasPrefix(text, ":") {
		return writeCommand(input, text)
	}
	if err := tlv.WriteTLV(input, tlv.TagUserT, text); err != nil {
		return err
	}
	return tlv.WriteTLV(input, tlv.TagUserEnd, "")
}
