package agent

// Session I/O: input pump, command handling, prompt processing.
//
// All command dispatching happens in the run() goroutine via
// handleInputMsg.  The input pump is a pure TLV parser — it has no
// knowledge of command names and never touches session state.
// This keeps the design simple: one goroutine owns everything,
// no split-path exceptions for :cancel / :tool_confirm / etc.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/alayacore/alayacore/internal/config"
	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/protocol"
	"github.com/alayacore/alayacore/internal/tlv"
)

// ============================================================================
// Input pump — inputMsg, inputPump, handleInputFrame
//
// The input pump runs in its own goroutine, reading TLV frames from the
// input stream and dispatching parsed messages (inputMsg) to the run()
// goroutine via inputMsgCh. It has no knowledge of command names and
// never touches session state — all command handling lives in run().
// ============================================================================

// inputMsg carries a parsed input message from the I/O pump to run().
//
// For prompt messages:
//   - contentParts holds the combined message (media parts + optional text part)
//   - isCmd is false, cmd is empty
//
// For command messages (from CI frames):
//   - cmd holds the command name (no ':' prefix)
//   - cmdInput holds the command's argument string
//   - cmdID holds the adapter-generated call ID, echoed in the CO result
//   - contentParts is nil, isCmd is true
type inputMsg struct {
	contentParts []llm.ContentPart // combined user content (media + text)
	cmd          string            // command name for commands, empty for prompts
	cmdInput     string            // command argument string (from CI frame)
	cmdID        string            // command call ID (from CI frame), echoed in CO
	isCmd        bool              // true when cmd is set
	err          error             // non-nil when the input pump hit a validation error
}

// inputPump runs in its own goroutine.  It reads TLV frames from the
// input stream, builds inputMsg values, and sends them to inputMsgCh.
// It does NOT interpret commands or access session state — all of
// that lives in the run() goroutine.
func (s *Session) inputPump() {
	var staged []llm.ContentPart

	for {
		tag, value, err := tlv.ReadTLV(s.Input)
		if err != nil {
			if len(staged) > 0 {
				s.inputMsgCh <- inputMsg{contentParts: staged}
			}
			close(s.inputMsgCh)
			return
		}
		staged = s.handleInputFrame(tag, value, staged)
	}
}

// handleInputFrame processes a single TLV frame from the input stream.
// Returns the updated staged content (nil when staged content has been
// consumed by UE or discarded by an error). Media tags (UI/UV/UA/UD)
// and regular text (UT) are staged until UE or EOF. Command frames (CI)
// are sent immediately without staging.
func (s *Session) handleInputFrame(tag, value string, staged []llm.ContentPart) []llm.ContentPart {
	switch tag {
	case tlv.TagUserI:
		return append(staged, &llm.ImagePart{URI: value})
	case tlv.TagUserV:
		return append(staged, &llm.VideoPart{URI: value})
	case tlv.TagUserA:
		return append(staged, &llm.AudioPart{URI: value})
	case tlv.TagUserD:
		return append(staged, &llm.DocumentPart{URI: value})
	case tlv.TagUserT:
		if value != "" {
			return append(staged, &llm.TextPart{Text: value})
		}
		return staged
	case tlv.TagCommandIn:
		if len(staged) > 0 {
			s.inputMsgCh <- inputMsg{err: fmt.Errorf("command can not be sent with staged content")}
			return nil
		}
		var cmd protocol.CmdMsg
		if err := json.Unmarshal([]byte(value), &cmd); err != nil {
			s.inputMsgCh <- inputMsg{err: fmt.Errorf("invalid command frame: %v", err)}
			return staged
		}
		s.inputMsgCh <- inputMsg{isCmd: true, cmd: cmd.Name, cmdInput: cmd.Input, cmdID: cmd.ID}
		return staged
	case tlv.TagUserEnd:
		if len(staged) > 0 {
			s.inputMsgCh <- inputMsg{contentParts: staged}
		}
		return nil
	default:
		s.inputMsgCh <- inputMsg{err: fmt.Errorf("invalid input tag: %s", tag)}
		return nil
	}
}

// ============================================================================
// Registered command handlers — handleFork, handleToolConfirm/Decline
//
// These are registered via LookupCommand and dispatched by handleInputMsg
// according to their CmdImmediate / CmdIdle policy.
// ============================================================================

