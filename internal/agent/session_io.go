package agent

// Session I/O: input pump, command handling, prompt processing.
//
// All command dispatching happens in the run() goroutine via
// handleInputMsg. Commands arrive as CI (command Input) frames — the
// input pump parses them into inputMsg but has no knowledge of command
// names and never touches session state. Results are written as CO
// (command Output) frames.
// This keeps the design simple: one goroutine owns everything,
// no split-path exceptions for :cancel / :tool_confirm / etc.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/alayacore/alayacore/internal/commands"
	"github.com/alayacore/alayacore/internal/config"
	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/protocol"
	"github.com/alayacore/alayacore/internal/wire"
)

// ============================================================================
// Input pump — inputMsg, inputPump, handleInputFrame
//
// The input pump runs in its own goroutine, reading frames from the
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
//   - args holds the command's argument string
//   - commandID holds the adapter-generated command id, echoed in the CO result
//   - contentParts is nil, isCmd is true
//
// For the input-end frame (CE):
//   - inputEnd is true and carries nothing else
//
// err is a frame-level failure: a malformed frame, a bad content frame, an
// unknown tag, or a CI whose JSON cannot be read. None of those is a command
// request, and the last one cannot even yield an id, so they are reported as an
// SM error — the session plane — not as a CO.
//
// reject is a command request that was read but refused before dispatch
// (currently only "a CI arrived with unsent content still staged"). It has an
// id, so it is answered with a CO like any other command: the request exists
// even though the answer is no.
type inputMsg struct {
	contentParts []llm.ContentPart // combined user content (media + text)
	cmd          string            // command name for commands, empty for prompts
	args         string            // command argument string (from CI frame)
	commandID    string            // command id (from CI frame), echoed in CO
	isCmd        bool              // true when cmd is set
	inputEnd     bool              // true for CE: the adapter has no more input
	err          error             // non-nil for a frame-level failure (reported as an SM error)
	reject       *cmdErr           // non-nil for a readable command request that must be refused
}

// inputPump runs in its own goroutine.  It reads frames from the input
// stream, builds inputMsg values, and sends them to inputMsgCh. It does NOT
// interpret commands or access session state — all of that lives in the run()
// goroutine.
//
// EOF is reported by closing inputMsgCh: a reader that ends (a pipe whose
// writer closed, a keyboard at Ctrl-D) is the adapter saying it has no more
// input, exactly like a CE frame, and run() treats the two identically.
//
// The reader is local to this goroutine, so it carries the bytes it has read
// ahead of the frame it returned without any synchronization.
func (s *Session) inputPump() {
	var staged []llm.ContentPart
	frames := wire.NewReader(s.Input)

	for {
		tag, value, err := frames.ReadFrame()
		if err != nil {
			if errors.Is(err, io.EOF) {
				if len(staged) > 0 {
					s.inputMsgCh <- inputMsg{contentParts: staged}
				}
			} else {
				// A malformed frame: the bytes are not a frame and there is no
				// frame boundary to resynchronise on, so the message being staged
				// is not a message. Drop it and report — handleInputMsg writes an
				// SM error, so the adapter learns why the input stopped instead of
				// the session quietly going deaf. The stream is over at this
				// point, which is a session event, not a command result: there is
				// no CI to answer and no id to echo.
				s.inputMsgCh <- inputMsg{err: fmt.Errorf("invalid frame: %w", err)}
			}
			close(s.inputMsgCh)
			return
		}
		staged = s.handleInputFrame(tag, value, staged)
	}
}

