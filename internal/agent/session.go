package agent

// Session manages conversation state and task execution.
//
// ARCHITECTURE:
//   Session uses an actor model: the run() goroutine owns all mutable
//   state, and the task goroutine communicates state changes via typed
//   events on taskEventCh. All cross-goroutine communication is channel-based.
//
//   Three goroutines:
//     1. inputPump — reads TLV frames from input, sends parsed messages
//        to the main loop via s.inputMsgCh.  It has no knowledge of commands
//        and never touches session state.
//     2. run() — main loop that owns Contents, active task, and system
//        info. Processes input messages, dispatches commands, manages
//        cancellation.
//     3. task goroutine — spawned by run() to execute each task. It
//        receives a copy of s.Contents, accumulates new content parts,
//        and sends the final state back to run() via taskResultCh on
//        completion.
//
//   Cross-goroutine communication:
//     inputMsgCh (inputMsg channel)  — inputPump → run()
//     taskEventCh (taskEvent)        — task → run()
//     taskCancel (func call)         — run() → task (cancellation via cancelRunningTask)
//     taskResultCh                   — task → run (full ContentParts list)
//     mcpService.Events()            — mcpService → run() (MCP init events: connect/OAuth/discover)
//
// Related files:
//   - session_types.go — type definitions (Task, SessionConfig, etc.)
//   - session_event.go — taskEvent types for actor model communication
//   - session_model.go — model management, provider creation, reasoning level
//   - session_task.go  — input processing, prompt execution, agent loop
//   - session_io.go    — command handling, summarize, save
//   - session_output.go — TLV write helpers, usage tracking, system info
//   - session_persist.go — session save/load, markdown format

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/alayacore/alayacore/internal/config"
	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/protocol"
	"github.com/alayacore/alayacore/internal/skills"
	"github.com/alayacore/alayacore/internal/tlv"
)

// sessionConfig groups fields that are set once at construction and
// never modified thereafter.
type sessionConfig struct {
	modelService  *modelService
	SkillsManager *skills.Manager

	SessionConfig

	toolConfirmSet map[string]struct{}
}

// taskHandle encapsulates the mutable state of a currently running task.
// It is created by tryStartNextTask and consumed by handleTaskDone.
// Grouping these fields prevents inconsistent state that could arise from
// out-of-order method calls on individual fields (e.g. setting inProgress
// without a cancel func, or clearing them separately).
type taskHandle struct {
	cancel    context.CancelFunc // cancels the task's per-task context
	step      int                // current agent step (set via stepStartEvent)
	commandID string             // CI command ID when started by :continue/:summarize
}

// runState groups fields owned exclusively by the run() goroutine.
// All reads and writes happen in the run() event loop.
type runState struct {
	Contents []llm.ContentPart // flat, ordered, 1:1 with TLV — set from task result

	activeTask *taskHandle // non-nil when a task is running; nil when idle

	// taskCommandID holds the command ID of the task that just finished.
	// sendTaskMsg uses it for the completion taskMsg, since activeTask is
	// cleared before the final broadcast.
	taskCommandID string

	inputMsgCh   chan inputMsg // inputPump → run: parsed TLV messages
	taskEventCh  chan taskEvent
	taskResultCh chan []llm.ContentPart

	// cancelReqCh lets adapters request a task cancellation from any
	// goroutine. The request carries a channel for the outcome (true if a
	// task was running and was canceled); run() processes the request so
	// activeTask stays single-threaded. Buffered so the sender never
	// blocks when run() is between select iterations.
	cancelReqCh chan chan bool

	// mcpService drives the entire MCP initialization lifecycle.
	// The run() goroutine reads from its Events() channel and reacts:
	//   "auth_required" → shows dialog, sends :mcp_confirm <code> <redirect_uri>
	//   "done"         → applies tools, marks MCP ready
	mcpService *mcpService
}