// handleFork saves all content from the start of the session up to (and
// including) the content identified by history ID to a session file.
// Usage: :fork <history_id> <filename>
func (s *Session) handleFork(args string) (any, error) {
	fields := strings.Fields(args)
	if len(fields) < 2 {
		return nil, &CmdErr{Code: "INVALID_ARGS", Message: "usage: :fork <history_id> <filename>"}
	}

	id, err := strconv.ParseUint(fields[0], 10, 64)
	if err != nil {
		return nil, &CmdErr{Code: "INVALID_ARGS", Message: fmt.Sprintf("invalid history ID: %s", fields[0])}
	}

	// Find the index of the content with this history ID.
	var endIdx = -1
	for i, part := range s.Contents {
		if part.GetHistoryID() == id {
			endIdx = i
			break
		}
	}
	if endIdx < 0 {
		return nil, &CmdErr{Code: "NOT_FOUND", Message: fmt.Sprintf("no content found with history ID %d", id)}
	}

	path := config.ExpandPath(fields[1])
	if err := s.saveContentToFile(path, s.Contents[:endIdx+1]); err != nil {
		return nil, &CmdErr{Code: "IO_ERROR", Message: fmt.Sprintf("failed to fork: %v", err)}
	}
	return map[string]any{"path": path, "count": endIdx + 1}, nil
}

// handleToolConfirmCmd processes a `:tool_confirm <id>` command.
// It looks up the per-tool confirmation channel and allows the tool.
func (s *Session) handleToolConfirmCmd(args string) (any, error) {
	fields := strings.Fields(args)
	if len(fields) != 1 {
		return nil, &CmdErr{Code: "INVALID_ARGS", Message: "usage: :tool_confirm <id>"}
	}
	return s.resolveToolConfirm(fields[0], true)
}

// handleToolDeclineCmd processes a `:tool_decline <id>` command.
// It looks up the per-tool confirmation channel and denies the tool.
func (s *Session) handleToolDeclineCmd(args string) (any, error) {
	fields := strings.Fields(args)
	if len(fields) != 1 {
		return nil, &CmdErr{Code: "INVALID_ARGS", Message: "usage: :tool_decline <id>"}
	}
	return s.resolveToolConfirm(fields[0], false)
}

// resolveToolConfirm looks up the confirmation channel and sends the decision.
func (s *Session) resolveToolConfirm(id string, allowed bool) (any, error) {
	s.confirmMu.Lock()
	ch, ok := s.confirmChs[id]
	delete(s.confirmChs, id)
	s.confirmMu.Unlock()

	if !ok {
		return nil, &CmdErr{Code: "NOT_FOUND", Message: "No pending tool confirmation for " + id}
	}

	ch <- allowed
	return map[string]any{"tool_id": id}, nil
}

// ============================================================================
// Command dispatch — handleInputMsg
//
// handleInputMsg is the central dispatcher for all incoming messages. It
// runs in the run() goroutine and routes each inputMsg to the appropriate
// handler based on whether it's a command or a regular prompt.
// ============================================================================

// prepareTask checks preconditions and creates a cancellable context for
// a new task. Returns an error (wrapped as CmdErr where meaningful) if
// the task cannot start; callers decide how to report it (CO for task
// commands, SM error for normal prompts).
func (s *Session) prepareTask() (context.Context, error) {
	// Before MCP is ready, the tool list is incomplete. Sending an LLM
	// request would produce a response without MCP tools, and the
	// subsequent agent reset (when MCP init completes) would invalidate provider's cache
	if s.mcpService != nil && !s.mcpService.IsReady() {
		return nil, &CmdErr{Code: "MCP_NOT_READY",
			Message: "MCP servers are still initializing or OAuth authorization is pending. " +
				"Please wait for initialization to complete."}
	}
	if s.activeTask != nil {
		return nil, &CmdErr{Code: "BUSY",
			Message: "A task is already running. Wait for it to complete or cancel it."}
	}
	if err := s.ensureAgentInitialized(); err != nil {
		return nil, err
	}
	taskCtx, taskCancel := context.WithCancel(s.sessionCtx)
	s.activeTask = &taskHandle{cancel: taskCancel, step: 0}
	return taskCtx, nil
}

// startTaskCommand starts an async task command (:continue/:summarize).
// It replies CO immediately — {"status":"started"} on acceptance, or an
// error if the task cannot start. Task completion is reported via TaskMsg
// carrying the command ID (see sendTaskMsg).
func (s *Session) startTaskCommand(id string, run func(context.Context)) {
	ctx, err := s.prepareTask()
	if err != nil {
		s.writeCmdResult(id, nil, err)
		return
	}
	if id != "" {
		s.activeTask.commandID = id
	}
	s.writeCmdResult(id, map[string]any{"status": "started"}, nil)
	go run(ctx)
}

