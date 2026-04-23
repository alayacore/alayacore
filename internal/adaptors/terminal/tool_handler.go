package terminal

// Tool display handlers for formatting tool calls and results.
// Each tool has a handler that knows how to format its display.

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/alayacore/alayacore/internal/tools"
)

// ToolCallData represents a tool call (FC tag payload).
type ToolCallData struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Input string `json:"input"`
}

// ToolResultData represents a tool result (FR tag payload).
type ToolResultData struct {
	ID     string `json:"id"`
	Output string `json:"output"`
}

// ToolDisplayHandler handles display formatting for a specific tool.
type ToolDisplayHandler interface {
	// FormatCall formats the tool call for display.
	// Returns the formatted string (may contain diff markers for edit_file).
	FormatCall(input json.RawMessage, styles *Styles) string

	// ShouldShowOutput returns true if tool output should be displayed.
	ShouldShowOutput() bool
}

// ============================================================================
// Handler Implementations
// ============================================================================

// GenericHandler handles tools with simple one-line display.
type GenericHandler struct {
	name       string
	showOutput bool
}

func (h *GenericHandler) FormatCall(input json.RawMessage, _ *Styles) string {
	// Add newline at end so output starts on new line
	return fmt.Sprintf("%s: %s\n", h.name, string(input))
}

func (h *GenericHandler) ShouldShowOutput() bool {
	return h.showOutput
}

// ExecuteCommandHandler handles execute_command calls.
type ExecuteCommandHandler struct{}

func (h *ExecuteCommandHandler) FormatCall(input json.RawMessage, _ *Styles) string {
	var args struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "execute_command: <parse error>"
	}
	// Add newline at end so output starts on new line
	return fmt.Sprintf("execute_command: %s\n", escapeNewlines(args.Command))
}

func (h *ExecuteCommandHandler) ShouldShowOutput() bool {
	return true
}

// ReadFileHandler handles read_file calls.
type ReadFileHandler struct{}

func (h *ReadFileHandler) FormatCall(input json.RawMessage, _ *Styles) string {
	var args tools.ReadFileInput
	if err := json.Unmarshal(input, &args); err != nil {
		return "read_file: <parse error>"
	}

	parts := []string{args.Path}
	if args.StartLine > 0 {
		parts = append(parts, fmt.Sprintf("%d", args.StartLine))
	}
	if args.EndLine > 0 {
		parts = append(parts, fmt.Sprintf("%d", args.EndLine))
	}
	// Add newline at end so output starts on new line
	return fmt.Sprintf("read_file: %s\n", strings.Join(parts, ", "))
}

func (h *ReadFileHandler) ShouldShowOutput() bool {
	return true
}

// WriteFileHandler handles write_file calls.
type WriteFileHandler struct{}

func (h *WriteFileHandler) FormatCall(input json.RawMessage, _ *Styles) string {
	var args tools.WriteFileInput
	if err := json.Unmarshal(input, &args); err != nil {
		return "write_file: <parse error>"
	}
	return fmt.Sprintf("write_file: %s\n%s", args.Path, args.Content)
}

func (h *WriteFileHandler) ShouldShowOutput() bool {
	return false
}

// EditFileHandler handles edit_file calls with diff display.
type EditFileHandler struct{}

func (h *EditFileHandler) FormatCall(input json.RawMessage, _ *Styles) string {
	var args tools.EditFileInput
	if err := json.Unmarshal(input, &args); err != nil {
		return "edit_file: <parse error>"
	}

	var lines []string
	lines = append(lines, fmt.Sprintf("edit_file: %s", args.Path))

	oldLines := strings.Split(args.OldString, "\n")
	newLines := strings.Split(args.NewString, "\n")

	diffPairs := computeDiff(oldLines, newLines)

	for _, pair := range diffPairs {
		old := strings.ReplaceAll(pair.old, "\n", "\\n")
		newText := strings.ReplaceAll(pair.new, "\n", "\\n")

		oldEmpty := pair.old == ""
		newEmpty := pair.new == ""
		isSame := pair.old == pair.new

		switch {
		case isSame:
			lines = append(lines, "  "+old)
		case oldEmpty:
			lines = append(lines, "+ "+newText)
		case newEmpty:
			lines = append(lines, "- "+old)
		default:
			lines = append(lines, "- "+old)
			lines = append(lines, "+ "+newText)
		}
	}

	return strings.Join(lines, "\n")
}

