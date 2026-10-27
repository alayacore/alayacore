package agent

// Session task execution: processing prompts through the agent loop,
// auto-summarization, and cleaning incomplete tool calls.
//
// The main event loop lives in session_loop.go.
// I/O (input pump, command dispatch) lives in session_io.go.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/protocol"
	"github.com/alayacore/alayacore/internal/wire"
)

// ============================================================================
// Core: processPrompt
//
// The central function that sends a conversation to the LLM via the agent's
// Stream method. All task goroutines (runTaskNormal, runTaskContinue,
// runTaskSummarize) ultimately call this function.
//
// The deltaWriter, callback methods (handleTextComplete, handleToolConfirm,
// etc.), and cleanIncompleteToolInputs below are its direct dependencies.
// ============================================================================

// promptKind says which of the two things processPrompt has been asked to do.
// They share this function because they share almost everything — the same
// stream, the same multi-step tool loop, the same callbacks — but they are
// different jobs, and the difference is not a detail this code may forget:
//
//   - userPrompt is the task behind the user's own words. Its progress belongs
//     on the adapter's status bar, its history is the conversation's, and it is
//     the only kind that may take the user's steering.
//   - summarizeCall is a summarize — :summarize, the auto-summarize at the
//     start of a task, or the one a full context triggers between steps. It
//     drives the same loop (it can even call tools), but its result never
//     becomes the conversation: it replaces the history or is thrown away. So
//     nothing about it is published, and it must keep its hands off the user's
//     words.
//
// Both are tasks (both cross `in_progress`), which is why "task" has no
// second word: what distinguishes them is the kind, and every sentence that
// needs to say which says kind.
//
// Named rather than a bare bool because the value is read at the call site,
// where "false" said only "do not publish steps" — one of the three things it
// actually means.
type promptKind int

const (
	userPrompt promptKind = iota
	summarizeCall
)