// handleInputFrame processes a single frame from the input stream.
// Returns the updated staged content (nil when staged content has been
// consumed by UE or discarded by an error). Content frames (UX, carrying
// text or media) are staged until UE or EOF. Command frames (CI)
// are sent immediately without staging. CE reports that the adapter has no
// more input — the same fact as EOF, on a stream that stays open — and
// flushes whatever is staged before it.
func (s *Session) handleInputFrame(tag, value string, staged []llm.ContentPart) []llm.ContentPart {
	switch tag {
	case wire.TagUserX:
		part, err := contentPartFromJSON(wire.TagUserX, []byte(value))
		if err != nil {
			s.inputMsgCh <- inputMsg{err: fmt.Errorf("invalid content frame: %v", err)}
			return nil
		}
		if tp, ok := part.(*llm.TextPart); ok && tp.Text == "" {
			return staged // an empty text part contributes nothing
		}
		return append(staged, part)
	case wire.TagCommandIn:
		var cmd protocol.CmdMsg
		if err := json.Unmarshal([]byte(value), &cmd); err != nil {
			// The id is part of what could not be read, so there is nothing to
			// echo and no CO can be correlated: this is a frame-level failure and
			// goes out as an SM error (see inputMsg.err).
			s.inputMsgCh <- inputMsg{err: fmt.Errorf("invalid command frame: %v", err)}
			return staged
		}
		if cmd.CommandID == "" {
			// Readable but uncorrelatable. Running it would produce a side
			// effect the adapter could not match to anything it sent, and a CO
			// would carry the empty id this plane exists not to have — so the
			// frame is refused on the session plane instead.
			s.inputMsgCh <- inputMsg{err: fmt.Errorf("command frame without command_id: %q", cmd.Name)}
			return staged
		}
		if len(staged) > 0 {
			// The request is readable, so it gets its answer: refusing it is
			// still a command result, and the adapter can correlate it.
			s.inputMsgCh <- inputMsg{
				isCmd:     true,
				cmd:       cmd.Name,
				args:      cmd.Args,
				commandID: cmd.CommandID,
				reject:    &cmdErr{Code: "INVALID_ARGS", Message: "command can not be sent with staged content"},
			}
			return nil
		}
		s.inputMsgCh <- inputMsg{isCmd: true, cmd: cmd.Name, args: cmd.Args, commandID: cmd.CommandID}
		return staged
	case wire.TagUserEnd:
		if len(staged) > 0 {
			s.inputMsgCh <- inputMsg{contentParts: staged}
		}
		return nil
	case wire.TagInputEnd:
		// The adapter has no more input. This is the same fact as EOF on the
		// stream, so it takes the same shape: whatever is staged is a complete
		// message first, and only then does the input end. The order cannot be
		// swapped — one goroutine sending on one channel is FIFO, so the prompt
		// is always dequeued before the end.
		if len(staged) > 0 {
			s.inputMsgCh <- inputMsg{contentParts: staged}
		}
		s.inputMsgCh <- inputMsg{inputEnd: true}
		return nil
	default:
		s.inputMsgCh <- inputMsg{err: fmt.Errorf("invalid input tag: %s", tag)}
		return nil
	}
}

// ============================================================================
// Registered command handlers — handleFork, handleToolConfirm/Decline
//
// These are registered via lookupCommand and dispatched by handleInputMsg
// according to their cmdImmediate / cmdIdle policy.
// ============================================================================

// handleFork saves all content from the start of the session up to (and
// including) the content identified by history ID to a session file.
// The cut is then extended to keep every kept tool call's result with it, and a
// history whose pairing cannot be repaired is refused rather than written —
// see forkEndIndex.
// Usage: :fork <history_id> <filename>
func (s *Session) handleFork(args string) (any, error) {
	// Two arguments: the history id, then the filename — and the filename is
	// everything after it, because a path may contain spaces. Splitting on
	// whitespace would turn ":fork 5 my report.alaya" into the file "my".
	idStr, pathArg := commands.SplitFirst(args)
	if idStr == "" || pathArg == "" {
		return nil, &cmdErr{Code: "INVALID_ARGS", Message: commands.UsageLine(commands.CommandNameFork)}
	}

	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil {
		return nil, &cmdErr{Code: "INVALID_ARGS", Message: fmt.Sprintf("invalid history ID: %s", idStr)}
	}

	endIdx := forkEndIndex(s.Contents, id)
	if endIdx < 0 {
		return nil, &cmdErr{Code: "NOT_FOUND", Message: fmt.Sprintf("no content found with history ID %d", id)}
	}

	// A call whose result exists nowhere cannot be completed by widening the
	// cut. When it trails the prefix (the shape a cancel leaves behind) the
	// existing cleaner removes it; when it sits mid-history the only honest
	// answer is to write nothing, because a file with an orphaned tool_use
	// loads fine and fails on the next prompt.
	prefix := cleanIncompleteToolInputs(s.Contents[:endIdx+1])
	if dangling := unpairedToolUse(prefix); dangling != "" {
		return nil, &cmdErr{Code: "INVALID_STATE",
			Message: fmt.Sprintf("cannot fork: tool call %q has no result in this session; fork at a point before it", dangling)}
	}

	path := config.ExpandPath(pathArg)
	if err := s.saveContentToFile(path, prefix); err != nil {
		return nil, &cmdErr{Code: "IO_ERROR", Message: fmt.Sprintf("failed to fork: %v", err)}
	}
	return map[string]any{"path": path, "count": len(prefix), "history_id": id}, nil
}

// unpairedToolUse returns the ID of the first tool call in parts that has no
// matching tool result, or "" when every call is paired.
func unpairedToolUse(parts []llm.ContentPart) string {
	calls, results := map[string]bool{}, map[string]bool{}
	var order []string
	for _, p := range parts {
		switch v := p.(type) {
		case *llm.ToolInputPart:
			if !calls[v.ID] {
				calls[v.ID] = true
				order = append(order, v.ID)
			}
		case *llm.ToolOutputPart:
			results[v.ID] = true
		}
	}
	for _, id := range order {
		if !results[id] {
			return id
		}
	}
	return ""
}