// handleInputMsg processes a parsed input message. Called from run() goroutine.
func (s *Session) handleInputMsg(msg inputMsg) {
	if msg.err != nil {
		// Input-pump validation errors (invalid CI JSON, staged-content
		// conflict, unknown tags). No request ID is available — the empty
		// id in the CO marks an uncorrelated error.
		s.writeCmdResult("", nil, &CmdErr{Code: "BAD_FRAME", Message: msg.err.Error()})
		return
	}

	if !msg.isCmd {
		if ctx, err := s.prepareTask(); err != nil {
			// Non-command failure — reported as an SM error.
			s.writeError(err.Error())
		} else {
			go s.runTaskNormal(ctx, msg.contentParts)
		}
		return
	}

	// Command dispatch. CI frames carry the command name and input
	// separately; each handler parses the input string as appropriate
	// for its command.
	name := msg.cmd
	args := msg.cmdInput
	if name == "" {
		s.writeCmdResult(msg.cmdID, nil, &CmdErr{Code: "INVALID_ARGS", Message: "empty command"})
		return
	}

	// Task commands — :continue and :summarize start a task goroutine.
	// CO replies immediately ({"status":"started"} or an error); task
	// completion is correlated via TaskMsg.command_id.
	switch name {
	case CommandNameContinue:
		s.startTaskCommand(msg.cmdID, s.runTaskContinue)
		return
	case CommandNameSummarize:
		s.startTaskCommand(msg.cmdID, s.runTaskSummarize)
		return
	}

	// Registry commands — synchronous dispatch in the run() goroutine.
	cmdDef, ok := LookupCommand(name)
	if !ok {
		s.writeCmdResult(msg.cmdID, nil, &CmdErr{Code: "UNKNOWN_COMMAND", Message: fmt.Sprintf("unknown command: %s", name)})
		return
	}

	if cmdDef.Policy == CmdIdle && s.activeTask != nil {
		s.writeCmdResult(msg.cmdID, nil, &CmdErr{Code: "BUSY",
			Message: "Cannot run this command while a task is in progress. Please wait or cancel the current task."})
		return
	}
	s.execCommand(s.sessionCtx, msg.cmdID, cmdDef, args)
}

// execCommand runs a registered command handler and writes its CO result.
// The handler is synchronous (runs in the run() goroutine), so the call ID
// is passed directly and never needs to be stored on the Session.
func (s *Session) execCommand(ctx context.Context, id string, cmdDef *Command, args string) {
	result, err := cmdDef.Handler(s, ctx, args)
	s.writeCmdResult(id, result, err)
}

// ============================================================================
// Model commands — handleModelSet, handleModelLoad, handleModelSync
//
// These commands manage model configuration: switching the active model,
// reloading from file, and syncing from an adapter editor session.
// ============================================================================

// checkModelManager is a shared preamble for model handlers. Returns the
// model manager, or a CmdErr if it is not initialized.
func (s *Session) checkModelManager() (*ModelManager, error) {
	mm := s.modelService.manager
	if mm == nil {
		return nil, &CmdErr{Code: "NOT_INITIALIZED", Message: "model manager not initialized"}
	}
	return mm, nil
}

func (s *Session) handleModelSet(args string) (any, error) {
	mm, err := s.checkModelManager()
	if err != nil {
		return nil, err
	}

	if args == "" {
		return nil, &CmdErr{Code: "INVALID_ARGS", Message: "usage: :model_set <id>"}
	}

	modelID, err := strconv.Atoi(args)
	if err != nil {
		return nil, &CmdErr{Code: "INVALID_ARGS", Message: fmt.Sprintf("model_set: invalid model ID: %s", args)}
	}
	model := mm.GetModel(modelID)
	if model == nil {
		return nil, &CmdErr{Code: "MODEL_NOT_FOUND", Message: fmt.Sprintf("model_set: model not found: %d", modelID)}
	}

	if err := mm.SetActive(modelID); err != nil {
		return nil, &CmdErr{Code: "MODEL_ERROR", Message: err.Error()}
	}

	// Persist the switch. Sessions with a file-specified model store the
	// preference in-memory (saved to the session file on :save), while
	// sessions without one write to the global runtime.conf.
	var persistErr error
	if s.SessionFile != "" {
		s.modelService.sessionMetaModel = model.Name
	} else if rm := s.modelService.runtimeMgr; rm != nil {
		persistErr = rm.SetActiveModel(model.Name)
	}

	if err := s.SwitchModel(model); err != nil {
		return nil, &CmdErr{Code: "MODEL_ERROR", Message: "Failed to switch model: " + err.Error()}
	}
	if persistErr != nil {
		return nil, &CmdErr{Code: "PERSIST_FAILED", Message: fmt.Sprintf("Failed to persist model switch: %v", persistErr)}
	}
	return map[string]any{"active_id": modelID, "active_name": model.Name}, nil
}