// sharedState groups fields that are either genuinely cross-goroutine
// (synchronized via atomics) or owned by a single goroutine with
// design guarantees that prevent concurrent access.
type sharedState struct {
	ContextTokens int64 // last-known context token count; updated by run() from task events
	ContextLimit  int64 // maximum context window size (input+output); set from model config

	histCounter uint64

	sessionCtx    context.Context
	sessionCancel context.CancelFunc

	confirmChs map[string]chan bool
	confirmMu  sync.Mutex

	outputBroken atomic.Bool

	// state is the startup lifecycle phase (SessionState), published
	// atomically so adapters can query it from any goroutine. Written
	// only by the run() goroutine (and constructors); see syncState().
	state atomic.Int32
}

// Session manages conversation state and task execution.
// Fields are grouped into embedded sub-structs by ownership:
//   - sessionConfig — immutable after construction
//   - runState      — owned by the run() goroutine
//   - sharedState   — cross-goroutine, synchronized via atomics/channels
//     (incl. the startup lifecycle state, SessionState)
type Session struct {
	sessionConfig
	runState
	sharedState

	runDoneCh chan struct{} // closed when run() exits
	CreatedAt time.Time
}

// activeTaskStep returns the current step of the active task, or 0 if idle.
func (s *Session) activeTaskStep() int {
	if s.activeTask != nil {
		return s.activeTask.step
	}
	return 0
}

// Done returns a channel that is closed when run() has exited.
func (s *Session) Done() <-chan struct{} {
	return s.runDoneCh
}

// State returns the session's startup lifecycle phase.
// Safe to call from any goroutine; the value is published atomically.
// External callers may observe SessionStarting until run() has processed
// the first events — see setState/syncState for the exact transition points.
func (s *Session) State() SessionState {
	return SessionState(s.state.Load())
}

// IsInitialized reports whether initialization is complete: the session
// has been loaded (replay done — guaranteed by construction) and MCP init
// has settled (done/canceled/aborted, or never configured).
// It does NOT imply that an LLM agent has been created — agent creation
// is lazy (first task) by design.
func (s *Session) IsInitialized() bool {
	return s.State() == SessionReady
}

// setState writes a new lifecycle phase. Transitions to SessionReady
// broadcast exactly one SM "session" frame so adapters have an
// authoritative "ready" signal. No-op when the phase is unchanged.
// Must only be called from the run() goroutine (constructors store the
// initial SessionStarting directly — it is never broadcast).
func (s *Session) setState(phase SessionState) {
	if s.State() == phase {
		return
	}
	s.state.Store(int32(phase))
	if phase == SessionReady {
		s.writeSystemMsg(sessionMsg{State: SessionReady.String()})
	}
}

// syncState advances the lifecycle state after MCP init progress.
// Idempotent: only transitions SessionInitializing → SessionReady once
// mcpService reports ready. Must only be called from the run() goroutine
// (or run()'s own setup); readers use the atomic publish in State().
func (s *Session) syncState() {
	if s.State() == SessionInitializing && s.mcpService.IsReady() {
		s.setState(SessionReady)
	}
}

// HasModels returns true if the model manager has at least one model.
func (s *Session) HasModels() bool {
	return s.modelService.HasModels()
}

func (s *Session) ModelConfigPath() string {
	return s.modelService.ModelConfigPath()
}

// GetLoadErrors returns model config parse/validation errors.
func (s *Session) GetLoadErrors() []string { return s.modelService.GetLoadErrors() }

// HasRejected returns true if model configs were present but ALL were
// rejected (no usable models remain).
func (s *Session) HasRejected() bool { return s.modelService.HasRejected() }

// ============================================================================
// Session Lifecycle
// ============================================================================

