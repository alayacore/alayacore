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
	"strconv"
	"strings"
	"time"

	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/protocol"
	"github.com/alayacore/alayacore/internal/tlv"
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
//   - userTurn is the user's own turn. Its progress belongs on the adapter's
//     status bar, its history is the conversation's, and it is the only kind
//     that may take the user's steering.
//   - summarizeCall is a summarize — :summarize, the auto-summarize at the
//     start of a turn, or the one a full context triggers between steps. It
//     drives the same loop (it can even call tools), but its result never
//     becomes the conversation: it replaces the history or is thrown away. So
//     nothing about it is published, and it must keep its hands off the user's
//     words.
//
// Named rather than a bare bool because the value is read at the call site,
// where "false" said only "do not publish steps" — one of the three things it
// actually means.
type promptKind int

const (
	userTurn promptKind = iota
	summarizeCall
)

// processPrompt sends the conversation history to the LLM via the agent's
// Stream method, using registered callbacks for streaming output, tool
// execution, and step tracking. Returns the full response contents and
// total output token usage.
//
// kind decides what the call is given, and everything it is given follows from
// that one answer (see promptKind): a userTurn publishes its step progress
// (stepStartEvent/stepStatsEvent for the status bar, and a stepFinishEvent
// delta per step so :save/:fork see in-progress task content) and may rewrite
// the history it is about to send — that is where a mid-task compaction and the
// user's steering are spliced in. A summarizeCall gets none of that: publishing
// its steps would move the displayed step and blank the speed mid-turn, its
// internals (prompt, response, and any tool calls of its own) must not
// transiently pollute Contents, and it must not compact again or take steering
// into a history that will be discarded.
func (s *Session) processPrompt(ctx context.Context, history []llm.ContentPart, kind promptKind) ([]llm.ContentPart, int64, error) {
	var fullContents []llm.ContentPart
	var outputTokens int64

	// Baseline for per-step deltas: everything at or before this offset
	// was already published (prompt parts via promptPartsEvent), so each
	// step only publishes the parts after it. cleanIncompleteToolInputs
	// strips only orphaned calls from the current step's tail (never
	// parts before prevLen), so len(fullContents) >= prevLen always holds.
	prevLen := len(history)

	// pendingInjected is the batch of steering parts spliced into the step
	// about to be sent, held here until that step's finish. The parts are
	// numbered, echoed and published to Contents at the splice itself (see
	// onBeforeSend): they are what the request already carries, so they are part
	// of the conversation from that moment and not from the step's finish. This
	// field exists for the one case where the step never finishes — the words
	// were sent and shown, so the returned Contents must carry them too (an ID
	// the adapter was shown that Contents does not hold is a window :fork cannot
	// resolve, and a failed step cannot un-send them).
	//
	// stepBase is the history that step was sent on, without the splice. It is
	// the base the failure path needs, because the batch has to be added to the
	// history *it* was appended to — and that is neither fullContents (empty
	// when no step has finished) nor the caller's history (stale once a
	// compaction has replaced it at this very boundary, which is what the
	// assignment in onBeforeSend captures).
	//
	// turnFailed records that this call — the user's turn — did not land. The
	// deferred discard below reads it together with ctx: a turn that failed and
	// a turn that was canceled are the same event as far as the user's steering
	// is concerned.
	var pendingInjected []llm.ContentPart
	var stepBase []llm.ContentPart
	var turnFailed bool
	defer func() {
		// A turn that did not land — it failed, or it was canceled — takes the
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

		// The step landed, so the words it carried are in fullContents through
		// this same assignment — they were part of what the model was sent.
		// Clearing the batch keeps a later failure from adding them a second
		// time.
		pendingInjected = nil

		if n := usage.InputTokens + usage.OutputTokens + usage.CacheReadTokens + usage.CacheCreationTokens; n > 0 {
			contextTokens = n
		}
		ev := stepFinishEvent{
			InputTokens:         usage.InputTokens,
			OutputTokens:        usage.OutputTokens,
			CacheReadTokens:     usage.CacheReadTokens,
			CacheCreationTokens: usage.CacheCreationTokens,
		}
		if kind == userTurn {
			// NewParts is a view into the agent's accumulation; run()
			// copies the pointers out immediately and never retains the
			// view (see stepFinishEvent docs).
			//
			// cleanIncompleteToolInputs is documented to strip only from the
			// current step's tail, which keeps len(fullContents) >= prevLen.
			// Slicing on that faith turned a broken assumption into a panic
			// ("slice bounds out of range") inside the task goroutine, so the
			// bound is checked. When it does not hold the suffix is no longer
			// well-defined and nothing is published for this step; run() still
			// receives the complete history from taskResultCh at task end.
			if prevLen <= len(fullContents) {
				ev.NewParts = fullContents[prevLen:]
			}
		}
		s.sendEvent(ev)
		prevLen = len(fullContents)
		outputTokens += usage.OutputTokens
		return nil
	}

	// onBeforeSend runs at the top of every step of a userTurn, before its
	// request is sent. If the context has grown past the --auto-summarize
	// threshold mid-task, it summarizes the conversation and hands the compacted
	// history back for the rest of the turn; and it is where the user's steering
	// is spliced in. Both are rewrites of the history the next request is built
	// from, which is why this hook — like the step-boundary events — is
	// installed for a userTurn alone (see the callbacks below): a summarizeCall
	// never gets it, so it can neither re-trigger compaction nor swallow words
	// meant for the user.
	//
	// A failed compaction ends the turn rather than continuing on the
	// uncompressed history: --auto-summarize is a constraint the user declared,
	// and the request that would follow is the one it exists to prevent. The
	// room that is left may carry the turn to its end anyway — that is what this
	// rule costs — but the constraint would otherwise be broken in silence, and
	// where the provider enforces prompt + max_tokens it answers that request a
	// step later with an error naming neither the summarize nor the threshold
	// (docs/context-tracking.md).
	//
	// Nothing is retried here: the summarize request is an ordinary request, so
	// sendWithRetry has already retried it (llm.IsRetryable) before its error
	// arrives. What arrives is permanent or exhausted, and the next step's
	// context would only be larger.
	onBeforeSend := func(contents []llm.ContentPart) ([]llm.ContentPart, error) {
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
			prevLen = len(compacted)
			contents = compacted
		}

		// Splice in any steering that has arrived since the last step. It goes
		// last — after the tool results the model just asked for, and after a
		// compaction replacement — so it is the freshest thing the model reads
		// and nothing downstream can fold it away.
		//
		// The history this step is sent on, captured before the splice. A step
		// that never finishes publishes nothing, and this is the base the words
		// were appended to; the failure path below adds them back to it. It is
		// taken here, after the compaction, precisely so that a boundary that
		// both compacted and spliced keeps the compacted form.
		stepBase = contents
		if parts := s.takeSteering(); len(parts) > 0 {
			// The whole of the words' admission happens here, at the moment
			// they enter a request — fit, role, number, echo — exactly as a
			// fresh prompt gets it in runTaskNormal. Not at the step's finish:
			// the adapter draws windows in the order their frames arrive, so an
			// echo deferred to OnStepFinish lands after the very answer it
			// steered. The words are sent to the model here, so they are in the
			// conversation from here.
			parts = s.spliceSteering(parts)
			// Publish them to Contents in the same breath as that echo — the
			// same event runTaskNormal publishes a fresh prompt's parts with —
			// so a :save or :fork during this step sees the words the transcript
			// already shows. prevLen moves past them, which leaves the step's
			// delta carrying only the step's own output; without that, they
			// would enter Contents twice.
			s.sendEvent(promptPartsEvent{Parts: parts})
			next := make([]llm.ContentPart, len(contents), len(contents)+len(parts))
			copy(next, contents)
			next = append(next, parts...)
			prevLen += len(parts)
			contents = next
			pendingInjected = parts
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
	if kind == userTurn {
		callbacks.OnStepStart = s.handleStepStart
		callbacks.OnStepStats = s.handleStepStats
		callbacks.OnBeforeSend = onBeforeSend
	}

	if !s.NoDelta {
		// Delta streaming enabled: register delta callbacks.
		// AT/AR complete frames carry empty content (terminators only)
		// since the content was already delivered via deltas.
		callbacks.OnTextDelta = dw.handleTextDelta
		callbacks.OnReasoningDelta = dw.handleReasoningDelta
		callbacks.OnToolInputDelta = dw.handleToolInputDelta
		callbacks.OnToolOutputDelta = dw.handleToolOutputDelta
	}

	_, err := s.Agent().Stream(ctx, history, callbacks)

	if err != nil {
		turnFailed = true
		// The words spliced into the step that failed were sent and echoed at
		// the splice, so they are already part of the adapter's conversation.
		// Put them in the returned Contents too, on the history they were sent
		// with — stepBase, not fullContents: fullContents is empty when no step
		// has finished (this step is the first), and stale when a compaction
		// replaced the history at this very boundary. Without this the adapter
		// holds a window whose ID nothing resolves.
		if len(pendingInjected) > 0 {
			merged := make([]llm.ContentPart, 0, len(stepBase)+len(pendingInjected))
			merged = append(merged, stepBase...)
			merged = append(merged, pendingInjected...)
			fullContents = merged
		}
		return fullContents, outputTokens, err
	}

	return fullContents, outputTokens, nil
}

// ============================================================================
// processPrompt Dependencies: deltaWriter, TLV helpers, callbacks
// ============================================================================

// spliceSteering prepares the parts of one steering batch for the step they are
// being spliced into, and returns them for the caller to append to the request:
// it fits oversized attachments, gives each part the user role, numbers it, and
// echoes it to the adapter. It is the whole of a steering part's admission, and
// it is the same treatment a fresh prompt gets in runTaskNormal — fit, role,
// number, echo — at the same point in the life of the part: before the request
// that carries it is built.
//
// It runs at the SPLICE and not at the step's finish, for two reasons. The
// adapter renders windows in the order their frames arrive
// (WindowBuffer.AppendOrUpdate appends), so an echo deferred to OnStepFinish
// would land after the very answer the words steered — the prompt shown under
// the reasoning it caused. And the words are in the request from this moment, so
// they are in the conversation from this moment: the step's finish only carries
// them into Contents (by riding the step's delta), and a step that then fails
// cannot take back what the model was already sent.
//
// The role is set here and not left to the caller: each provider groups the
// history by role and stamps that role onto its wire messages, so a part whose
// role is still unset goes out as an empty-role message and the API rejects the
// whole request.
func (s *Session) spliceSteering(parts []llm.ContentPart) []llm.ContentPart {
	parts = llm.ShrinkImages(parts)
	for _, part := range parts {
		part.SetRole(llm.RoleUser)
		id := s.histIncAndGet()
		part.SetHistoryID(id)
		if tag, val, err := contentPartToTLV(part); err == nil && tag != "" {
			s.writeTLV(tag, tlv.WrapID(strconv.FormatUint(id, 10), val))
		}
	}
	return parts
}

// deltaWriter writes streaming delta frames directly to the TLV output,
// bypassing the session layer. Delta frames are ephemeral and not persisted.
type deltaWriter struct {
	output io.Writer
}

func (dw *deltaWriter) handleTextDelta(delta string, historyID uint64) error {
	return tlv.WriteTLV(dw.output, tlv.TagAssistantTDelta, tlv.WrapID(strconv.FormatUint(historyID, 10), delta))
}

func (dw *deltaWriter) handleReasoningDelta(delta string, historyID uint64) error {
	return tlv.WriteTLV(dw.output, tlv.TagAssistantRDelta, tlv.WrapID(strconv.FormatUint(historyID, 10), delta))
}

func (dw *deltaWriter) handleToolInputDelta(toolCallID, delta string, historyID uint64) error {
	data, err := json.Marshal(protocol.ToolInputDeltaData{ID: toolCallID, Delta: delta})
	if err != nil {
		return fmt.Errorf("failed to marshal tool input delta: %w", err)
	}
	return tlv.WriteTLV(dw.output, tlv.TagAssistantFDelta, tlv.WrapID(strconv.FormatUint(historyID, 10), string(data)))
}

// handleToolOutputDelta writes a TagUserFDelta (Uf) frame carrying an
// ephemeral tool result preview snapshot. Display-only: frames may be
// dropped or coalesced; the authoritative result arrives via UF.
func (dw *deltaWriter) handleToolOutputDelta(toolCallID, text string, historyID uint64) error {
	data, err := json.Marshal(protocol.ToolOutputDeltaData{ID: toolCallID, Text: text})
	if err != nil {
		return fmt.Errorf("failed to marshal tool output delta: %w", err)
	}
	return tlv.WriteTLV(dw.output, tlv.TagUserFDelta, tlv.WrapID(strconv.FormatUint(historyID, 10), string(data)))
}

// writeTLVWithID formats the historyID and writes a TLV entry to the output stream.
func (s *Session) writeTLVWithID(tag string, historyID uint64, data string) {
	id := strconv.FormatUint(historyID, 10)
	s.writeTLV(tag, tlv.WrapID(id, data))
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

	if err := protocol.WriteSystemMsg(s.Output, protocol.ToolConfirmMsg{ID: req.ID}); err != nil {
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
	data, marshalErr := marshalToolOutputData(toolCallID, contents, err != nil)
	if marshalErr != nil {
		return fmt.Errorf("failed to marshal tool output: %w", marshalErr)
	}
	s.writeTLVWithID(tlv.TagUserF, historyID, string(data))
	return nil
}

// handleTextComplete writes the complete authoritative text to the output.
// When delta streaming is enabled (default), the content is empty — the
// text was already delivered via At delta frames, so AT serves only as a
// terminator/flush signal. When --no-delta is set, AT carries the full text.
func (s *Session) handleTextComplete(text string, historyID uint64) error {
	content := text
	if !s.NoDelta {
		content = "" // deltas already delivered the content
	}
	s.writeTLVWithID(tlv.TagAssistantT, historyID, content)
	return nil
}

// handleReasoningComplete writes the complete authoritative reasoning to the output.
// When delta streaming is enabled (default), the content is empty — the
// reasoning was already delivered via Ar delta frames. When --no-delta is
// set, AR carries the full reasoning text.
func (s *Session) handleReasoningComplete(text string, historyID uint64) error {
	content := text
	if !s.NoDelta {
		content = "" // deltas already delivered the content
	}
	s.writeTLVWithID(tlv.TagAssistantR, historyID, content)
	return nil
}

// handleToolInputStart marshals and writes a tool call start frame.
func (s *Session) handleToolInputStart(toolCallID, name string, historyID uint64) error {
	data, err := marshalToolInputData(toolCallID, name, nil)
	if err != nil {
		return fmt.Errorf("failed to marshal tool input start: %w", err)
	}
	s.writeTLVWithID(tlv.TagAssistantF, historyID, string(data))
	return nil
}

// handleToolInputComplete marshals and writes a tool call input frame.
func (s *Session) handleToolInputComplete(toolCallID string, input json.RawMessage, historyID uint64) error {
	data, err := marshalToolInputData(toolCallID, "", input)
	if err != nil {
		return fmt.Errorf("failed to marshal tool input complete: %w", err)
	}
	s.writeTLVWithID(tlv.TagAssistantF, historyID, string(data))
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
// handled — a rate limit, an overload, a gateway blip — so it is visible while
// it happens instead of looking like a hang. It is deliberately a notify, not
// an error: the turn has not failed. Only when every retry is exhausted does
// the ordinary error path (writeError / the final failed step) report a
// failure.
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

// summarizeContents appends the summarize prompt, calls processPrompt,
// and formats the response as a summary request + the assistant's summary.
// On any failure, returns the original contents (without the prompt).
func (s *Session) summarizeContents(ctx context.Context, contents []llm.ContentPart) ([]llm.ContentPart, error) {
	// Build and append the summarize prompt. It is an instruction to the model,
	// not a turn the user took, so it is never echoed as a user tag: it is not
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

	// The LLM response must end with assistant text. If it's empty, a tool
	// call, reasoning, or empty text, summarization didn't produce a valid
	// result — keep the original history.
	response := fullContents[len(contents):]
	if len(response) == 0 {
		return contents, fmt.Errorf("summarization produced no content")
	}
	tp, ok := response[len(response)-1].(*llm.TextPart)
	if !ok || tp.Role != llm.RoleAssistant || tp.Text == "" {
		return contents, fmt.Errorf("summarization produced no text")
	}

	// Build the summarized conversation: the request the summary answers, as a
	// user turn, followed by the summary itself.
	//
	// The user turn is structural, not decoration. The summary is an assistant
	// message, so without a turn in front of it the compacted history would
	// open on an assistant message — and the summary cannot move to the user
	// side instead, because the next user part (the resuming prompt, or the
	// trailing resume turn) would then group into the same turn as the summary
	// and bury the instruction inside it.
	//
	// Its text is the request the model actually answered. It used to read
	// "Continue", which named an instruction nobody gave — the summary answers
	// "summarize the conversation", not "continue" — and collided with the
	// resume turn, which is the only other thing that word means here.
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
		Text: tp.Text,
		ContentPartMeta: llm.ContentPartMeta{
			HistoryID: summaryID,
			Role:      llm.RoleAssistant,
		},
	})
	if outputTokens > 0 {
		s.sendEvent(setContextTokensEvent{Tokens: outputTokens})
	}
	s.writeNotify("Summarized conversation")
	return result, nil
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
// is near the token limit — it must complete before the user's turn is
// processed, and the turn does not proceed if it fails.
//
// On success the conversation has been replaced by the summary. On failure it
// returns the error and the caller ends the turn rather than sending a request
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
// the whole turn — and folds them into the summary. The compacted history
// replaces the conversation and the loop continues from it, which is why no
// tool result is discarded: it was folded into the summary by the model that
// saw it.
//
// contextTokens is the caller's own view of the context size (the task-local
// count processPrompt keeps). It is passed, not read from s.ContextTokens,
// because this runs on the task goroutine while run() is writing that field.
//
// On failure it returns the error, which ends the turn: the caller must not
// continue on the uncompressed history (see onBeforeSend for why).
func (s *Session) compactForContinuation(ctx context.Context, contents []llm.ContentPart, contextTokens int64) ([]llm.ContentPart, error) {
	s.writeNotify("Context limit reached. Summarizing to continue...")
	s.summarizeBackup(contents, contextTokens)

	result, err := s.summarizeContents(ctx, contents)
	if err != nil {
		return nil, err
	}
	// End the replacement on a user turn. summarizeContents returns
	// [summary request (user), summary (assistant)]; sending that as-is would be
	// the one request in the session that ends on an assistant message, which
	// every API reads as "continue this assistant turn" (prefill) — the model
	// may keep writing the summary instead of resuming the work. A trailing
	// "Continue", the resume word the other call sites use, makes it an ordinary
	// "respond to the user" turn, exactly as runTaskContinue does.
	continuePart := &llm.TextPart{Text: "Continue"}
	id := s.histIncAndGet()
	continuePart.SetHistoryID(id)
	continuePart.SetRole(llm.RoleUser)
	result = append(result, continuePart)

	// Echo it, as runTaskContinue echoes its "Continue": the part is part of the
	// session's Contents (published just below), so the adapter must be shown it
	// too — otherwise it would appear only after a session reload, and the
	// reloaded conversation would differ from the live one.
	if tag, val, err := contentPartToTLV(continuePart); err == nil && tag != "" {
		s.writeTLV(tag, tlv.WrapID(strconv.FormatUint(id, 10), val))
	}

	// Publish the replacement so :save during the task saves the compacted form
	// (the adapter was already told about the trailing "Continue" above).
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
//                       space; a turn that grows past the limit mid-flight is
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
// synchronously inside the turn that needs the space — the first before a new
// prompt is processed, the second between two of its steps — because each must
// free token space before the next request is sent. When one fails the turn
// ends rather than sending that request anyway. runTaskSummarize runs as an
// independent task goroutine because :summarize is an explicit user command,
// not a precondition for another operation.
// ============================================================================

// runTaskNormal executes a normal prompt in its own goroutine.
func (s *Session) runTaskNormal(ctx context.Context, parts []llm.ContentPart) {
	contents := make([]llm.ContentPart, len(s.Contents))
	copy(contents, s.Contents)

	defer func() {
		s.taskResultCh <- contents
	}()

	if s.shouldAutoSummarize() {
		compacted, err := s.doAutoSummarize(ctx, contents)
		if err != nil {
			s.writeErrorf("Auto-summarization failed: %v", err)
			// The turn never started, so nothing was spliced: whatever the user
			// steered in while this ran is still queued, and goes with the turn
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

	// Assign history IDs, append to contents, and echo to output.
	for _, part := range parts {
		id := s.histIncAndGet()
		part.SetHistoryID(id)
		part.SetRole(llm.RoleUser)
		contents = append(contents, part)
		if tag, val, err := contentPartToTLV(part); err == nil && tag != "" {
			s.writeTLV(tag, tlv.WrapID(strconv.FormatUint(id, 10), val))
		}
	}
	// Parts are finalized (IDs/roles assigned) — publish them so :save
	// during the task includes the just-submitted prompt.
	s.sendEvent(promptPartsEvent{Parts: parts})

	fullContents, _, err := s.processPrompt(ctx, contents, userTurn)
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
	contents := make([]llm.ContentPart, len(s.Contents))
	copy(contents, s.Contents)

	defer func() {
		s.taskResultCh <- contents
	}()

	if len(contents) == 0 {
		s.writeError("No messages to resend")
		return
	}

	// Same task-start check as runTaskNormal: a retried turn is still a turn,
	// and its history can be over the threshold. Done before "Continue" is
	// appended, so the summarize prompt lands on the completed conversation
	// rather than after a fresh user part.
	if s.shouldAutoSummarize() {
		compacted, err := s.doAutoSummarize(ctx, contents)
		if err != nil {
			s.writeErrorf("Auto-summarization failed: %v", err)
			// The turn never started, so nothing was spliced — see runTaskNormal.
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
		id := s.histIncAndGet()
		part.SetHistoryID(id)
		part.SetRole(llm.RoleUser)
		contents = append(contents, part)
		if tag, val, err := contentPartToTLV(part); err == nil && tag != "" {
			s.writeTLV(tag, tlv.WrapID(strconv.FormatUint(id, 10), val))
		}
		// Finalized — publish so :save during the task sees it.
		s.sendEvent(promptPartsEvent{Parts: []llm.ContentPart{part}})
	}

	fullContents, _, err := s.processPrompt(ctx, contents, userTurn)
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