// processPrompt sends the conversation history to the LLM via the agent's
// Stream method, using registered callbacks for streaming output, tool
// execution, and step tracking. Returns the full response contents and
// total output token usage.
//
// kind decides what the call is given, and everything it is given follows from
// that one answer (see promptKind): a userPrompt publishes its step progress
// (stepStartEvent/stepStatsEvent for the status bar, and a stepFinishEvent
// delta per step so :save/:fork see in-progress task content) and may rewrite
// the history it is about to send — that is where a mid-task compaction and the
// user's steering are spliced in. A summarizeCall gets none of that: publishing
// its steps would move the displayed step and blank the speed mid-task, its
// internals (prompt, response, and any tool calls of its own) must not
// transiently pollute Contents, and it must not compact again or take steering
// into a history that will be discarded.
func (s *Session) processPrompt(ctx context.Context, history []llm.ContentPart, kind promptKind) ([]llm.ContentPart, int64, error) {
	var fullContents []llm.ContentPart
	var outputTokens int64

	// The facts a step boundary shares — what run() has been told about, the
	// history the current step was sent on, and the batch appended at this
	// boundary — live on one value, which is where their invariants are stated
	// (see stepRecord).
	rec := newStepRecord(history)

	// turnFailed records that this call — the user's task — did not land. The
	// deferred discard below reads it together with ctx: a task that failed and
	// a task that was canceled are the same event as far as the user's steering
	// is concerned.
	var turnFailed bool
	defer func() {
		// A task that did not land — it failed, or it was canceled — takes the
		// steering it never spliced with it, and says so. Only the queue: what a
		// step had already spliced was numbered, echoed and sent, and belongs to
		// the conversation whatever the step did next (see session_steering.go).
		if !turnFailed && ctx.Err() == nil {
			return
		}
		s.discardQueuedSteering()
	}()

	// contextTokens is this task's view of the context size, taken from the
	// provider's authoritative usage after each step, and the number the
	// mid-task check reads. Unlike s.ContextTokens (owned by run() and updated
	// asynchronously from task events), it is written and read on the task
	// goroutine only, so the check needs no synchronization.
	//
	// It measures the step that just finished, which is one tool result short
	// of what the next step will send — the exact count is only known once
	// that request returns. The --auto-summarize headroom is what absorbs the
	// difference, the same approximation the task-start check has always used.
	var contextTokens int64

	onStepFinish := func(contents []llm.ContentPart, usage llm.Usage) error {
		fullContents = cleanIncompleteToolInputs(contents)

		if n := usage.InputTokens + usage.OutputTokens + usage.CacheReadTokens + usage.CacheCreationTokens; n > 0 {
			contextTokens = n
		}
		ev := stepFinishEvent{
			InputTokens:         usage.InputTokens,
			OutputTokens:        usage.OutputTokens,
			CacheReadTokens:     usage.CacheReadTokens,
			CacheCreationTokens: usage.CacheCreationTokens,
		}
		// The step's own output, and the baseline moved past it. Both happen for
		// a summarizeCall too — its steps are not published, but the baseline is
		// the same baseline, and leaving it behind is how a later suffix could
		// describe parts nobody was told about.
		newParts := rec.finished(fullContents)
		if kind == userPrompt {
			// NewParts is a view into the agent's accumulation; run() copies the
			// pointers out immediately and never retains the view (see
			// stepFinishEvent docs).
			//
			// Stamp the fields a part cannot know about itself — its message
			// time, and a video's session default — HERE, in the task goroutine,
			// before the parts are handed over.
			//
			// After the handoff both goroutines hold these parts: run() echoes
			// them and appends them to Contents, while this goroutine reads them
			// back for the next step's request and for a mid-task session save. A
			// write from either side after that point is a data race on the part
			// (a summarize that saves the history while run() stamps it, for
			// one). Stamping once, on the near side, is what keeps the part
			// immutable to everyone else.
			ev.NewParts = newParts
			s.stampParts(newParts)
		}
		s.sendEvent(ev)
		outputTokens += usage.OutputTokens
		return nil
	}

	// onBeforeSend runs at the top of every step of a userPrompt, before its
	// request is sent. If the context has grown past the --auto-summarize
	// threshold mid-task, it summarizes the conversation and hands the compacted
	// history back for the rest of the task; and it is where the user's steering
	// is spliced in. Both are rewrites of the history the next request is built
	// from, which is why this hook — like the step-boundary events — is
	// installed for a userPrompt alone (see the callbacks below): a summarizeCall
	// never gets it, so it can neither re-trigger compaction nor swallow words
	// meant for the user.
	//
	// A failed compaction ends the task rather than continuing on the
	// uncompressed history: --auto-summarize is a constraint the user declared,
	// and the request that would follow is the one it exists to prevent. The
	// room that is left may carry the task to its end anyway — that is what this
	// rule costs — but the constraint would otherwise be broken in silence, and
	// where the provider enforces prompt + max_tokens it answers that request a
	// step later with an error naming neither the summarize nor the threshold
	// (docs/context-tracking.md).
	//
	// Nothing is retried here: the summarize request is an ordinary request, so
	// sendWithRetry has already re-sent whatever could be re-sent (IsRetryable,
	// streamBreakError) before its error arrives — and the one semantic retry a
	// textless reply gets has already happened inside summarizeContents. What arrives is permanent or
	// exhausted, and the next step's context would only be larger.
	onBeforeSend := func(contents []llm.ContentPart) ([]llm.ContentPart, error) {
		// The step's clock, captured before anything else this step does. Both
		// the echo helpers and stampParts read it, so what the adapter is shown
		// live is the value that ends up in the record.
		s.stepStartedAt = time.Now()
		// Compaction first, steering second — never the other way round. A
		// compaction replaces the history wholesale, so a steering part
		// appended before it would vanish from Contents while the adapter kept
		// the ID it was shown.
		if s.exceedsAutoSummarizeThreshold(contextTokens) {
			compacted, err := s.compactForContinuation(ctx, contents, contextTokens)
			if err != nil {
				return nil, fmt.Errorf("Auto-summarization failed: %w", err)
			}
			// The replacement is the published baseline now: the next step's
			// delta is measured from its end, not from the old history's.
			rec.replaced(compacted)
			contents = compacted
		}

		// Append the user parts this step must be sent with — the user's
		// steering, or the synthetic "Continue" a compaction's replacement needs
		// (the rule is stepUserParts'). They go last: after the tool results the
		// model just asked for, and after a compaction replacement, so they are
		// the freshest thing the model reads.
		//
		// The record takes the base here, after the compaction, so a boundary that
		// both compacted and appended keeps the compacted form — a step that never
		// finishes publishes nothing, and the failure path needs the base the
		// parts were appended to.
		rec.sentOn(contents)
		if parts, steering := s.stepUserParts(contents); len(parts) > 0 {
			if steering {
				// The batch left the queue here. Report it delivered before the
				// echo frames below, so a client tracking the queue sees it
				// settle before the words appear as content.
				s.writeSteering(protocol.SteeringDelivered, "", "")
			}
			// The whole of their admission: fit, role, number, echo, publish —
			// at the moment they enter a request, and not at the step's finish
			// (spliceUserParts covers why, and why the two must not be split).
			parts = s.spliceUserParts(parts)
			// Publish them to Contents in the same breath as that echo — the
			// same event runTaskNormal publishes a fresh prompt's parts with —
			// so a :save or :fork during this step sees what the transcript
			// already shows (see stepRecord.spliced for the baseline that move
			// makes).
			s.sendEvent(promptPartsEvent{Parts: parts})
			contents = rec.spliced(contents, parts)
		}
		return contents, nil
	}

	dw := &deltaWriter{output: s.Output}

	callbacks := llm.StreamCallbacks{
		OnTextComplete:      s.handleTextComplete,
		OnReasoningComplete: s.handleReasoningComplete,
		OnToolInputStart:    s.handleToolInputStart,
		OnToolInputComplete: s.handleToolInputComplete,
		OnToolOutput:        s.handleToolOutput,
		OnToolConfirm:       s.handleToolConfirm,
		ToolNeedsConfirm:    s.needsToolConfirm,
		OnRetry:             s.handleRetry,
		OnStepFinish:        onStepFinish,
		IDGen:               s.histIncAndGet,
	}

	// The hooks a call gets follow from kind — this is the whole of that
	// decision, in one place. A summarize gets no step-boundary events and no
	// OnBeforeSend: that hook is "rewrite the history the next request is built
	// from", which a summarize has no business doing (see promptKind).
	if kind == userPrompt {
		callbacks.OnStepStart = s.handleStepStart
		callbacks.OnStepStats = s.handleStepStats
		callbacks.OnBeforeSend = onBeforeSend
	}

	if !s.NoDelta {
		// Delta streaming enabled: register delta callbacks.
		// The complete AX frames then carry empty content (terminators only),
		// since the content was already delivered via Ax deltas — text and
		// reasoning alike, distinguished by the item's "type".
		callbacks.OnTextDelta = dw.handleTextDelta
		callbacks.OnReasoningDelta = dw.handleReasoningDelta
		callbacks.OnToolInputDelta = dw.handleToolInputDelta
		callbacks.OnToolOutputDelta = dw.handleToolOutputDelta
	}

	_, err := s.Agent().Stream(ctx, history, callbacks)

	if err != nil {
		turnFailed = true
		// The parts appended to the step that failed were sent and echoed at the
		// append, so they are already part of the adapter's conversation. Put
		// them in the returned Contents too, on the history they were sent with;
		// without this the adapter holds a window whose ID nothing resolves
		// (stepRecord.salvaged covers which history that is and why it is
		// neither fullContents nor the caller's).
		if salvaged := rec.salvaged(); salvaged != nil {
			fullContents = salvaged
		}
		return fullContents, outputTokens, err
	}

	return fullContents, outputTokens, nil
}

