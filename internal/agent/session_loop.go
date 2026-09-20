package agent

// Session event loop: main select loop.
//
// The run() goroutine owns all mutable state. It processes events from
// the input pump, the task goroutine, and system info requests.
//
// There is no task queue: one task runs at a time, and a prompt that arrives
// while one is in flight is refused (BUSY) rather than parked. The one prompt
// that *is* held is one that arrives before the session is ready (MCP init still
// running) — it waits in a single slot, because a client cannot see that stage
// and one at EOF has no way to send the prompt again (see submitPrompt).
//
// Extracted from session_task.go to separate concerns:
//   - session_task.go:        prompt processing, agent loop, auto-summarization
//   - session_loop.go:        event loop
//   - session_io.go:          input pump, command dispatch

import (
	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/mcp"
)

// ============================================================================
// Main Event Loop
// ============================================================================

// run is the main event loop. It processes:
//   - Input messages from the user (via inputPump → inputMsgCh)
//   - Task state changes (via task goroutine → taskEventCh)
//   - Task completion signals (via taskResultCh)
//   - MCP initialization events (via mcpService.Events())
func (s *Session) run() {
	// The session's last act, in this order: stop the housekeeping (task
	// goroutines see sessionCtx done), publish the terminal state — which is
	// written after every other frame, since nothing else can write once the
	// state is stored — and only then wake the in-process waiters of Done().
	defer func() {
		s.sessionCancel()
		s.setState(SessionClosed)
		close(s.runDoneCh)
	}()

	// Start MCP initialization — the goroutine sends events that
	// we read from mcpService.Events() in the main select below.
	s.mcpService.Start(s.sessionCtx)

	// Start the I/O pump goroutine.
	s.inputMsgCh = make(chan inputMsg, 100)
	go s.inputPump()

	// Capture the MCP events channel once. When the channel is closed
	// (init complete), we set mcpEvents to nil to disable the select case.
	mcpEvents := s.mcpService.Events()

	// Initial lifecycle state: with MCP configured, the session stays
	// Initializing until the init events settle; without MCP, it is
	// Ready from the start (mcpService.IsReady() is true by construction)
	// and setState broadcasts the session-ready frame here.
	if s.mcpService.IsReady() {
		s.setState(SessionReady)
	} else {
		s.setState(SessionInitializing)
	}

	// Capture the input channel in a local: the loop stops selecting on it once
	// the stream ends, while s.inputMsgCh itself must stay valid — inputPump
	// sends on that field.
	ch := s.inputMsgCh

	for {
		if s.shouldExit() {
			return
		}

		select {
		case msg, ok := <-ch:
			if !ok {
				// The adapter's stream ended (EOF): no more input, but the
				// input is not what is in flight — a running task or a
				// deferred prompt still is. Clear the case so it cannot spin
				// on a channel that is permanently ready, and let the loop
				// finish what was already accepted.
				s.inputEnded = true
				ch = nil
				continue
			}
			s.handleInputMsg(msg)

		case done := <-s.cancelReqCh:
			_, err := s.cancelTask()
			done <- err == nil

		case ev := <-s.taskEventCh:
			s.handleTaskEvent(ev)

		case contents := <-s.taskResultCh:
			s.handleTaskDone(contents)

		case evt, ok := <-mcpEvents:
			if !ok {
				// Channel closed — disable this case permanently.
				mcpEvents = nil
				if !s.mcpService.IsReady() {
					s.mcpService.MarkAborted()
					s.writeError("MCP initialization canceled.")
					s.mcpService.sendSystemMsg(&mcpMsgData{Status: "done"})
				}
				// MCP init has settled (done, canceled, or aborted) —
				// advance the lifecycle state.
				s.syncState()
				break
			}
			s.handleMCPEvent(&evt)

		case <-s.sessionCtx.Done():
			return
		}
	}
}

// shouldExit reports whether run() can return: the session's context is done
// (its output broke, or the process is going away), or its input has ended or a
// quit was accepted *and* nothing is in flight.
//
// "Nothing in flight" is the point. A task that is still running is work the
// user asked for, and so is a prompt accepted before the session was ready: the
// session holds them rather than dropping them, which is only possible because
// the input end is a fact about the input, not a teardown of the conversation.
func (s *Session) shouldExit() bool {
	if s.sessionCtx.Err() != nil {
		return true
	}
	return (s.quitting || s.inputEnded) && s.activeTask == nil && s.pending == nil
}

