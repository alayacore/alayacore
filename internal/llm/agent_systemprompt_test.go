package llm

import (
	"context"
	"encoding/json"
	"iter"
	"testing"
)

// mockProvider captures the system prompts it receives
type mockProvider struct {
	lastSystemPrompt      string
	lastExtraSystemPrompt string
}

func (m *mockProvider) StreamMessages(
	_ context.Context,
	_ []ContentPart,
	_ []ToolDefinition,
	systemPrompt string,
	extraSystemPrompt string,
) (iter.Seq2[StreamEvent, error], error) {
	m.lastSystemPrompt = systemPrompt
	m.lastExtraSystemPrompt = extraSystemPrompt

	// Return empty iterator
	return func(func(StreamEvent, error) bool) {}, nil
}

func (m *mockProvider) SetReasoningLevel(_ int)                       {}
func (m *mockProvider) SetReasoningConfigs(_ map[int]json.RawMessage) {}

// TestAgentSystemPromptSeparation: the identity prompt and the user's extra
// system prompts stay two values on the way to a provider, never one string.
// A provider may send them as two array items (Anthropic's "system": [{"type":
// "text"...}, …]; OpenAI's two {"role":"system"} messages), which is what keeps
// each cacheable on its own key and keeps the default prompt out of what a
// --system flag adds. What this test can pin is where the adapter hands them
// over; a provider's request shape is asserted in internal/llm/providers.
func TestAgentSystemPromptSeparation(t *testing.T) {
	tests := []struct {
		name              string
		systemPrompt      string
		extraSystemPrompt string
		expectedBase      string
		expectedExtra     string
	}{
		{
			name:              "only base prompt",
			systemPrompt:      "Base system prompt",
			extraSystemPrompt: "",
			expectedBase:      "Base system prompt",
			expectedExtra:     "",
		},
		{
			name:              "base and extra prompt",
			systemPrompt:      "Base system prompt",
			extraSystemPrompt: "Extra instructions",
			expectedBase:      "Base system prompt",
			expectedExtra:     "Extra instructions",
		},
		{
			name:              "multiple extra prompts pre-joined",
			systemPrompt:      "Base system prompt",
			extraSystemPrompt: "Extra 1\n\nExtra 2\n\nExtra 3",
			expectedBase:      "Base system prompt",
			expectedExtra:     "Extra 1\n\nExtra 2\n\nExtra 3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := &mockProvider{}

			agent := NewAgent(AgentConfig{
				Provider:          mock,
				Tools:             []Tool{},
				SystemPrompt:      tt.systemPrompt,
				ExtraSystemPrompt: tt.extraSystemPrompt,
				MaxSteps:          1,
			})

			// Stream with empty contents to trigger provider call
			_, err := agent.Stream(context.Background(), []ContentPart{}, StreamCallbacks{})
			if err != nil {
				t.Fatalf("Stream() error = %v", err)
			}

			// Verify the prompts were passed separately to the provider
			if mock.lastSystemPrompt != tt.expectedBase {
				t.Errorf("SystemPrompt = %q, want %q", mock.lastSystemPrompt, tt.expectedBase)
			}
			if mock.lastExtraSystemPrompt != tt.expectedExtra {
				t.Errorf("ExtraSystemPrompt = %q, want %q", mock.lastExtraSystemPrompt, tt.expectedExtra)
			}
		})
	}
}