// LoadOrNewSession loads a session from file or creates a new one.
// Returns an error if the session file exists but has an incompatible version
// (version must match messageVersion exactly).
// A missing session file is the normal first-run case and is silent.
// If the session file exists but fails to load for other reasons (corrupt data,
// permissions), an error is printed to stderr and a new session is created.
// The returned session is ready to use but NOT yet started —
// call Start() to begin processing input.
func LoadOrNewSession(cfg SessionConfig) (*Session, string, error) {
	cfg.SessionFile = config.ExpandPath(cfg.SessionFile)
	if cfg.SessionFile == "" {
		return newSession(cfg), cfg.SessionFile, nil
	}

	data, loadErr := loadSession(cfg.SessionFile)
	if loadErr == nil {
		s := RestoreFromSession(cfg, data)
		if replayErr := s.replayContentsToAdapter(); replayErr != nil {
			s.modelService.initError = replayErr
		}
		return s, cfg.SessionFile, nil
	}

	if errors.Is(loadErr, errSessionVersionMismatch) {
		return nil, "", loadErr
	}

	// A missing session file is the normal "start fresh" case — silent.
	// Only report when the file EXISTS but cannot be loaded (corrupt data,
	// permission error, etc.): emit system error messages and start fresh
	// rather than failing entirely.
	if !errors.Is(loadErr, os.ErrNotExist) {
		_ = protocol.WriteSystemMsg(cfg.Output, protocol.ErrorMsg{Text: fmt.Sprintf("could not load session file %q: %v", cfg.SessionFile, loadErr)})
		_ = protocol.WriteSystemMsg(cfg.Output, protocol.ErrorMsg{Text: "starting new session"})
	}
	return newSession(cfg), cfg.SessionFile, nil
}

// newSession creates a fresh session. Does NOT start goroutines —
// call Start() to begin processing input.
func newSession(cfg SessionConfig) *Session {
	ctx, cancel := context.WithCancel(context.Background())

	modelService := newModelService(newModelManager(cfg.ModelConfigPath), newRuntimeManager(cfg.RuntimeConfigPath))
	modelService.overrideModel = cfg.OverrideActiveModel
	modelService.proxyURL = cfg.ProxyURL
	modelService.debugDir = cfg.DebugLogDir

	s := &Session{
		sessionConfig: sessionConfig{
			modelService:  modelService,
			SkillsManager: cfg.SkillsMgr,
			SessionConfig: cfg,
		},
		runState: runState{
			Contents:     make([]llm.ContentPart, 0),
			taskEventCh:  make(chan taskEvent, 64),
			taskResultCh: make(chan []llm.ContentPart, 1),
			cancelReqCh:  make(chan chan bool, 1),
		},
		sharedState: sharedState{
			sessionCtx:    ctx,
			sessionCancel: cancel,
			confirmChs:    make(map[string]chan bool),
		},
		runDoneCh: make(chan struct{}),
		CreatedAt: time.Now(),
	}
	s.initToolConfirmSet(cfg.ToolConfirmTools)
	s.modelService.ResolveActiveModel()

	// --reasoning-level CLI flag overrides the hardcoded default for fresh
	// sessions. modelService.SetReasoningLevel (not Session.SetReasoningLevel)
	// is used so no SM reasoning frame is emitted before the startup broadcast.
	if cfg.ReasoningLevelSet {
		s.modelService.SetReasoningLevel(cfg.ReasoningLevel)
	}

	if model := s.modelService.ActiveModel(); model != nil {
		s.ContextLimit = s.modelService.contextLimit
	}

	// Set up MCP service (manages init lifecycle).
	s.mcpService = newMCPService(cfg.MCPInit, s.Output)

	// Session load + replay are synchronous and complete by construction
	// (LoadOrNewSession), so the initial lifecycle state is Starting.
	s.state.Store(int32(SessionStarting))

	s.sendSystemInfo(systemInfoAll)
	return s
}

