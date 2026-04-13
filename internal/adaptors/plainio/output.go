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

// systemInfo mirrors the SystemInfo JSON from the agent package.
type systemInfo struct {
	InProgress bool `json:"in_progress"`
}

// stdoutOutput implements stream.Output.
// It parses TLV messages and prints human-readable text to stdout.
type stdoutOutput struct {
	mu           sync.Mutex
	writer       io.Writer
	buf          []byte
	inProgress   bool
	hasError     bool
	errorOnce    sync.Once
	errorCh      chan struct{} // closed on first SE tag
	lastTag      string        // last tag that produced visible output
	lastStreamID string        // tracks last stream ID prefix within TA/TR group
}

func newStdoutOutput() *stdoutOutput {
	return &stdoutOutput{
		writer:  os.Stdout,
		errorCh: make(chan struct{}),
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
	o.handleTag(tag, value)
}

func (o *stdoutOutput) handleTag(tag, value string) {
	switch tag {
	case stream.TagTextAssistant, stream.TagTextReasoning:
		prefix, content := extractStreamPrefix(value)
		if o.lastTag != "" && (o.lastTag != stream.TagTextAssistant && o.lastTag != stream.TagTextReasoning) {
			// Transitioning from a different tag group → separator
			fmt.Fprintln(o.writer)
		} else if o.lastStreamID != "" && prefix != o.lastStreamID {
			// Same group but different stream → separator
			fmt.Fprintln(o.writer)
		}
		o.lastTag = tag
		o.lastStreamID = prefix
		fmt.Fprint(o.writer, content)

	case stream.TagTextUser:
		o.emitSeparator(tag)
		fmt.Fprintf(o.writer, "> %s", value)

	case stream.TagSystemError:
		o.emitSeparator(tag)
		fmt.Fprintf(o.writer, "[error: %s]", value)
		o.hasError = true
		o.errorOnce.Do(func() { close(o.errorCh) })

	case stream.TagSystemNotify:
		o.emitSeparator(tag)
		fmt.Fprintf(o.writer, "[%s]", value)

	case stream.TagFunctionCall:
		if o.lastTag != "" {
			fmt.Fprintln(o.writer)
		}
		o.lastTag = tag
		o.lastStreamID = ""
		o.printFunctionCall(value)

	case stream.TagFunctionResult:
		// Suppress tool result content; do not update lastTag.

	case stream.TagFunctionState:
		// Skip state indicators for plainio

	case stream.TagSystemData:
		o.handleSystemData(value)

	default:
		o.emitSeparator(tag)
		fmt.Fprintf(o.writer, "[unknown-tag:%s %s]", tag, value)
	}
}

// emitSeparator prints a newline if the previous visible tag differs from the
// new tag. It updates lastTag to the new tag.
func (o *stdoutOutput) emitSeparator(tag string) {
	if o.lastTag != "" && o.lastTag != tag {
		fmt.Fprintln(o.writer)
	}
	o.lastTag = tag
	o.lastStreamID = ""
}

func (o *stdoutOutput) printFunctionCall(value string) {
	var tc struct {
		ID    string `json:"id"`
		Name  string `json:"name"`
		Input string `json:"input"`
	}
	if err := json.Unmarshal([]byte(value), &tc); err != nil {
		return
	}
	fmt.Fprintf(o.writer, "%s", formatToolCall(tc.Name, tc.Input))
}

// formatToolCall formats a tool call header for display (name + key args, no content).
func formatToolCall(name, input string) string {
	switch name {
	case "shell":
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

// handleSystemData detects task completion transitions and prints a trailing newline.
func (o *stdoutOutput) handleSystemData(value string) {
	var info systemInfo
	if err := json.Unmarshal([]byte(value), &info); err != nil {
		return
	}
	if o.inProgress && !info.InProgress {
		fmt.Fprintln(o.writer)
		o.lastTag = ""
		o.lastStreamID = ""
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
