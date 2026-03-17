package agent

import (
	"fmt"
	"os"
	"time"

	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/stream"
)

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
			_ = stream.WriteTLV(s.Output, stream.TagTextAssistant, p.Text) //nolint:errcheck // output stream
			s.Output.Flush()
		case llm.ReasoningPart:
			_ = stream.WriteTLV(s.Output, stream.TagTextReasoning, p.Text) //nolint:errcheck // output stream
			s.Output.Flush()
		case llm.ToolCallPart:
			if info := formatToolCall(p.ToolName, string(p.Input)); info != "" {
				_ = stream.WriteTLV(s.Output, stream.TagFunctionShow, info) //nolint:errcheck // output stream
				s.Output.Flush()
			}
		}
	}
}

func (s *Session) displayToolMessage(msg llm.Message) {
	for _, part := range msg.Content {
		if tc, ok := part.(llm.ToolCallPart); ok {
			if info := formatToolCall(tc.ToolName, string(tc.Input)); info != "" {
				_ = stream.WriteTLV(s.Output, stream.TagFunctionShow, info) //nolint:errcheck // output stream
				s.Output.Flush()
			}
		}
	}
}
