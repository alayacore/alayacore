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

// processPrompt sends the conversation history to the LLM via the agent's
// Stream method, using registered callbacks for streaming output, tool
// execution, and step tracking. Returns the full response contents and
// total output token usage.
//
// publishSteps controls per-step publication: when true, each
// OnStepFinish delta (the new parts produced since the previous step) is
// sent to the run() goroutine via stepFinishEvent so :save/:fork can see
// in-progress task content. Summarization calls pass false — summarize is
// a conversation-replacement operation whose internals (prompt + response)
// must not transiently pollute Contents; the replacement itself is
// published separately (contentsReplacedEvent / taskResultCh).
func (s *Session) processPrompt(ctx context.Context, history []llm.ContentPart, publishSteps bool) ([]llm.ContentPart, int64, error) {
	var fullContents []llm.ContentPart
	var outputTokens int64

	// Baseline for per-step deltas: everything at or before this offset
	// was already published (prompt parts via promptPartsEvent), so each
	// step only publishes the parts after it. cleanIncompleteToolInputs
	// strips only orphaned calls from the current step's tail (never
	// parts before prevLen), so len(fullContents) >= prevLen always holds.
	prevLen := len(history)

	onStepFinish := func(contents []llm.ContentPart, usage llm.Usage) error {
		fullContents = cleanIncompleteToolInputs(contents)
		ev := stepFinishEvent{
			InputTokens:         usage.InputTokens,
			OutputTokens:        usage.OutputTokens,
			CacheReadTokens:     usage.CacheReadTokens,
			CacheCreationTokens: usage.CacheCreationTokens,
		}
		if publishSteps {
			// NewParts is a view into the agent's accumulation; run()
			// copies the pointers out immediately and never retains the
			// view (see stepFinishEvent docs).
			ev.NewParts = fullContents[prevLen:]
		}
		s.sendEvent(ev)
		prevLen = len(fullContents)
		outputTokens += usage.OutputTokens
		return nil
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
		OnStepStart:         s.handleStepStart,
		OnStepFinish:        onStepFinish,
		OnStepStats:         s.handleStepStats,
		IDGen:               s.histIncAndGet,
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
		return fullContents, outputTokens, err
	}

	return fullContents, outputTokens, nil
}

// ============================================================================
// processPrompt Dependencies: deltaWriter, TLV helpers, callbacks
// ============================================================================

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
// and formats the response as a "Continue" + summary conversation.
// On any failure, returns the original contents (without the prompt).
func (s *Session) summarizeContents(ctx context.Context, contents []llm.ContentPart) ([]llm.ContentPart, error) {
	// Build and append the summarize prompt, assigning a history ID
	// so the adapter can track it in the UI.
	promptPart := &llm.TextPart{Text: summarizePrompt}
	id := s.histIncAndGet()
	promptPart.SetHistoryID(id)
	promptPart.SetRole(llm.RoleUser)
	if tag, val, err := contentPartToTLV(promptPart); err == nil && tag != "" {
		s.writeTLV(tag, tlv.WrapID(strconv.FormatUint(id, 10), val))
	}

	// Send the conversation (with the prompt) to the LLM.
	promptContents := append(contents, promptPart) //nolint:gocritic // intentional — keep contents unchanged
	fullContents, outputTokens, err := s.processPrompt(ctx, promptContents, false)
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

	// Build the summarized conversation: a "Continue" user message
	// followed by the summary text.
	result := make([]llm.ContentPart, 0, 2)
	continueID := s.histIncAndGet()
	result = append(result, &llm.TextPart{
		Text: "Continue",
		ContentPartMeta: llm.ContentPartMeta{
			HistoryID: continueID,
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
	limit := s.ContextLimit
	return s.AutoSummarize > 0 && limit > 0 && s.ContextTokens > 0 &&
		s.ContextTokens >= limit*int64(s.AutoSummarize)/100
}

// summarizeBackup saves a timestamped backup of the current session contents
// before summarization. Silently skips if no session file is configured.
// Failures are reported as system errors — without the backup the original
// conversation is unrecoverable after summarization.
func (s *Session) summarizeBackup(contents []llm.ContentPart) {
	if s.SessionFile == "" {
		return
	}
	ext := filepath.Ext(s.SessionFile)
	base := strings.TrimSuffix(s.SessionFile, ext)
	backupPath := fmt.Sprintf("%s-%s%s", base, time.Now().Format("20060102150405"), ext)
	if err := s.saveContentToFile(backupPath, contents); err != nil {
		s.writeErrorf("Failed to create pre-summarize backup: %v", err)
	} else {
		s.writeNotifyf("Pre-summarize backup saved to %s", backupPath)
	}
}

// doAutoSummarize logs progress notifications, triggers summarization,
// and reports failures as system errors.
// Called synchronously from runTaskNormal when the context is near
// the token limit — must complete before the user's prompt is processed.
func (s *Session) doAutoSummarize(ctx context.Context, contents []llm.ContentPart) []llm.ContentPart {
	limit := s.ContextLimit
	usage := float64(s.ContextTokens) * 100 / float64(limit)
	s.writeNotifyf("Context usage at %d/%d tokens (%.0f%%). Auto-summarizing...",
		s.ContextTokens, limit, usage)

	s.summarizeBackup(contents)
	s.writeNotify("Summarizing conversation...")

	result, err := s.summarizeContents(ctx, contents)
	if err != nil {
		s.writeErrorf("Auto-summarization failed: %v", err)
		return result // == contents on failure: no replacement event, no copy
	}

	// Success: the conversation was replaced by the summary. Publish the
	// replacement so :save during the task sees the compressed form. The
	// clone transfers ownership: run() keeps this slice, while the task
	// goroutine keeps appending to its own copy below.
	s.sendEvent(contentsReplacedEvent{Contents: cloneParts(result)})
	return result
}

// ============================================================================
// Task goroutines — runTaskNormal, runTaskContinue, runTaskSummarize
//
// These three functions are the entry points for task goroutines, each started
// by prepareTask (called from handleInputMsg) for normal prompts, :continue,
// They all call processPrompt (which blocks on the LLM) and therefore run in
// their own goroutine to keep the main event loop responsive.
//
// Relationships:
//
//   runTaskNormal     — normal prompt. Appends user parts to history, calls
//                       processPrompt. If the context was near the token limit,
//                       it synchronously runs doAutoSummarize first to free space.
//
//   runTaskContinue   — retry last prompt. If the last response was assistant
//                       (canceled mid-stream), appends "Continue" and resends.
//                       Otherwise (user/tool message), resends the history as-is.
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
// The key difference: doAutoSummarize (called from runTaskNormal) is synchronous
// — it must complete before the user's new prompt can be processed, because
// it needs to free token space first. runTaskSummarize runs as an independent
// task goroutine because :summarize is an explicit user command, not a
// precondition for another operation.
// ============================================================================

// runTaskNormal executes a normal prompt in its own goroutine.
func (s *Session) runTaskNormal(ctx context.Context, parts []llm.ContentPart) {
	contents := make([]llm.ContentPart, len(s.Contents))
	copy(contents, s.Contents)

	defer func() {
		s.taskResultCh <- contents
	}()

	if s.shouldAutoSummarize() {
		contents = s.doAutoSummarize(ctx, contents)
	}

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

	fullContents, _, err := s.processPrompt(ctx, contents, true)
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

	fullContents, _, err := s.processPrompt(ctx, contents, true)
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

	s.summarizeBackup(contents)
	s.writeNotify("Summarizing conversation...")

	contents, err := s.summarizeContents(ctx, contents)
	if err != nil {
		s.writeError(err.Error())
		return
	}
}
