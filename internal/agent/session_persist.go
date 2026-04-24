package agent

// Session persistence: saving, loading, and displaying sessions.

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/alayacore/alayacore/internal/config"
	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/stream"
)

// ============================================================================
// Load/Save
// ============================================================================

// LoadSession loads a session from a file.
func LoadSession(path string) (*SessionData, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read session file: %w", err)
	}
	return parseSessionMarkdown(data)
}

func (s *Session) saveSessionToFile(path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data := SessionData{
		SessionMeta: SessionMeta{
			CreatedAt:       s.CreatedAt,
			UpdatedAt:       time.Now(),
			ThinkingEnabled: s.thinkingEnabled,
			ActiveModel:     s.activeModelName(),
		},
		Messages: s.Messages,
	}

	raw, err := formatSessionMarkdown(&data)
	if err != nil {
		return fmt.Errorf("failed to format session data: %w", err)
	}
	if err := os.WriteFile(path, raw, 0600); err != nil {
		return fmt.Errorf("failed to write session file: %w", err)
	}
	return nil
}

// ============================================================================
// Markdown Format (TLV encoding)
// ============================================================================

// formatFrontmatter writes the frontmatter using struct tags for field names.
func formatFrontmatter(meta *SessionMeta) string {
	var buf strings.Builder
	buf.WriteString("---\n")

	// Always write both fields for consistent format
	buf.WriteString("created_at: ")
	buf.WriteString(meta.CreatedAt.Format(time.RFC3339))
	buf.WriteString("\n")

	buf.WriteString("updated_at: ")
	buf.WriteString(meta.UpdatedAt.Format(time.RFC3339))
	buf.WriteString("\n")

	if meta.ThinkingEnabled {
		buf.WriteString("thinking_enabled: true\n")
	} else {
		buf.WriteString("thinking_enabled: false\n")
	}

	if meta.ActiveModel != "" {
		buf.WriteString("active_model: \"")
		buf.WriteString(meta.ActiveModel)
		buf.WriteString("\"\n")
	}

	buf.WriteString("---\n")
	return buf.String()
}

