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
	mu               sync.Mutex
	writer           io.Writer
	buf              []byte
	inProgress       bool
	textOnly         bool
	hasError         bool
	errorOnce        sync.Once
	errorCh          chan struct{} // closed on first SE tag
	lastStreamPrefix string        // tracks last stream ID prefix for newline separation
}

func newStdoutOutput(textOnly bool) *stdoutOutput {
	return &stdoutOutput{
		writer:   os.Stdout,
		textOnly: textOnly,
		errorCh:  make(chan struct{}),
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

// WaitForError blocks until an SE (system error) tag is received.
// Returns immediately if an error has already been recorded.
func (o *stdoutOutput) WaitForError() {
	<-o.errorCh
}

// HasError returns true if any SE tag was ever received.
func (o *stdoutOutput) HasError() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.hasError
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
	// In text-only mode, only show assistant text and user prompts.
	// Still track task completion internally.
	if o.textOnly && tag != stream.TagTextAssistant && tag != stream.TagTextUser {
		if tag == stream.TagSystemData {
			o.handleSystemData(value)
		}
		if tag == stream.TagSystemError {
			o.hasError = true
			o.errorOnce.Do(func() { close(o.errorCh) })
		}
		return
	}

	switch tag {
	case stream.TagTextAssistant:
		prefix, content := extractStreamPrefix(value)
		if o.lastStreamPrefix != "" && prefix != o.lastStreamPrefix {
			fmt.Fprintln(o.writer)
		}
		o.lastStreamPrefix = prefix
		fmt.Fprint(o.writer, content)

	case stream.TagTextReasoning:
		prefix, content := extractStreamPrefix(value)
		if o.lastStreamPrefix != "" && prefix != o.lastStreamPrefix {
			fmt.Fprintln(o.writer)
		}
		o.lastStreamPrefix = prefix
		fmt.Fprint(o.writer, content)

	case stream.TagTextUser:
		fmt.Fprintf(o.writer, "\n> %s\n", value)
		o.lastStreamPrefix = ""

	case stream.TagSystemError:
		fmt.Fprintf(o.writer, "\nError: %s\n", value)
		o.hasError = true
		o.errorOnce.Do(func() { close(o.errorCh) })
		o.lastStreamPrefix = ""

	case stream.TagSystemNotify:
		fmt.Fprintf(o.writer, "\n[%s]\n", value)
		o.lastStreamPrefix = ""

	case stream.TagFunctionCall:
		o.printFunctionCall(value)

	case stream.TagFunctionResult:
		o.printFunctionResult(value)

	case stream.TagFunctionState:
		// Skip state indicators for plainio

	case stream.TagSystemData:
		o.handleSystemData(value)

	default:
		fmt.Fprintf(o.writer, "\n<%s>%s\n", tag, value)
	}
}

func (o *stdoutOutput) printFunctionCall(value string) {
	var tc toolCallData
	if err := json.Unmarshal([]byte(value), &tc); err != nil {
		return
	}
	formatted := formatToolCall(tc.Name, tc.Input)
	fmt.Fprintf(o.writer, "\n%s\n", formatted)
	o.lastStreamPrefix = ""
}

func (o *stdoutOutput) printFunctionResult(value string) {
	var tr toolResultData
	if err := json.Unmarshal([]byte(value), &tr); err != nil {
		return
	}
	fmt.Fprint(o.writer, tr.Output)
	o.lastStreamPrefix = ""
}

// handleSystemData detects task completion transitions and prints a trailing newline.
func (o *stdoutOutput) handleSystemData(value string) {
	var info systemInfo
	if err := json.Unmarshal([]byte(value), &info); err != nil {
		return
	}
	if o.inProgress && !info.InProgress {
		fmt.Fprintln(o.writer)
		o.lastStreamPrefix = ""
	}
	o.inProgress = info.InProgress
}

// extractStreamPrefix splits "[:id:]content" into the prefix key and content.
// The prefix key is everything between "[:":]" (including the brackets).
// If there is no stream ID prefix, it returns ("", value).
func extractStreamPrefix(value string) (prefix string, content string) {
	if !strings.HasPrefix(value, "[:") {
		return "", value
	}
	endIdx := strings.Index(value, ":]")
	if endIdx == -1 {
		return "", value
	}
	return value[:endIdx+2], value[endIdx+2:]
}

// stripStreamID removes the "[:id:]content" prefix from streaming deltas.
func stripStreamID(value string) string {
	_, content := extractStreamPrefix(value)
	return content
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
