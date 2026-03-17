package agent

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/stream"
)

func (s *Session) signalPromptStart(prompt string) {
	s.writeGapped(stream.TagTextUser, prompt)
}

func (s *Session) signalCommandStart(cmd string) {
	s.writeGapped(stream.TagTextUser, ":"+cmd)
}

func (s *Session) writeError(msg string) {
	s.writeGapped(stream.TagSystemError, msg)
}

func (s *Session) writeNotify(msg string) {
	s.writeGapped(stream.TagSystemNotify, msg)
}

// writeNotifyf writes a formatted notification message
func (s *Session) writeNotifyf(format string, args ...any) {
	s.writeNotify(fmt.Sprintf(format, args...))
}

func (s *Session) writeGapped(tag string, msg string) {
	if s.Output == nil {
		return
	}
	_ = stream.WriteTLV(s.Output, tag, msg) //nolint:errcheck // output stream, errors not critical
	s.Output.Flush()
}

func (s *Session) writeToolCall(toolName, input, id string) {
	if value := formatToolCall(toolName, input); value != "" {
		_ = stream.WriteTLV(s.Output, stream.TagFunctionShow, "[:"+id+":]"+value) //nolint:errcheck // output stream
	}
}

// trackUsage updates token usage statistics
func (s *Session) trackUsage(usage llm.Usage) {
	s.mu.Lock()
	s.TotalSpent.InputTokens += usage.InputTokens
	s.TotalSpent.OutputTokens += usage.OutputTokens
	// ContextTokens tracks the total context size (input tokens sent to API)
	// This is what counts toward provider context limits
	s.ContextTokens = usage.InputTokens
	s.mu.Unlock()
	s.sendSystemInfo()
}

func (s *Session) sendSystemInfo() {
	s.sendSystemInfoInternal(nil)
}

func (s *Session) sendSystemInfoInternal(activeModelConfig *ModelConfig) {
	if s.Output == nil {
		return
	}

	var models []ModelInfo
	var activeID string
	var activeModelName string
	var modelConfigPath string
	var hasModels bool

	if s.ModelManager != nil {
		models = s.ModelManager.GetModels()
		activeID = s.ModelManager.GetActiveID()
		if activeModelConfig != nil {
			activeModelName = activeModelConfig.Name
		} else if activeModel := s.ModelManager.GetActive(); activeModel != nil {
			activeModelName = activeModel.Name
		}
		modelConfigPath = s.ModelManager.GetFilePath()
		hasModels = s.ModelManager.HasModels()
	}

	s.mu.Lock()
	queueItems := make([]QueueItemInfo, len(s.taskQueue))
	for i, item := range s.taskQueue {
		var itemType, content string
		switch t := item.Task.(type) {
		case UserPrompt:
			itemType = "prompt"
			content = t.Text
		case CommandPrompt:
			itemType = "command"
			content = t.Command
		}
		queueItems[i] = QueueItemInfo{
			QueueID:   item.QueueID,
			Type:      itemType,
			Content:   content,
			CreatedAt: item.CreatedAt.Format(time.RFC3339),
		}
	}
	inProgress := s.inProgress
	contextTokens := s.ContextTokens
	contextLimit := s.ContextLimit
	totalTokens := s.TotalSpent.InputTokens + s.TotalSpent.OutputTokens
	s.mu.Unlock()

	info := SystemInfo{
		ContextTokens:     contextTokens,
		ContextLimit:      contextLimit,
		TotalTokens:       totalTokens,
		QueueItems:        queueItems,
		InProgress:        inProgress,
		Models:            models,
		ActiveModelID:     activeID,
		ActiveModelConfig: activeModelConfig,
		ActiveModelName:   activeModelName,
		HasModels:         hasModels,
		ModelConfigPath:   modelConfigPath,
	}
	data, _ := json.Marshal(info)                                     //nolint:errcheck // system info serialization
	_ = stream.WriteTLV(s.Output, stream.TagSystemData, string(data)) //nolint:errcheck // output stream
	s.Output.Flush()
}

// cleanIncompleteToolCalls removes incomplete tool calls from messages.
// Uses two-phase approach:
// 1. Forward pass to identify all unmatched tool calls (tool_call without corresponding tool_result)
// 2. Reverse pass to remove/filter messages containing unmatched tool calls
func cleanIncompleteToolCalls(messages []llm.Message) []llm.Message {
	// Phase 1: Forward pass to find all unmatched tool calls
	unmatchedCalls := make(map[string]bool)
	for _, msg := range messages {
		for _, part := range msg.Content {
			switch p := part.(type) {
			case llm.ToolCallPart:
				unmatchedCalls[p.ToolCallID] = true
			case llm.ToolResultPart:
				delete(unmatchedCalls, p.ToolCallID)
			}
		}
	}

	// Early exit if no unmatched calls
	if len(unmatchedCalls) == 0 {
		return messages
	}

	// Phase 2: Reverse pass to remove/filter messages with unmatched tool calls
	// Process from end, stopping at first complete message or user message
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]

		// Check if message has any unmatched tool calls
		hasUnmatchedCall := false
		for _, part := range msg.Content {
			if tc, ok := part.(llm.ToolCallPart); ok && unmatchedCalls[tc.ToolCallID] {
				hasUnmatchedCall = true
				break
			}
		}

		if hasUnmatchedCall {
			// Filter out unmatched tool calls
			filteredParts := make([]llm.ContentPart, 0, len(msg.Content))
			for _, part := range msg.Content {
				if tc, ok := part.(llm.ToolCallPart); ok && unmatchedCalls[tc.ToolCallID] {
					continue // Skip unmatched tool call
				}
				filteredParts = append(filteredParts, part)
			}

			if len(filteredParts) > 0 {
				// Has other content (text, reasoning, matched calls) - keep and stop
				messages[i].Content = filteredParts
				return messages[:i+1]
			}
			// Only had unmatched tool calls - remove and continue
			messages = messages[:i]
			continue
		}

		// No unmatched tool calls - message is complete, stop here
		return messages[:i+1]
	}

	return messages
}
