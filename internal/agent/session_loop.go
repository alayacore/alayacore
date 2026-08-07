package agent

// Session event loop: main select loop.
//
// The run() goroutine owns all mutable state. It processes events from
// the input pump, the task goroutine, and system info requests.
// There is no task queue — prompts and LLM-requiring commands run
// immediately in a task goroutine.  Input during a running task is
// rejected.
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
	defer close(s.runDoneCh)
	defer s.sessionCancel()

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

	for {
		if s.sessionCtx.Err() != nil {
			return
		}

		select {
		case msg, ok := <-s.inputMsgCh:
			if !ok {
				// Input closed (EOF). Drain the currently running task.
				if s.activeTask != nil {
					s.drainUntilTaskDone()
				}
				return
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

// drainUntilTaskDone processes task completion signals until the currently
// running task finishes. Used during shutdown (input EOF) to let the active
// task complete before the session exits. Cancel requests are still served
// so SIGINT can abort the drained task instead of waiting for it.
//
// Priority: taskResultCh is checked first to avoid processing redundant
// events when the task has already finished.
func (s *Session) drainUntilTaskDone() {
	for {
		// Check taskResultCh first with priority to avoid unnecessary
		// event processing when the task has already completed.
		select {
		case contents := <-s.taskResultCh:
			s.handleTaskDone(contents)
			return
		default:
		}

		select {
		case ev := <-s.taskEventCh:
			s.handleTaskEvent(ev)
		case contents := <-s.taskResultCh:
			s.handleTaskDone(contents)
			return
		case done := <-s.cancelReqCh:
			_, err := s.cancelTask()
			done <- err == nil
		case <-s.sessionCtx.Done():
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
		s.sendSystemInfo(systemInfoTask)

	case stepFinishEvent:
		newContext := e.InputTokens + e.OutputTokens + e.CacheReadTokens + e.CacheCreationTokens
		if newContext > 0 {
			s.ContextTokens = newContext
		}
		s.sendSystemInfo(systemInfoTask)

	case setContextTokensEvent:
		if e.Tokens > 0 {
			s.ContextTokens = e.Tokens
		}
		s.sendSystemInfo(systemInfoTask)
	}
}
