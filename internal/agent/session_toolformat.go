package agent

import (
	"encoding/json"
	"fmt"
	"strings"
)

//nolint:gocyclo // tool formatting requires handling many tool types
func formatToolCall(toolName, input string) string {
	var fields map[string]interface{}
	if err := json.Unmarshal([]byte(input), &fields); err != nil {
		return ""
	}

	switch toolName {
	case "posix_shell":
		if cmd, ok := fields["command"].(string); ok {
			return fmt.Sprintf("%s: %s", toolName, escapeNewlines(cmd))
		}
	case "activate_skill":
		if name, ok := fields["name"].(string); ok {
			return fmt.Sprintf("%s: %s", toolName, name)
		}
	case "read_file":
		args := []string{}
		if path, ok := fields["path"].(string); ok {
			args = append(args, path)
		}
		if startLine, ok := fields["start_line"].(string); ok && startLine != "" {
			args = append(args, startLine)
		}
		if endLine, ok := fields["end_line"].(string); ok && endLine != "" {
			args = append(args, endLine)
		}
		if len(args) > 0 {
			return fmt.Sprintf("%s: %s", toolName, strings.Join(args, ", "))
		}
	case "write_file":
		args := []string{}
		if path, ok := fields["path"].(string); ok {
			args = append(args, path)
		}
		if content, ok := fields["content"].(string); ok {
			truncated := truncateString(content, 50)
			args = append(args, truncated)
		}
		if len(args) > 0 {
			return fmt.Sprintf("%s: %s", toolName, strings.Join(args, ", "))
		}
	case "edit_file":
		path, _ := fields["path"].(string)         //nolint:errcheck // type assertion for optional field
		oldStr, _ := fields["old_string"].(string) //nolint:errcheck // type assertion for optional field
		newStr, _ := fields["new_string"].(string) //nolint:errcheck // type assertion for optional field
		if path == "" || oldStr == "" || newStr == "" {
			return ""
		}

		var lines []string
		lines = append(lines, fmt.Sprintf("%s: %s", toolName, path))

		oldLines := strings.Split(oldStr, "\n")
		newLines := strings.Split(newStr, "\n")

		// Pair up old and new lines
		maxLines := max(len(oldLines), len(newLines))

		// Use null byte as separator for raw data - terminal will format with adaptive width
		for i := range maxLines {
			var oldPart, newPart string
			if i < len(oldLines) {
				oldPart = strings.ReplaceAll(oldLines[i], "\n", "\\n")
			}
			if i < len(newLines) {
				newPart = strings.ReplaceAll(newLines[i], "\n", "\\n")
			}
			// Format: \x00old_content\x00new_content
			lines = append(lines, fmt.Sprintf("\x00%s\x00%s", oldPart, newPart))
		}

		return strings.Join(lines, "\n")
	}
	return ""
}

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func escapeNewlines(s string) string {
	s = strings.ReplaceAll(s, "\n", "\\n")
	s = strings.ReplaceAll(s, "\t", "\\t")
	return s
}
