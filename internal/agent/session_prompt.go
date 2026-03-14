package agent

import (
	"context"
	"fmt"
	"strconv"
	"sync/atomic"

	"charm.land/fantasy"
	"github.com/alayacore/alayacore/internal/stream"
)

// ============================================================================
// Prompt Handling
// ============================================================================

func (s *Session) handleUserPrompt(ctx context.Context, prompt string) {
	// Auto-summarize when context usage reaches 80% of the limit
	if s.shouldAutoSummarize() {
		s.autoSummarize(ctx)
	}

	s.Messages = append(s.Messages, fantasy.NewUserMessage(prompt))
	history := s.Messages[:len(s.Messages)-1]

	_, err := s.processPrompt(ctx, prompt, history)

	// Clean incomplete tool calls to prevent API errors on next request
	// (messages are appended incrementally in OnStepFinish)
	s.Messages = cleanIncompleteToolCalls(s.Messages)

	if err != nil {
		s.writeError(err.Error())
		return
	}
}

// shouldAutoSummarize checks if context usage exceeds 80% threshold
func (s *Session) shouldAutoSummarize() bool {
	return s.ContextLimit > 0 && s.ContextTokens > 0 &&
		s.ContextTokens >= s.ContextLimit*80/100
}

// autoSummarize performs synchronous summarization to reduce context
func (s *Session) autoSummarize(ctx context.Context) {
	usage := float64(s.ContextTokens) * 100 / float64(s.ContextLimit)
	s.writeNotify(fmt.Sprintf("Context usage at %d/%d tokens (%.0f%%). Auto-summarizing...",
		s.ContextTokens, s.ContextLimit, usage))
	s.summarize(ctx)
}

// processPrompt processes a prompt, appending messages to s.Messages via callbacks.
// Returns the output tokens from the response.
func (s *Session) processPrompt(ctx context.Context, prompt string, history []fantasy.Message) (int64, error) {
	call := fantasy.AgentStreamCall{Prompt: prompt}
	promptId := atomic.AddUint64(&s.nextPromptID, 1) - 1

	var stepCount int
	var outputTokens int64

	if len(history) > 0 {
		call.Messages = history
	}

	/// the final ID is [:promptId-stepCount-id:]
	assembleId := func(id string) string {
		return "[:" + strconv.FormatUint(promptId, 10) + "-" + strconv.FormatInt(int64(stepCount), 10) + "-" + id + ":]"
	}

	call.OnStepStart = func(step int) error {
		stepCount = step
		return nil
	}
	call.OnStepFinish = func(stepResult fantasy.StepResult) error {
		s.trackUsage(stepResult.Usage)
		// Append messages incrementally so they're preserved on cancellation
		// (fantasy returns nil result on error, losing all steps)
		if len(stepResult.Messages) > 0 {
			s.Messages = append(s.Messages, stepResult.Messages...)
		}
		outputTokens += stepResult.Usage.OutputTokens
		return nil
	}

	// The `id` in the callback is not reliable, it does not work for some providers.
	// Here we only need to distinguish the delta type, so we give numbers directly.
	call.OnTextDelta = func(_, text string) error {
		stream.WriteTLV(s.Output, stream.TagAssistantText, assembleId("t")+text)
		s.Output.Flush()
		return nil
	}
	call.OnReasoningDelta = func(_, text string) error {
		stream.WriteTLV(s.Output, stream.TagReasoning, assembleId("r")+text)
		s.Output.Flush()
		return nil
	}
	call.OnToolCall = func(tc fantasy.ToolCallContent) error {
		s.writeToolCall(tc.ToolName, tc.Input, tc.ToolCallID)
		s.Output.Flush()
		return nil
	}

	_, err := s.Agent.Stream(ctx, call)
	s.Output.Flush()

	return outputTokens, err
}

func (s *Session) trackUsage(usage fantasy.Usage) {
	s.mu.Lock()
	s.TotalSpent.InputTokens += usage.InputTokens
	s.TotalSpent.OutputTokens += usage.OutputTokens
	s.TotalSpent.TotalTokens += usage.TotalTokens
	s.TotalSpent.ReasoningTokens += usage.ReasoningTokens
	// ContextTokens tracks the total context size (input tokens sent to API)
	// This is what counts toward provider context limits
	s.ContextTokens = usage.InputTokens
	s.mu.Unlock()
	s.sendSystemInfo()
}

// ============================================================================
// Clean Incomplete Tool Calls
// ============================================================================

// cleanIncompleteToolCalls removes incomplete tool calls from messages.
// Uses two-phase approach:
// 1. Forward pass to identify all unmatched tool calls (tool_call without corresponding tool_result)
// 2. Reverse pass to remove/filter messages containing unmatched tool calls
func cleanIncompleteToolCalls(messages []fantasy.Message) []fantasy.Message {
	// Phase 1: Forward pass to find all unmatched tool calls
	unmatchedCalls := make(map[string]bool)
	for _, msg := range messages {
		for _, part := range msg.Content {
			switch p := part.(type) {
			case fantasy.ToolCallPart:
				unmatchedCalls[p.ToolCallID] = true
			case fantasy.ToolResultPart:
				delete(unmatchedCalls, p.ToolCallID)
			}
		}
	}

	// Early exit if no unmatched calls
	if len(unmatchedCalls) == 0 {
		return messages
	}

	// Phase 2: Reverse pass to remove/filter messages with unmatched tool calls
	// Process from end, stopping at first complete message or user message
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]

		// Check if message has any unmatched tool calls
		hasUnmatchedCall := false
		for _, part := range msg.Content {
			if tc, ok := part.(fantasy.ToolCallPart); ok && unmatchedCalls[tc.ToolCallID] {
				hasUnmatchedCall = true
				break
			}
		}

		if hasUnmatchedCall {
			// Filter out unmatched tool calls
			filteredParts := make([]fantasy.MessagePart, 0, len(msg.Content))
			for _, part := range msg.Content {
				if tc, ok := part.(fantasy.ToolCallPart); ok && unmatchedCalls[tc.ToolCallID] {
					continue // Skip unmatched tool call
				}
				filteredParts = append(filteredParts, part)
			}

			if len(filteredParts) > 0 {
				// Has other content (text, reasoning, matched calls) - keep and stop
				messages[i].Content = filteredParts
				return messages[:i+1]
			} else {
				// Only had unmatched tool calls - remove and continue
				messages = messages[:i]
				continue
			}
		}

		// No unmatched tool calls - message is complete, stop here
		return messages[:i+1]
	}

	return messages
}