func (s *Session) handleModelLoad() (any, error) {
	mm, err := s.checkModelManager()
	if err != nil {
		return nil, err
	}

	path := mm.GetFilePath()
	if path == "" {
		return nil, &CmdErr{Code: "NOT_CONFIGURED", Message: "no model file path configured"}
	}

	if err := mm.LoadFromFile(path); err != nil {
		return nil, &CmdErr{Code: "LOAD_FAILED", Message: fmt.Sprintf("model_load: failed to load models: %v", err)}
	}

	// Apply the successfully loaded models regardless of validation
	// outcome — then fail the command if any model block was rejected.
	// The user must see these errors to fix their config.
	s.modelService.ResolveActiveModel()
	if model := mm.GetActive(); model != nil {
		if err := s.SwitchModel(model); err != nil {
			return nil, &CmdErr{Code: "MODEL_ERROR", Message: "Failed to reinitialize model after reload: " + err.Error()}
		}
	}
	s.sendModelListMsg()

	if msgs := mm.GetLoadErrors(); len(msgs) > 0 {
		return nil, &CmdErr{Code: "MODEL_VALIDATION", Message: strings.Join(msgs, "; ")}
	}
	return map[string]any{"models": mm.GetModels()}, nil
}

// handleModelSync replaces all models with JSON content from an adapter
// editor session. The JSON is received as a single string (cut on first
// space), so string values with spaces (e.g. model names) are preserved.
func (s *Session) handleModelSync(args string) (any, error) {
	mm, err := s.checkModelManager()
	if err != nil {
		return nil, err
	}

	if args == "" {
		return nil, &CmdErr{Code: "INVALID_ARGS", Message: "usage: :model_sync <json>"}
	}

	msgs := mm.SyncFromContent(args)

	// Apply the successfully synced models, then fail the command if any
	// model was rejected or persistence failed.
	s.modelService.ResolveActiveModel()
	if model := mm.GetActive(); model != nil {
		if err := s.SwitchModel(model); err != nil {
			return nil, &CmdErr{Code: "MODEL_ERROR", Message: "Failed to reinitialize model after sync: " + err.Error()}
		}
	}
	s.sendModelListMsg()

	if len(msgs) > 0 {
		return nil, &CmdErr{Code: "MODEL_VALIDATION", Message: strings.Join(msgs, "; ")}
	}
	return map[string]any{"models": mm.GetModels()}, nil
}

// ============================================================================
// Configuration commands — handleReason, handleVideoConfig, handleThemeSet
//
// These commands let the user adjust session-level settings such as
// reasoning effort, video encoding parameters, and the active theme.
// ============================================================================

func (s *Session) handleReason(args string) (any, error) {
	level, err := strconv.Atoi(args)
	if err != nil || level < config.ReasoningLevelOff || level > config.ReasoningLevelMax {
		return nil, &CmdErr{Code: "INVALID_ARGS", Message: "usage: :reason [0|1|2]  (0=off, 1=normal, 2=max)"}
	}
	s.SetReasoningLevel(level)
	return map[string]any{"level": level}, nil
}

// handleVideoConfig sets the default video FPS and resolution for video attachments.
// Usage: :video_config <fps> <resolution>
//
//	fps:        frames per second (positive integer, e.g. 2)
//	resolution: 0=default, 1=max
func (s *Session) handleVideoConfig(args string) (any, error) {
	const usage = "usage: :video_config <fps> <resolution>  (fps: positive integer, resolution: 0=default, 1=max)"
	fields := strings.Fields(args)
	if len(fields) < 2 {
		return nil, &CmdErr{Code: "INVALID_ARGS", Message: usage}
	}
	fps, err := strconv.Atoi(fields[0])
	if err != nil || fps < 1 {
		return nil, &CmdErr{Code: "INVALID_ARGS", Message: usage}
	}
	res, err := strconv.Atoi(fields[1])
	if err != nil || res < 0 || res > 1 {
		return nil, &CmdErr{Code: "INVALID_ARGS", Message: usage}
	}
	s.SetVideoConfig(fps, res)
	return map[string]any{"fps": fps, "res": res}, nil
}