// ============================================================================
// processPrompt Dependencies: deltaWriter, frame helpers, callbacks
// ============================================================================

// stepUserParts returns the user parts a step's request must be sent with: the
// user's steering if any is waiting, and otherwise the synthetic "Continue" a
// compaction's replacement needs. The second return says which — true only for
// the user's steering, the case a boundary reports as a delivered batch.
// It is empty when the boundary needs neither, which is the ordinary case.
//
// The rule behind the second case is that a request must not end on an
// assistant message: every API reads that as "continue this assistant message"
// (prefill), and the model may keep writing the summary instead of resuming the
// work. A compaction's replacement ends on the assistant summary, so it needs
// the trailing user message; a boundary that did not compact ends on the tool
// results the model just asked for, and needs nothing.
func (s *Session) stepUserParts(contents []llm.ContentPart) (parts []llm.ContentPart, steering bool) {
	if p := s.takeSteering(); len(p) > 0 {
		return p, true
	}
	if len(contents) > 0 && contents[len(contents)-1].GetRole() == llm.RoleAssistant {
		return []llm.ContentPart{&llm.TextPart{Text: "Continue"}}, false
	}
	return nil, false
}

// spliceUserParts prepares the user parts being appended to the step about to
// be sent, and returns them for the caller to append to the request: it fits
// oversized attachments, gives each part the user role, numbers it, and echoes
// it to the adapter. It is the whole of their admission, and it is the same
// treatment a fresh prompt gets in runTaskNormal — fit, role, number, echo — at
// the same point in the life of the parts: before the request that carries them
// is built.
//
// Two things arrive through it: a batch of the user's steering, and the
// synthetic "Continue" a compaction's replacement needs so the request does not
// end on an assistant message (see onBeforeSend). They are the same kind of
// thing to everything downstream — a user part appended at a boundary — and
// living in one place is what keeps them from diverging.
//
// It runs at the boundary and not at the step's finish, for two reasons. The
// adapter renders windows in the order their frames arrive
// (WindowBuffer.AppendOrUpdate appends), so an echo deferred to OnStepFinish
// would land after the very answer the words steered — the prompt shown under
// the reasoning it caused. And the parts are in the request from this moment, so
// they are in the conversation from this moment: a step that then fails cannot
// take back what the model was already sent.
//
// The role is set here and not left to the caller: each provider groups the
// history by role and stamps that role onto its wire messages, so a part whose
// role is still unset goes out as an empty-role message and the API rejects the
// whole request.
func (s *Session) spliceUserParts(parts []llm.ContentPart) []llm.ContentPart {
	parts = llm.ShrinkImages(parts)
	for _, part := range parts {
		s.finalizeUserPart(part)
	}
	return parts
}

// finalizeUserPart is the single place a user part is finished and echoed: it
// assigns the role, stamps the time, resolves the video default (so the part
// carries its own fps/res and serialization never consults global state),
// numbers it, and writes the echo frame. Doing all of it here is what keeps the
// echoed shape and the stored shape from drifting apart.
func (s *Session) finalizeUserPart(part llm.ContentPart) {
	part.SetRole(llm.RoleUser)
	part.SetTime(time.Now())
	s.applyVideoDefault(part)
	id := s.histIncAndGet()
	part.SetHistoryID(id)
	if tag, body, err := contentPartJSON(part, wireID(id)); err == nil && tag != "" {
		s.writeFrame(tag, body)
	}
}

// applyVideoDefault fills an unset video's fps/res from the session default, so
// the part's serialization is a pure function of the part. Tool results nest
// their content, so it recurses.
func (s *Session) applyVideoDefault(part llm.ContentPart) {
	switch v := part.(type) {
	case *llm.VideoPart:
		if v.FPS == 0 && s.modelService != nil {
			v.FPS, v.Res = s.modelService.videoFPS, s.modelService.videoRes
		}
	case *llm.ToolOutputPart:
		for _, sub := range v.Output {
			s.applyVideoDefault(sub)
		}
	}
}

// stampParts stamps the fields a freshly assembled assistant/tool part cannot
// know about itself: its time, and (for a video) the session's current default.
//
// It must run in the goroutine that OWNS the parts, before they are handed to
// run() — that is onStepFinish, in the task goroutine (and finalizeUserPart for
// user parts, and handleToolOutput for a tool result). Stamping after the
// handoff would race the other goroutine reading the part back, and every
// reader below serialization must see a part that is already complete: a later
// serialization reads only the part and never consults live state.
func (s *Session) stampParts(parts []llm.ContentPart) {
	now := s.stepStartedAt
	if now.IsZero() {
		now = time.Now()
	}
	for _, p := range parts {
		if p.GetTime().IsZero() {
			p.SetTime(now)
		}
		s.applyVideoDefault(p)
	}
}

// deltaWriter writes streaming delta frames directly to the frame output,
// bypassing the session layer. Delta frames are ephemeral and not persisted.
type deltaWriter struct {
	output io.Writer
}

func (dw *deltaWriter) handleTextDelta(delta string, historyID uint64) error {
	return dw.handleContentDelta(protocol.TypeText, delta, historyID)
}

