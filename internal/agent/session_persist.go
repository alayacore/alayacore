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

	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/stream"
	"gopkg.in/yaml.v3"
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
		Messages:  s.Messages,
		UpdatedAt: time.Now(),
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
// Display Messages
// ============================================================================

func (s *Session) displayMessages() {
	if s.Output == nil {
		return
	}
	for _, msg := range s.Messages {
		switch msg.Role {
		case llm.RoleUser:
			s.displayUserMessage(msg)
		case llm.RoleAssistant:
			s.displayAssistantMessage(msg)
		case llm.RoleTool:
			s.displayToolMessage(msg)
		}
	}
}

func (s *Session) displayUserMessage(msg llm.Message) {
	var text string
	for _, part := range msg.Content {
		if tp, ok := part.(llm.TextPart); ok {
			text += tp.Text
		}
	}
	if text != "" {
		s.signalPromptStart(text)
	}
}

func (s *Session) displayAssistantMessage(msg llm.Message) {
	for _, part := range msg.Content {
		switch p := part.(type) {
		case llm.TextPart:
			_ = stream.WriteTLV(s.Output, stream.TagTextAssistant, p.Text)
			s.Output.Flush()
		case llm.ReasoningPart:
			_ = stream.WriteTLV(s.Output, stream.TagTextReasoning, p.Text)
			s.Output.Flush()
		case llm.ToolCallPart:
			if info := formatToolCall(p.ToolName, string(p.Input)); info != "" {
				idPrefixedInfo := "[:" + p.ToolCallID + ":]" + info
				_ = stream.WriteTLV(s.Output, stream.TagFunctionNotify, idPrefixedInfo)
				s.Output.Flush()
			}
		}
	}
}

func (s *Session) displayToolMessage(msg llm.Message) {
	for _, part := range msg.Content {
		if tc, ok := part.(llm.ToolCallPart); ok {
			if info := formatToolCall(tc.ToolName, string(tc.Input)); info != "" {
				idPrefixedInfo := "[:" + tc.ToolCallID + ":]" + info
				_ = stream.WriteTLV(s.Output, stream.TagFunctionNotify, idPrefixedInfo)
				s.Output.Flush()
			}
		}
	}
}

// ============================================================================
// Markdown Format (TLV encoding)
// ============================================================================

// formatSessionMarkdown converts SessionData to markdown format with TLV encoding.
func formatSessionMarkdown(data *SessionData) ([]byte, error) {
	var buf strings.Builder

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
func parseSessionMarkdown(data []byte) (*SessionData, error) {
	content := string(data)

	if !strings.HasPrefix(content, "---\n") {
		return nil, fmt.Errorf("session file missing YAML frontmatter")
	}

	endIdx := strings.Index(content[4:], "\n---\n")
	if endIdx == -1 {
		return nil, fmt.Errorf("session file missing frontmatter end marker")
	}

	frontmatter := content[4 : endIdx+4]
	body := content[endIdx+9:]

	var meta SessionMeta
	if err := yaml.Unmarshal([]byte(frontmatter), &meta); err != nil {
		return nil, fmt.Errorf("failed to parse frontmatter: %w", err)
	}

	sd := &SessionData{
		UpdatedAt: meta.UpdatedAt,
	}

	if len(body) > 0 {
		msgs, err := parseMessagesTLV(body)
		if err != nil {
			return nil, err
		}
		sd.Messages = msgs
	}

	return sd, nil
}

//nolint:gocyclo // parsing requires multiple branches for tag types
func parseMessagesTLV(body string) ([]llm.Message, error) {
	var messages []llm.Message
	var currentMsg *llm.Message

	reader := strings.NewReader(body)

	for {
		for {
			b, err := reader.ReadByte()
			if err == io.EOF {
				if currentMsg != nil {
					messages = append(messages, *currentMsg)
				}
				return messages, nil
			}
			if err != nil {
				return nil, fmt.Errorf("failed to read: %w", err)
			}
			if b != '\n' && b != '\r' && b != ' ' && b != '\t' {
				reader.UnreadByte()
				break
			}
		}

		tagBytes := make([]byte, 2)
		if _, err := io.ReadFull(reader, tagBytes); err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("failed to read tag: %w", err)
		}
		tag := string(tagBytes)

		var length int32
		if err := binary.Read(reader, binary.BigEndian, &length); err != nil {
			return nil, fmt.Errorf("failed to read length: %w", err)
		}

		if length < 0 || length > 10*1024*1024 {
			return nil, fmt.Errorf("invalid length: %d", length)
		}

		content := make([]byte, length)
		if _, err := io.ReadFull(reader, content); err != nil {
			return nil, fmt.Errorf("failed to read content: %w", err)
		}

		var msgPart llm.ContentPart
		var msgRole llm.MessageRole
		newMessage := false

		switch tag {
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

func formatToolResultOutput(output llm.ToolResultOutput) string {
	if text, ok := output.(llm.ToolResultOutputText); ok {
		return text.Text
	}
	if e, ok := output.(llm.ToolResultOutputError); ok {
		return e.Error
	}
	return fmt.Sprintf("%v", output)
}