// handleThemeSet sets the active theme, persists it to runtime config,
// and sends updated system info so adapters receive the full theme data.
func (s *Session) handleThemeSet(args string) (any, error) {
	if s.NoTheme {
		return nil, &CmdErr{Code: "UNAVAILABLE", Message: "theme management is not available in this mode"}
	}
	if args == "" {
		return nil, &CmdErr{Code: "INVALID_ARGS", Message: "usage: :theme_set <name>"}
	}
	name := args

	// Validate that the theme exists before persisting.
	if s.ThemesFolder != "" {
		themePath := filepath.Join(s.ThemesFolder, name+".conf")
		if _, err := os.Stat(themePath); os.IsNotExist(err) {
			return nil, &CmdErr{Code: "NOT_FOUND", Message: fmt.Sprintf("Theme %q not found", name)}
		}
	}

	var persistErr error
	if s.modelService.runtimeMgr != nil {
		persistErr = s.modelService.runtimeMgr.SetActiveTheme(name)
	}
	s.sendSystemInfo(SystemInfoTheme)
	if persistErr != nil {
		return nil, &CmdErr{Code: "PERSIST_FAILED", Message: fmt.Sprintf("Failed to persist theme switch: %v", persistErr)}
	}
	return map[string]any{"name": name}, nil
}

// ============================================================================
// MCP commands — handleMCPCancel, handleMCPConfirm, handleMCPDecline
//
// These commands manage MCP server lifecycle: cancel initialization,
// confirm an OAuth authorization code, or decline an authorization request.
// ============================================================================

// handleMCPCancel handles the :mcp_cancel command.
// Called when the user presses Ctrl+G (init overlay or globally) or types
// the command directly. Cancels the entire MCP initialization.
func (s *Session) handleMCPCancel() (any, error) {
	if err := s.checkMCPReady(); err != nil {
		return nil, err
	}
	s.mcpService.Cancel()
	return nil, nil
}

// checkMCPReady is a shared preamble for MCP handlers that need to
// verify MCP is configured and initializing.
func (s *Session) checkMCPReady() error {
	if !s.mcpService.HasInit() {
		return &CmdErr{Code: "NOT_CONFIGURED", Message: "No MCP servers configured."}
	}
	if s.mcpService.IsReady() {
		return &CmdErr{Code: "NOT_IN_PROGRESS", Message: "MCP initialization is not in progress."}
	}
	return nil
}

// handleMCPConfirm handles the :mcp_confirm command.
//
// Usage: :mcp_confirm <server> <code> <redirect_uri>
func (s *Session) handleMCPConfirm(_ context.Context, args string) (any, error) {
	fields := strings.Fields(args)
	if len(fields) < 3 {
		return nil, &CmdErr{Code: "INVALID_ARGS", Message: "usage: :mcp_confirm <server> <code> <redirect_uri>"}
	}
	if err := s.checkMCPReady(); err != nil {
		return nil, err
	}

	server := fields[0]
	code := fields[1]
	redirectURI := fields[2]
	if !s.mcpService.SendAuthCodeResult(server, code, redirectURI) {
		return nil, &CmdErr{Code: "NOT_FOUND", Message: fmt.Sprintf("No pending authorization for MCP server %q.", server)}
	}
	return map[string]any{"server": server}, nil
}

// handleMCPDecline handles the :mcp_decline command.
//
// Usage: :mcp_decline <server>
func (s *Session) handleMCPDecline(args string) (any, error) {
	fields := strings.Fields(args)
	if len(fields) < 1 {
		return nil, &CmdErr{Code: "INVALID_ARGS", Message: "usage: :mcp_decline <server>"}
	}
	if err := s.checkMCPReady(); err != nil {
		return nil, err
	}

	server := fields[0]
	if !s.mcpService.SendAuthCodeResult(server, "", "") {
		return nil, &CmdErr{Code: "NOT_FOUND", Message: fmt.Sprintf("No pending authorization for MCP server %q.", server)}
	}
	return map[string]any{"server": server}, nil
}

// ============================================================================
// Session management — saveSession, cancelTask
//
// saveSession persists the current conversation to a file.
// cancelTask aborts the currently running task (if any).
// ============================================================================

func (s *Session) saveSession(args string) (any, error) {
	var path string
	if args == "" {
		if s.SessionFile == "" {
			return nil, &CmdErr{Code: "NOT_CONFIGURED", Message: "no session file set"}
		}
		path = s.SessionFile
	} else {
		path = config.ExpandPath(args)
	}

	if err := s.saveContentToFile(path, s.Contents); err != nil {
		return nil, &CmdErr{Code: "IO_ERROR", Message: fmt.Sprintf("save: failed to save session: %v", err)}
	}
	return map[string]any{"path": path}, nil
}

func (s *Session) cancelTask() (any, error) {
	if s.activeTask != nil {
		if s.cancelRunningTask() {
			return nil, nil
		}
	}
	return nil, &CmdErr{Code: "NOTHING_TO_CANCEL", Message: "nothing to cancel"}
}
