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
	s.sendSystemInfo(systemInfoModel)
	// A task in flight is running on the agent that was just replaced, and it
	// will keep running on it: processPrompt captured its snapshot when the task
	// started, so the agent loop cannot change model under itself. Say so where
	// it happens — the alternative is a user watching an answer arrive from a
	// model they believe they already left.
	if s.activeTask != nil {
		s.writeNotifyf("model switched to %q; the task in flight finishes on the previous model, and the next prompt uses this one", modelConfig.Name)
	}
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
	return s.modelService.runtimeMgr.getLoadErrors()
}

func (s *Session) ensureAgentInitialized() error {
	if err := s.modelService.EnsureInitialized(s.BaseTools, s.SystemPrompt, s.ExtraSystemPrompt, s.MaxSteps); err != nil {
		return err
	}
	// The lazy build publishes its snapshot with the limit it was built from
	// (EnsureInitialized -> SwitchModel -> publishRuntime), so there is nothing
	// to sync here: the task reads that one value.
	return nil
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
func (s *Session) Agent() *llm.Agent {
	if rt := s.modelService.currentRuntime(); rt != nil {
		return rt.agent
	}
	return nil
}

// currentRuntime is the single snapshot a task works from: the agent it runs on
// and the context limit that agent's window sets. Taking it once, at the top of
// a task, is what keeps a switch from landing in the middle of one.
func (s *Session) currentRuntime() *modelRuntime { return s.modelService.currentRuntime() }

// contextLimit is the window the request being built is measured against. The
// published snapshot answers for an initialized session; before the first agent
// exists, the resolved model's number does (that is the value the status bar
// shows from startup).
func (s *Session) contextLimit() int64 {
	if rt := s.currentRuntime(); rt != nil {
		return rt.contextLimit
	}
	return s.modelService.contextLimit.Load()
}