func (h *EditFileHandler) ShouldShowOutput() bool {
	return false
}

// SearchContentHandler handles search_content calls.
type SearchContentHandler struct{}

func (h *SearchContentHandler) FormatCall(input json.RawMessage, _ *Styles) string {
	var args tools.SearchContentInput
	if err := json.Unmarshal(input, &args); err != nil {
		return "search_content: <parse error>"
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
	// Add newline at end so output starts on new line
	return fmt.Sprintf("search_content: %s\n", label)
}

func (h *SearchContentHandler) ShouldShowOutput() bool {
	return true
}

// ============================================================================
// Handler Registry
// ============================================================================

// ToolHandlers maps tool names to their display handlers.
var ToolHandlers = map[string]ToolDisplayHandler{
	"execute_command": &ExecuteCommandHandler{},
	"read_file":       &ReadFileHandler{},
	"write_file":      &WriteFileHandler{},
	"edit_file":       &EditFileHandler{},
	"search_content":  &SearchContentHandler{},
}

// GetHandler returns the handler for a tool, or a generic fallback.
func GetHandler(toolName string) ToolDisplayHandler {
	if h, ok := ToolHandlers[toolName]; ok {
		return h
	}
	// Fallback generic handler
	return &GenericHandler{name: toolName, showOutput: true}
}

// ============================================================================
// Helpers
// ============================================================================

func escapeNewlines(s string) string {
	s = strings.ReplaceAll(s, "\n", "\\n")
	s = strings.ReplaceAll(s, "\t", "\\t")
	return s
}

// diffPair represents a pair of old/new lines in a diff
type diffPair struct {
	old string
	new string
}

// computeDiff computes the LCS-based diff between old and new lines
func computeDiff(oldLines, newLines []string) []diffPair {
	lcs := computeLCS(oldLines, newLines)

	var result []diffPair
	i, j := 0, 0

	for _, lcsLine := range lcs {
		for i < len(oldLines) && oldLines[i] != lcsLine {
			result = append(result, diffPair{old: oldLines[i], new: ""})
			i++
		}

		for j < len(newLines) && newLines[j] != lcsLine {
			result = append(result, diffPair{old: "", new: newLines[j]})
			j++
		}

		if i < len(oldLines) && j < len(newLines) {
			result = append(result, diffPair{old: oldLines[i], new: newLines[j]})
			i++
			j++
		}
	}

	for i < len(oldLines) {
		result = append(result, diffPair{old: oldLines[i], new: ""})
		i++
	}
	for j < len(newLines) {
		result = append(result, diffPair{old: "", new: newLines[j]})
		j++
	}

	return result
}

// computeLCS computes the Longest Common Subsequence of two string slices
func computeLCS(a, b []string) []string {
	if len(a) == 0 || len(b) == 0 {
		return nil
	}

	m, n := len(a), len(b)
	dp := make([][]int, m+1)
	for i := range dp {
		dp[i] = make([]int, n+1)
	}

	for i := 1; i <= m; i++ {
		for j := 1; j <= n; j++ {
			if a[i-1] == b[j-1] {
				dp[i][j] = dp[i-1][j-1] + 1
			} else {
				dp[i][j] = max(dp[i-1][j], dp[i][j-1])
			}
		}
	}

	var lcs []string
	i, j := m, n
	for i > 0 && j > 0 {
		switch {
		case a[i-1] == b[j-1]:
			lcs = append([]string{a[i-1]}, lcs...)
			i--
			j--
		case dp[i-1][j] > dp[i][j-1]:
			i--
		default:
			j--
		}
	}

	return lcs
}