// handleMCPEvent processes a single MCP initialization event.
// Called from the main loop when an event arrives on mcpService.Events().
func (s *Session) handleMCPEvent(evt *mcp.InitEvent) {
	action := s.mcpService.HandleEvent(evt)
	if action == nil {
		return
	}

	// Send system message to the UI (progress updates, auth prompts, etc.)
	if action.SystemMsg != nil {
		s.mcpService.sendSystemMsg(action.SystemMsg)
	}

	// Display InitFailed errors in the chat window.
	if evt.Type == mcp.InitFailed && evt.Error != "" {
		s.writeErrorf("MCP: %v", evt.Error)
	}

	// Apply InitDone results.
	if action.ApplyResult {
		if action.Tools != nil {
			s.BaseTools = append(s.BaseTools, action.Tools...)
		}
		if action.SysFragment != "" {
			s.SystemPrompt += action.SysFragment
		}
		// Recreate agent if it was already initialized.
		if s.Agent() != nil {
			s.modelService.Reset()
		}
		serverCount := 0
		if action.Manager != nil {
			serverCount = action.Manager.ActiveServerCount()
		}
		s.writeNotifyf("MCP servers initialized: %d servers, %d tools loaded",
			serverCount, len(action.Tools))
	}

	// Log abort messages.
	if action.Aborted {
		s.writeError("MCP initialization canceled.")
	}

	// MCP init may have settled (InitDone/canceled) — advance the
	// lifecycle state. Idempotent; safe after every event.
	s.syncState()
}

// handleTaskDone processes a task completion signal from the task goroutine.
func (s *Session) handleTaskDone(contents []llm.ContentPart) {
	s.flushPendingEvents()
	if s.activeTask != nil {
		// Preserve the command ID for the final taskMsg — activeTask is
		// cleared below, before the completion broadcast.
		s.taskCommandID = s.activeTask.commandID
	}
	s.activeTask = nil

	// The task is over: no confirmation prompt can be answered anymore.
	// Drop any channels left behind by canceled tasks (the user never
	// responded, so resolveToolConfirm never removed them).
	s.cleanupConfirmChannels()

	if len(contents) > 0 {
		s.Contents = contents
	}

	if s.SessionFile != "" {
		if err := s.saveContentToFile(s.SessionFile, s.Contents); err != nil {
			s.writeErrorf("Auto-save failed: %v", err)
		}
	}

	s.sendSystemInfo(systemInfoTask)
}

// flushPendingEvents drains remaining taskEventCh events from the
// just-finished task before the next one starts.
func (s *Session) flushPendingEvents() {
	for {
		select {
		case ev := <-s.taskEventCh:
			s.handleTaskEvent(ev)
		default:
			return
		}
	}
}

// handleTaskEvent processes a state change event from the task goroutine.
func (s *Session) handleTaskEvent(ev taskEvent) {
	switch e := ev.(type) {
	case stepStartEvent:
		if s.activeTask != nil {
			s.activeTask.step = e.Step
		}
		// A new task starts at step 1: clear the previous task's speed
		// values so the step-1 broadcast (sent below) never carries
		// stale data — visible to rawio and other consumers beyond the
		// TUI.
		if e.Step == 1 {
			s.lastStepTPS = 0
			s.lastTTFTMS = 0
		}
		s.sendSystemInfo(systemInfoTask)

	case stepStatsEvent:
		// Speed metrics for the just-finished step. Totals were reset by
		// the stepStartEvent(Step==1) of this task; the values are read
		// by the stepFinishEvent broadcast that follows (FIFO).
		//
		// TokensPerSec is the simple end-to-end throughput of the step
		// (output tokens / round-trip duration). Steps with no output
		// tokens carry 0, which clears the displayed speed — nothing
		// to measure.
		s.lastStepTPS = e.TokensPerSec
		s.lastTTFTMS = e.TimeToFirstToken.Milliseconds()

	case stepFinishEvent:
		if len(e.NewParts) > 0 {
			s.Contents = append(s.Contents, e.NewParts...)
		}
		newContext := e.InputTokens + e.OutputTokens + e.CacheReadTokens + e.CacheCreationTokens
		if newContext > 0 {
			s.ContextTokens = newContext
		}
		s.sendSystemInfo(systemInfoTask)

	case promptPartsEvent:
		s.Contents = append(s.Contents, e.Parts...)

	case contentsReplacedEvent:
		s.Contents = e.Contents

	case setContextTokensEvent:
		if e.Tokens > 0 {
			s.ContextTokens = e.Tokens
		}
		s.sendSystemInfo(systemInfoTask)
	}
}
