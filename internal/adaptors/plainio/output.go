package plainio

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/alayacore/alayacore/internal/stream"
)

// toolCallData represents a tool call (FC tag payload).
type toolCallData struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Input string `json:"input"`
}

// toolResultData represents a tool result (FR tag payload).
type toolResultData struct {
	ID     string `json:"id"`
	Output string `json:"output"`
}

// systemInfo mirrors the SystemInfo JSON from the agent package.
type systemInfo struct {
	InProgress bool `json:"in_progress"`
}

// stdoutOutput implements stream.Output.
// It parses TLV messages and prints human-readable text to stdout.
type stdoutOutput struct {
	mu         sync.Mutex
	writer     io.Writer
	buf        []byte
	inProgress bool
	textOnly   bool
}

func newStdoutOutput(textOnly bool) *stdoutOutput {
	return &stdoutOutput{
		writer:   os.Stdout,
		textOnly: textOnly,
	}
}

func (o *stdoutOutput) Write(p []byte) (int, error) {
	o.mu.Lock()
	o.buf = append(o.buf, p...)
	o.processBuffer()
	o.mu.Unlock()
	return len(p), nil
}

func (o *stdoutOutput) WriteString(s string) (int, error) {
	return o.Write([]byte(s))
}

func (o *stdoutOutput) Flush() error {
	return nil
}

// processBuffer parses and prints complete TLV frames from the buffer.
func (o *stdoutOutput) processBuffer() {
	for len(o.buf) >= 6 {
		tag := string(o.buf[0:2])
		length := int(binary.BigEndian.Uint32(o.buf[2:6]))
		if len(o.buf) < 6+length {
			break
		}
		value := string(o.buf[6 : 6+length])
		o.buf = o.buf[6+length:]
		o.printMessage(tag, value)
	}
}

func (o *stdoutOutput) printMessage(tag string, value string) {
	switch tag {
	case stream.TagTextAssistant:
		content := stripStreamID(value)
		fmt.Fprint(o.writer, content)

	case stream.TagTextReasoning:
		if o.textOnly {
			return
		}
		content := stripStreamID(value)
		fmt.Fprint(o.writer, content)

	case stream.TagTextUser:
		if o.textOnly {
			return
		}
		fmt.Fprintf(o.writer, "\n> %s\n", value)

	case stream.TagSystemError:
		if o.textOnly {
			return
		}
		fmt.Fprintf(o.writer, "\nError: %s\n", value)

	case stream.TagSystemNotify:
		if o.textOnly {
			return
		}
		fmt.Fprintf(o.writer, "\n[%s]\n", value)

	case stream.TagFunctionCall:
		if o.textOnly {
			return
		}
		var tc toolCallData
		if err := json.Unmarshal([]byte(value), &tc); err == nil {
			formatted := formatToolCall(tc.Name, tc.Input)
			fmt.Fprintf(o.writer, "\n%s\n", formatted)
		}

	case stream.TagFunctionResult:
		if o.textOnly {
			return
		}
		var tr toolResultData
		if err := json.Unmarshal([]byte(value), &tr); err == nil {
			fmt.Fprint(o.writer, tr.Output)
		}

	case stream.TagFunctionState:
		// Skip state indicators for plainio

	case stream.TagSystemData:
		o.handleSystemData(value)

	default:
		if o.textOnly {
			return
		}
		fmt.Fprintf(o.writer, "[%s] %s\n", tag, value)
	}
}

// handleSystemData detects task completion transitions and prints a trailing newline.
func (o *stdoutOutput) handleSystemData(value string) {
	var info systemInfo
	if err := json.Unmarshal([]byte(value), &info); err != nil {
		return
	}
	if o.inProgress && !info.InProgress {
		fmt.Fprintln(o.writer)
	}
	o.inProgress = info.InProgress
}

// stripStreamID removes the "[:id:]content" prefix from streaming deltas.
func stripStreamID(value string) string {
	if !strings.HasPrefix(value, "[:") {
		return value
	}
	endIdx := strings.Index(value, ":]")
	if endIdx == -1 {
		return value
	}
	return value[endIdx+2:]
}

// formatToolCall formats a tool call for display.
func formatToolCall(name, input string) string {
	switch name {
	case "posix_shell":
		var args struct {
			Command string `json:"command"`
		}
		if err := json.Unmarshal([]byte(input), &args); err == nil {
			return fmt.Sprintf("[%s: %s]", name, args.Command)
		}
	case "read_file":
		var args struct {
			Path      string `json:"path"`
			StartLine string `json:"start_line"`
			EndLine   string `json:"end_line"`
		}
		if err := json.Unmarshal([]byte(input), &args); err == nil {
			parts := []string{args.Path}
			if args.StartLine != "" {
				parts = append(parts, args.StartLine)
			}
			if args.EndLine != "" {
				parts = append(parts, args.EndLine)
			}
			return fmt.Sprintf("[%s: %s]", name, strings.Join(parts, ", "))
		}
	case "write_file":
		var args struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		}
		if err := json.Unmarshal([]byte(input), &args); err == nil {
			return fmt.Sprintf("[%s: %s]", name, args.Path)
		}
	case "edit_file":
		var args struct {
			Path      string `json:"path"`
			OldString string `json:"old_string"`
			NewString string `json:"new_string"`
		}
		if err := json.Unmarshal([]byte(input), &args); err == nil {
			return fmt.Sprintf("[%s: %s]", name, args.Path)
		}
	case "activate_skill":
		var args struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal([]byte(input), &args); err == nil {
			return fmt.Sprintf("[%s: %s]", name, args.Name)
		}
	}
	return fmt.Sprintf("[%s]", name)
}
