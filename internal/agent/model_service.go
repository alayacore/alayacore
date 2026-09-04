package agent

// Model service: model management, provider/agent creation, reasoning level.
//
// Extracted from session_model.go. Owns modelManager, runtimeManager, and
// the agent/provider pair. The run() goroutine owns the service; the task
// goroutine reads agent/provider via accessors. Model switching is cmdIdle
// (rejected during a task), so no mutex is needed for agent/provider access.

import (
	"fmt"

	"github.com/alayacore/alayacore/internal/config"
	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/llm/factory"
	"github.com/alayacore/alayacore/internal/llm/providers"
)

// modelService manages model configuration, provider/agent lifecycle, and
// reasoning/video settings. All owned by the run() goroutine.
type modelService struct {
	manager    *modelManager
	runtimeMgr *runtimeManager

	// Model resolution state
	sessionMetaModel string // model name from session file frontmatter
	overrideModel    string // --model CLI flag override
	initError        error  // set if --model refers to a non-existent model

	// Provider/Agent — written by run(), read by task goroutine.
	// Safe because model switching (cmdIdle) is rejected during tasks.
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

// newModelService creates a modelService with the given managers.
func newModelService(manager *modelManager, runtimeMgr *runtimeManager) *modelService {
	return &modelService{
		manager:        manager,
		runtimeMgr:     runtimeMgr,
		reasoningLevel: config.DefaultReasoningLevel,
	}
}

// ============================================================================
// Accessors (safe from task goroutine — written before task starts)
// ============================================================================

// ActiveModelName returns the active model's display name, or "".
func (ms *modelService) ActiveModelName() string {
	if ms.manager == nil {
		return ""
	}
	if model := ms.manager.getActive(); model != nil {
		return model.Name
	}
	return ""
}

// ActiveModel returns the active model config, or nil.
func (ms *modelService) ActiveModel() *modelConfig {
	if ms.manager == nil {
		return nil
	}
	return ms.manager.getActive()
}

// ActiveModelID returns the active model's ID.
func (ms *modelService) ActiveModelID() int {
	if ms.manager == nil {
		return 0
	}
	return ms.manager.getActiveID()
}

// HasModels returns true if at least one model is configured.
func (ms *modelService) HasModels() bool {
	return ms.manager != nil && ms.manager.hasModels()
}

// ModelConfigPath returns the path to the model config file.
func (ms *modelService) ModelConfigPath() string {
	if ms.manager == nil {
		return ""
	}
	return ms.manager.getFilePath()
}

// GetLoadErrors returns model config parse/validation errors.
func (ms *modelService) GetLoadErrors() []string {
	if ms.manager == nil {
		return nil
	}
	return ms.manager.getLoadErrors()
}

// HasRejected returns true if model configs were present but ALL were
// rejected (no usable models remain).
func (ms *modelService) HasRejected() bool {
	return ms.manager != nil && ms.manager.hasRejected()
}

// GetModels returns all configured models.
func (ms *modelService) GetModels() []modelConfig {
	if ms.manager == nil {
		return nil
	}
	return ms.manager.getModels()
}

// ============================================================================
// Model Resolution (priority chain)
// ============================================================================

// ResolveActiveModel applies the standard priority chain:
// runtime.conf → session file frontmatter → --model CLI flag.
// It also syncs the derived context limit from the resolved model so
// the startup model broadcast (sendSystemInfo(systemInfoAll)) carries
// the correct limit before SwitchModel runs — agent creation is lazy
// (first task), so SwitchModel alone would leave the limit at 0 for the
// entire startup broadcast and break the status bar's "tokens/limit".
func (ms *modelService) ResolveActiveModel() {
	ms.setActiveFromRuntimeConfig()
	ms.setActiveFromSessionMeta()
	ms.setActiveFromCliFlag()
	if model := ms.ActiveModel(); model != nil {
		ms.contextLimit = int64(model.ContextLimit)
	}
}

func (ms *modelService) setActiveFromRuntimeConfig() {
	if ms.manager == nil || ms.runtimeMgr == nil {
		return
	}
	activeModelName := ms.runtimeMgr.getActiveModel()
	if activeModelName != "" {
		if err := ms.manager.setActiveByName(activeModelName); err == nil {
			return
		}
	}
	ms.manager.setActiveToFirst()
}

func (ms *modelService) setActiveFromSessionMeta() {
	if ms.sessionMetaModel == "" || ms.manager == nil {
		return
	}
	_ = ms.manager.setActiveByName(ms.sessionMetaModel)
}

func (ms *modelService) setActiveFromCliFlag() {
	if ms.overrideModel == "" || ms.manager == nil {
		return
	}
	if err := ms.manager.setActiveByName(ms.overrideModel); err != nil {
		ms.initError = err
	}
}

// ============================================================================
// Model Switching
// ============================================================================

// SwitchModel creates a new provider and agent for the given model config.
func (ms *modelService) SwitchModel(modelConfig *modelConfig, baseTools []llm.Tool, systemPrompt, extraSystemPrompt string, maxSteps int) error {
	provider, agent, err := ms.createProviderAndAgent(modelConfig, baseTools, systemPrompt, extraSystemPrompt, maxSteps)
	if err != nil {
		return err
	}
	ms.provider = provider
	ms.agent = agent
	ms.contextLimit = int64(modelConfig.ContextLimit)
	if ms.provider != nil {
		ms.provider.SetReasoningLevel(ms.reasoningLevel)
		ms.provider.SetReasoningConfigs(modelConfig.ReasoningConfigs())
		ms.provider.SetVideoConfig(ms.videoFPS, ms.videoRes)
	}
	return nil
}

// EnsureInitialized checks if agent/provider are ready; if not, creates them
// from the active model. Safe to call multiple times — fast path when ready.
func (ms *modelService) EnsureInitialized(baseTools []llm.Tool, systemPrompt, extraSystemPrompt string, maxSteps int) error {
	if ms.agent != nil && ms.provider != nil {
		return nil
	}
	if ms.manager == nil {
		return fmt.Errorf("model manager not initialized")
	}
	activeModel := ms.manager.getActive()
	if activeModel == nil {
		return fmt.Errorf("no model configured; please add a model to model.conf")
	}
	return ms.SwitchModel(activeModel, baseTools, systemPrompt, extraSystemPrompt, maxSteps)
}

// Reset clears the agent and provider (e.g. after MCP init updates tools/prompt).
func (ms *modelService) Reset() {
	ms.agent = nil
	ms.provider = nil
}

// ============================================================================
// Settings
// ============================================================================

// SetReasoningLevel sets the reasoning level and syncs to the provider.
func (ms *modelService) SetReasoningLevel(level int) {
	ms.reasoningLevel = level
	if ms.provider != nil {
		ms.provider.SetReasoningLevel(level)
	}
}

// SetVideoConfig sets the default video FPS and resolution, and syncs to the provider.
func (ms *modelService) SetVideoConfig(fps int, resolution int) {
	ms.videoFPS = fps
	ms.videoRes = resolution
	if ms.provider != nil {
		ms.provider.SetVideoConfig(fps, resolution)
	}
}

// ============================================================================
// Provider/Agent Creation
// ============================================================================

func (ms *modelService) createProviderAndAgent(
	modelConfig *modelConfig,
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
		// Straight through, no inversion: model.conf spells the option
		// negatively so its absent form is already the behavior the agent
		// falls back on.
		SerialToolCalls: modelConfig.SerialToolCalls,
	})
	return provider, agent, nil
}

// ============================================================================
// Package-level helper
// ============================================================================

func createProviderFromConfig(modelCfg *modelConfig, debugDir, proxyURL string) (llm.Provider, error) {
	client, err := providers.NewHTTPClient(proxyURL, debugDir)
	if err != nil {
		return nil, fmt.Errorf("provider: failed to create HTTP client: %w", err)
	}

	return factory.NewProvider(factory.ProviderConfig{
		Type:             modelCfg.ProtocolType,
		APIKey:           modelCfg.APIKey,
		BaseURL:          modelCfg.BaseURL,
		Model:            modelCfg.ModelName,
		HTTPClient:       client,
		MaxTokens:        modelCfg.MaxTokens,
		ReasoningConfigs: modelCfg.ReasoningConfigs(),
		ReasoningField:   modelCfg.ReasoningField,
		// The same setting the agent got: the request field and the execution
		// order are one decision, and neither layer re-derives it.
		SerialToolCalls: modelCfg.SerialToolCalls,
	})
}