// formatSessionMarkdown converts SessionData to markdown format with TLV encoding.
func formatSessionMarkdown(data *SessionData) ([]byte, error) {
	var buf strings.Builder
	buf.WriteString(formatFrontmatter(&data.SessionMeta))

	var binaryBuf strings.Builder
	for _, msg := range data.Messages {
		for _, part := range msg.Content {
			switch p := part.(type) {
			case llm.TextPart:
				tag := stream.TagTextUser
				if msg.Role == llm.RoleAssistant {
					tag = stream.TagTextAssistant
				}
				writeTLV(&binaryBuf, tag, p.Text)

			case llm.ReasoningPart:
				writeTLV(&binaryBuf, stream.TagTextReasoning, p.Text)

			case llm.ToolCallPart:
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

func writeTLV(buf *strings.Builder, tag string, content string) {
	data := []byte(content)
	length := len(data)

	buf.WriteString("\n\n")
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

type toolCallData struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Input string `json:"input"`
}

type toolResultData struct {
	ID     string `json:"id"`
	Output string `json:"output"`
}

// parseSessionMarkdown parses markdown format with TLV encoding.
// parseFrontmatter extracts the frontmatter and body from content with "---" delimiters.
// Returns the frontmatter content (between the delimiters) and the body (after the closing delimiter).
func parseFrontmatter(content string) (frontmatter, body string, err error) {
	if !strings.HasPrefix(content, "---\n") {
		return "", "", fmt.Errorf("session file missing frontmatter")
	}

	endIdx := strings.Index(content[4:], "\n---\n")
	if endIdx == -1 {
		return "", "", fmt.Errorf("session file missing frontmatter end marker")
	}

	frontmatter = content[4 : endIdx+4]
	body = content[endIdx+9:]
	return frontmatter, body, nil
}

// parseSessionMeta parses key-value pairs from frontmatter into SessionMeta using struct tags.
func parseSessionMeta(frontmatter string) SessionMeta {
	var meta SessionMeta
	config.ParseKeyValue(frontmatter, &meta)
	return meta
}

func parseSessionMarkdown(data []byte) (*SessionData, error) {
	frontmatter, body, err := parseFrontmatter(string(data))
	if err != nil {
		return nil, err
	}

	sd := &SessionData{
		SessionMeta: parseSessionMeta(frontmatter),
	}

	if len(body) > 0 {
		msgs, chunks, err := parseMessagesTLV(body)
		if err != nil {
			return nil, err
		}
		sd.Messages = msgs
		sd.TLVChunks = chunks
	}

	return sd, nil
}

//nolint:gocyclo // parsing requires multiple branches for tag types
func parseMessagesTLV(body string) ([]llm.Message, []TLVChunk, error) {
	var messages []llm.Message
	var chunks []TLVChunk
	var currentMsg *llm.Message

	reader := strings.NewReader(body)

	for {
		for {
			b, err := reader.ReadByte()
			if err == io.EOF {
				if currentMsg != nil {
					messages = append(messages, *currentMsg)
				}
				return messages, chunks, nil
			}
			if err != nil {
				return nil, nil, fmt.Errorf("failed to read: %w", err)
			}
			if b != '\n' && b != '\r' && b != ' ' && b != '\t' {
				if unreadErr := reader.UnreadByte(); unreadErr != nil {
					return nil, nil, fmt.Errorf("failed to unread: %w", unreadErr)
				}
				break
			}
		}

		tagBytes := make([]byte, 2)
		if _, err := io.ReadFull(reader, tagBytes); err != nil {
			if err == io.EOF {
				break
			}
			return nil, nil, fmt.Errorf("failed to read tag: %w", err)
		}
		tag := string(tagBytes)

		var length int32
		if err := binary.Read(reader, binary.BigEndian, &length); err != nil {
			return nil, nil, fmt.Errorf("failed to read length: %w", err)
		}

		if length < 0 || length > 10*1024*1024 {
			return nil, nil, fmt.Errorf("invalid length: %d", length)
		}

		content := make([]byte, length)
		if _, err := io.ReadFull(reader, content); err != nil {
			return nil, nil, fmt.Errorf("failed to read content: %w", err)
		}

		// Store TLV chunk for display
		chunks = append(chunks, TLVChunk{Tag: tag, Value: string(content)})

		var msgPart llm.ContentPart
		var msgRole llm.MessageRole
		newMessage := false

		switch tag {
		case stream.TagTextUser:
			newMessage = true
			msgRole = llm.RoleUser
			msgPart = llm.TextPart{Type: "text", Text: string(content)}

		case stream.TagTextAssistant:
			// Do NOT force newMessage: an assistant message may start with
			// TagTextReasoning or TagFunctionCall; the text part belongs in
			// the same message.  A new message is still created when
			// currentMsg is nil or the role doesn't match.
			msgRole = llm.RoleAssistant
			msgPart = llm.TextPart{Type: "text", Text: string(content)}

		case stream.TagTextReasoning:
			msgRole = llm.RoleAssistant
			msgPart = llm.ReasoningPart{Type: "thinking", Text: string(content)}

		case stream.TagFunctionCall:
			msgRole = llm.RoleAssistant
			var tc toolCallData
			if err := json.Unmarshal(content, &tc); err != nil {
				return nil, nil, fmt.Errorf("failed to parse tool call: %w", err)
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
				return nil, nil, fmt.Errorf("failed to parse tool result: %w", err)
			}
			msgPart = llm.ToolResultPart{
				Type:       "tool_result",
				ToolCallID: tr.ID,
				Output:     llm.ToolResultOutputText{Type: "text", Text: tr.Output},
			}

		default:
			return nil, nil, fmt.Errorf("unknown tag: %s", tag)
		}

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

	return messages, chunks, nil
}

func formatToolResultOutput(output llm.ToolResultOutput) string {
	if text, ok := output.(llm.ToolResultOutputText); ok {
		return text.Text
	}
	if e, ok := output.(llm.ToolResultOutputError); ok {
		return e.Error
	}
	return fmt.Sprintf("%v", output)
}