// forkEndIndex returns the index of the last content part that a fork at id
// must write, or -1 when no part carries that id.
//
// The requested entry is located by equality on the history ID and the prefix
// ends there, which can cut a tool call away from its result: a file ending in
// a tool_use with no tool_result loads without complaint and is rejected by the
// API on the *next* prompt — far from the action that caused it. llm's
// cleanIncompleteToolInputs guards that shape on the task path only.
//
// So the cut is extended forward until every tool call inside the prefix also
// has its result inside it. This writes whole groups rather than halves of one:
// a step with two calls forked at the first yields both calls and both results,
// because the results of a message are emitted after all of its calls
// (call1, call2, result1, result2), so no earlier cut can pair the first call
// without reaching past the second. That is the "up to and including" promise
// kept rather than quietly narrowed — the named entry is always in the output.
func forkEndIndex(contents []llm.ContentPart, id uint64) int {
	end := -1
	for i, part := range contents {
		if part.GetHistoryID() == id {
			end = i
			break
		}
	}
	if end < 0 {
		return -1
	}

	for {
		calls, results := map[string]bool{}, map[string]bool{}
		for _, part := range contents[:end+1] {
			switch v := part.(type) {
			case *llm.ToolInputPart:
				calls[v.ID] = true
			case *llm.ToolOutputPart:
				results[v.ID] = true
			}
		}

		extended := false
		for i := end + 1; i < len(contents); i++ {
			if v, ok := contents[i].(*llm.ToolOutputPart); ok && calls[v.ID] && !results[v.ID] {
				end, extended = i, true
			}
		}
		if !extended {
			return end // every kept call has its kept result
		}
	}
}

// handleToolConfirmCmd processes a `:tool_confirm <id>` command.
// It looks up the per-tool confirmation channel and allows the tool.
func (s *Session) handleToolConfirmCmd(args string) (any, error) {
	fields := strings.Fields(args)
	if len(fields) != 1 {
		return nil, &cmdErr{Code: "INVALID_ARGS", Message: commands.UsageLine(commands.CommandNameToolConfirm)}
	}
	return s.resolveToolConfirm(fields[0], true)
}

// handleToolDeclineCmd processes a `:tool_decline <id>` command.
// It looks up the per-tool confirmation channel and denies the tool.
func (s *Session) handleToolDeclineCmd(args string) (any, error) {
	fields := strings.Fields(args)
	if len(fields) != 1 {
		return nil, &cmdErr{Code: "INVALID_ARGS", Message: commands.UsageLine(commands.CommandNameToolDecline)}
	}
	return s.resolveToolConfirm(fields[0], false)
}

// resolveToolConfirm looks up the confirmation channel and sends the decision.
// toolCallID is the *tool* call's id (the value AF carries), not a command id:
// it arrives as this command's argument, and the CO result echoes it as
// "call_id" so the adapter can match the decision to the confirm prompt.
func (s *Session) resolveToolConfirm(toolCallID string, allowed bool) (any, error) {
	s.confirmMu.Lock()
	ch, ok := s.confirmChs[toolCallID]
	delete(s.confirmChs, toolCallID)
	s.confirmMu.Unlock()

	if !ok {
		return nil, &cmdErr{Code: "NOT_FOUND", Message: "No pending tool confirmation for " + toolCallID}
	}

	ch <- allowed
	return map[string]any{"call_id": toolCallID}, nil
}

// cleanupConfirmChannels removes any leftover tool-confirmation channels
// after a task finishes. Entries remain in the map when the user never
// responds and the task is canceled: the agent's confirmation goroutine
// exits via ctx.Done without going through resolveToolConfirm, so nothing
// ever deletes the entry — leaking the buffered channel (and preventing
// GC of anything it references).
//
// Must be called from the run() goroutine after the task has completed.
// By then every confirmation goroutine has already sent its result (the
// agent's result-collection loop blocks until then), so no live goroutine
// can be waiting on a removed channel — entries are simply dropped, never
// signaled.
func (s *Session) cleanupConfirmChannels() {
	s.confirmMu.Lock()
	defer s.confirmMu.Unlock()
	if len(s.confirmChs) == 0 {
		return
	}
	for id := range s.confirmChs {
		delete(s.confirmChs, id)
	}
}

// ============================================================================
// Command dispatch — handleInputMsg
//
// handleInputMsg is the central dispatcher for all incoming messages. It
// runs in the run() goroutine and routes each inputMsg to the appropriate
// handler based on whether it's a command or a regular prompt.
// ============================================================================

// The two refusal reasons callers must tell apart by value, via errors.Is: one
// is a reason to *hold* a prompt (the session is not ready yet, which passes),
// the other a reason to report it (there is nowhere to put it). beginTask has
// two further exits — quitting and agent-initialization failure — that need no
// such distinction.
var (
	// errMCPNotReady: MCP initialization has not settled, so the tool list is
	// incomplete. Transient — the prompt waits for readiness instead of being
	// refused. The code stays MCP_NOT_READY for wire compatibility.
	errMCPNotReady = &cmdErr{Code: "MCP_NOT_READY",
		Message: "MCP servers are still initializing or OAuth authorization is pending. " +
			"Please wait for initialization to complete."}

	// errTaskBusy: a task is already running. Accepting a second one would
	// need a queue, which this session does not have.
	errTaskBusy = &cmdErr{Code: "BUSY",
		Message: "A task is already running. Wait for it to complete or cancel it."}
)

