package agent

import (
	"context"
	"encoding/json"
	"strconv"
	"sync/atomic"

	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/stream"
)

func (s *Session) handleUserPrompt(ctx context.Context, prompt string) {
	// Auto-summarize when context usage reaches 80% of the limit
	if s.shouldAutoSummarize() {
		s.autoSummarize(ctx)
	}

	s.Messages = append(s.Messages, llm.NewUserMessage(prompt))

	_, err := s.processPrompt(ctx, prompt, s.Messages)

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
func (s *Session) processPrompt(ctx context.Context, prompt string, history []llm.Message) (int64, error) {
	promptID := atomic.AddUint64(&s.nextPromptID, 1) - 1

	var stepCount int
	var outputTokens int64

	/// the final ID is [:promptID-stepCount-id:]
	assembleID := func(id string) string {
		return "[:" + strconv.FormatUint(promptID, 10) + "-" + strconv.FormatInt(int64(stepCount), 10) + "-" + id + ":]"
	}

	// Stream with callbacks
	_, err := s.Agent.Stream(ctx, history, llm.StreamCallbacks{
		OnStepStart: func(step int) error {
			stepCount = step
			return nil
		},
		OnStepFinish: func(messages []llm.Message, usage llm.Usage) error {
			s.trackUsage(usage)
			// Append messages incrementally so they're preserved on cancellation
			if len(messages) > 0 {
				s.Messages = append(s.Messages, messages...)
			}
			outputTokens += usage.OutputTokens
			return nil
		},
		OnToolResult: func(toolCallID string, output llm.ToolResultOutput) error {
			// Add tool result message to session messages
			s.Messages = append(s.Messages, llm.Message{
				Role: llm.RoleTool,
				Content: []llm.ContentPart{llm.ToolResultPart{
					Type:       "tool_result",
					ToolCallID: toolCallID,
					Output:     output,
				}},
			})
			return nil
		},
		OnTextDelta: func(delta string) error {
			stream.WriteTLV(s.Output, stream.TagTextAssistant, assembleID("t")+delta)
			s.Output.Flush()
			return nil
		},
		OnReasoningDelta: func(delta string) error {
			stream.WriteTLV(s.Output, stream.TagTextReasoning, assembleID("r")+delta)
			s.Output.Flush()
			return nil
		},
		OnToolCall: func(toolCallID, toolName string, input json.RawMessage) error {
			s.writeToolCall(toolName, string(input), toolCallID)
			s.Output.Flush()
			return nil
		},
	})

	s.Output.Flush()

	if err != nil {
		return 0, err
	}

	return outputTokens, nil
}
