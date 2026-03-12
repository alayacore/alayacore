package agent

import (
	"encoding/json"
	"fmt"
	"strings"

	"charm.land/fantasy"
	"gopkg.in/yaml.v3"
)

// ============================================================================
// Markdown Session Format
// ============================================================================

const (
	msgSep = "\x00" // NUL character as message separator
)

// formatSessionMarkdown converts SessionData to markdown format with NUL separators.
func formatSessionMarkdown(data *SessionData) ([]byte, error) {
	var buf strings.Builder

	// Write YAML frontmatter
	meta := SessionMeta{
		BaseURL:       data.BaseURL,
		ModelName:     data.ModelName,
		TotalTokens:   data.TotalSpent.TotalTokens,
		ContextTokens: data.ContextTokens,
		CreatedAt:     data.CreatedAt,
		UpdatedAt:     data.UpdatedAt,
	}

	metaBytes, err := yaml.Marshal(meta)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal metadata: %w", err)
	}
	buf.WriteString("---\n")
	buf.Write(metaBytes)
	buf.WriteString("---\n")

	// Write messages - each message is a separate NUL-separated entry
	for _, msg := range data.Messages {
		for _, part := range msg.Content {
			switch p := part.(type) {
			case fantasy.TextPart:
				buf.WriteString(msgSep)
				buf.WriteString("msg:")
				buf.WriteString(string(msg.Role))
				buf.WriteByte('\n')
				buf.WriteString(p.Text)
				if !strings.HasSuffix(p.Text, "\n") {
					buf.WriteByte('\n')
				}
			case fantasy.ReasoningPart:
				buf.WriteString(msgSep)
				buf.WriteString("msg:reasoning\n")
				buf.WriteString(p.Text)
				if !strings.HasSuffix(p.Text, "\n") {
					buf.WriteByte('\n')
				}
			case fantasy.ToolCallPart:
				buf.WriteString(msgSep)
				buf.WriteString("tool_call\n")
				fmt.Fprintf(&buf, "id: %s\n", p.ToolCallID)
				fmt.Fprintf(&buf, "name: %s\n", p.ToolName)
				buf.WriteString("input:\n")
				buf.WriteString(indentString(p.Input, "  "))
				if !strings.HasSuffix(p.Input, "\n") {
					buf.WriteByte('\n')
				}
			case fantasy.ToolResultPart:
				buf.WriteString(msgSep)
				buf.WriteString("tool_result\n")
				fmt.Fprintf(&buf, "id: %s\n", p.ToolCallID)
				buf.WriteString("output:\n")
				outputStr := formatToolResultOutput(p.Output)
				buf.WriteString(indentString(outputStr, "  "))
				if !strings.HasSuffix(outputStr, "\n") {
					buf.WriteByte('\n')
				}
			}
		}
	}

	return []byte(buf.String()), nil
}

// parseSessionMarkdown parses markdown format with NUL separators to SessionData.
func parseSessionMarkdown(data []byte) (*SessionData, error) {
	content := string(data)

	// Split frontmatter and body
	if !strings.HasPrefix(content, "---\n") {
		return nil, fmt.Errorf("session file missing YAML frontmatter")
	}

	endIdx := strings.Index(content[4:], "\n---\n")
	if endIdx == -1 {
		return nil, fmt.Errorf("session file missing frontmatter end marker")
	}

	frontmatter := content[4 : endIdx+4]
	body := content[endIdx+8:]

	// Parse metadata
	var meta SessionMeta
	if err := yaml.Unmarshal([]byte(frontmatter), &meta); err != nil {
		return nil, fmt.Errorf("failed to parse frontmatter: %w", err)
	}

	sd := &SessionData{
		BaseURL:       meta.BaseURL,
		ModelName:     meta.ModelName,
		TotalSpent:    fantasy.Usage{TotalTokens: meta.TotalTokens},
		ContextTokens: meta.ContextTokens,
		CreatedAt:     meta.CreatedAt,
		UpdatedAt:     meta.UpdatedAt,
	}

	// Parse messages
	if body != "" {
		msgs, err := parseMessages(body)
		if err != nil {
			return nil, err
		}
		sd.Messages = msgs
	}

	return sd, nil
}

