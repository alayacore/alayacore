package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"testing"
)

// TestAgentPreservesTextWithToolCalls verifies that the agent preserves
// text content when tool calls are present in the assistant message
func TestAgentPreservesTextWithToolCalls(t *testing.T) {
	// Create mock provider that returns text + tool call
	provider := &mockProviderWithTextAndTools{
		responses: []mockResponse{
			{
				text: "Let me check that for you.",
				toolCalls: []ToolInputPart{
					{ID: "call_123", Name: "get_weather", Input: []byte(`{"location":"SF"}`)},
				},
			},
			{
				text: "The weather in SF is sunny.",
			},
		},
	}

	// Create agent
	agent := NewAgent(AgentConfig{
		Provider: provider,
		Tools: []Tool{
			{
				Definition: ToolDefinition{Name: "get_weather", Description: "Get weather", Schema: []byte(`{"type":"object"}`)},
				Execute: func(_ context.Context, _ json.RawMessage) ([]ContentPart, error) {
					return []ContentPart{&TextPart{Text: "Sunny, 72F"}}, nil
				},
			},
		},
		MaxSteps: 5,
	})

	// Track contents received via OnStepFinish callback
	var allStepContents [][]ContentPart
	_, err := agent.Stream(context.Background(), []ContentPart{
		&TextPart{Text: "What's the weather?", ContentPartMeta: ContentPartMeta{Role: RoleUser}},
	}, StreamCallbacks{
		OnStepFinish: func(contents []ContentPart, _ Usage) error {
			allStepContents = append(allStepContents, contents)
			return nil
		},
	})

	if err != nil {
		t.Fatalf("Agent.Stream failed: %v", err)
	}

	// Verify first step has both text and tool call
	if len(allStepContents) < 1 {
		t.Fatal("No step contents received")
	}

	firstStep := allStepContents[0]

	// Find the assistant parts (after user prompt, before tool results)
	hasText := false
	hasToolCall := false
	for _, part := range firstStep {
		if part.GetRole() == RoleAssistant {
			switch p := part.(type) {
			case *TextPart:
				hasText = true
				if p.Text != "Let me check that for you." && p.Text != "What's the weather?" {
					t.Errorf("Unexpected text: %q", p.Text)
				}
			case *ToolInputPart:
				hasToolCall = true
				if p.Name != "get_weather" {
					t.Errorf("Tool name mismatch: %s", p.Name)
				}
			}
		}
	}

	if !hasText {
		t.Error("CRITICAL BUG: Assistant content missing text content! Text lost when tool calls present")
	}

	if !hasToolCall {
		t.Error("Assistant content missing tool call")
	}

	if hasText && hasToolCall {
		t.Log("PASS: Agent preserves text content when tool calls are present")
	}
}

// mockProviderWithTextAndTools returns responses with both text and tool calls
type mockProviderWithTextAndTools struct {
	responses []mockResponse
	callCount int
}

type mockResponse struct {
	text      string
	toolCalls []ToolInputPart
}

func (m *mockProviderWithTextAndTools) StreamMessages(_ context.Context, _ []ContentPart, _ []ToolDefinition, _, _ string) (iter.Seq2[StreamEvent, error], error) {
	resp := m.responses[m.callCount]
	m.callCount++

	return func(yield func(StreamEvent, error) bool) {
		// Send text delta
		if resp.text != "" {
			if !yield(TextDeltaEvent{Delta: resp.text, Key: "block:0"}, nil) {
				return
			}
			if !yield(TextCompleteEvent{Key: "block:0"}, nil) {
				return
			}
		}

		// Send tool call events — identity, arguments, then where they ended.
		// A stub provider states only what the wire carried: llm.Agent assembles
		// the step's record from these, so there is no second copy to build and
		// no way for this fake to describe the turn differently from the events
		// it sent.
		for i, tc := range resp.toolCalls {
			key := fmt.Sprintf("block:%d", i+1) // 0 is the text block
			if !yield(ToolInputStartEvent{ID: tc.ID, Name: tc.Name, Key: key}, nil) {
				return
			}
			if !yield(ToolInputDeltaEvent{ID: tc.ID, Delta: string(tc.Input), Key: key}, nil) {
				return
			}
			if !yield(ToolInputCompleteEvent{ID: tc.ID, Key: key}, nil) {
				return
			}
		}

		yield(StepCompleteEvent{
			Usage: Usage{InputTokens: 10, OutputTokens: 20},
		}, nil)
	}, nil
}

func (m *mockProviderWithTextAndTools) SetReasoningLevel(_ int)                       {}
func (m *mockProviderWithTextAndTools) SetReasoningConfigs(_ map[int]json.RawMessage) {}
func (m *mockProviderWithTextAndTools) SetVideoConfig(_ int, _ int)                   {}