// beginTask checks preconditions, reserves the task slot, and creates a
// cancellable context for a new task. Reserving the slot is also where the
// running state is announced to the adapter — see the comment at the
// sendSystemInfo call. commandID labels the task when it was started by an
// async command (:continue/:summarize); pass "" for a prompt. Returns an error
// (wrapped as cmdErr where meaningful) if the task cannot start; callers decide
// how to report it (CO for task commands, SM error for normal prompts).
func (s *Session) beginTask(commandID string) (context.Context, error) {
	// :quit has been accepted: the session is on its way out and must not
	// take on work it would drop.
	if s.quitting {
		return nil, &cmdErr{Code: "SHUTTING_DOWN", Message: "the session is shutting down"}
	}
	// Before the session is ready (MCP init pending), the tool list is
	// incomplete. Sending an LLM request would produce a response without
	// MCP tools, and the subsequent agent reset (when MCP init completes)
	// would invalidate the provider's cache. beginTask only runs after
	// run() has started, so State() is Initializing or Ready here.
	if s.State() != SessionReady {
		return nil, errMCPNotReady
	}
	if s.activeTask != nil {
		return nil, errTaskBusy
	}
	if err := s.ensureAgentInitialized(); err != nil {
		return nil, err
	}
	taskCtx, taskCancel := context.WithCancel(s.sessionCtx)
	s.activeTask = &taskHandle{cancel: taskCancel, step: 0, commandID: commandID}
	// This task owns the steering queue afresh: a cancel that drained the
	// previous task's queue (cancelSteering) must not suppress this task's
	// deferred discard. See steeringDrained.
	s.steeringMu.Lock()
	s.steeringDrained = false
	s.steeringMu.Unlock()
	// Clear the previous task's final speed: a new task has produced no step,
	// so neither the frame below nor any later one may carry the run that just
	// ended. Doing it here — rather than at step 1 — is what makes a summarize
	// (which never reaches a step) clear it too.
	s.lastStepTPS = 0
	s.lastTTFTMS = 0

	// Announce the running state, here rather than at the spawn sites: setting
	// activeTask is the one thing every task start must do (the BUSY gate,
	// cancellation and completion all read it), so a new entry point cannot
	// forget the broadcast by forgetting some second helper.
	//
	// The frame is required, not decorative. Task status otherwise reaches the
	// adapter only on step boundaries (stepStartEvent/stepFinishEvent →
	// sendSystemInfo). A summarize suppresses the boundary events that would
	// open a step (processPrompt runs as a summarizeCall), and its lone
	// stepFinishEvent arrives only once the round trip is over — so for
	// essentially the whole summarize the adapter would read the session as
	// idle, and the status bar's indicator would sit on the idle glyph instead
	// of spinning. (The task-start auto-summarize runs before the task's first
	// step event and has the same gap.)
	s.sendSystemInfo(systemInfoTask)
	return taskCtx, nil
}

// startTaskCommand starts an async task command (:continue/:summarize).
// It replies CO immediately — {"status":"started"} on acceptance, or an
// error if the task cannot start. Task completion is reported via taskMsg
// carrying the command ID (see sendTaskMsg).
//
// The CO is written after beginTask's running-state frame. Accepting the
// command and starting the task are one event; the two frames are its
// projections onto the control plane (CO) and the state plane (SM), and their
// interleaving is not part of the contract (adapter-guide → "Async task
// commands").
func (s *Session) startTaskCommand(commandID string, run func(context.Context)) {
	ctx, err := s.beginTask(commandID)
	if err != nil {
		s.writeCmdResult(commandID, nil, err)
		return
	}
	s.writeCmdResult(commandID, map[string]any{"status": "started"}, nil)
	go run(ctx)
}

// submitPrompt takes a user prompt and starts it, steers with it, or holds it.
//
// Three outcomes, and the difference between them is the point:
//
//   - The session is idle: the prompt starts a task.
//   - A task is in flight: the prompt is spliced into that task at its next
//     step boundary (steering — see session_steering.go) instead of being
//     refused. This is the case that used to be a plain BUSY error and the
//     reason a correction no longer costs a cancel plus a wasted task.
//   - The session cannot start a task at all: refused. A *stage* it is passing
//     through — MCP initialization — is not a refusal but a hold: the adapter
//     has no way to know when that stage ends, and a client that submitted its
//     only prompt and then hit EOF has no way to retry.
//
// The holding place is a single slot, not a queue: while it is occupied there
// is nothing in flight, so a prompt that arrives on top of it can only mean a
// client that wants two tasks at once — refused, as it always was.
func (s *Session) submitPrompt(parts []llm.ContentPart) {
	ctx, err := s.beginTask("")
	switch {
	case err == nil:
		go s.runTaskNormal(ctx, parts)
	case errors.Is(err, errMCPNotReady) && s.pending == nil:
		s.pending = parts
		s.writeNotify("prompt deferred until MCP initialization completes")
	case errors.Is(err, errTaskBusy):
		// A task is running: steer it rather than refuse. Only a full steering
		// queue is refused, and the refusal carries its own reason — the generic
		// BUSY text ("a task is already running") names a condition that is not
		// why THIS prompt was refused.
		if !s.queueSteering(parts) {
			s.writeSteering(protocol.SteeringRefused, "",
				"Too many prompts are already queued for the running task; this one was not queued. Wait for it to continue, or cancel it.")
			return
		}
		// Accepted: say so. Nothing else tells the user their words were taken.
		// They reach the transcript at the task's next step boundary, where they
		// are spliced in and echoed — a whole tool call away, or not at all if
		// the task ends first.
		s.writeSteering(protocol.SteeringQueued, "",
			"steering queued — delivered into the running task, or as your next prompt")
	default:
		s.writeError(err.Error()) // MCP slot already taken, or a permanent failure
	}
}