func (dw *deltaWriter) handleReasoningDelta(delta string, historyID uint64) error {
	return dw.handleContentDelta(protocol.TypeReasoning, delta, historyID)
}

// handleContentDelta writes an Ax (assistant content delta) frame. The content
// kind (text vs reasoning) travels in the JSON, since the tag no longer names it.
func (dw *deltaWriter) handleContentDelta(kind, text string, historyID uint64) error {
	body, err := marshalContentBody(protocol.ContentMeta{ID: wireID(historyID)}, protocol.ContentItem{Type: kind, Text: text})
	if err != nil {
		return err
	}
	return wire.WriteFrame(dw.output, wire.TagAssistantXDelta, body)
}

func (dw *deltaWriter) handleToolInputDelta(toolCallID, delta string, historyID uint64) error {
	data, err := json.Marshal(protocol.ToolInputDeltaData{
		CallID:      toolCallID,
		Delta:       delta,
		ContentMeta: protocol.ContentMeta{ID: wireID(historyID)},
	})
	if err != nil {
		return fmt.Errorf("failed to marshal tool input delta: %w", err)
	}
	return wire.WriteFrame(dw.output, wire.TagAssistantFDelta, string(data))
}

// handleToolOutputDelta writes a TagUserFDelta (Uf) frame carrying an
// ephemeral tool result preview snapshot. Display-only: frames may be
// dropped or coalesced; the authoritative result arrives via UF.
func (dw *deltaWriter) handleToolOutputDelta(toolCallID, text string, historyID uint64) error {
	data, err := json.Marshal(protocol.ToolOutputDeltaData{
		CallID:      toolCallID,
		Text:        text,
		ContentMeta: protocol.ContentMeta{ID: wireID(historyID)},
	})
	if err != nil {
		return fmt.Errorf("failed to marshal tool output delta: %w", err)
	}
	return wire.WriteFrame(dw.output, wire.TagUserFDelta, string(data))
}

// handleToolConfirm handles a tool confirmation request.
// It creates a buffered channel and either:
//   - Sends false immediately if output is broken (no map entry needed)
//   - Registers the channel in confirmChs map and sends the SM notification
//   - On SM write failure: removes the map entry and sends false
//
// The caller (agent goroutine) blocks on the returned channel until the
// user responds via :tool_confirm / :tool_decline command, which writes
// to the channel via handleToolConfirmCmd / handleToolDeclineCmd.
func (s *Session) handleToolConfirm(req llm.ToolConfirmRequest) <-chan bool {
	// Buffer 1 is sufficient: only the confirm/decline command handlers write
	// to the channel in the normal flow, and the error path below only sends
	// when the channel hasn't been consumed yet.
	ch := make(chan bool, 1)

	if s.outputBroken.Load() {
		ch <- false
		return ch
	}

	// Store in map first so the channel is findable the instant the
	// user sees the SM notification and responds.
	s.confirmMu.Lock()
	s.confirmChs[req.ID] = ch
	s.confirmMu.Unlock()

	if err := protocol.WriteSystemMsg(s.Output, protocol.ToolConfirmMsg{CallID: req.ID}); err != nil {
		s.markOutputBroken()
		// Only send false if handleConfirmCommand hasn't already
		// consumed the channel. Otherwise we'd race with its write.
		s.confirmMu.Lock()
		if existingCh, ok := s.confirmChs[req.ID]; ok {
			delete(s.confirmChs, req.ID)
			existingCh <- false
		}
		s.confirmMu.Unlock()
	}

	return ch
}

// handleToolOutput serializes tool results and writes them to the output stream.
func (s *Session) handleToolOutput(toolCallID string, contents []llm.ContentPart, err error, historyID uint64) error {
	for _, part := range contents {
		s.applyVideoDefault(part)
	}
	data, marshalErr := marshalToolOutputBody(s.wireMetaNow(historyID), toolCallID, contents, err != nil)
	if marshalErr != nil {
		return fmt.Errorf("failed to marshal tool output: %w", marshalErr)
	}
	s.writeFrame(wire.TagUserF, data)
	return nil
}

// handleTextComplete writes the complete authoritative text to the output.
// When delta streaming is enabled (default), the content is empty — the
// text was already delivered via Ax delta frames, so AX serves only as a
// terminator/flush signal. When --no-delta is set, AX carries the full text.
func (s *Session) handleTextComplete(text string, historyID uint64) error {
	content := text
	if !s.NoDelta {
		content = "" // deltas already delivered the content
	}
	return s.writeContentFrame(protocol.TypeText, content, historyID)
}

// handleReasoningComplete writes the complete authoritative reasoning to the output.
// When delta streaming is enabled (default), the content is empty — the
// reasoning was already delivered via Ax delta frames. When --no-delta is
// set, AX carries the full reasoning text.
func (s *Session) handleReasoningComplete(text string, historyID uint64) error {
	content := text
	if !s.NoDelta {
		content = "" // deltas already delivered the content
	}
	return s.writeContentFrame(protocol.TypeReasoning, content, historyID)
}

// writeContentFrame writes a complete AX content frame.
func (s *Session) writeContentFrame(kind, text string, historyID uint64) error {
	body, err := marshalContentBody(s.wireMetaNow(historyID), protocol.ContentItem{Type: kind, Text: text})
	if err != nil {
		return err
	}
	s.writeFrame(wire.TagAssistantX, body)
	return nil
}

