package plainio

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/alayacore/alayacore/internal/commands"
	"github.com/alayacore/alayacore/internal/protocol"
	"github.com/alayacore/alayacore/internal/wire"
)

// errQuitPrompt is returned by readPrompts when the user types :quit or :q.
// The adapter sends the session's quit command first and then treats it as a
// clean exit request (code 0), like EOF — task errors never affect the exit
// code — unlike a stdin read error (code 1).
var errQuitPrompt = errors.New("quit")

// plainCommands is this adapter's CI writer: a typed line becomes a frame here,
// and the output side reads that command's name back when its CO arrives
// (output.go), since a CO carries only the id. See commands.Writer.
var plainCommands = commands.NewWriter("plain")

// readPrompts reads lines from stdin and emits them as frames.
// Lines ending with `\` are continued on the next line (backslash-escaped newline).
// Returns nil on EOF (Ctrl-D), errQuitPrompt on :quit/:q (which is sent to the
// session as a quit command first), or a read/write error.
//
// Nothing here waits for the session: a prompt that arrives before MCP
// initialization settles is accepted and held by the session, which is the only
// side that can tell whether a refusal would leave the client with no way to
// retry (a pipe at EOF cannot type its prompt again).
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

		// :quit/:q stop reading and ask the session to end. Ending the
		// session is the session's decision (it knows whether a task is
		// still running); the exit code stays the adapter's (0).
		if text == ":quit" || text == ":q" {
			_, _ = plainCommands.Write(input, "quit") // best effort: the session may be gone
			return errQuitPrompt
		}

		if err := sendPrompt(input, text); err != nil {
			return err
		}
	}
}

// sendPrompt writes a prompt to the frame stream, followed by UE to flush.
// Commands (starting with ':') are sent as CI frames without UE — they do not
// start a task that needs MCP tools, and some of them exist precisely to steer
// MCP init. Returns the first write error, if any.
func sendPrompt(input io.Writer, text string) error {
	if strings.HasPrefix(text, ":") {
		_, err := plainCommands.Write(input, text)
		return err
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