// startDeferredPrompt starts the prompt that was held back until the session
// became ready.
//
// Invariant: pending != nil implies State() != SessionReady, and syncState is
// the only place that transition happens — so this runs at most once, and only
// after the ready frame has been written.
func (s *Session) startDeferredPrompt() {
	if s.pending == nil {
		return
	}
	parts := s.pending
	s.pending = nil
	ctx, err := s.beginTask("")
	if err != nil {
		// Unreachable: the slot is only filled while the session is not ready,
		// and this runs on the transition out of that state.
		s.writeError(err.Error())
		return
	}
	go s.runTaskNormal(ctx, parts)
}

// handleInputMsg processes a parsed input message. Called from run() goroutine.
func (s *Session) handleInputMsg(msg inputMsg) {
	if msg.inputEnd {
		// The adapter is done sending prompts. Whatever it sent before this
		// was already handled (one goroutine, one channel, FIFO), so all that
		// is left is to stop accepting new work and let run() finish what it
		// already took on.
		s.inputEnded = true
		return
	}

	if msg.err != nil {
		// Frame-level failures go to the session plane, not the command plane.
		// A CO answers exactly one command request, and none of these is one: a
		// malformed frame, a bad content frame, an unknown tag. A CI whose JSON
		// cannot be read lands here too — its id is precisely the part that
		// could not be read, so a CO would be a frame no adapter could match to
		// anything it sent.
		s.writeError(msg.err.Error())
		return
	}

	// A readable command request that was refused before dispatch still gets
	// its CO: the request exists, the id is there to echo, and the adapter can
	// match the refusal to what it sent.
	if msg.reject != nil {
		s.writeCmdResult(msg.commandID, nil, msg.reject)
		return
	}

	if !msg.isCmd {
		s.submitPrompt(msg.contentParts)
		return
	}

	// command dispatch. CI frames carry the command name and argument text
	// separately; each handler parses its argument string as appropriate
	// for its command.
	name := msg.cmd
	args := msg.args
	if name == "" {
		s.writeCmdResult(msg.commandID, nil, &cmdErr{Code: "INVALID_ARGS", Message: "empty command"})
		return
	}

	// A command that declares no arguments takes none. An extra word is a
	// typo, and ignoring it hides the mistake — `:quit` has always refused one,
	// and the rest should not disagree with it. The declaration is the usage
	// line in commands.Defs, the same one an adapter renders, so the check and
	// the help text cannot drift apart: a command that gains an argument gains
	// a usage line, and with it the right to have one.
	if def, ok := commands.DefinitionOf(commands.Canonical(name)); ok &&
		def.Usage == "" && strings.TrimSpace(args) != "" {
		s.writeCmdResult(msg.commandID, nil, &cmdErr{Code: "INVALID_ARGS", Message: commands.UsageLine(def.Name)})
		return
	}

	// Task commands — :continue and :summarize start a task goroutine.
	// CO replies immediately ({"status":"started"} or an error); task
	// completion is correlated via taskMsg.command_id.
	switch name {
	case commands.CommandNameContinue:
		s.startTaskCommand(msg.commandID, s.runTaskContinue)
		return
	case commands.CommandNameSummarize:
		s.startTaskCommand(msg.commandID, s.runTaskSummarize)
		return
	}

	// Registry commands — synchronous dispatch in the run() goroutine.
	// Aliases (:q) resolve to their canonical name first: which words name
	// a command is the vocabulary package's business, not the dispatcher's.
	cmdDef, ok := lookupCommand(commands.Canonical(name))
	if !ok {
		s.writeCmdResult(msg.commandID, nil, &cmdErr{Code: "UNKNOWN_COMMAND", Message: fmt.Sprintf("unknown command: %s", name)})
		return
	}

	if cmdDef.Policy == cmdIdle && s.activeTask != nil {
		s.writeCmdResult(msg.commandID, nil, &cmdErr{Code: "BUSY",
			Message: "Cannot run this command while a task is in progress. Please wait or cancel the current task."})
		return
	}
	s.execCommand(s.sessionCtx, msg.commandID, cmdDef, args)
}

// execCommand runs a registered command handler and writes its CO result.
// The handler is synchronous (runs in the run() goroutine), so the command ID
// is passed directly and never needs to be stored on the Session.
func (s *Session) execCommand(ctx context.Context, commandID string, cmdDef *command, args string) {
	result, err := cmdDef.Handler(s, ctx, args)
	s.writeCmdResult(commandID, result, err)
}