// handleToolInputStart marshals and writes a tool call start frame.
func (s *Session) handleToolInputStart(toolCallID, name string, historyID uint64) error {
	data, err := marshalToolInputBody(s.wireMetaNow(historyID), toolCallID, name, nil)
	if err != nil {
		return fmt.Errorf("failed to marshal tool input start: %w", err)
	}
	s.writeFrame(wire.TagAssistantF, data)
	return nil
}

// handleToolInputComplete marshals and writes a tool call input frame.
func (s *Session) handleToolInputComplete(toolCallID string, input json.RawMessage, historyID uint64) error {
	data, err := marshalToolInputBody(s.wireMetaNow(historyID), toolCallID, "", input)
	if err != nil {
		return fmt.Errorf("failed to marshal tool input complete: %w", err)
	}
	s.writeFrame(wire.TagAssistantF, data)
	return nil
}

// needsToolConfirm reports whether a tool requires user confirmation.
func (s *Session) needsToolConfirm(name string) bool {
	if s.toolConfirmSet == nil {
		return false
	}
	_, ok := s.toolConfirmSet[name]
	return ok
}

// handleStepStart handles the start of a new agent step.
func (s *Session) handleStepStart(step int) error {
	s.sendEvent(stepStartEvent{Step: step})
	return nil
}

// handleStepStats forwards the just-finished step's speed metrics to the
// run() goroutine via stepStatsEvent. Called from the task goroutine
// after the step's stream completes. Steps with no output tokens carry
// TokensPerSec 0, which clears the displayed speed — nothing to measure.
func (s *Session) handleStepStats(stats llm.StepStats) error {
	s.sendEvent(stepStatsEvent{
		TokensPerSec:     stats.TokensPerSec,
		TimeToFirstToken: stats.TimeToFirstToken,
	})
	return nil
}

// handleRetry tells the adapter that a transient provider failure is being
// handled — a rate limit, an overload, a gateway blip, a dropped connection —
// so it is visible while it happens instead of looking like a hang. It is
// deliberately a notify, not an error: the task has not failed. Only when every
// retry is exhausted does the ordinary error path (writeError / the final failed
// step) report a failure.
func (s *Session) handleRetry(n llm.RetryNotice) error {
	s.writeNotifyf("%s — retrying in %s (retry %d/%d)",
		n.Reason, n.Wait.Round(time.Second), n.Retry, n.MaxRetries)
	return nil
}

// cleanIncompleteToolInputs removes orphaned tool calls from the end of
// the content slice. This happens when the user cancels mid-cycle: the model
// emitted tool calls but the agent never executed them. Only the most recent
// assistant content parts can have orphaned calls — earlier steps are already
// complete.
func cleanIncompleteToolInputs(contents []llm.ContentPart) []llm.ContentPart {
	if len(contents) == 0 {
		return contents
	}

	// Find the last assistant segment and remove ToolInputParts from it.
	// Work backwards: find where the last batch of assistant parts starts.
	lastIdx := len(contents) - 1
	for lastIdx >= 0 && contents[lastIdx].GetRole() != llm.RoleAssistant {
		lastIdx--
	}
	if lastIdx < 0 {
		return contents
	}

	// If there are ToolOutputParts after the last assistant segment,
	// the tools were actually executed — keep everything.
	for _, part := range contents[lastIdx+1:] {
		if _, ok := part.(*llm.ToolOutputPart); ok {
			return contents
		}
	}

	// Find the start of this assistant segment
	startIdx := lastIdx
	for startIdx > 0 && contents[startIdx-1].GetRole() == llm.RoleAssistant {
		startIdx--
	}

	// Check if any tool calls in this segment
	hasToolCalls := false
	for _, part := range contents[startIdx : lastIdx+1] {
		if _, ok := part.(*llm.ToolInputPart); ok {
			hasToolCalls = true
			break
		}
	}
	if !hasToolCalls {
		return contents
	}

	// Filter out ToolInputParts from the last assistant segment
	filtered := make([]llm.ContentPart, 0, len(contents))
	filtered = append(filtered, contents[:startIdx]...)
	for _, part := range contents[startIdx : lastIdx+1] {
		if _, ok := part.(*llm.ToolInputPart); !ok {
			filtered = append(filtered, part)
		}
	}
	filtered = append(filtered, contents[lastIdx+1:]...)

	return filtered
}

// ============================================================================
// Summarization (built on processPrompt)
// ============================================================================

// summarizeRetryPrompt is the user part appended to the summarize request when
// the model answered with no summary text. It names what was missing and what
// the retry must produce; the summarize prompt is still in front of it.
//
// The retry is the original request, not the failed attempt's history: that
// history would make the retry larger than the request that just fit, and a
// summarize must not become the oversized request the --auto-summarize
// threshold exists to prevent (docs/context-tracking.md → "Limits").
const summarizeRetryPrompt = "Your previous reply contained no summary text. Write the summary now: the sections requested above, in full, as text. Do not call tools."

