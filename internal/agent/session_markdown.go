package agent

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/stream"
	"gopkg.in/yaml.v3"
)

// Session file uses TLV (Tag-Length-Value) encoding to avoid recursion issues
// when session files contain tool results that might include session-like content.
// The format is: 2-byte tag + 4-byte length (big-endian) + content
// Tags are shared with stream package for consistency.

// formatSessionMarkdown converts SessionData to markdown format with TLV encoding.
// Format: YAML frontmatter + binary TLV-encoded messages
func formatSessionMarkdown(data *SessionData) ([]byte, error) {
	var buf strings.Builder

	// Write YAML frontmatter
	meta := SessionMeta{
		UpdatedAt: data.UpdatedAt,
	}

	metaBytes, err := yaml.Marshal(meta)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal metadata: %w", err)
	}
	buf.WriteString("---\n")
	buf.Write(metaBytes)
	buf.WriteString("---\n")

	// Build binary section
	var binaryBuf strings.Builder
	for _, msg := range data.Messages {
		for _, part := range msg.Content {
			switch p := part.(type) {
			// Text content tags
			case llm.TextPart:
				tag := stream.TagTextUser
				if msg.Role == llm.RoleAssistant {
					tag = stream.TagTextAssistant
				}
				writeTLV(&binaryBuf, tag, p.Text)

			case llm.ReasoningPart:
				writeTLV(&binaryBuf, stream.TagTextReasoning, p.Text)

			// Function tags
			case llm.ToolCallPart:
				// Encode tool call as JSON
				tc := toolCallData{
					ID:    p.ToolCallID,
					Name:  p.ToolName,
					Input: string(p.Input),
				}
				jsonData, err := json.Marshal(tc)
				if err != nil {
					return nil, fmt.Errorf("failed to marshal tool call: %w", err)
				}
				writeTLV(&binaryBuf, stream.TagFunctionCall, string(jsonData))

			case llm.ToolResultPart:
				// Encode tool result as JSON
				tr := toolResultData{
					ID:     p.ToolCallID,
					Output: formatToolResultOutput(p.Output),
				}
				jsonData, err := json.Marshal(tr)
				if err != nil {
					return nil, fmt.Errorf("failed to marshal tool result: %w", err)
				}
				writeTLV(&binaryBuf, stream.TagFunctionResult, string(jsonData))
			}
		}
	}

	buf.Write([]byte(binaryBuf.String()))
	return []byte(buf.String()), nil
}

// writeTLV writes a TLV-encoded entry with separator: \n\n + 2-byte tag + 4-byte length + content
func writeTLV(buf *strings.Builder, tag string, content string) {
	data := []byte(content)
	length := len(data)

	buf.WriteString("\n\n") // Separator for readability
	buf.WriteByte(tag[0])
	buf.WriteByte(tag[1])
	buf.Write([]byte{
		byte(length >> 24),
		byte(length >> 16),
		byte(length >> 8),
		byte(length),
	})
	buf.Write(data)
}

// toolCallData for JSON serialization
type toolCallData struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Input string `json:"input"`
}

// toolResultData for JSON serialization
type toolResultData struct {
	ID     string `json:"id"`
	Output string `json:"output"`
}

// parseSessionMarkdown parses markdown format with TLV or legacy NUL separators.
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
	body := content[endIdx+9:] // Skip "---\n" (4) + content + "\n---\n" (5)

	// Parse metadata
	var meta SessionMeta
	if err := yaml.Unmarshal([]byte(frontmatter), &meta); err != nil {
		return nil, fmt.Errorf("failed to parse frontmatter: %w", err)
	}

	sd := &SessionData{
		UpdatedAt: meta.UpdatedAt,
	}

	// Parse messages using TLV format
	if len(body) > 0 {
		msgs, err := parseMessagesTLV(body)
		if err != nil {
			return nil, err
		}
		sd.Messages = msgs
	}

	return sd, nil
}

