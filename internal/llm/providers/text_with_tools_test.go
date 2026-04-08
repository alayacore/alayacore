package providers_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/llm/providers"
)

// TestTextWithToolCalls verifies that when an assistant responds with both
// text content AND tool calls, both are preserved in the StepCompleteEvent
func TestTextWithToolCalls(t *testing.T) {
	// Create mock server that returns text + tool call
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		flusher, _ := w.(http.Flusher)

		// OpenAI-style response: text followed by tool call
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Let me help\"},\"finish_reason\":null}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\" with that.\"},\"finish_reason\":null}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_123\",\"type\":\"function\",\"function\":{\"name\":\"get_weather\",\"arguments\":\"{\\\"location\\\":\\\"SF\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":20}}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")

		flusher.Flush()
	}))
	defer server.Close()

	// Create provider
	provider, err := providers.NewOpenAI(
		providers.WithOpenAIAPIKey("test-key"),
		providers.WithOpenAIBaseURL(server.URL),
	)
	if err != nil {
		t.Fatalf("Failed to create provider: %v", err)
	}

	// Stream messages
	messages := []llm.Message{
		{Role: llm.RoleUser, Content: []llm.ContentPart{llm.TextPart{Type: "text", Text: "What's the weather?"}}},
	}

	events, err := provider.StreamMessages(context.Background(), messages, nil, "You are helpful", "")
	if err != nil {
		t.Fatalf("Failed to stream: %v", err)
	}

	// Collect events
	var textReceived strings.Builder
	var stepComplete *llm.StepCompleteEvent

	for event, _ := range events {
		switch e := event.(type) {
		case llm.TextDeltaEvent:
			textReceived.WriteString(e.Delta)
		case llm.StepCompleteEvent:
			stepComplete = &e
		}
	}

	// Verify streaming text
	if textReceived.String() != "Let me help with that." {
		t.Errorf("Expected streaming text 'Let me help with that.', got '%s'", textReceived.String())
	}

	// Verify StepCompleteEvent contains both text AND tool calls
	if stepComplete == nil {
		t.Fatal("No StepCompleteEvent received")
	}

	if len(stepComplete.Messages) == 0 {
		t.Fatal("StepCompleteEvent has no messages")
	}

	msg := stepComplete.Messages[0]
	t.Logf("Message role: %s", msg.Role)
	t.Logf("Message content parts: %d", len(msg.Content))

	// Check for text part
	hasText := false
	hasToolCall := false
	for _, part := range msg.Content {
		switch p := part.(type) {
		case llm.TextPart:
			hasText = true
			t.Logf("  TextPart: %q", p.Text)
			if p.Text != "Let me help with that." {
				t.Errorf("TextPart content mismatch: %q", p.Text)
			}
		case llm.ToolCallPart:
			hasToolCall = true
			t.Logf("  ToolCallPart: %s(%s)", p.ToolName, string(p.Input))
		}
	}

	if !hasText {
		t.Error("CRITICAL BUG: Message missing TextPart! Text is lost when tool calls are present")
	}

	if !hasToolCall {
		t.Error("Message missing ToolCallPart")
	}

	if hasText && hasToolCall {
		t.Log("PASS: Both text and tool calls preserved in StepCompleteEvent")
	}
}