// RestoreFromSession creates a session from saved data.
// Does NOT start goroutines — call Start() to begin processing input.
func RestoreFromSession(cfg SessionConfig, data *sessionData) *Session {
	ctx, cancel := context.WithCancel(context.Background())

	modelService := newModelService(newModelManager(cfg.ModelConfigPath), newRuntimeManager(cfg.RuntimeConfigPath))
	modelService.sessionMetaModel = data.ActiveModel
	modelService.overrideModel = cfg.OverrideActiveModel
	modelService.proxyURL = cfg.ProxyURL
	modelService.debugDir = cfg.DebugLogDir

	s := &Session{
		sessionConfig: sessionConfig{
			modelService:  modelService,
			SkillsManager: cfg.SkillsMgr,
			SessionConfig: cfg,
		},
		runState: runState{
			Contents:     data.Contents,
			taskEventCh:  make(chan taskEvent, 64),
			taskResultCh: make(chan []llm.ContentPart, 1),
			cancelReqCh:  make(chan chan bool, 1),
		},
		sharedState: sharedState{
			sessionCtx:    ctx,
			sessionCancel: cancel,
			confirmChs:    make(map[string]chan bool),
		},
		runDoneCh: make(chan struct{}),
		CreatedAt: data.CreatedAt,
	}
	s.ContextTokens = data.ContextTokens
	s.histCounter = uint64(len(s.Contents))

	s.initToolConfirmSet(cfg.ToolConfirmTools)
	s.modelService.ResolveActiveModel()

	// Precedence: an explicit --reasoning-level CLI flag wins over the
	// session file's saved reasoning_level; without it, the saved value
	// (or the package default for files missing the key) is restored.
	reasoningLevel := data.ReasoningLevel
	if cfg.ReasoningLevelSet {
		reasoningLevel = cfg.ReasoningLevel
	}
	s.modelService.SetReasoningLevel(reasoningLevel)

	// Set up MCP service (manages init lifecycle).
	s.mcpService = newMCPService(cfg.MCPInit, s.Output)

	// Apply context limit from the resolved model so the status bar
	// can show "tokens/limit (pct%)" immediately, before any API call.
	if model := s.modelService.ActiveModel(); model != nil {
		s.ContextLimit = s.modelService.contextLimit
	}

	// Session load + replay are synchronous and complete by construction
	// (LoadOrNewSession), so the initial lifecycle state is Starting.
	s.state.Store(int32(SessionStarting))

	s.sendSystemInfo(systemInfoAll)
	return s
}

// replayContentsToAdapter sends all content parts to the adapter with history IDs,
// so the adapter can reference them by ID even after session reload.
// No UE markers are needed — writeColored's non-user-tag flush and the
// bufferUserContent / AppendFromTLV incremental path handle grouping.
func (s *Session) replayContentsToAdapter() error {
	for _, part := range s.Contents {
		tag, content, err := contentPartToTLV(part)
		if err != nil {
			return fmt.Errorf("corrupt session file: failed to serialize content part (HistoryID=%d): %w", part.GetHistoryID(), err)
		}

		s.writeTLV(tag, tlv.WrapID(strconv.FormatUint(part.GetHistoryID(), 10), content))
	}

	return nil
}

func (s *Session) histIncAndGet() uint64 {
	s.histCounter++
	return s.histCounter
}

// Start begins processing input in a single goroutine.
// Must be called exactly once after construction.
func (s *Session) Start() {
	go s.run()
}

// cancelRunningTask cancels the currently running task via its per-task
// context. Returns true if a task was actually running and was canceled.
func (s *Session) cancelRunningTask() bool {
	if s.activeTask == nil {
		return false
	}
	if s.activeTask.cancel != nil {
		s.activeTask.cancel()
		return true
	}
	return false
}

// initToolConfirmSet builds the tool confirmation lookup set from config.
// If ToolConfirmTools is empty, toolConfirmSet stays nil and no tools
// require confirmation.
func (s *Session) initToolConfirmSet(tools []string) {
	if len(tools) == 0 {
		return
	}
	s.toolConfirmSet = make(map[string]struct{}, len(tools))
	for _, name := range tools {
		s.toolConfirmSet[name] = struct{}{}
	}
}
