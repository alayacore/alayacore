package agent

import (
	"encoding/json"

	"github.com/alayacore/alayacore/internal/stream"
)

// ============================================================================
// Output Helpers
// ============================================================================

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

func (s *Session) writeGapped(tag string, msg string) {
	if s.Output == nil {
		return
	}
	stream.WriteTLV(s.Output, tag, msg)
	s.Output.Flush()
}

func (s *Session) writeToolCall(toolName, input, id string) {
	if value := formatToolCall(toolName, input); value != "" {
		stream.WriteTLV(s.Output, stream.TagFunctionShow, "[:"+id+":]"+value)
	}
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
	queueCount := len(s.taskQueue)
	inProgress := s.inProgress
	contextTokens := s.ContextTokens
	contextLimit := s.ContextLimit
	totalTokens := s.TotalSpent.TotalTokens
	s.mu.Unlock()

	info := SystemInfo{
		ContextTokens:     contextTokens,
		ContextLimit:      contextLimit,
		TotalTokens:       totalTokens,
		QueueCount:        queueCount,
		InProgress:        inProgress,
		Models:            models,
		ActiveModelID:     activeID,
		ActiveModelConfig: activeModelConfig,
		ActiveModelName:   activeModelName,
		HasModels:         hasModels,
		ModelConfigPath:   modelConfigPath,
	}
	data, _ := json.Marshal(info)
	stream.WriteTLV(s.Output, stream.TagSystemData, string(data))
	s.Output.Flush()
}