// parseMessages parses NUL-separated message content.
func parseMessages(body string) ([]fantasy.Message, error) {
	var messages []fantasy.Message
	var currentMsg *fantasy.Message

	// Split by NUL character
	parts := strings.Split(body, msgSep)

	for _, part := range parts {
		if part == "" {
			continue
		}

		// First line is role/type
		lines := strings.SplitN(part, "\n", 2)
		role := lines[0]
		content := ""
		if len(lines) > 1 {
			content = lines[1]
		}

		var msgPart fantasy.MessagePart
		var msgRole fantasy.MessageRole
		newMessage := false

		// Check for "msg:" prefix which indicates a new message
		if strings.HasPrefix(role, "msg:") {
			newMessage = true
			role = strings.TrimPrefix(role, "msg:")
		}

		switch role {
		case string(fantasy.MessageRoleUser):
			msgRole = fantasy.MessageRoleUser
			msgPart = fantasy.TextPart{Text: strings.TrimSuffix(content, "\n")}
		case string(fantasy.MessageRoleAssistant):
			msgRole = fantasy.MessageRoleAssistant
			msgPart = fantasy.TextPart{Text: strings.TrimSuffix(content, "\n")}
		case string(fantasy.MessageRoleTool):
			msgRole = fantasy.MessageRoleTool
			msgPart = parseToolResultContent(content)
		case "reasoning":
			msgRole = fantasy.MessageRoleAssistant
			msgPart = fantasy.ReasoningPart{Text: strings.TrimSuffix(content, "\n")}
		case "tool_call":
			msgRole = fantasy.MessageRoleAssistant
			msgPart = parseToolCallContent(content)
		case "tool_result":
			msgRole = fantasy.MessageRoleTool
			msgPart = parseToolResultContent(content)
		default:
			continue
		}

		// Create new message or append to current
		// Start a new message if: explicitly requested, no current message, or role mismatch
		roleMismatch := currentMsg != nil && currentMsg.Role != msgRole
		if newMessage || currentMsg == nil || roleMismatch {
			if currentMsg != nil {
				messages = append(messages, *currentMsg)
			}
			currentMsg = &fantasy.Message{
				Role:    msgRole,
				Content: []fantasy.MessagePart{msgPart},
			}
		} else {
			currentMsg.Content = append(currentMsg.Content, msgPart)
		}
	}

	if currentMsg != nil {
		messages = append(messages, *currentMsg)
	}

	return messages, nil
}

// parseToolCallContent parses tool_call section content.
func parseToolCallContent(content string) fantasy.ToolCallPart {
	tc := fantasy.ToolCallPart{}
	lines := strings.Split(content, "\n")

	var inputLines []string
	inInput := false

	for _, line := range lines {
		if inInput {
			inputLines = append(inputLines, strings.TrimPrefix(line, "  "))
		} else if strings.HasPrefix(line, "id: ") {
			tc.ToolCallID = strings.TrimPrefix(line, "id: ")
		} else if strings.HasPrefix(line, "name: ") {
			tc.ToolName = strings.TrimPrefix(line, "name: ")
		} else if line == "input:" {
			inInput = true
		}
	}

	tc.Input = strings.Join(inputLines, "\n")
	return tc
}

// parseToolResultContent parses tool_result section content.
func parseToolResultContent(content string) fantasy.ToolResultPart {
	tr := fantasy.ToolResultPart{}
	lines := strings.Split(content, "\n")

	var outputLines []string
	inOutput := false

	for _, line := range lines {
		if inOutput {
			outputLines = append(outputLines, strings.TrimPrefix(line, "  "))
		} else if strings.HasPrefix(line, "id: ") {
			tr.ToolCallID = strings.TrimPrefix(line, "id: ")
		} else if line == "output:" {
			inOutput = true
		}
	}

	output := strings.Join(outputLines, "\n")
	tr.Output = fantasy.ToolResultOutputContentText{Text: output}
	return tr
}

// formatToolResultOutput converts ToolResultOutputContent to string.
func formatToolResultOutput(output fantasy.ToolResultOutputContent) string {
	if text, ok := output.(fantasy.ToolResultOutputContentText); ok {
		return text.Text
	}
	if m, ok := output.(fantasy.ToolResultOutputContentMedia); ok {
		data, _ := json.Marshal(m)
		return string(data)
	}
	if e, ok := output.(fantasy.ToolResultOutputContentError); ok {
		return e.Error.Error()
	}
	return fmt.Sprintf("%v", output)
}

// indentString indents each line of a string.
func indentString(s, indent string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if line != "" {
			lines[i] = indent + line
		}
	}
	return strings.Join(lines, "\n")
}