// summarizeContents appends the summarize prompt, calls processPrompt,
// and formats the response as a summary request + the assistant's summary.
// On any failure, returns the original contents (without the prompt).
func (s *Session) summarizeContents(ctx context.Context, contents []llm.ContentPart) ([]llm.ContentPart, error) {
	// Build and append the summarize prompt. It is an instruction to the model,
	// not words the user wrote, so it is never echoed as a user tag: it is not
	// part of Contents (the callers discard it on failure and the summary
	// replaces it on success), and an ID advertised to the adapter that no
	// record holds is one :fork cannot resolve. The notify frames around the
	// summarize already say it is happening. It still takes a history ID, like
	// every finalized part.
	promptPart := &llm.TextPart{Text: summarizePrompt}
	promptPart.SetHistoryID(s.histIncAndGet())
	promptPart.SetRole(llm.RoleUser)

	// Send the conversation (with the prompt) to the LLM. The prompt is
	// appended to a copy: `contents` belongs to the caller (for a mid-task
	// summarize it is the agent loop's working history), and append into its
	// spare capacity would write past the caller's length into a backing array
	// the loop keeps appending to.
	promptContents := make([]llm.ContentPart, len(contents), len(contents)+1)
	copy(promptContents, contents)
	promptContents = append(promptContents, promptPart)
	fullContents, outputTokens, err := s.processPrompt(ctx, promptContents, summarizeCall)
	if err != nil {
		return contents, err
	}

	response := fullContents[len(contents):]
	text, ok := summaryText(response)
	if !ok {
		// The summarize is answered by the same model that drives the task, so
		// it can end without text: reasoning its way to "now write it" and
		// stopping there, or an empty completion. Ask once more — one retry,
		// not a loop, because every attempt is the whole conversation and a
		// model that refuses the text twice will refuse it a third time.
		//
		// The retry is the original request — the conversation and the summarize
		// prompt — plus a user part asking for the text. The failed attempt's
		// response is deliberately not carried: the summarize runs near the
		// window's ceiling, so putting that response back would make the retry
		// larger than the request that just fit (docs/context-tracking.md →
		// "Limits"). The cost is that the model thinks again, and a tool the
		// failed attempt ran is not carried either; the retry forbids tools,
		// which is what keeps it from running one again.
		s.writeNotify("Summarization produced no text; retrying once")
		retryPart := &llm.TextPart{Text: summarizeRetryPrompt}
		retryPart.SetHistoryID(s.histIncAndGet())
		retryPart.SetRole(llm.RoleUser)
		retryContents := make([]llm.ContentPart, len(promptContents), len(promptContents)+1)
		copy(retryContents, promptContents)
		retryContents = append(retryContents, retryPart)
		fullContents, outputTokens, err = s.processPrompt(ctx, retryContents, summarizeCall)
		if err != nil {
			return contents, err
		}
		text, ok = summaryText(fullContents[len(contents):])
	}
	if !ok {
		return contents, fmt.Errorf("summarization produced no text")
	}

	// Build the summarized conversation: the request the summary answers, as a
	// user message, followed by the summary itself.
	//
	// The user message is structural, not decoration. The summary is an assistant
	// message, so without a task in front of it the compacted history would
	// open on an assistant message — and the summary cannot move to the user
	// side instead, because the next user part (the resuming prompt, or the
	// trailing resume task) would then group into the same task as the summary
	// and bury the instruction inside it.
	//
	// Its text is the request the model actually answered. It used to read
	// "Continue", which named an instruction nobody gave — the summary answers
	// "summarize the conversation", not "continue" — and collided with the
	// resume task, which is the only other thing that word means here.
	result := make([]llm.ContentPart, 0, 2)
	requestID := s.histIncAndGet()
	result = append(result, &llm.TextPart{
		Text: "Summarize the conversation so far.",
		ContentPartMeta: llm.ContentPartMeta{
			HistoryID: requestID,
			Role:      llm.RoleUser,
		},
	})
	summaryID := s.histIncAndGet()
	result = append(result, &llm.TextPart{
		Text: text,
		ContentPartMeta: llm.ContentPartMeta{
			HistoryID: summaryID,
			Role:      llm.RoleAssistant,
		},
	})
	if outputTokens > 0 {
		s.sendEvent(setContextTokensEvent{Tokens: outputTokens})
	}
	// The compacted parts are new records like any other: stamp their message
	// time here, in the goroutine that owns them, before they are handed to
	// run() (see stampParts). Without it a reloaded summary would carry no time.
	s.stampParts(result)
	s.writeNotify("Summarized conversation")
	return result, nil
}

// summaryText returns the summary a summarize response carries, if any.
//
// It reads the response's *final step*: the parts after the last tool result.
// A tool result ends the step that asked for it, so text before it belongs to
// an earlier step, where it was a preamble to that step's tool call and not the
// answer. Promoting such a preamble to the summary would replace the
// conversation with a sentence about the work instead of the work.
//
// Within that step the text need not be the last part: block layout is the
// provider's to declare, and a provider that declares none leaves the record in
// arrival order, so an answer can precede the reasoning that produced it —
// requiring the last part to be text would discard that answer as "no text".
//
// Every non-empty text part of the final step is kept, in order: a step that
// wrote the answer in pieces (or resumed after an interleaved reasoning block)
// said all of it, and dropping the earlier pieces would silently shorten the
// summary. Whitespace-only text does not count as text — accepting it would
// replace the conversation with nothing, and the pre-summarize backup would be
// the only way back.
func summaryText(parts []llm.ContentPart) (string, bool) {
	start := 0
	for i, p := range parts {
		if _, ok := p.(*llm.ToolOutputPart); ok {
			start = i + 1
		}
	}
	texts := make([]string, 0, len(parts))
	for _, p := range parts[start:] {
		tp, ok := p.(*llm.TextPart)
		if !ok || tp.Role != llm.RoleAssistant || strings.TrimSpace(tp.Text) == "" {
			continue
		}
		texts = append(texts, tp.Text)
	}
	if len(texts) == 0 {
		return "", false
	}
	return strings.Join(texts, "\n"), true
}

// shouldAutoSummarize returns true when auto-summarization is enabled and
// the current context tokens exceed s.AutoSummarize of the configured limit.
func (s *Session) shouldAutoSummarize() bool {
	return s.exceedsAutoSummarizeThreshold(s.ContextTokens)
}

