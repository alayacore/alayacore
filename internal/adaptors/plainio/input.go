package plainio

import (
	"bufio"
	"io"
	"os"
	"strings"

	"github.com/alayacore/alayacore/internal/stream"
)

// readPrompts reads lines from stdin and emits them as TLV messages.
// Lines ending with `\` are continued on the next line (backslash-escaped newline).
// Returns nil on EOF (Ctrl-D) or an error.
func readPrompts(input *stream.ChanInput, reader io.Reader) error {
	scanner := bufio.NewReader(reader)
	var prompt strings.Builder

	for {
		line, err := scanner.ReadString('\n')
		if err != nil {
			// EOF (Ctrl-D): emit any partial prompt, then close input
			if err == io.EOF {
				if prompt.Len() > 0 || len(line) > 0 {
					prompt.WriteString(line)
					text := strings.TrimRight(prompt.String(), "\n")
					if text != "" {
						_ = input.EmitTLV(stream.TagTextUser, text) //nolint:errcheck // best effort on EOF
					}
				}
				return nil
			}
			return err
		}

		// Check if line ends with backslash (escaped newline)
		trimmed := strings.TrimRight(line, "\n")
		if strings.HasSuffix(trimmed, "\\") {
			// Remove the trailing backslash and append, continue to next line
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

		if err := input.EmitTLV(stream.TagTextUser, text); err != nil {
			return err
		}
	}
}

// readPromptsFromStdin is a convenience wrapper that reads from os.Stdin.
func readPromptsFromStdin(input *stream.ChanInput) error {
	return readPrompts(input, os.Stdin)
}
