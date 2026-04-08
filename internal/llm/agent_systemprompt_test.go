package llm

import (
	"context"
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
	_ []Message,
	_ []ToolDefinition,
	systemPrompt string,
	extraSystemPrompt string,
) (iter.Seq2[StreamEvent, error], error) {
	m.lastSystemPrompt = systemPrompt
	m.lastExtraSystemPrompt = extraSystemPrompt

	// Return empty iterator
	return func(func(StreamEvent, error) bool) {}, nil
}

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

			// Stream with empty messages to trigger provider call
			_, err := agent.Stream(context.Background(), []Message{}, StreamCallbacks{})
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

// TestProviderSystemMessageArray documents that providers should send
// system messages as separate array items, not merged into one string
func TestProviderSystemMessageArray(_ *testing.T) {
	// This test documents the expected behavior for provider implementations:
	//
	// Anthropic API format:
	// {
	//   "system": [
	//     {"type": "text", "text": "IDENTITY: ..."},
	//     {"type": "text", "text": "Extra 1\n\nExtra 2"}
	//   ]
	// }
	//
	// OpenAI API format:
	// {
	//   "messages": [
	//     {"role": "system", "content": "IDENTITY: ..."},
	//     {"role": "system", "content": "Extra 1\n\nExtra 2"},
	//     {"role": "user", "content": "..."}
	//   ]
	// }
	//
	// This allows:
	// 1. Better API cache efficiency (separate cache keys for each system message)
	// 2. Proper separation of default prompt from user-provided extras
	// 3. Multiple --system flags are merged with "\n\n" but kept separate from default
}