// ============================================================================
// Model commands — handleModelSet, handleModelLoad, handleModelSync
//
// These commands manage model configuration: switching the active model,
// reloading from file, and syncing from an adapter editor session.
// ============================================================================

// checkModelManager is a shared preamble for model handlers. Returns the
// model manager, or a cmdErr if it is not initialized.
func (s *Session) checkModelManager() (*modelManager, error) {
	mm := s.modelService.manager
	if mm == nil {
		return nil, &cmdErr{Code: "NOT_INITIALIZED", Message: "model manager not initialized"}
	}
	return mm, nil
}

func (s *Session) handleModelSet(args string) (any, error) {
	mm, err := s.checkModelManager()
	if err != nil {
		return nil, err
	}

	if args == "" {
		return nil, &cmdErr{Code: "INVALID_ARGS", Message: commands.UsageLine(commands.CommandNameModelSet)}
	}

	modelID, err := strconv.Atoi(args)
	if err != nil {
		return nil, &cmdErr{Code: "INVALID_ARGS", Message: fmt.Sprintf("model_set: invalid model ID: %s", args)}
	}
	model := mm.getModel(modelID)
	if model == nil {
		return nil, &cmdErr{Code: "MODEL_NOT_FOUND", Message: fmt.Sprintf("model_set: model not found: %d", modelID)}
	}

	if err := mm.setActive(modelID); err != nil {
		return nil, &cmdErr{Code: "MODEL_ERROR", Message: err.Error()}
	}

	// Persist the switch. Sessions with a file-specified model store the
	// preference in-memory (saved to the session file on :save), while
	// sessions without one write to the global runtime.conf.
	var persistErr error
	if s.SessionFile != "" {
		s.modelService.sessionMetaModel = model.Name
	} else if rm := s.modelService.runtimeMgr; rm != nil {
		persistErr = rm.setActiveModel(model.Name)
	}

	if err := s.SwitchModel(model); err != nil {
		return nil, &cmdErr{Code: "MODEL_ERROR", Message: "Failed to switch model: " + err.Error()}
	}
	if persistErr != nil {
		return nil, &cmdErr{Code: "PERSIST_FAILED", Message: fmt.Sprintf("Failed to persist model switch: %v", persistErr)}
	}
	return map[string]any{"active_id": modelID, "active_name": model.Name}, nil
}

func (s *Session) handleModelLoad() (any, error) {
	mm, err := s.checkModelManager()
	if err != nil {
		return nil, err
	}

	path := mm.getFilePath()
	if path == "" {
		return nil, &cmdErr{Code: "NOT_CONFIGURED", Message: "no model file path configured"}
	}

	if err := mm.loadFromFile(path); err != nil {
		return nil, &cmdErr{Code: "LOAD_FAILED", Message: fmt.Sprintf("model_load: failed to load models: %v", err)}
	}

	// Apply the successfully loaded models regardless of validation
	// outcome — then fail the command if any model block was rejected.
	// The user must see these errors to fix their config.
	s.modelService.ResolveActiveModel()
	if model := mm.getActive(); model != nil {
		if err := s.SwitchModel(model); err != nil {
			return nil, &cmdErr{Code: "MODEL_ERROR", Message: "Failed to reinitialize model after reload: " + err.Error()}
		}
	}
	s.sendModelListMsg()

	if msgs := mm.getLoadErrors(); len(msgs) > 0 {
		return nil, &cmdErr{Code: "MODEL_VALIDATION", Message: strings.Join(msgs, "; ")}
	}
	return map[string]any{"models": mm.getModels()}, nil
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
		return nil, &cmdErr{Code: "INVALID_ARGS", Message: commands.UsageLine(commands.CommandNameModelSync)}
	}

	msgs := mm.syncFromContent(args)

	// Apply the successfully synced models, then fail the command if any
	// model was rejected or persistence failed.
	s.modelService.ResolveActiveModel()
	if model := mm.getActive(); model != nil {
		if err := s.SwitchModel(model); err != nil {
			return nil, &cmdErr{Code: "MODEL_ERROR", Message: "Failed to reinitialize model after sync: " + err.Error()}
		}
	}
	s.sendModelListMsg()

	if len(msgs) > 0 {
		return nil, &cmdErr{Code: "MODEL_VALIDATION", Message: strings.Join(msgs, "; ")}
	}
	return map[string]any{"models": mm.getModels()}, nil
}

// ============================================================================
// Configuration commands — handleReasoningLevelSet, handleVideoConfig, handleThemeSet
//
// These commands let the user adjust session-level settings such as
// reasoning effort, video encoding parameters, and the active theme.
// ============================================================================

func (s *Session) handleReasoningLevelSet(args string) (any, error) {
	level, err := strconv.Atoi(args)
	if err != nil || level < config.ReasoningLevelOff || level > config.ReasoningLevelMax {
		return nil, &cmdErr{Code: "INVALID_ARGS", Message: commands.UsageLine(commands.CommandNameReasoningLevelSet) + "  (0=off, 1=normal, 2=max)"}
	}
	s.SetReasoningLevel(level)
	return map[string]any{"level": level}, nil
}

