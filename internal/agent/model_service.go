package agent

// Model service: model management, provider/agent creation, reasoning level.
//
// Extracted from session_model.go. Owns modelManager, runtimeManager, and
// the agent/provider pair. The run() goroutine owns the service; the task
// goroutine reads agent/provider via accessors. Model switching is cmdIdle
// (rejected during a task), so no mutex is needed for agent/provider access.
//
// The two settings a running task must see change (:reasoning_level_set,
// :video_config) are the exception and are held atomically — see the fields
// below.

import (
	"fmt"
	"sync/atomic"

	"github.com/alayacore/alayacore/internal/config"
	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/llm/factory"
	"github.com/alayacore/alayacore/internal/llm/providers"
	"github.com/alayacore/alayacore/internal/protocol"
)

// modelService manages model configuration, provider/agent lifecycle, and
// reasoning/video settings. All owned by the run() goroutine, except the two
// atomically held settings a running task reads.
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

	// Settings read by a task while it runs.
	//
	// These two are the exception to the "run() owns it" rule above, because the
	// commands that write them (:reasoning_level_set, :video_config) are
	// immediate: a task already running may keep going, and the change has to be
	// visible to it from its next request on.
	//
	// reasoningLevel is shared, not copied: this is the one instance, and the
	// provider reads through a pointer to it (llm.ReasoningLevel) instead of
	// holding a second copy that every provider construction had to be told about.
	// videoDefault has no second reader — nothing pushes it anywhere — so it is a
	// plain swapped snapshot; the pair travels together because applyVideoDefault
	// stamps fps and res at once (see videoConfig).
	reasoningLevel llm.ReasoningLevel
	videoDefault   atomic.Pointer[videoConfig]

	// Context limit derived from active model config
	contextLimit int64

	// Configuration passed through from Session for agent creation
	debugDir string
	proxyURL string
}

// newModelService creates a modelService with the given managers.
func newModelService(manager *modelManager, runtimeMgr *runtimeManager) *modelService {
	ms := &modelService{
		manager:    manager,
		runtimeMgr: runtimeMgr,
	}
	ms.reasoningLevel.Set(config.DefaultReasoningLevel)
	return ms
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
		ms.provider.SetReasoningConfigs(modelConfig.ReasoningConfigs())
	}
	return nil
}

// EnsureInitialized checks if agent/provider are ready; if not, creates them
// from the active model. Safe to call multiple times — fast path when ready.
//
// Each failure carries the code a client acts on: nothing configured, nothing
// initialized, or the model subsystem refusing to build. Returning them bare
// would put the command plane's INTERNAL_UNCLASSIFIED fallback on the wire, which tells
// a caller nothing about whether to fix its config or its provider.
func (ms *modelService) EnsureInitialized(baseTools []llm.Tool, systemPrompt, extraSystemPrompt string, maxSteps int) error {
	if ms.agent != nil && ms.provider != nil {
		return nil
	}
	if ms.manager == nil {
		return &cmdErr{Code: protocol.ErrCodeInternalNotInitialized, Message: "model manager not initialized"}
	}
	activeModel := ms.manager.getActive()
	if activeModel == nil {
		return &cmdErr{Code: protocol.ErrCodeConfigMissing, Message: "no model configured; please add a model to model.conf"}
	}
	if err := ms.SwitchModel(activeModel, baseTools, systemPrompt, extraSystemPrompt, maxSteps); err != nil {
		return &cmdErr{Code: protocol.ErrCodeConfigModelFailed, Message: err.Error()}
	}
	return nil
}

// Reset clears the agent and provider (e.g. after MCP init updates tools/prompt).
func (ms *modelService) Reset() {
	ms.agent = nil
	ms.provider = nil
}

// ============================================================================
// Settings
// ============================================================================

// SetReasoningLevel sets the reasoning level.
//
// Callable from run() while a task runs (:reasoning_level_set is immediate). One
// store reaches every reader: the provider, which builds the next request body
// from this object, and the session's own consumers — the SM reasoning frame and
// the session file's metadata, the latter written from the task goroutine. A
// request already being built keeps the level it started with; the next one picks
// this up.
func (ms *modelService) SetReasoningLevel(level int) {
	ms.reasoningLevel.Set(level)
}

// ReasoningLevel returns the current reasoning level.
//
// Safe from the task goroutine.
func (ms *modelService) ReasoningLevel() int {
	return ms.reasoningLevel.Get()
}

// videoConfig is one snapshot of the session's video default.
//
// A struct rather than two fields because fps and res are read together — by
// applyVideoDefault, which stamps them onto a VideoPart — and two independent
// reads could pair the new fps with the old res, a setting that never existed.
// One pointer, swapped whole, makes the pair indivisible.
type videoConfig struct {
	FPS int
	Res int
}

// SetVideoConfig sets the default video FPS and resolution applied to videos
// that enter a session. It is a session-level *default*: each VideoPart
// snapshots the value when it is ingested, so changing the default never
// rewrites a message already in the record. Nothing is pushed to the provider —
// there is no global video state to push (see llm.VideoPart).
//
// Callable from run() while a task runs (:video_config is immediate): the task
// goroutine stamps the video it is producing with whichever snapshot it loads,
// and the next video stamped gets this one.
func (ms *modelService) SetVideoConfig(fps int, resolution int) {
	ms.videoDefault.Store(&videoConfig{FPS: fps, Res: resolution})
}

// VideoConfig returns the video default as it was at one instant.
//
// Safe from the task goroutine. Unset (never configured) and 0/0 are the same
// thing: a part with no settings of its own is stamped 0/0, which is how "let
// the provider decide" travels (see llm.VideoPart).
func (ms *modelService) VideoConfig() videoConfig {
	if d := ms.videoDefault.Load(); d != nil {
		return *d
	}
	return videoConfig{}
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
	provider, err := ms.createProviderFromConfig(modelConfig)
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
		// nil stays nil: "not declared" must reach the agent as the zero value
		// it already treats as "the protocol decides", and an empty declaration
		// ("no media") must not be confused with it.
		InputModalities: modelConfig.InputModalities,
	})
	return provider, agent, nil
}

// ============================================================================
// Package-level helper
// ============================================================================

// createProviderFromConfig builds one provider for modelCfg.
//
// A method rather than a package function because the provider needs one piece of
// session state at construction: the reasoning level object it will read on every
// request. Handing over the address of ms.reasoningLevel is what makes the level
// one value instead of a copy each side has to keep in step.
func (ms *modelService) createProviderFromConfig(modelCfg *modelConfig) (llm.Provider, error) {
	client, err := providers.NewHTTPClient(ms.proxyURL, ms.debugDir)
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
		ReasoningLevel:   &ms.reasoningLevel,
		ReasoningConfigs: modelCfg.ReasoningConfigs(),
		ReasoningField:   modelCfg.ReasoningField,
		// The same setting the agent got: the request field and the execution
		// order are one decision, and neither layer re-derives it.
		SerialToolCalls: modelCfg.SerialToolCalls,
	})
}
