package plainio

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync/atomic"

	"github.com/alayacore/alayacore/internal/protocol"
	"github.com/alayacore/alayacore/internal/tlv"
)

// commandSeq generates unique command call IDs for CI frames.
var commandSeq atomic.Uint64

// writeCommand sends a colon-command (e.g. ":save /tmp/x") as a CI frame.
// The adapter translates the human-facing text into {id, name, input}.
func writeCommand(input io.Writer, cmd string) error {
	name, args, _ := strings.Cut(strings.TrimPrefix(cmd, ":"), " ")
	payload, err := json.Marshal(protocol.CmdMsg{
		ID:    fmt.Sprintf("plain-%d", commandSeq.Add(1)),
		Name:  name,
		Input: args,
	})
	if err != nil {
		return err
	}
	return tlv.WriteTLV(input, tlv.TagCommandIn, string(payload))
}

// readPrompts reads lines from stdin and emits them as TLV messages.
// Lines ending with `\` are continued on the next line (backslash-escaped newline).
// Returns nil on EOF (Ctrl-D), a read error, or when done is closed.
// When done is closed, any line already buffered in bufio.Reader is discarded.
func readPrompts(done <-chan struct{}, input io.Writer, reader io.Reader) error {
	scanner := bufio.NewReader(reader)
	var prompt strings.Builder

	for {
		line, err := scanner.ReadString('\n')

		// Check for cancellation before processing any data.
		// This ensures buffered lines (bufio.Reader internal buffer)
		// are discarded when done is closed, even if the underlying
		// file descriptor was already closed.
		select {
		case <-done:
			return nil
		default:
		}

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
			return nil
		}

		if err := sendPrompt(input, text); err != nil {
			return err
		}
	}
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