// exceedsAutoSummarizeThreshold reports whether tokens is at or above the
// configured --auto-summarize percentage of the context limit. It is the one
// predicate behind both trigger points: the start of a task (shouldAutoSummarize)
// and each step of a running one (processPrompt's OnBeforeSend). 0 disables it.
func (s *Session) exceedsAutoSummarizeThreshold(tokens int64) bool {
	limit := s.ContextLimit
	return s.AutoSummarize > 0 && limit > 0 && tokens > 0 &&
		tokens >= limit*int64(s.AutoSummarize)/100
}

// summarizeBackup saves a timestamped backup of the current session contents
// before summarization. Silently skips if no session file is configured.
// Failures are reported as system errors — without the backup the original
// conversation is unrecoverable after summarization.
//
// contextTokens is the size to record in the backup's metadata. It is passed in
// rather than read from s.ContextTokens: a mid-task caller runs on the task
// goroutine, which does not own that field (run() writes it concurrently). The
// task-start callers pass their own snapshot.
//
// The timestamp carries sub-second precision because a single task can now
// summarize more than once; a seconds-only stamp would make the second backup
// silently overwrite the first.
func (s *Session) summarizeBackup(contents []llm.ContentPart, contextTokens int64) {
	if s.SessionFile == "" {
		return
	}
	ext := filepath.Ext(s.SessionFile)
	base := strings.TrimSuffix(s.SessionFile, ext)
	backupPath := fmt.Sprintf("%s-%s%s", base, time.Now().Format("20060102150405.000000000"), ext)
	if err := s.saveContentToFileWithContext(backupPath, contents, contextTokens); err != nil {
		s.writeErrorf("Failed to create pre-summarize backup: %v", err)
	} else {
		s.writeNotifyf("Pre-summarize backup saved to %s", backupPath)
	}
}

// doAutoSummarize logs progress notifications and triggers summarization.
// Called synchronously from runTaskNormal and runTaskContinue when the context
// is near the token limit — it must complete before the user's task is
// processed, and the task does not proceed if it fails.
//
// On success the conversation has been replaced by the summary. On failure it
// returns the error and the caller ends the task rather than sending a request
// the --auto-summarize threshold exists to prevent (see onBeforeSend for the
// same rule mid-task). The uncompressed history is returned unchanged and stays
// the session's, so nothing is lost.
func (s *Session) doAutoSummarize(ctx context.Context, contents []llm.ContentPart) ([]llm.ContentPart, error) {
	limit := s.ContextLimit
	usage := float64(s.ContextTokens) * 100 / float64(limit)
	s.writeNotifyf("Context usage at %d/%d tokens (%.0f%%). Auto-summarizing...",
		s.ContextTokens, limit, usage)

	// Task-start: no step of this task has run, so s.ContextTokens is ours to
	// read (run() wrote it before this goroutine started, and will not write it
	// again until this task sends its first event).
	s.summarizeBackup(contents, s.ContextTokens)
	s.writeNotify("Summarizing conversation...")

	result, err := s.summarizeContents(ctx, contents)
	if err != nil {
		return contents, err
	}

	// Success: the conversation was replaced by the summary. Publish the
	// replacement so :save during the task sees the compressed form. The
	// clone transfers ownership: run() keeps this slice, while the task
	// goroutine keeps appending to its own copy below.
	s.sendEvent(contentsReplacedEvent{Contents: cloneParts(result)})
	return result, nil
}

// compactForContinuation summarizes the conversation between two agent steps
// and returns the compacted history to continue from.
//
// It is the mid-task counterpart of doAutoSummarize. The history it is given
// ends with a tool result the model asked for and has not yet acted on; the
// summarize prompt is appended after it, so the model reads that result — and
// the whole task — and folds them into the summary. The compacted history
// replaces the conversation and the loop continues from it, which is why no
// tool result is discarded: it was folded into the summary by the model that
// saw it.
//
// contextTokens is the caller's own view of the context size (the task-local
// count processPrompt keeps). It is passed, not read from s.ContextTokens,
// because this runs on the task goroutine while run() is writing that field.
//
// On failure it returns the error, which ends the task: the caller must not
// continue on the uncompressed history (see onBeforeSend for why).
func (s *Session) compactForContinuation(ctx context.Context, contents []llm.ContentPart, contextTokens int64) ([]llm.ContentPart, error) {
	s.writeNotify("Context limit reached. Summarizing to continue...")
	s.summarizeBackup(contents, contextTokens)

	result, err := s.summarizeContents(ctx, contents)
	if err != nil {
		return nil, err
	}
	// The replacement is [summary request (user), summary (assistant)], and it
	// ends on the assistant. The trailing user message that fixes that is added by
	// the caller — see stepUserParts, which is where the rule lives because that
	// is the one place that knows whether the user's own words are arriving to
	// end the request instead.
	//
	// Publish the replacement so :save during the task saves the compacted form.
	s.sendEvent(contentsReplacedEvent{Contents: cloneParts(result)})
	return result, nil
}

