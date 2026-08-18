package terminal

// Tool display handlers for formatting tool calls and results.
// Each tool has a handler that knows how to format its display.

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/alayacore/alayacore/internal/tools"
)

// ToolDisplayHandler handles display formatting for a specific tool.
type ToolDisplayHandler interface {
	// FormatCall formats the tool call for display.
	// Returns the formatted string (may contain diff markers for edit_file).
	FormatCall(input json.RawMessage) string
}

// ============================================================================
// Handler Implementations
// ============================================================================

// GenericHandler handles tools with simple one-line display.
type GenericHandler struct {
	name string
}

func (h *GenericHandler) FormatCall(input json.RawMessage) string {
	// Add newline at end so output starts on new line
	return fmt.Sprintf("%s: %s\n", h.name, string(input))
}

// ExecuteCommandHandler handles execute_command calls.
type ExecuteCommandHandler struct{}

func (h *ExecuteCommandHandler) FormatCall(input json.RawMessage) string {
	var args struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "execute_command: <parse error>"
	}
	// Add newline at end so output starts on new line
	return fmt.Sprintf("execute_command: %s\n", escapeNewlines(args.Command))
}

// ReadFileHandler handles read_file calls.
type ReadFileHandler struct{}

func (h *ReadFileHandler) FormatCall(input json.RawMessage) string {
	var args tools.ReadFileInput
	if err := json.Unmarshal(input, &args); err != nil {
		return "read_file: <parse error>"
	}

	parts := []string{args.Path}
	if args.StartLine > 0 {
		parts = append(parts, fmt.Sprintf("start=%d", args.StartLine))
	}
	if args.NumLines > 0 {
		parts = append(parts, fmt.Sprintf("count=%d", args.NumLines))
	}
	// Add newline at end so output starts on new line
	return fmt.Sprintf("read_file: %s\n", strings.Join(parts, ", "))
}

// WriteFileHandler handles write_file calls.
type WriteFileHandler struct{}

func (h *WriteFileHandler) FormatCall(input json.RawMessage) string {
	var args tools.WriteFileInput
	if err := json.Unmarshal(input, &args); err != nil {
		return "write_file: <parse error>"
	}

	if args.Content == "" {
		// Empty content — just show the path
		return fmt.Sprintf("write_file: %s\n", args.Path)
	}
	return fmt.Sprintf("write_file: %s\n%s", args.Path, args.Content)
}

// EditFileHandler handles edit_file calls with diff display.
type EditFileHandler struct{}

func (h *EditFileHandler) FormatCall(input json.RawMessage) string {
	var args tools.EditFileInput
	if err := json.Unmarshal(input, &args); err != nil {
		return "edit_file: <parse error>"
	}

	if args.OldString == "" && args.NewString == "" {
		// Empty content — just show the path
		return fmt.Sprintf("edit_file: %s\n", args.Path)
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

// SearchContentHandler handles search_content calls.
type SearchContentHandler struct{}

func (h *SearchContentHandler) FormatCall(input json.RawMessage) string {
	var args tools.SearchContentInput
	if err := json.Unmarshal(input, &args); err != nil {
		return "search_content: <parse error>"
	}

	var parts []string

	// Pattern and path
	part := args.Pattern
	if args.Path != "" {
		part += " in " + args.Path
	}
	parts = append(parts, part)

	// FileType and/or Glob
	switch {
	case args.FileType != "" && args.Glob != "":
		parts = append(parts, fmt.Sprintf("for %s files (%s)", args.FileType, args.Glob))
	case args.FileType != "":
		parts = append(parts, fmt.Sprintf("for %s files", args.FileType))
	case args.Glob != "":
		parts = append(parts, fmt.Sprintf("matching %s", args.Glob))
	}

	// Modifiers
	if args.IgnoreCase {
		parts = append(parts, "ignoring case")
	}
	if args.MaxLines > 0 {
		parts = append(parts, fmt.Sprintf("limit %d", args.MaxLines))
	}

	// Add newline at end so output starts on new line
	return fmt.Sprintf("search_content: %s\n", strings.Join(parts, ", "))
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
	return &GenericHandler{name: toolName}
}

// ============================================================================
// Helpers
// ============================================================================

func escapeNewlines(s string) string {
	s = strings.ReplaceAll(s, "\n", "\\n")
	s = strings.ReplaceAll(s, "\t", "\\t")
	return s
}

// ============================================================================
// Tool Renderer — per-tool rendering of tool call input
// ============================================================================
