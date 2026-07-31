package agent

// Model service: model management, provider/agent creation, reasoning level.
//
// Extracted from session_model.go. Owns ModelManager, RuntimeManager, and
// the agent/provider pair. The run() goroutine owns the service; the task
// goroutine reads agent/provider via accessors. Model switching is CmdIdle
// (rejected during a task), so no mutex is needed for agent/provider access.

import (
	"fmt"

	"github.com/alayacore/alayacore/internal/config"
	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/llm/factory"
	"github.com/alayacore/alayacore/internal/llm/providers"
)

// ModelService manages model configuration, provider/agent lifecycle, and
// reasoning/video settings. All owned by the run() goroutine.
type ModelService struct {
	manager    *ModelManager
	runtimeMgr *RuntimeManager

	// Model resolution state
	sessionMetaModel string // model name from session file frontmatter
	overrideModel    string // --model CLI flag override
	initError        error  // set if --model refers to a non-existent model

	// Provider/Agent — written by run(), read by task goroutine.
	// Safe because model switching (CmdIdle) is rejected during tasks.
	provider llm.Provider
	agent    *llm.Agent

	// Settings synced to provider
	reasoningLevel int
	videoFPS       int
	videoRes       int

	// Context limit derived from active model config
	contextLimit int64

	// Configuration passed through from Session for agent creation
	debugDir string
	proxyURL string
}

// NewModelService creates a ModelService with the given managers.
func NewModelService(manager *ModelManager, runtimeMgr *RuntimeManager) *ModelService {
	return &ModelService{
		manager:        manager,
		runtimeMgr:     runtimeMgr,
		reasoningLevel: config.DefaultReasoningLevel,
	}
}

// ============================================================================
// Accessors (safe from task goroutine — written before task starts)
// ============================================================================

// ActiveModelName returns the active model's display name, or "".
func (ms *ModelService) ActiveModelName() string {
	if ms.manager == nil {
		return ""
	}
	if model := ms.manager.GetActive(); model != nil {
		return model.Name
	}
	return ""
}

// ActiveModel returns the active model config, or nil.
func (ms *ModelService) ActiveModel() *config.ModelConfig {
	if ms.manager == nil {
		return nil
	}
	return ms.manager.GetActive()
}

// ActiveModelID returns the active model's ID.
func (ms *ModelService) ActiveModelID() int {
	if ms.manager == nil {
		return 0
	}
	return ms.manager.GetActiveID()
}

// HasModels returns true if at least one model is configured.
func (ms *ModelService) HasModels() bool {
	return ms.manager != nil && ms.manager.HasModels()
}

// ModelConfigPath returns the path to the model config file.
func (ms *ModelService) ModelConfigPath() string {
	if ms.manager == nil {
		return ""
	}
	return ms.manager.GetFilePath()
}

// GetLoadErrors returns model config parse/validation errors.
func (ms *ModelService) GetLoadErrors() []string {
	if ms.manager == nil {
		return nil
	}
	return ms.manager.GetLoadErrors()
}

// HasRejected returns true if model configs were present but ALL were
// rejected (no usable models remain).
func (ms *ModelService) HasRejected() bool {
	return ms.manager != nil && ms.manager.HasRejected()
}

// GetModels returns all configured models.
func (ms *ModelService) GetModels() []config.ModelConfig {
	if ms.manager == nil {
		return nil
	}
	return ms.manager.GetModels()
}

// ============================================================================
// Model Resolution (priority chain)
// ============================================================================

// ResolveActiveModel applies the standard priority chain:
// runtime.conf → session file frontmatter → --model CLI flag.
func (ms *ModelService) ResolveActiveModel() {
	ms.setActiveFromRuntimeConfig()
	ms.setActiveFromSessionMeta()
	ms.setActiveFromCliFlag()
}

func (ms *ModelService) setActiveFromRuntimeConfig() {
	if ms.manager == nil || ms.runtimeMgr == nil {
		return
	}
	activeModelName := ms.runtimeMgr.GetActiveModel()
	if activeModelName != "" {
		if err := ms.manager.SetActiveByName(activeModelName); err == nil {
			return
		}
	}
	ms.manager.SetActiveToFirst()
}

