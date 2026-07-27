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
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/alayacore/alayacore/internal/config"
	"github.com/alayacore/alayacore/internal/llm"
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
// For command messages:
//   - cmd holds the command text (without ':' prefix)
//   - contentParts is nil, isCmd is true
type inputMsg struct {
	contentParts []llm.ContentPart // combined user content (media + text)
	cmd          string            // command text for commands, empty for prompts
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
// and regular text (UT without ':') are staged until UE or EOF.
// Command text (UT starting with ':') is sent immediately without staging.
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
		if len(value) > 0 && value[0] == ':' {
			if len(staged) > 0 {
				s.inputMsgCh <- inputMsg{err: fmt.Errorf("command can not be sent with staged content")}
				return nil
			}
			s.inputMsgCh <- inputMsg{isCmd: true, cmd: value[1:]}
			return staged
		}
		if value != "" {
			return append(staged, &llm.TextPart{Text: value})
		}
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
func (s *Session) handleFork(args string) {
	fields := strings.Fields(args)
	if len(fields) < 2 {
		s.writeError("usage: :fork <history_id> <filename>")
		return
	}

	id, err := strconv.ParseUint(fields[0], 10, 64)
	if err != nil {
		s.writeError(fmt.Sprintf("invalid history ID: %s", fields[0]))
		return
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
		s.writeError(fmt.Sprintf("no content found with history ID %d", id))
		return
	}

	path := config.ExpandPath(fields[1])
	if err := s.saveContentToFile(path, s.Contents[:endIdx+1]); err != nil {
		s.writeError(fmt.Sprintf("failed to fork: %v", err))
		return
	}
	s.writeNotifyf("Session forked to %s (up to content ID %d)", path, id)
}

// handleToolConfirmCmd processes a `:tool_confirm <id>` command.
// It looks up the per-tool confirmation channel and allows the tool.
func (s *Session) handleToolConfirmCmd(args string) {
	fields := strings.Fields(args)
	if len(fields) != 1 {
		s.writeError("usage: :tool_confirm <id>")
		return
	}
	s.resolveToolConfirm(fields[0], true)
}

// handleToolDeclineCmd processes a `:tool_decline <id>` command.
// It looks up the per-tool confirmation channel and denies the tool.
func (s *Session) handleToolDeclineCmd(args string) {
	fields := strings.Fields(args)
	if len(fields) != 1 {
		s.writeError("usage: :tool_decline <id>")
		return
	}
	s.resolveToolConfirm(fields[0], false)
}

// resolveToolConfirm looks up the confirmation channel and sends the decision.
func (s *Session) resolveToolConfirm(id string, allowed bool) {
	s.confirmMu.Lock()
	ch, ok := s.confirmChs[id]
	delete(s.confirmChs, id)
	s.confirmMu.Unlock()

	if !ok {
		s.writeError("No pending tool confirmation for " + id)
		return
	}

	ch <- allowed
}

// ============================================================================
// Command dispatch — handleInputMsg
//
// handleInputMsg is the central dispatcher for all incoming messages. It
// runs in the run() goroutine and routes each inputMsg to the appropriate
// handler based on whether it's a command or a regular prompt.
// ============================================================================

// prepareTask checks preconditions and creates a cancellable context for
// a new task. Returns ok=false if setup failed (caller should return immediately).
func (s *Session) prepareTask() (ctx context.Context, ok bool) {
	// Before MCP is ready, the tool list is incomplete. Sending an LLM
	// request would produce a response without MCP tools, and the
	// subsequent agent reset (when MCP init completes) would invalidate provider's cache
	if s.mcpService != nil && !s.mcpService.IsReady() {
		s.writeError("MCP servers are still initializing or OAuth authorization is pending. " +
			"Please wait for initialization to complete.")
		return nil, false
	}
	if s.activeTask != nil {
		s.writeError("A task is already running. Wait for it to complete or cancel it.")
		return nil, false
	}
	if err := s.ensureAgentInitialized(); err != nil {
		s.writeError(err.Error())
		return nil, false
	}
	taskCtx, taskCancel := context.WithCancel(s.sessionCtx)
	s.activeTask = &taskHandle{cancel: taskCancel, step: 0}
	return taskCtx, true
}

// handleInputMsg processes a parsed input message. Called from run() goroutine.
func (s *Session) handleInputMsg(msg inputMsg) {
	if msg.err != nil {
		s.writeError(msg.err.Error())
		return
	}

	if !msg.isCmd {
		if ctx, ok := s.prepareTask(); ok {
			go s.runTaskNormal(ctx, msg.contentParts)
		}
		return
	}

	// Command dispatch: split on first space only.
	// Each handler parses the args string as appropriate for its command.
	name, args, _ := strings.Cut(msg.cmd, " ")
	if name == "" {
		s.writeError("empty command")
		return
	}

	// Task commands — :continue and :summarize start a task goroutine,
	// same as normal prompts. Check them first so they're grouped with
	// the normal prompt path above (both use prepareTask).
	switch name {
	case CommandNameContinue:
		if ctx, ok := s.prepareTask(); ok {
			go s.runTaskContinue(ctx)
		}
		return
	case CommandNameSummarize:
		if ctx, ok := s.prepareTask(); ok {
			go s.runTaskSummarize(ctx)
		}
		return
	}

	// Registry commands — synchronous dispatch in the run() goroutine.
	cmdDef, ok := LookupCommand(name)
	if !ok {
		s.writeError(fmt.Sprintf("unknown command: %s", name))
		return
	}

	if cmdDef.Policy == CmdIdle && s.activeTask != nil {
		s.writeError("Cannot run this command while a task is in progress. Please wait or cancel the current task.")
		return
	}
	cmdDef.Handler(s, s.sessionCtx, args)
}

// ============================================================================
// Model commands — handleModelSet, handleModelLoad, handleModelSync
//
// These commands manage model configuration: switching the active model,
// reloading from file, and syncing from an adapter editor session.
// ============================================================================

// checkModelManager is a shared preamble for model handlers. Returns the
// model manager if initialized, or writes an error and returns nil.
func (s *Session) checkModelManager() *ModelManager {
	mm := s.modelService.manager
	if mm == nil {
		s.writeError("model manager not initialized")
		return nil
	}
	return mm
}

func (s *Session) handleModelSet(args string) {
	mm := s.checkModelManager()
	if mm == nil {
		return
	}

	if args == "" {
		s.writeError("usage: :model_set <id>")
		return
	}

	modelID, err := strconv.Atoi(args)
	if err != nil {
		s.writeError(fmt.Sprintf("model_set: invalid model ID: %s", args))
		return
	}
	model := mm.GetModel(modelID)
	if model == nil {
		s.writeError(fmt.Sprintf("model_set: model not found: %d", modelID))
		return
	}

	if err := mm.SetActive(modelID); err != nil {
		s.writeError(err.Error())
		return
	}

	// Persist the switch. Sessions with a file-specified model store the
	// preference in-memory (saved to the session file on :save), while
	// sessions without one write to the global runtime.conf.
	if s.SessionFile != "" {
		s.modelService.sessionMetaModel = model.Name
	} else if rm := s.modelService.runtimeMgr; rm != nil {
		if err := rm.SetActiveModel(model.Name); err != nil {
			s.writeNotifyf("Failed to persist model switch: %v", err)
		}
	}

	if err := s.SwitchModel(model); err != nil {
		s.writeError("Failed to switch model: " + err.Error())
		return
	}
}

func (s *Session) handleModelLoad() {
	mm := s.checkModelManager()
	if mm == nil {
		return
	}

	path := mm.GetFilePath()
	if path == "" {
		s.writeError("no model file path configured")
		return
	}

	if err := mm.LoadFromFile(path); err != nil {
		s.writeError(fmt.Sprintf("model_load: failed to load models: %v", err))
		return
	}

	// Report validation messages (unknown protocol_type, missing fields, etc.)
	if msgs := mm.GetLoadErrors(); len(msgs) > 0 {
		for _, m := range msgs {
			s.writeError(m)
		}
	}

	// Re-resolve the active model using the standard priority chain.
	s.modelService.ResolveActiveModel()

	// Re-initialize the provider/agent with the (potentially edited) model config.
	if model := mm.GetActive(); model != nil {
		if err := s.SwitchModel(model); err != nil {
			s.writeError("Failed to reinitialize model after reload: " + err.Error())
		}
	}

	s.sendModelListMsg()
}

// handleModelSync replaces all models with JSON content from an adapter
// editor session. The JSON is received as a single string (cut on first
// space), so string values with spaces (e.g. model names) are preserved.
func (s *Session) handleModelSync(args string) {
	mm := s.checkModelManager()
	if mm == nil {
		return
	}

	if args == "" {
		s.writeError("usage: :model_sync <json>")
		return
	}

	msgs := mm.SyncFromContent(args)

	// Report validation messages
	for _, m := range msgs {
		s.writeError(m)
	}

	// Re-resolve the active model
	s.modelService.ResolveActiveModel()

	// Re-initialize the provider/agent with the (potentially edited) model config
	if model := mm.GetActive(); model != nil {
		if err := s.SwitchModel(model); err != nil {
			s.writeError("Failed to reinitialize model after sync: " + err.Error())
		}
	}

	s.sendModelListMsg()
}

// ============================================================================
// Configuration commands — handleReason, handleVideoConfig, handleThemeSet
//
// These commands let the user adjust session-level settings such as
// reasoning effort, video encoding parameters, and the active theme.
// ============================================================================

func (s *Session) handleReason(args string) {
	level, err := strconv.Atoi(args)
	if err != nil || level < config.ReasoningLevelOff || level > config.ReasoningLevelMax {
		s.writeError("usage: :reason [0|1|2]  (0=off, 1=normal, 2=max)")
		return
	}
	s.SetReasoningLevel(level)
}

// handleVideoConfig sets the default video FPS and resolution for video attachments.
// Usage: :video_config <fps> <resolution>
//
//	fps:        frames per second (positive integer, e.g. 2)
//	resolution: 0=default, 1=max
func (s *Session) handleVideoConfig(args string) {
	const usage = "usage: :video_config <fps> <resolution>  (fps: positive integer, resolution: 0=default, 1=max)"
	fields := strings.Fields(args)
	if len(fields) < 2 {
		s.writeError(usage)
		return
	}
	fps, err := strconv.Atoi(fields[0])
	if err != nil || fps < 1 {
		s.writeError(usage)
		return
	}
	res, err := strconv.Atoi(fields[1])
	if err != nil || res < 0 || res > 1 {
		s.writeError(usage)
		return
	}
	s.SetVideoConfig(fps, res)
}

// handleThemeSet sets the active theme, persists it to runtime config,
// and sends updated system info so adapters receive the full theme data.
func (s *Session) handleThemeSet(args string) {
	if s.NoTheme {
		s.writeError("theme management is not available in this mode")
		return
	}
	if args == "" {
		s.writeError("usage: :theme_set <name>")
		return
	}
	name := args

	// Validate that the theme exists before persisting.
	if s.ThemesFolder != "" {
		themePath := filepath.Join(s.ThemesFolder, name+".conf")
		if _, err := os.Stat(themePath); os.IsNotExist(err) {
			s.writeError(fmt.Sprintf("Theme %q not found", name))
			return
		}
	}

	if s.modelService.runtimeMgr != nil {
		if err := s.modelService.runtimeMgr.SetActiveTheme(name); err != nil {
			s.writeNotifyf("Failed to persist theme switch: %v", err)
		}
	}
	s.sendSystemInfo(SystemInfoTheme)
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
func (s *Session) handleMCPCancel() {
	if !s.checkMCPReady() {
		return
	}
	s.mcpService.Cancel()
}

// checkMCPReady is a shared preamble for MCP handlers that need to
// verify MCP is configured and initializing. Returns false if either
// check fails (the error has already been written).
func (s *Session) checkMCPReady() bool {
	if !s.mcpService.HasInit() {
		s.writeError("No MCP servers configured.")
		return false
	}
	if s.mcpService.IsReady() {
		s.writeError("MCP initialization is not in progress.")
		return false
	}
	return true
}

// handleMCPConfirm handles the :mcp_confirm command.
//
// Usage: :mcp_confirm <server> <code> <redirect_uri>
func (s *Session) handleMCPConfirm(_ context.Context, args string) {
	fields := strings.Fields(args)
	if len(fields) < 3 {
		s.writeError("usage: :mcp_confirm <server> <code> <redirect_uri>")
		return
	}
	if !s.checkMCPReady() {
		return
	}

	server := fields[0]
	code := fields[1]
	redirectURI := fields[2]
	if s.mcpService.SendAuthCodeResult(server, code, redirectURI) {
		s.writeNotifyf("MCP auth code received for %q.", server)
	} else {
		s.writeError(fmt.Sprintf("No pending authorization for MCP server %q.", server))
	}
}

// handleMCPDecline handles the :mcp_decline command.
//
// Usage: :mcp_decline <server>
func (s *Session) handleMCPDecline(args string) {
	fields := strings.Fields(args)
	if len(fields) < 1 {
		s.writeError("usage: :mcp_decline <server>")
		return
	}
	if !s.checkMCPReady() {
		return
	}

	server := fields[0]
	if s.mcpService.SendAuthCodeResult(server, "", "") {
		s.writeNotifyf("MCP authorization for %q declined.", server)
	} else {
		s.writeError(fmt.Sprintf("No pending authorization for MCP server %q.", server))
	}
}

// ============================================================================
// Session management — saveSession, cancelTask
//
// saveSession persists the current conversation to a file.
// cancelTask aborts the currently running task (if any).
// ============================================================================

func (s *Session) saveSession(args string) {
	var path string
	if args == "" {
		if s.SessionFile == "" {
			s.writeError("no session file set")
			return
		}
		path = s.SessionFile
	} else {
		path = config.ExpandPath(args)
	}

	if err := s.saveContentToFile(path, s.Contents); err != nil {
		s.writeError(fmt.Sprintf("save: failed to save session: %v", err))
	} else {
		s.writeNotifyf("Session saved to %s", path)
	}
}

func (s *Session) cancelTask() {
	if s.activeTask != nil {
		if s.cancelRunningTask() {
			return
		}
	}
	s.writeError("nothing to cancel")
}
