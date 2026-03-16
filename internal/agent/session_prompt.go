package agent

import (
	"context"
	"strconv"
	"sync/atomic"

	"charm.land/fantasy"
	"github.com/alayacore/alayacore/internal/stream"
)

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
	s.writeNotifyf("Context usage at %d/%d tokens (%.0f%%). Auto-summarizing...",
		s.ContextTokens, s.ContextLimit, usage)
	s.summarize(ctx)
}

// processPrompt processes a prompt, appending messages to s.Messages via callbacks.
// Returns the output tokens from the response.
func (s *Session) processPrompt(ctx context.Context, prompt string, history []fantasy.Message) (int64, error) {
	call := fantasy.AgentStreamCall{Prompt: prompt}
	promptID := atomic.AddUint64(&s.nextPromptID, 1) - 1

	var stepCount int
	var outputTokens int64

	if len(history) > 0 {
		call.Messages = history
	}

	/// the final ID is [:promptID-stepCount-id:]
	assembleID := func(id string) string {
		return "[:" + strconv.FormatUint(promptID, 10) + "-" + strconv.FormatInt(int64(stepCount), 10) + "-" + id + ":]"
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
		stream.WriteTLV(s.Output, stream.TagTextAssistant, assembleID("t")+text)
		s.Output.Flush()
		return nil
	}
	call.OnReasoningDelta = func(_, text string) error {
		stream.WriteTLV(s.Output, stream.TagTextReasoning, assembleID("r")+text)
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