func (ms *ModelService) setActiveFromSessionMeta() {
	if ms.sessionMetaModel == "" || ms.manager == nil {
		return
	}
	_ = ms.manager.SetActiveByName(ms.sessionMetaModel)
}

func (ms *ModelService) setActiveFromCliFlag() {
	if ms.overrideModel == "" || ms.manager == nil {
		return
	}
	if err := ms.manager.SetActiveByName(ms.overrideModel); err != nil {
		ms.initError = err
	}
}

// ============================================================================
// Model Switching
// ============================================================================

// SwitchModel creates a new provider and agent for the given model config.
func (ms *ModelService) SwitchModel(modelConfig *config.ModelConfig, baseTools []llm.Tool, systemPrompt, extraSystemPrompt string, maxSteps int) error {
	provider, agent, err := ms.createProviderAndAgent(modelConfig, baseTools, systemPrompt, extraSystemPrompt, maxSteps)
	if err != nil {
		return err
	}
	ms.provider = provider
	ms.agent = agent
	ms.contextLimit = int64(modelConfig.ContextLimit)
	if ms.provider != nil {
		ms.provider.SetReasoningLevel(ms.reasoningLevel)
		ms.provider.SetVideoConfig(ms.videoFPS, ms.videoRes)
	}
	return nil
}

// EnsureInitialized checks if agent/provider are ready; if not, creates them
// from the active model. Safe to call multiple times — fast path when ready.
func (ms *ModelService) EnsureInitialized(baseTools []llm.Tool, systemPrompt, extraSystemPrompt string, maxSteps int) error {
	if ms.agent != nil && ms.provider != nil {
		return nil
	}
	if ms.manager == nil {
		return fmt.Errorf("model manager not initialized")
	}
	activeModel := ms.manager.GetActive()
	if activeModel == nil {
		return fmt.Errorf("no model configured; please add a model to model.conf")
	}
	return ms.SwitchModel(activeModel, baseTools, systemPrompt, extraSystemPrompt, maxSteps)
}

// Reset clears the agent and provider (e.g. after MCP init updates tools/prompt).
func (ms *ModelService) Reset() {
	ms.agent = nil
	ms.provider = nil
}

// ============================================================================
// Settings
// ============================================================================

// SetReasoningLevel sets the reasoning level and syncs to the provider.
func (ms *ModelService) SetReasoningLevel(level int) {
	ms.reasoningLevel = level
	if ms.provider != nil {
		ms.provider.SetReasoningLevel(level)
	}
}

// SetVideoConfig sets the default video FPS and resolution, and syncs to the provider.
func (ms *ModelService) SetVideoConfig(fps int, resolution int) {
	ms.videoFPS = fps
	ms.videoRes = resolution
	if ms.provider != nil {
		ms.provider.SetVideoConfig(fps, resolution)
	}
}

// ============================================================================
// Provider/Agent Creation
// ============================================================================

func (ms *ModelService) createProviderAndAgent(
	modelConfig *config.ModelConfig,
	baseTools []llm.Tool,
	systemPrompt, extraSystemPrompt string,
	maxSteps int,
) (llm.Provider, *llm.Agent, error) {
	provider, err := createProviderFromConfig(modelConfig, ms.debugDir, ms.proxyURL)
	if err != nil {
		return nil, nil, err
	}
	agent := llm.NewAgent(llm.AgentConfig{
		Provider:          provider,
		Tools:             baseTools,
		SystemPrompt:      systemPrompt,
		ExtraSystemPrompt: extraSystemPrompt,
		MaxSteps:          maxSteps,
	})
	return provider, agent, nil
}

// ============================================================================
// Package-level helper
// ============================================================================

func createProviderFromConfig(modelCfg *config.ModelConfig, debugDir, proxyURL string) (llm.Provider, error) {
	client, err := providers.NewHTTPClient(proxyURL, debugDir)
	if err != nil {
		return nil, fmt.Errorf("provider: failed to create HTTP client: %w", err)
	}

	return factory.NewProvider(factory.ProviderConfig{
		Type:       modelCfg.ProtocolType,
		APIKey:     modelCfg.APIKey,
		BaseURL:    modelCfg.BaseURL,
		Model:      modelCfg.ModelName,
		HTTPClient: client,
		MaxTokens:  modelCfg.MaxTokens,
	})
}
