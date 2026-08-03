package agent

// Session model management: switching models, creating providers,
// syncing reasoning levels.
//
// These are thin wrappers over modelService. The model service
// owns the modelManager, runtimeManager, provider/agent instances,
// and model resolution logic.

import (
	"github.com/alayacore/alayacore/internal/llm"
)

// ============================================================================
// Model Switching
// ============================================================================

// SwitchModel switches the session to use a new model.
func (s *Session) SwitchModel(modelConfig *modelConfig) error {
	if err := s.modelService.SwitchModel(modelConfig, s.BaseTools, s.SystemPrompt, s.ExtraSystemPrompt, s.MaxSteps); err != nil {
		return err
	}
	// Sync back context limit from the service.
	s.ContextLimit = s.modelService.contextLimit
	s.sendSystemInfo(systemInfoModel)
	return nil
}

// InitError returns a non-nil error if session construction encountered a
// fatal problem (e.g. --model specified a non-existent model).
func (s *Session) InitError() error {
	return s.modelService.initError
}

func (s *Session) activeModelName() string {
	if s.modelService == nil {
		return ""
	}
	return s.modelService.ActiveModelName()
}

// GetRuntimeLoadErrors returns parse errors collected while loading
// runtime.conf (typos, unknown keys, etc.).
func (s *Session) GetRuntimeLoadErrors() []string {
	if s.modelService == nil || s.modelService.runtimeMgr == nil {
		return nil
	}
	return s.modelService.runtimeMgr.GetLoadErrors()
}

func (s *Session) ensureAgentInitialized() error {
	return s.modelService.EnsureInitialized(s.BaseTools, s.SystemPrompt, s.ExtraSystemPrompt, s.MaxSteps)
}

// ============================================================================
// Settings
// ============================================================================

// SetReasoningLevel sets the reasoning level.
func (s *Session) SetReasoningLevel(level int) {
	s.modelService.SetReasoningLevel(level)
	s.sendSystemInfo(systemInfoReasoning)
}

// SetVideoConfig sets the default video FPS and resolution.
func (s *Session) SetVideoConfig(fps int, resolution int) {
	s.modelService.SetVideoConfig(fps, resolution)
	s.sendSystemInfo(systemInfoVideoConfig)
}

// ============================================================================
// Accessors (for task goroutine)
// ============================================================================

// Agent returns the current LLM agent. Called from the task goroutine.
func (s *Session) Agent() *llm.Agent { return s.modelService.agent }
