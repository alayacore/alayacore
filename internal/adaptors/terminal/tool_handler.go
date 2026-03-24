package terminal

// Tool display handlers for formatting tool calls and results.
// Each tool has a handler that knows how to format its display.

import (
	"encoding/json"
	"fmt"
	"strings"
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

func (h *GenericHandler) FormatCall(input json.RawMessage, styles *Styles) string {
	// Add newline at end so output starts on new line
	return fmt.Sprintf("%s: %s\n", h.name, string(input))
}

func (h *GenericHandler) ShouldShowOutput() bool {
	return h.showOutput
}

// PosixShellHandler handles posix_shell commands.
type PosixShellHandler struct{}

func (h *PosixShellHandler) FormatCall(input json.RawMessage, styles *Styles) string {
	var args struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "posix_shell: <parse error>"
	}
	// Add newline at end so output starts on new line
	return fmt.Sprintf("posix_shell: %s\n", escapeNewlines(args.Command))
}

func (h *PosixShellHandler) ShouldShowOutput() bool {
	return true
}

// ReadFileHandler handles read_file calls.
type ReadFileHandler struct{}

func (h *ReadFileHandler) FormatCall(input json.RawMessage, styles *Styles) string {
	var args struct {
		Path      string `json:"path"`
		StartLine string `json:"start_line"`
		EndLine   string `json:"end_line"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "read_file: <parse error>"
	}

	parts := []string{args.Path}
	if args.StartLine != "" {
		parts = append(parts, args.StartLine)
	}
	if args.EndLine != "" {
		parts = append(parts, args.EndLine)
	}
	// Add newline at end so output starts on new line
	return fmt.Sprintf("read_file: %s\n", strings.Join(parts, ", "))
}

func (h *ReadFileHandler) ShouldShowOutput() bool {
	return true
}

// WriteFileHandler handles write_file calls.
type WriteFileHandler struct{}

func (h *WriteFileHandler) FormatCall(input json.RawMessage, styles *Styles) string {
	var args struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
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

func (h *EditFileHandler) FormatCall(input json.RawMessage, styles *Styles) string {
	var args struct {
		Path      string `json:"path"`
		OldString string `json:"old_string"`
		NewString string `json:"new_string"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "edit_file: <parse error>"
	}

	var lines []string
	lines = append(lines, fmt.Sprintf("edit_file: %s", args.Path))

	oldLines := strings.Split(args.OldString, "\n")
	newLines := strings.Split(args.NewString, "\n")

	diffPairs := computeDiff(oldLines, newLines)

	for _, pair := range diffPairs {
		oldPart := strings.ReplaceAll(pair.old, "\n", "\\n")
		newPart := strings.ReplaceAll(pair.new, "\n", "\\n")
		lines = append(lines, fmt.Sprintf("\x00%s\x00%s", oldPart, newPart))
	}

	return strings.Join(lines, "\n")
}

func (h *EditFileHandler) ShouldShowOutput() bool {
	return false
}

// ActivateSkillHandler handles activate_skill calls.
type ActivateSkillHandler struct{}

func (h *ActivateSkillHandler) FormatCall(input json.RawMessage, styles *Styles) string {
	var args struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "activate_skill: <parse error>"
	}
	// Add newline at end so output starts on new line
	return fmt.Sprintf("activate_skill: %s\n", args.Name)
}

func (h *ActivateSkillHandler) ShouldShowOutput() bool {
	return true
}

// ============================================================================
// Handler Registry
// ============================================================================

// ToolHandlers maps tool names to their display handlers.
var ToolHandlers = map[string]ToolDisplayHandler{
	"posix_shell":    &PosixShellHandler{},
	"read_file":      &ReadFileHandler{},
	"write_file":     &WriteFileHandler{},
	"edit_file":      &EditFileHandler{},
	"activate_skill": &ActivateSkillHandler{},
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
