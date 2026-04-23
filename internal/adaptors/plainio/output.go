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
	"github.com/alayacore/alayacore/internal/tools"
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
	case "execute_command":
		return formatExecuteCommand(input)
	case "read_file":
		return formatReadFile(input)
	case "write_file":
		return formatWriteFile(input)
	case "edit_file":
		return formatEditFile(input)
	case "search_content":
		return formatSearchContent(input)
	default:
		return fmt.Sprintf("[%s]", name)
	}
}

func formatExecuteCommand(input string) string {
	// Use anonymous struct since executeCommandInput is unexported
	var args struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal([]byte(input), &args); err != nil {
		return "[execute_command]"
	}
	return fmt.Sprintf("[execute_command: %s]", args.Command)
}

func formatReadFile(input string) string {
	var args tools.ReadFileInput
	if err := json.Unmarshal([]byte(input), &args); err != nil {
		return "[read_file]"
	}
	parts := []string{args.Path}
	if args.StartLine > 0 {
		parts = append(parts, fmt.Sprintf("%d", args.StartLine))
	}
	if args.EndLine > 0 {
		parts = append(parts, fmt.Sprintf("%d", args.EndLine))
	}
	return fmt.Sprintf("[read_file: %s]", strings.Join(parts, ", "))
}

func formatWriteFile(input string) string {
	var args tools.WriteFileInput
	if err := json.Unmarshal([]byte(input), &args); err != nil {
		return "[write_file]"
	}
	return fmt.Sprintf("[write_file: %s]", args.Path)
}

func formatEditFile(input string) string {
	var args tools.EditFileInput
	if err := json.Unmarshal([]byte(input), &args); err != nil {
		return "[edit_file]"
	}
	return fmt.Sprintf("[edit_file: %s]", args.Path)
}

func formatSearchContent(input string) string {
	var args tools.SearchContentInput
	if err := json.Unmarshal([]byte(input), &args); err != nil {
		return "[search_content]"
	}
	label := args.Pattern
	if args.Path != "" {
		label += " in " + args.Path
	}
	if args.FileType != "" {
		label += " (" + args.FileType + ")"
	}
	if args.Glob != "" {
		label += " [" + args.Glob + "]"
	}
	if args.IgnoreCase == "true" {
		label += " -i"
	}
	if args.MaxLines > 0 {
		label += fmt.Sprintf(" (max: %d)", args.MaxLines)
	}
	return fmt.Sprintf("[search_content: %s]", label)
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