// parseMessagesTLV parses TLV-encoded message content.
//
//nolint:gocyclo // parsing requires multiple branches for tag types
func parseMessagesTLV(body string) ([]llm.Message, error) {
	var messages []llm.Message
	var currentMsg *llm.Message

	reader := strings.NewReader(body)

	for {
		// Skip newlines and whitespace before tag (for readability)
		for {
			b, err := reader.ReadByte()
			if err == io.EOF {
				// End of input
				if currentMsg != nil {
					messages = append(messages, *currentMsg)
				}
				return messages, nil
			}
			if err != nil {
				return nil, fmt.Errorf("failed to read: %w", err)
			}
			if b != '\n' && b != '\r' && b != ' ' && b != '\t' {
				// Found a non-whitespace byte - this is our tag
				reader.UnreadByte() //nolint:errcheck // we just read a byte, unreading should succeed
				break
			}
		}

		// Read tag (2 bytes)
		tagBytes := make([]byte, 2)
		if _, err := io.ReadFull(reader, tagBytes); err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("failed to read tag: %w", err)
		}
		tag := string(tagBytes)

		// Read length (4 bytes big-endian)
		var length int32
		if err := binary.Read(reader, binary.BigEndian, &length); err != nil {
			return nil, fmt.Errorf("failed to read length: %w", err)
		}

		// Sanity check
		if length < 0 || length > 10*1024*1024 { // Max 10MB per message
			return nil, fmt.Errorf("invalid length: %d", length)
		}

		// Read content
		content := make([]byte, length)
		if _, err := io.ReadFull(reader, content); err != nil {
			return nil, fmt.Errorf("failed to read content: %w", err)
		}

		// Parse based on tag
		var msgPart llm.ContentPart
		var msgRole llm.MessageRole
		newMessage := false

		switch tag {
		// Text content tags
		case stream.TagTextUser:
			newMessage = true
			msgRole = llm.RoleUser
			msgPart = llm.TextPart{Type: "text", Text: string(content)}

		case stream.TagTextAssistant:
			newMessage = true
			msgRole = llm.RoleAssistant
			msgPart = llm.TextPart{Type: "text", Text: string(content)}

		case stream.TagTextReasoning:
			msgRole = llm.RoleAssistant
			msgPart = llm.ReasoningPart{Type: "thinking", Text: string(content)}

		// Function tags
		case stream.TagFunctionCall:
			msgRole = llm.RoleAssistant
			var tc toolCallData
			if err := json.Unmarshal(content, &tc); err != nil {
				return nil, fmt.Errorf("failed to parse tool call: %w", err)
			}
			msgPart = llm.ToolCallPart{
				Type:       "tool_use",
				ToolCallID: tc.ID,
				ToolName:   tc.Name,
				Input:      json.RawMessage(tc.Input),
			}

		case stream.TagFunctionResult:
			msgRole = llm.RoleTool
			var tr toolResultData
			if err := json.Unmarshal(content, &tr); err != nil {
				return nil, fmt.Errorf("failed to parse tool result: %w", err)
			}
			msgPart = llm.ToolResultPart{
				Type:       "tool_result",
				ToolCallID: tr.ID,
				Output:     llm.ToolResultOutputText{Type: "text", Text: tr.Output},
			}

		default:
			return nil, fmt.Errorf("unknown tag: %s", tag)
		}

		// Create new message or append to current
		roleMismatch := currentMsg != nil && currentMsg.Role != msgRole
		if newMessage || currentMsg == nil || roleMismatch {
			if currentMsg != nil {
				messages = append(messages, *currentMsg)
			}
			currentMsg = &llm.Message{
				Role:    msgRole,
				Content: []llm.ContentPart{msgPart},
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

// formatToolResultOutput converts ToolResultOutput to string.
func formatToolResultOutput(output llm.ToolResultOutput) string {
	if text, ok := output.(llm.ToolResultOutputText); ok {
		return text.Text
	}
	if e, ok := output.(llm.ToolResultOutputError); ok {
		return e.Error
	}
	return fmt.Sprintf("%v", output)
}