// handleVideoConfig sets the default video FPS and resolution for video attachments.
// Usage: :video_config <fps> <resolution>
//
//	fps: frames per second (positive integer, e.g. 2)
//	res: 0=default, 1=max
func (s *Session) handleVideoConfig(args string) (any, error) {
	usage := commands.UsageLine(commands.CommandNameVideoConfig) + "  (fps: positive integer, res: 0=default, 1=max)"
	fields := strings.Fields(args)
	// Exactly two: a third token used to be ignored, which turns a typo into a
	// setting the user did not ask for.
	if len(fields) != 2 {
		return nil, &cmdErr{Code: "INVALID_ARGS", Message: usage}
	}
	fps, err := strconv.Atoi(fields[0])
	if err != nil || fps < 1 {
		return nil, &cmdErr{Code: "INVALID_ARGS", Message: usage}
	}
	res, err := strconv.Atoi(fields[1])
	if err != nil || res < 0 || res > 1 {
		return nil, &cmdErr{Code: "INVALID_ARGS", Message: usage}
	}
	s.SetVideoConfig(fps, res)
	return map[string]any{"fps": fps, "res": res}, nil
}

// handleThemeSet sets the active theme, persists it to runtime config,
// and sends updated system info so adapters receive the full theme data.
// Rejected with UNAVAILABLE when NoTheme is set (non-TUI modes).
func (s *Session) handleThemeSet(args string) (any, error) {
	if s.NoTheme {
		return nil, &cmdErr{Code: "UNAVAILABLE", Message: "theme management is not available in this mode"}
	}
	// ":theme_set <name>" — the name is the whole tail, trimmed: a theme is
	// named by its file (my theme.conf → "my theme"), so inner spaces are part
	// of the name and invisible whitespace at the ends is not.
	name := strings.TrimSpace(args)
	if name == "" {
		return nil, &cmdErr{Code: "INVALID_ARGS", Message: commands.UsageLine(commands.CommandNameThemeSet)}
	}

	// Validate that the theme exists before persisting.
	if s.ThemesFolder != "" {
		themePath := filepath.Join(s.ThemesFolder, name+".conf")
		// Only a missing file is NOT_FOUND. Any other stat failure means the
		// theme cannot be checked — and applying it would leave the name active
		// in the runtime config while the UI kept its old colors, because the
		// adapter resolves a theme by looking it up in the list it was sent.
		if _, err := os.Stat(themePath); err != nil {
			if os.IsNotExist(err) {
				return nil, &cmdErr{Code: "NOT_FOUND", Message: fmt.Sprintf("Theme %q not found", name)}
			}
			return nil, &cmdErr{Code: "THEME_ERROR", Message: fmt.Sprintf("Cannot read theme %q: %v", name, err)}
		}
	}

	var persistErr error
	if s.modelService.runtimeMgr != nil {
		persistErr = s.modelService.runtimeMgr.setActiveTheme(name)
	}
	s.sendSystemInfo(systemInfoTheme)
	if persistErr != nil {
		return nil, &cmdErr{Code: "PERSIST_FAILED", Message: fmt.Sprintf("Failed to persist theme switch: %v", persistErr)}
	}
	return map[string]any{"name": name}, nil
}

// ============================================================================
// MCP commands — handleMCPSkip, handleMCPConfirm, handleMCPDecline
//
// These commands manage MCP server lifecycle: cancel initialization,
// confirm an OAuth authorization code, or decline an authorization request.
// ============================================================================

// handleMCPSkip handles the :mcp_skip command.
// Called when the user presses Ctrl+G (init overlay or globally) or types
// the command directly. Cancels the entire MCP initialization.
func (s *Session) handleMCPSkip() (any, error) {
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
		return &cmdErr{Code: "NOT_CONFIGURED", Message: "No MCP servers configured."}
	}
	if s.mcpService.IsReady() {
		return &cmdErr{Code: "NOT_IN_PROGRESS", Message: "MCP initialization is not in progress."}
	}
	return nil
}

// handleMCPConfirm handles the :mcp_confirm command.
//
// Usage: :mcp_confirm <server> <code> <redirect_uri> [iss]
//
// The optional 4th argument is the RFC 9207 `iss` parameter from the
// authorization response. It is only available on the automatic callback
// path; manual confirmation (pasting a code from the browser) omits it,
// in which case issuer validation is skipped unless the authorization
// server advertises mandatory support.
func (s *Session) handleMCPConfirm(_ context.Context, args string) (any, error) {
	fields := strings.Fields(args)
	// Three, or four with the optional iss. Anything else is a usage error
	// rather than a misparse: a server name with a space arrives as two tokens,
	// and answering NOT_FOUND for the first half names a server the user never
	// typed.
	if len(fields) != 3 && len(fields) != 4 {
		return nil, &cmdErr{Code: "INVALID_ARGS", Message: commands.UsageLine(commands.CommandNameMCPConfirm)}
	}
	if err := s.checkMCPReady(); err != nil {
		return nil, err
	}

	server := fields[0]
	code := fields[1]
	redirectURI := fields[2]
	iss := ""
	if len(fields) >= 4 {
		iss = fields[3]
	}
	if !s.mcpService.SendAuthCodeResult(server, code, redirectURI, iss) {
		return nil, &cmdErr{Code: "NOT_FOUND", Message: fmt.Sprintf("No pending authorization for MCP server %q.", server)}
	}
	return map[string]any{"server": server}, nil
}