// ============================================================================
// Task goroutines — runTaskNormal, runTaskContinue, runTaskSummarize
//
// These three functions are the entry points for task goroutines. Each is
// started right after a successful beginTask: handleInputMsg for a prompt and
// for :continue/:summarize, syncState for the prompt held for MCP readiness.
// They all call processPrompt (which blocks on the LLM) and therefore run in
// their own goroutine to keep the main event loop responsive.
//
// Relationships:
//
//   runTaskNormal     — normal prompt. Appends user parts to history, calls
//                       processPrompt. If the context was near the token limit,
//                       it synchronously runs doAutoSummarize first to free
//                       space; a task that grows past the limit mid-flight is
//                       compacted between steps by processPrompt's OnBeforeSend.
//
//   runTaskContinue   — retry last prompt. First runs the same task-start
//                       auto-summarize as runTaskNormal, then: if the last
//                       response was assistant (canceled mid-stream), appends
//                       "Continue" and resends; otherwise (user/tool message),
//                       resends the history as-is.
//
// Cancellation: a canceled task simply ends with whatever completed — the
// salvaged [tool_use, tool_result] pairs, or the previous history if no
// step finished. No "Canceled" marker is inserted: the tail shape (user /
// assistant / tool) is already exactly what :continue expects, and a fake
// assistant utterance would pollute the history and the UI.
//
//   runTaskSummarize  — :summarize command. Calls summarizeContents which appends
//                       the summarize prompt, calls processPrompt, then replaces
//                       the conversation with a summary.
//
// The key difference: doAutoSummarize (from runTaskNormal) and
// compactForContinuation (from processPrompt's OnBeforeSend) both run
// synchronously inside the task that needs the space — the first before a new
// prompt is processed, the second between two of its steps — because each must
// free token space before the next request is sent. When one fails the task
// ends rather than sending that request anyway. runTaskSummarize runs as an
// independent task goroutine because :summarize is an explicit user command,
// not a precondition for another operation.
// ============================================================================

// runTaskNormal executes a normal prompt in its own goroutine.
func (s *Session) runTaskNormal(ctx context.Context, parts []llm.ContentPart) {
	// This task owns the step clock: clear what the previous task left, so a
	// task that never sets it (a summarize) cannot timestamp frames with a
	// stale value.
	s.stepStartedAt = time.Time{}
	contents := make([]llm.ContentPart, len(s.Contents))
	copy(contents, s.Contents)

	defer func() {
		s.taskResultCh <- contents
	}()

	if s.shouldAutoSummarize() {
		compacted, err := s.doAutoSummarize(ctx, contents)
		if err != nil {
			s.writeErrorf("Auto-summarization failed: %v", err)
			// The task never started, so nothing was spliced: whatever the user
			// steered in while this ran is still queued, and goes with the task
			// (session_steering.go).
			s.discardQueuedSteering()
			return
		}
		contents = compacted
	}

	// Shrink oversized images before they are numbered and echoed: the adapter,
	// the stored session and the model must all see the same part, and the
	// shrink should be paid here once rather than on every send. This is the
	// ingest form — it never substitutes a note, so an attachment this client
	// cannot decode stays in the session as itself. See internal/llm/media_fit.go.
	parts = llm.ShrinkImages(parts)

	// Finalize (role / id / time / video default), echo, then keep in contents.
	for _, part := range parts {
		s.finalizeUserPart(part)
		contents = append(contents, part)
	}
	// Parts are finalized (IDs/roles assigned) — publish them so :save
	// during the task includes the just-submitted prompt.
	s.sendEvent(promptPartsEvent{Parts: parts})

	fullContents, _, err := s.processPrompt(ctx, contents, userPrompt)
	if err != nil {
		s.writeError(err.Error())
	}
	if len(fullContents) > 0 {
		contents = fullContents
	}
}

// runTaskContinue constructs a "Continue" user prompt and processes it as
// a normal user message.  If the last message was from the assistant,
// a "Continue" text is appended; otherwise the last prompt is resent.
func (s *Session) runTaskContinue(ctx context.Context) {
	s.stepStartedAt = time.Time{}
	contents := make([]llm.ContentPart, len(s.Contents))
	copy(contents, s.Contents)

	defer func() {
		s.taskResultCh <- contents
	}()

	if len(contents) == 0 {
		s.writeError("No messages to resend")
		return
	}

	// Same task-start check as runTaskNormal: a retried task is still a task,
	// and its history can be over the threshold. Done before "Continue" is
	// appended, so the summarize prompt lands on the completed conversation
	// rather than after a fresh user part.
	if s.shouldAutoSummarize() {
		compacted, err := s.doAutoSummarize(ctx, contents)
		if err != nil {
			s.writeErrorf("Auto-summarization failed: %v", err)
			// The task never started, so nothing was spliced — see runTaskNormal.
			s.discardQueuedSteering()
			return
		}
		contents = compacted
	}

	lastPart := contents[len(contents)-1]
	if lastPart.GetRole() == llm.RoleAssistant {
		// Assistant message — LLM was interrupted mid-response.
		// Append "Continue" as a user message to tell it to pick up where it left off.
		part := &llm.TextPart{Text: "Continue"}
		s.finalizeUserPart(part)
		contents = append(contents, part)
		// Finalized — publish so :save during the task sees it.
		s.sendEvent(promptPartsEvent{Parts: []llm.ContentPart{part}})
	}

	fullContents, _, err := s.processPrompt(ctx, contents, userPrompt)
	if err != nil {
		s.writeError(err.Error())
	}
	if len(fullContents) > 0 {
		contents = fullContents
	}
}

// runTaskSummarize constructs a summarization prompt and processes it.
// After the LLM responds, the conversation is replaced with a summary.
func (s *Session) runTaskSummarize(ctx context.Context) {
	s.stepStartedAt = time.Time{}
	contents := make([]llm.ContentPart, len(s.Contents))
	copy(contents, s.Contents)

	defer func() {
		s.taskResultCh <- contents
	}()

	// Task-start: as in doAutoSummarize, s.ContextTokens is ours to read here.
	s.summarizeBackup(contents, s.ContextTokens)
	s.writeNotify("Summarizing conversation...")

	contents, err := s.summarizeContents(ctx, contents)
	if err != nil {
		s.writeError(err.Error())
		return
	}
}
