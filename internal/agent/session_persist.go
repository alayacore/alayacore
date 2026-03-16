package agent

import (
	"fmt"
	"os"
	"time"

	"charm.land/fantasy"
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
	if err := os.WriteFile(path, raw, 0644); err != nil {
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
		case fantasy.MessageRoleUser:
			s.displayUserMessage(msg)
		case fantasy.MessageRoleAssistant:
			s.displayAssistantMessage(msg)
		case fantasy.MessageRoleTool:
			s.displayToolMessage(msg)
		}
	}
}

func (s *Session) displayUserMessage(msg fantasy.Message) {
	var text string
	for _, part := range msg.Content {
		if tp, ok := part.(fantasy.TextPart); ok {
			text += tp.Text
		}
	}
	if text != "" {
		s.signalPromptStart(text)
	}
}

func (s *Session) displayAssistantMessage(msg fantasy.Message) {
	for _, part := range msg.Content {
		switch p := part.(type) {
		case fantasy.TextPart:
			stream.WriteTLV(s.Output, stream.TagTextAssistant, p.Text)
			s.Output.Flush()
		case fantasy.ReasoningPart:
			stream.WriteTLV(s.Output, stream.TagTextReasoning, p.Text)
			s.Output.Flush()
		case fantasy.ToolCallPart:
			if info := formatToolCall(p.ToolName, p.Input); info != "" {
				stream.WriteTLV(s.Output, stream.TagFunctionShow, info)
				s.Output.Flush()
			}
		}
	}
}

func (s *Session) displayToolMessage(msg fantasy.Message) {
	for _, part := range msg.Content {
		if tc, ok := part.(fantasy.ToolCallPart); ok {
			if info := formatToolCall(tc.ToolName, tc.Input); info != "" {
				stream.WriteTLV(s.Output, stream.TagFunctionShow, info)
				s.Output.Flush()
			}
		}
	}
}