// handleMCPDecline handles the :mcp_decline command.
//
// Usage: :mcp_decline <server>
func (s *Session) handleMCPDecline(args string) (any, error) {
	fields := strings.Fields(args)
	// Exactly one server name for the same reason: "my db" is one name split in
	// two, and declining "my" would report a server the user never configured.
	if len(fields) != 1 {
		return nil, &cmdErr{Code: "INVALID_ARGS", Message: commands.UsageLine(commands.CommandNameMCPDecline)}
	}
	if err := s.checkMCPReady(); err != nil {
		return nil, err
	}

	server := fields[0]
	if !s.mcpService.SendAuthCodeResult(server, "", "", "") {
		return nil, &cmdErr{Code: "NOT_FOUND", Message: fmt.Sprintf("No pending authorization for MCP server %q.", server)}
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
	// ":save [filename]" — the filename is the whole tail, and its surrounding
	// whitespace is trimmed: "…alaya " names a file the user can see, not one
	// ending in a space.
	filename := strings.TrimSpace(args)
	if filename == "" {
		if s.SessionFile == "" {
			return nil, &cmdErr{Code: "NOT_CONFIGURED", Message: "no session file set"}
		}
		path = s.SessionFile
	} else {
		path = config.ExpandPath(filename)
	}

	if err := s.saveContentToFile(path, s.Contents); err != nil {
		return nil, &cmdErr{Code: "IO_ERROR", Message: fmt.Sprintf("failed to save session: %v", err)}
	}
	return map[string]any{"path": path}, nil
}

// handleQuit handles the :quit command. It asks the session to end: the
// deferred prompt is dropped, no new work is accepted, and run() returns as
// soon as nothing is in flight — a task already running still finishes.
// Idempotent.
func (s *Session) handleQuit(args string) (any, error) {
	if strings.TrimSpace(args) != "" {
		return nil, &cmdErr{Code: "INVALID_ARGS", Message: commands.UsageLine(commands.CommandNameQuit)}
	}
	s.quitting = true
	if s.pending != nil {
		// A prompt held for MCP readiness will not run: the session is leaving.
		// Its deferral was announced, so its loss is too — a drop is never
		// silent.
		s.pending = nil
		s.writeNotify("prompt dropped — the session is quitting")
	}
	return nil, nil
}

func (s *Session) cancelTask() (any, error) {
	// A prompt accepted but not started: canceling it means dropping it, and
	// then there is nothing in flight for run() to wait for. The drop is
	// announced, like every other.
	if s.pending != nil {
		s.pending = nil
		s.writeNotify("prompt dropped — it was canceled before it could start")
		return nil, nil
	}
	if s.activeTask != nil {
		if s.cancelRunningTask() {
			// Canceling means stop, so anything the user steered into the task
			// being canceled goes with it: leaving it queued would start a new
			// task on its own the instant this one unwinds, which is a stop
			// button that keeps going. The user is told, because the words are
			// theirs to retype.
			//
			// Only what is queued at this instant is dropped. A prompt that
			// arrives after the cancel is a fresh intent — "cancel, then type
			// what I actually wanted" is an ordinary sequence, and swallowing
			// it would be the silent loss this rule exists to avoid.
			//
			// What the task goroutine has already spliced is not here to take,
			// and is not dropped: those words were sent to the model and echoed
			// to the adapter, so they are part of the conversation. A cancel
			// stops the task; it cannot unsend what the model was given (see
			// session_steering.go).
			if s.cancelSteering() {
				s.writeSteering(protocol.SteeringDropped, protocol.SteeringDroppedCanceled,
					"steering dropped — the task was canceled before it was delivered")
			}
			return nil, nil
		}
	}
	return nil, &cmdErr{Code: "NOTHING_TO_CANCEL", Message: "nothing to cancel"}
}

// CancelTask aborts the currently running task, if any. Safe to call from
// any goroutine: the request is processed by the run() goroutine (which
// owns activeTask) via cancelReqCh. Returns true if a task was running
// and was canceled, false if there was nothing to cancel or the session
// has already exited. Used by the plainio/terseio SIGINT handlers — the
// frame input pipe cannot carry a CI frame after the adapter closed it at
// EOF, so cancellation must not depend on the pipe.
func (s *Session) CancelTask() bool {
	done := make(chan bool, 1)
	select {
	case s.cancelReqCh <- done:
	case <-s.sessionCtx.Done():
		return false
	}
	select {
	case ok := <-done:
		return ok
	case <-s.sessionCtx.Done():
		return false
	}
}
