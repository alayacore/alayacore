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
//     mcpService.Events()            — MCPService → run() (MCP init events: connect/OAuth/discover)
//
// Related files:
//   - session_types.go — type definitions (Task, SessionConfig, etc.)
//   - session_event.go — TaskEvent types for actor model communication
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
	modelService  *ModelService
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
	step      int                // current agent step (set via StepStartEvent)
	commandID string             // CI command ID when started by :continue/:summarize
}

// runState groups fields owned exclusively by the run() goroutine.
// All reads and writes happen in the run() event loop.
type runState struct {
	Contents []llm.ContentPart // flat, ordered, 1:1 with TLV — set from task result

	activeTask *taskHandle // non-nil when a task is running; nil when idle

	// taskCommandID holds the command ID of the task that just finished.
	// sendTaskMsg uses it for the completion TaskMsg, since activeTask is
	// cleared before the final broadcast.
	taskCommandID string

	inputMsgCh   chan inputMsg // inputPump → run: parsed TLV messages
	taskEventCh  chan TaskEvent
	taskResultCh chan []llm.ContentPart

	// mcpService drives the entire MCP initialization lifecycle.
	// The run() goroutine reads from its Events() channel and reacts:
	//   "auth_required" → shows dialog, sends :mcp_confirm <code> <redirect_uri>
	//   "done"         → applies tools, marks MCP ready
	mcpService *MCPService
}

// activeTaskStep returns the current step of the active task, or 0 if idle.
func (s *Session) activeTaskStep() int {
	if s.activeTask != nil {
		return s.activeTask.step
	}
	return 0
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
}

// Session manages conversation state and task execution.
// Fields are grouped into embedded sub-structs by ownership:
//   - sessionConfig — immutable after construction
//   - runState      — owned by the run() goroutine
//   - sharedState   — cross-goroutine, synchronized via atomics/channels
type Session struct {
	sessionConfig
	runState
	sharedState

	runDoneCh chan struct{} // closed when run() exits
	CreatedAt time.Time
}

// Done returns a channel that is closed when run() has exited.
func (s *Session) Done() <-chan struct{} {
	return s.runDoneCh
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
// (version must match MessageVersion exactly).
// A missing session file is the normal first-run case and is silent.
// If the session file exists but fails to load for other reasons (corrupt data,
// permissions), an error is printed to stderr and a new session is created.
// The returned session is ready to use but NOT yet started —
// call Start() to begin processing input.
func LoadOrNewSession(cfg SessionConfig) (*Session, string, error) {
	cfg.SessionFile = config.ExpandPath(cfg.SessionFile)
	if cfg.SessionFile == "" {
		return NewSession(cfg), cfg.SessionFile, nil
	}

	data, loadErr := LoadSession(cfg.SessionFile)
	if loadErr == nil {
		s := RestoreFromSession(cfg, data)
		if replayErr := s.replayContentsToAdapter(); replayErr != nil {
			s.modelService.initError = replayErr
		}
		return s, cfg.SessionFile, nil
	}

	if errors.Is(loadErr, ErrSessionVersionMismatch) {
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
	return NewSession(cfg), cfg.SessionFile, nil
}

// NewSession creates a fresh session. Does NOT start goroutines —
// call Start() to begin processing input.
func NewSession(cfg SessionConfig) *Session {
	ctx, cancel := context.WithCancel(context.Background())

	modelService := NewModelService(NewModelManager(cfg.ModelConfigPath), NewRuntimeManager(cfg.RuntimeConfigPath))
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
			taskEventCh:  make(chan TaskEvent, 64),
			taskResultCh: make(chan []llm.ContentPart, 1),
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

	if model := s.modelService.ActiveModel(); model != nil {
		s.ContextLimit = s.modelService.contextLimit
	}

	// Set up MCP service (manages init lifecycle).
	s.mcpService = NewMCPService(cfg.MCPInit, s.Output)

	s.sendSystemInfo(SystemInfoAll)
	return s
}

// RestoreFromSession creates a session from saved data.
// Does NOT start goroutines — call Start() to begin processing input.
func RestoreFromSession(cfg SessionConfig, data *SessionData) *Session {
	ctx, cancel := context.WithCancel(context.Background())

	modelService := NewModelService(NewModelManager(cfg.ModelConfigPath), NewRuntimeManager(cfg.RuntimeConfigPath))
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
			taskEventCh:  make(chan TaskEvent, 64),
			taskResultCh: make(chan []llm.ContentPart, 1),
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
	s.modelService.SetReasoningLevel(data.ReasoningLevel)

	// Set up MCP service (manages init lifecycle).
	s.mcpService = NewMCPService(cfg.MCPInit, s.Output)

	// Apply context limit from the resolved model so the status bar
	// can show "tokens/limit (pct%)" immediately, before any API call.
	if model := s.modelService.ActiveModel(); model != nil {
		s.ContextLimit = s.modelService.contextLimit
	}

	s.sendSystemInfo(SystemInfoAll)
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
