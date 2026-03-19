package providers_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/llm/providers"
)

func TestAnthropicProvider(t *testing.T) {
	// Create mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request
		if r.Header.Get("x-api-key") != "test-key" {
			t.Error("Missing API key")
		}
		if r.Header.Get("anthropic-version") == "" {
			t.Error("Missing anthropic version")
		}

		// Send SSE stream
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("Cannot flush")
		}

		// Send message_start with initial usage
		fmt.Fprint(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_123\",\"role\":\"assistant\",\"content\":[],\"usage\":{\"input_tokens\":10,\"output_tokens\":1}}}\n\n")
		// Send text delta
		fmt.Fprint(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")
		fmt.Fprint(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Hello\"}}\n\n")
		fmt.Fprint(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\" world\"}}\n\n")
		fmt.Fprint(w, "event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n")
		// Send message_delta with updated output_tokens
		fmt.Fprint(w, "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":15}}\n\n")
		// Send message_stop with final usage
		fmt.Fprint(w, "event: message_stop\ndata: {\"type\":\"message_stop\",\"usage\":{\"input_tokens\":10,\"output_tokens\":15}}\n\n")

		flusher.Flush()
	}))
	defer server.Close()

	// Create provider
	provider, err := providers.NewAnthropic(
		providers.WithAPIKey("test-key"),
		providers.WithBaseURL(server.URL),
	)
	if err != nil {
		t.Fatalf("Failed to create provider: %v", err)
	}

	// Stream messages
	messages := []llm.Message{
		{Role: llm.RoleUser, Content: []llm.ContentPart{llm.TextPart{Type: "text", Text: "Hi"}}},
	}

	eventChan, err := provider.StreamMessages(context.Background(), messages, nil, "You are helpful", "")
	if err != nil {
		t.Fatalf("Failed to stream: %v", err)
	}

	// Collect events
	var textReceived string
	var usageReceived *llm.Usage

	for event := range eventChan {
		switch e := event.(type) {
		case llm.TextDeltaEvent:
			textReceived += e.Delta
		case llm.StepCompleteEvent:
			usageReceived = &e.Usage
		case llm.StreamErrorEvent:
			t.Fatalf("Stream error: %v", e.Error)
		}
	}

	// Verify
	if textReceived != "Hello world" {
		t.Errorf("Expected 'Hello world', got '%s'", textReceived)
	}

	if usageReceived == nil {
		t.Error("No usage received")
	} else {
		if usageReceived.InputTokens != 10 || usageReceived.OutputTokens != 15 {
			t.Errorf("Unexpected usage: %+v", usageReceived)
		}
	}
}

func TestOpenAIProvider(t *testing.T) {
	// Create mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Error("Missing API key")
		}

		// Send SSE stream
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("Cannot flush")
		}

		// Send chunks
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Hello\"},\"finish_reason\":null}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\" there\"},\"finish_reason\":null}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"!\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":3}}\n\n")
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
		{Role: llm.RoleUser, Content: []llm.ContentPart{llm.TextPart{Type: "text", Text: "Hi"}}},
	}

	eventChan, err := provider.StreamMessages(context.Background(), messages, nil, "You are helpful", "")
	if err != nil {
		t.Fatalf("Failed to stream: %v", err)
	}

	// Collect events
	var textReceived string

	for event := range eventChan {
		switch e := event.(type) {
		case llm.TextDeltaEvent:
			textReceived += e.Delta
		case llm.StreamErrorEvent:
			t.Fatalf("Stream error: %v", e.Error)
		}
	}

	// Verify
	if textReceived != "Hello there!" {
		t.Errorf("Expected 'Hello there!', got '%s'", textReceived)
	}
}

func TestToolCallStreaming(t *testing.T) {
	// Test that tool calls are properly streamed
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		flusher, _ := w.(http.Flusher)

		// Send tool call
		toolCall := map[string]interface{}{
			"choices": []interface{}{
				map[string]interface{}{
					"delta": map[string]interface{}{
						"tool_calls": []interface{}{
							map[string]interface{}{
								"id":   "call-123",
								"type": "function",
								"function": map[string]interface{}{
									"name":      "test_tool",
									"arguments": "{\"arg\":\"value\"}",
								},
							},
						},
					},
				},
			},
		}
		data, _ := json.Marshal(toolCall)
		fmt.Fprintf(w, "data: %s\n\n", string(data))
		fmt.Fprint(w, "data: [DONE]\n\n")

		flusher.Flush()
	}))
	defer server.Close()

	provider, err := providers.NewOpenAI(
		providers.WithOpenAIAPIKey("test"),
		providers.WithOpenAIBaseURL(server.URL),
	)
	if err != nil {
		t.Fatal(err)
	}

	messages := []llm.Message{
		{Role: llm.RoleUser, Content: []llm.ContentPart{llm.TextPart{Type: "text", Text: "test"}}},
	}

	eventChan, err := provider.StreamMessages(context.Background(), messages, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}

	var toolCalls []llm.ToolCallEvent
	for event := range eventChan {
		if tc, ok := event.(llm.ToolCallEvent); ok {
			toolCalls = append(toolCalls, tc)
		}
	}

	if len(toolCalls) != 1 {
		t.Fatalf("Expected 1 tool call, got %d", len(toolCalls))
	}

	if toolCalls[0].ToolName != "test_tool" {
		t.Errorf("Expected tool name 'test_tool', got '%s'", toolCalls[0].ToolName)
	}

	if toolCalls[0].ToolCallID != "call-123" {
		t.Errorf("Expected tool call ID 'call-123', got '%s'", toolCalls[0].ToolCallID)
	}

	// Verify arguments are properly unquoted and can be unmarshaled
	var args struct {
		Arg string `json:"arg"`
	}
	if err := json.Unmarshal(toolCalls[0].Input, &args); err != nil {
		t.Errorf("Failed to unmarshal tool call arguments: %v (input was: %s)", err, toolCalls[0].Input)
	}
	if args.Arg != "value" {
		t.Errorf("Expected arg 'value', got '%s'", args.Arg)
	}
}

func TestToolCallStreamingChunked(t *testing.T) {
	// Test that tool calls with chunked arguments are properly accumulated
	// This simulates Qwen and other providers that split arguments across multiple deltas
	// Important: subsequent chunks have empty "id" but correct "index"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		flusher, _ := w.(http.Flusher)

		// First chunk: name + id + index (arguments empty)
		fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call-456\",\"type\":\"function\",\"function\":{\"name\":\"posix_shell\",\"arguments\":\"\"}}]}}]}\n\n")
		// Subsequent chunks: arguments are raw JSON fragments (not quoted strings)
		// The API sends the JSON object being built up piece by piece
		fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"\",\"function\":{\"arguments\":\"{\\\"command\\\": \\\"uname -a\\\"}\"}}]}}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")

		flusher.Flush()
	}))
	defer server.Close()

	provider, err := providers.NewOpenAI(
		providers.WithOpenAIAPIKey("test"),
		providers.WithOpenAIBaseURL(server.URL),
	)
	if err != nil {
		t.Fatal(err)
	}

	messages := []llm.Message{
		{Role: llm.RoleUser, Content: []llm.ContentPart{llm.TextPart{Type: "text", Text: "Run uname -a"}}},
	}

	eventChan, err := provider.StreamMessages(context.Background(), messages, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}

	var toolCalls []llm.ToolCallEvent
	for event := range eventChan {
		if tc, ok := event.(llm.ToolCallEvent); ok {
			toolCalls = append(toolCalls, tc)
		}
	}

	if len(toolCalls) != 1 {
		t.Fatalf("Expected 1 tool call, got %d", len(toolCalls))
	}

	if toolCalls[0].ToolName != "posix_shell" {
		t.Errorf("Expected tool name 'posix_shell', got '%s'", toolCalls[0].ToolName)
	}

	// Verify arguments were accumulated and can be unmarshaled
	var args struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(toolCalls[0].Input, &args); err != nil {
		t.Errorf("Failed to unmarshal tool call arguments: %v (input was: %s)", err, toolCalls[0].Input)
	}
	if args.Command != "uname -a" {
		t.Errorf("Expected command 'uname -a', got '%s'", args.Command)
	}
}

func TestAnthropicToolCallStreaming(t *testing.T) {
	// Test that tool calls are properly streamed from Anthropic
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		flusher, _ := w.(http.Flusher)

		// Send tool call start
		fmt.Fprint(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"toolu_123\",\"name\":\"get_weather\"}}\n\n")
		// Send tool input delta
		fmt.Fprint(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"location\\\":\\\"\"}}\n\n")
		fmt.Fprint(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"San Francisco\\\"}\"}}\n\n")
		// Send tool call stop
		fmt.Fprint(w, "event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n")
		// Send message stop
		fmt.Fprint(w, "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"},\"usage\":{\"input_tokens\":50,\"output_tokens\":20}}\n\n")
		fmt.Fprint(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")

		flusher.Flush()
	}))
	defer server.Close()

	provider, err := providers.NewAnthropic(
		providers.WithAPIKey("test"),
		providers.WithBaseURL(server.URL),
	)
	if err != nil {
		t.Fatal(err)
	}

	messages := []llm.Message{
		{Role: llm.RoleUser, Content: []llm.ContentPart{llm.TextPart{Type: "text", Text: "What's the weather?"}}},
	}

	eventChan, err := provider.StreamMessages(context.Background(), messages, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}

	var toolCalls []llm.ToolCallEvent
	var stepComplete *llm.StepCompleteEvent
	for event := range eventChan {
		switch e := event.(type) {
		case llm.ToolCallEvent:
			toolCalls = append(toolCalls, e)
		case llm.StepCompleteEvent:
			stepComplete = &e
		case llm.StreamErrorEvent:
			t.Fatalf("Stream error: %v", e.Error)
		}
	}

	if len(toolCalls) != 1 {
		t.Fatalf("Expected 1 tool call, got %d", len(toolCalls))
	}

	if toolCalls[0].ToolName != "get_weather" {
		t.Errorf("Expected tool name 'get_weather', got '%s'", toolCalls[0].ToolName)
	}

	if toolCalls[0].ToolCallID != "toolu_123" {
		t.Errorf("Expected tool call ID 'toolu_123', got '%s'", toolCalls[0].ToolCallID)
	}

	// Check the input JSON
	var input map[string]string
	if err := json.Unmarshal(toolCalls[0].Input, &input); err != nil {
		t.Fatalf("Failed to parse tool input: %v", err)
	}
	if input["location"] != "San Francisco" {
		t.Errorf("Expected location 'San Francisco', got '%s'", input["location"])
	}

	// Check step complete
	if stepComplete == nil {
		t.Fatal("Expected StepCompleteEvent")
	}
	if stepComplete.Usage.InputTokens != 50 {
		t.Errorf("Expected 50 input tokens, got %d", stepComplete.Usage.InputTokens)
	}
}

func TestAnthropicReasoningStreaming(t *testing.T) {
	// Test that reasoning/thinking content is properly streamed
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		flusher, _ := w.(http.Flusher)

		// Send thinking block
		fmt.Fprint(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"thinking\",\"thinking\":\"\"}}\n\n")
		fmt.Fprint(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"thinking_delta\",\"thinking\":\"Let me think...\"}}\n\n")
		fmt.Fprint(w, "event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n")
		// Send text block
		fmt.Fprint(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")
		fmt.Fprint(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"text_delta\",\"text\":\"The answer is 42.\"}}\n\n")
		fmt.Fprint(w, "event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":1}\n\n")
		fmt.Fprint(w, "event: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"input_tokens\":10,\"output_tokens\":30}}\n\n")
		fmt.Fprint(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")

		flusher.Flush()
	}))
	defer server.Close()

	provider, err := providers.NewAnthropic(
		providers.WithAPIKey("test"),
		providers.WithBaseURL(server.URL),
	)
	if err != nil {
		t.Fatal(err)
	}

	messages := []llm.Message{
		{Role: llm.RoleUser, Content: []llm.ContentPart{llm.TextPart{Type: "text", Text: "What is the answer?"}}},
	}

	eventChan, err := provider.StreamMessages(context.Background(), messages, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}

	var reasoningText string
	var textReceived string
	var stepComplete *llm.StepCompleteEvent

	for event := range eventChan {
		switch e := event.(type) {
		case llm.TextDeltaEvent:
			textReceived += e.Delta
		case llm.ReasoningDeltaEvent:
			reasoningText += e.Delta
		case llm.StepCompleteEvent:
			stepComplete = &e
		case llm.StreamErrorEvent:
			t.Fatalf("Stream error: %v", e.Error)
		}
	}

	if reasoningText != "Let me think..." {
		t.Errorf("Expected 'Let me think...', got '%s'", reasoningText)
	}

	if textReceived != "The answer is 42." {
		t.Errorf("Expected 'The answer is 42.', got '%s'", textReceived)
	}

	if stepComplete == nil {
		t.Fatal("Expected StepCompleteEvent")
	}

	// Check message content includes both reasoning and text
	if len(stepComplete.Messages) == 0 {
		t.Fatal("Expected at least one message")
	}

	msg := stepComplete.Messages[0]
	if len(msg.Content) != 2 {
		t.Fatalf("Expected 2 content parts, got %d", len(msg.Content))
	}

	// First should be reasoning
	if reasonPart, ok := msg.Content[0].(llm.ReasoningPart); !ok {
		t.Error("First content part should be ReasoningPart")
	} else if reasonPart.Text != "Let me think..." {
		t.Errorf("Reasoning text mismatch: %s", reasonPart.Text)
	}

	// Second should be text
	if textPart, ok := msg.Content[1].(llm.TextPart); !ok {
		t.Error("Second content part should be TextPart")
	} else if textPart.Text != "The answer is 42." {
		t.Errorf("Text mismatch: %s", textPart.Text)
	}
}

func TestAnthropicAPIError(t *testing.T) {
	// Test API error handling
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error": {"message": "Invalid API key"}}`))
	}))
	defer server.Close()

	provider, err := providers.NewAnthropic(
		providers.WithAPIKey("invalid-key"),
		providers.WithBaseURL(server.URL),
	)
	if err != nil {
		t.Fatal(err)
	}

	messages := []llm.Message{
		{Role: llm.RoleUser, Content: []llm.ContentPart{llm.TextPart{Type: "text", Text: "Hi"}}},
	}

	_, err = provider.StreamMessages(context.Background(), messages, nil, "", "")
	if err == nil {
		t.Error("Expected error for invalid API key")
	}
}

func TestAnthropicRefusalStopReason(t *testing.T) {
	// Test Anthropic refusal stop reason handling
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		flusher, _ := w.(http.Flusher)
		// Send message_start
		fmt.Fprint(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_123\",\"role\":\"assistant\",\"content\":[]}}\n\n")
		// Send text content
		fmt.Fprint(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")
		fmt.Fprint(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"I cannot\"}}\n\n")
		// Send refusal stop reason
		fmt.Fprint(w, "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"refusal\"}}\n\n")
		flusher.Flush()
	}))
	defer server.Close()

	provider, err := providers.NewAnthropic(
		providers.WithAPIKey("test-key"),
		providers.WithBaseURL(server.URL),
	)
	if err != nil {
		t.Fatal(err)
	}

	messages := []llm.Message{
		{Role: llm.RoleUser, Content: []llm.ContentPart{llm.TextPart{Type: "text", Text: "Hi"}}},
	}

	eventChan, err := provider.StreamMessages(context.Background(), messages, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}

	// Collect events - should get an error
	var gotError bool
	for event := range eventChan {
		if _, ok := event.(llm.StreamErrorEvent); ok {
			gotError = true
		}
	}

	if !gotError {
		t.Error("Expected StreamErrorEvent for refusal stop reason")
	}
}

func TestAnthropicUnknownStopReason(t *testing.T) {
	// Test Anthropic unknown stop reason handling
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		flusher, _ := w.(http.Flusher)
		// Send message_start
		fmt.Fprint(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_123\",\"role\":\"assistant\",\"content\":[]}}\n\n")
		// Send text content
		fmt.Fprint(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")
		fmt.Fprint(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Some text\"}}\n\n")
		// Send unknown stop reason
		fmt.Fprint(w, "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"unknown_reason\"}}\n\n")
		flusher.Flush()
	}))
	defer server.Close()

	provider, err := providers.NewAnthropic(
		providers.WithAPIKey("test-key"),
		providers.WithBaseURL(server.URL),
	)
	if err != nil {
		t.Fatal(err)
	}

	messages := []llm.Message{
		{Role: llm.RoleUser, Content: []llm.ContentPart{llm.TextPart{Type: "text", Text: "Hi"}}},
	}

	eventChan, err := provider.StreamMessages(context.Background(), messages, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}

	// Collect events - should get an error
	var gotError bool
	for event := range eventChan {
		if _, ok := event.(llm.StreamErrorEvent); ok {
			gotError = true
		}
	}

	if !gotError {
		t.Error("Expected StreamErrorEvent for unknown stop reason")
	}
}

func TestAnthropicValidStopReasons(t *testing.T) {
	// Test that valid Anthropic stop reasons don't trigger errors
	validReasons := []string{"end_turn", "max_tokens", "stop_sequence", "tool_use", "pause_turn"}

	for _, reason := range validReasons {
		t.Run(reason, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(http.StatusOK)

				flusher, _ := w.(http.Flusher)
				// Send message_start
				fmt.Fprint(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_123\",\"role\":\"assistant\",\"content\":[]}}\n\n")
				// Send text content
				fmt.Fprint(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")
				fmt.Fprint(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Response\"}}\n\n")
				fmt.Fprint(w, "event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n")
				// Send valid stop reason
				fmt.Fprintf(w, "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"%s\"},\"usage\":{\"output_tokens\":10}}\n\n", reason)
				fmt.Fprint(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
				flusher.Flush()
			}))
			defer server.Close()

			provider, err := providers.NewAnthropic(
				providers.WithAPIKey("test-key"),
				providers.WithBaseURL(server.URL),
			)
			if err != nil {
				t.Fatal(err)
			}

			messages := []llm.Message{
				{Role: llm.RoleUser, Content: []llm.ContentPart{llm.TextPart{Type: "text", Text: "Hi"}}},
			}

			eventChan, err := provider.StreamMessages(context.Background(), messages, nil, "", "")
			if err != nil {
				t.Fatal(err)
			}

			// Collect events - should NOT get an error
			var gotError bool
			var gotStepComplete bool
			for event := range eventChan {
				if _, ok := event.(llm.StreamErrorEvent); ok {
					gotError = true
				}
				if _, ok := event.(llm.StepCompleteEvent); ok {
					gotStepComplete = true
				}
			}

			if gotError {
				t.Errorf("Should not get error for valid stop reason '%s'", reason)
			}
			if !gotStepComplete {
				t.Errorf("Expected StepCompleteEvent for valid stop reason '%s'", reason)
			}
		})
	}
}

func TestOpenAIContentFilter(t *testing.T) {
	// Test OpenAI content_filter finish reason handling
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		flusher, _ := w.(http.Flusher)
		// Send partial content
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Some content\"}}]}\n\n")
		// Then content_filter
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"\"},\"finish_reason\":\"content_filter\"}]}\n\n")
		flusher.Flush()
	}))
	defer server.Close()

	provider, err := providers.NewOpenAI(
		providers.WithOpenAIAPIKey("test-key"),
		providers.WithOpenAIBaseURL(server.URL),
	)
	if err != nil {
		t.Fatal(err)
	}

	messages := []llm.Message{
		{Role: llm.RoleUser, Content: []llm.ContentPart{llm.TextPart{Type: "text", Text: "Hi"}}},
	}

	eventChan, err := provider.StreamMessages(context.Background(), messages, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}

	// Collect events - should get an error
	var gotError bool
	for event := range eventChan {
		if _, ok := event.(llm.StreamErrorEvent); ok {
			gotError = true
		}
	}

	if !gotError {
		t.Error("Expected StreamErrorEvent for content_filter finish reason")
	}
}

func TestOpenAILengthFinishReason(t *testing.T) {
	// Test OpenAI length finish reason (should NOT error - it's valid)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		flusher, _ := w.(http.Flusher)
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Truncated\"},\"finish_reason\":\"length\"}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer server.Close()

	provider, err := providers.NewOpenAI(
		providers.WithOpenAIAPIKey("test-key"),
		providers.WithOpenAIBaseURL(server.URL),
	)
	if err != nil {
		t.Fatal(err)
	}

	messages := []llm.Message{
		{Role: llm.RoleUser, Content: []llm.ContentPart{llm.TextPart{Type: "text", Text: "Hi"}}},
	}

	eventChan, err := provider.StreamMessages(context.Background(), messages, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}

	// Collect events - should NOT get an error
	var gotError bool
	var gotStepComplete bool
	for event := range eventChan {
		if _, ok := event.(llm.StreamErrorEvent); ok {
			gotError = true
		}
		if _, ok := event.(llm.StepCompleteEvent); ok {
			gotStepComplete = true
		}
	}

	if gotError {
		t.Error("Should not get error for 'length' finish reason (it's valid, just truncated)")
	}
	if !gotStepComplete {
		t.Error("Expected StepCompleteEvent for 'length' finish reason")
	}
}

func TestOpenAIAPIError(t *testing.T) {
	// Test OpenAI API error handling
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"error": {"message": "Rate limit exceeded"}}`))
	}))
	defer server.Close()

	provider, err := providers.NewOpenAI(
		providers.WithOpenAIAPIKey("test-key"),
		providers.WithOpenAIBaseURL(server.URL),
	)
	if err != nil {
		t.Fatal(err)
	}

	messages := []llm.Message{
		{Role: llm.RoleUser, Content: []llm.ContentPart{llm.TextPart{Type: "text", Text: "Hi"}}},
	}

	_, err = provider.StreamMessages(context.Background(), messages, nil, "", "")
	if err == nil {
		t.Error("Expected error for rate limit")
	}
}

func TestOpenAINetworkError(t *testing.T) {
	// Test OpenAI network_error finish reason handling
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		flusher, _ := w.(http.Flusher)
		// Send some content first
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Partial\"}}]}\n\n")
		// Then send network_error
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"\"},\"finish_reason\":\"network_error\"}]}\n\n")
		flusher.Flush()
	}))
	defer server.Close()

	provider, err := providers.NewOpenAI(
		providers.WithOpenAIAPIKey("test-key"),
		providers.WithOpenAIBaseURL(server.URL),
	)
	if err != nil {
		t.Fatal(err)
	}

	messages := []llm.Message{
		{Role: llm.RoleUser, Content: []llm.ContentPart{llm.TextPart{Type: "text", Text: "Hi"}}},
	}

	eventChan, err := provider.StreamMessages(context.Background(), messages, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}

	// Collect events - should get an error
	var gotError bool
	for event := range eventChan {
		if _, ok := event.(llm.StreamErrorEvent); ok {
			gotError = true
		}
	}

	if !gotError {
		t.Error("Expected StreamErrorEvent for network_error finish reason")
	}
}

func TestOpenAIWithSystemPrompt(t *testing.T) {
	// Test that system prompt is included in OpenAI requests
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request body
		var reqBody map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
			t.Error(err)
			return
		}

		messages, ok := reqBody["messages"].([]interface{})
		if !ok || len(messages) == 0 {
			t.Error("Expected messages array")
		}

		// First message should be system
		firstMsg, ok := messages[0].(map[string]interface{})
		if !ok || firstMsg["role"] != "system" {
			t.Error("Expected first message to be system")
		}
		if firstMsg["content"] != "You are helpful" {
			t.Errorf("Expected system content 'You are helpful', got '%v'", firstMsg["content"])
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		flusher, _ := w.(http.Flusher)
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"OK\"},\"finish_reason\":\"stop\"}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer server.Close()

	provider, err := providers.NewOpenAI(
		providers.WithOpenAIAPIKey("test-key"),
		providers.WithOpenAIBaseURL(server.URL),
	)
	if err != nil {
		t.Fatal(err)
	}

	messages := []llm.Message{
		{Role: llm.RoleUser, Content: []llm.ContentPart{llm.TextPart{Type: "text", Text: "Hi"}}},
	}

	eventChan, err := provider.StreamMessages(context.Background(), messages, nil, "You are helpful", "")
	if err != nil {
		t.Fatal(err)
	}

	for range eventChan {
		// Drain the channel
	}
}

func TestAnthropicWithTools(t *testing.T) {
	// Test that tools are properly sent to Anthropic API
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request body
		var reqBody map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
			t.Error(err)
			return
		}

		tools, ok := reqBody["tools"].([]interface{})
		if !ok || len(tools) != 1 {
			t.Error("Expected 1 tool in request")
		}

		tool, ok := tools[0].(map[string]interface{})
		if !ok || tool["name"] != "test_tool" {
			t.Error("Expected tool name 'test_tool'")
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		flusher, _ := w.(http.Flusher)
		fmt.Fprint(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")
		fmt.Fprint(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Done.\"}}\n\n")
		fmt.Fprint(w, "event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n")
		fmt.Fprint(w, "event: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"input_tokens\":10,\"output_tokens\":5}}\n\n")
		fmt.Fprint(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
		flusher.Flush()
	}))
	defer server.Close()

	provider, err := providers.NewAnthropic(
		providers.WithAPIKey("test-key"),
		providers.WithBaseURL(server.URL),
	)
	if err != nil {
		t.Fatal(err)
	}

	messages := []llm.Message{
		{Role: llm.RoleUser, Content: []llm.ContentPart{llm.TextPart{Type: "text", Text: "Use the tool"}}},
	}

	tools := []llm.ToolDefinition{
		{
			Name:        "test_tool",
			Description: "A test tool",
			Schema:      json.RawMessage(`{"type":"object","properties":{"input":{"type":"string"}}}`),
		},
	}

	eventChan, err := provider.StreamMessages(context.Background(), messages, tools, "", "")
	if err != nil {
		t.Fatal(err)
	}

	for range eventChan {
		// Drain the channel
	}
}

func TestOpenAIWithReasoning(t *testing.T) {
	// Test OpenAI provider with reasoning support (DeepSeek, Qwen, etc.)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		flusher, _ := w.(http.Flusher)

		// Send reasoning content first
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"Analyzing...\"}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\" computing...\"}}]}\n\n")
		// Then regular content
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Result: 123.\"}}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")

		flusher.Flush()
	}))
	defer server.Close()

	provider, err := providers.NewOpenAI(
		providers.WithOpenAIAPIKey("test"),
		providers.WithOpenAIBaseURL(server.URL),
	)
	if err != nil {
		t.Fatal(err)
	}

	messages := []llm.Message{
		{Role: llm.RoleUser, Content: []llm.ContentPart{llm.TextPart{Type: "text", Text: "Calculate"}}},
	}

	eventChan, err := provider.StreamMessages(context.Background(), messages, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}

	var reasoningText string
	var textReceived string
	var stepComplete *llm.StepCompleteEvent

	for event := range eventChan {
		switch e := event.(type) {
		case llm.TextDeltaEvent:
			textReceived += e.Delta
		case llm.ReasoningDeltaEvent:
			reasoningText += e.Delta
		case llm.StepCompleteEvent:
			stepComplete = &e
		case llm.StreamErrorEvent:
			t.Fatalf("Stream error: %v", e.Error)
		}
	}

	if reasoningText != "Analyzing... computing..." {
		t.Errorf("Expected reasoning text 'Analyzing... computing...', got '%s'", reasoningText)
	}

	if textReceived != "Result: 123." {
		t.Errorf("Expected text 'Result: 123.', got '%s'", textReceived)
	}

	// Verify step complete contains both reasoning and text
	if stepComplete == nil {
		t.Fatal("Expected StepCompleteEvent")
	}
	if len(stepComplete.Messages) != 1 {
		t.Fatalf("Expected 1 message, got %d", len(stepComplete.Messages))
	}

	msg := stepComplete.Messages[0]
	if len(msg.Content) < 2 {
		t.Fatalf("Expected at least 2 content parts (reasoning + text), got %d", len(msg.Content))
	}

	// First should be reasoning
	if rp, ok := msg.Content[0].(llm.ReasoningPart); !ok {
		t.Error("First content part should be ReasoningPart")
	} else if rp.Text != "Analyzing... computing..." {
		t.Errorf("Reasoning text mismatch: %s", rp.Text)
	}

	// Second should be text
	if tp, ok := msg.Content[1].(llm.TextPart); !ok {
		t.Error("Second content part should be TextPart")
	} else if tp.Text != "Result: 123." {
		t.Errorf("Text mismatch: %s", tp.Text)
	}
}

func TestAnthropicToolResultMessageFormat(t *testing.T) {
	// Test that tool result messages are properly formatted for Anthropic API
	// Tool results must be in a "user" role message, not "tool" role
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request body
		var reqBody map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
			t.Error(err)
			return
		}

		messages, ok := reqBody["messages"].([]interface{})
		if !ok {
			t.Fatal("Expected messages array")
		}

		// Should have: user message, assistant with tool_use, user with tool_result
		if len(messages) < 3 {
			t.Fatalf("Expected at least 3 messages, got %d", len(messages))
		}

		// Check assistant message has tool_use
		assistantMsg, ok := messages[1].(map[string]interface{})
		if !ok || assistantMsg["role"] != "assistant" {
			t.Fatal("Expected second message to be assistant")
		}

		// Check tool result message is "user" role (not "tool")
		toolResultMsg, ok := messages[2].(map[string]interface{})
		if !ok {
			t.Fatal("Expected third message to be an object")
		}
		if toolResultMsg["role"] != "user" {
			t.Errorf("Expected tool result message role to be 'user', got '%v'", toolResultMsg["role"])
		}

		// Check content has tool_result type
		content, ok := toolResultMsg["content"].([]interface{})
		if !ok || len(content) == 0 {
			t.Fatal("Expected tool result message to have content")
		}
		firstContent, ok := content[0].(map[string]interface{})
		if !ok || firstContent["type"] != "tool_result" {
			t.Errorf("Expected content type 'tool_result', got '%v'", firstContent["type"])
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		flusher, _ := w.(http.Flusher)
		fmt.Fprint(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")
		fmt.Fprint(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Done.\"}}\n\n")
		fmt.Fprint(w, "event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n")
		fmt.Fprint(w, "event: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"input_tokens\":20,\"output_tokens\":5}}\n\n")
		fmt.Fprint(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
		flusher.Flush()
	}))
	defer server.Close()

	provider, err := providers.NewAnthropic(
		providers.WithAPIKey("test-key"),
		providers.WithBaseURL(server.URL),
	)
	if err != nil {
		t.Fatal(err)
	}

	// Simulate a conversation with tool call and result
	messages := []llm.Message{
		{Role: llm.RoleUser, Content: []llm.ContentPart{llm.TextPart{Type: "text", Text: "Use the tool"}}},
		{Role: llm.RoleAssistant, Content: []llm.ContentPart{llm.ToolCallPart{
			Type:       "tool_use",
			ToolCallID: "tool-123",
			ToolName:   "test_tool",
			Input:      json.RawMessage(`{"input": "value"}`),
		}}},
		{Role: llm.RoleTool, Content: []llm.ContentPart{llm.ToolResultPart{
			Type:       "tool_result",
			ToolCallID: "tool-123",
			Output:     llm.ToolResultOutputText{Type: "text", Text: "Tool executed successfully"},
		}}},
	}

	eventChan, err := provider.StreamMessages(context.Background(), messages, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}

	for event := range eventChan {
		if err, ok := event.(llm.StreamErrorEvent); ok {
			t.Fatalf("Stream error: %v", err.Error)
		}
	}
}

func TestAnthropicMultiToolCall(t *testing.T) {
	// Test multiple tool calls in a single response
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		flusher, _ := w.(http.Flusher)

		// First tool call
		fmt.Fprint(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"tool-1\",\"name\":\"get_weather\"}}\n\n")
		fmt.Fprint(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"city\\\":\\\"NYC\\\"}\"}}\n\n")
		fmt.Fprint(w, "event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n")

		// Second tool call
		fmt.Fprint(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"tool_use\",\"id\":\"tool-2\",\"name\":\"get_weather\"}}\n\n")
		fmt.Fprint(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"city\\\":\\\"LA\\\"}\"}}\n\n")
		fmt.Fprint(w, "event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":1}\n\n")

		fmt.Fprint(w, "event: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"input_tokens\":10,\"output_tokens\":20}}\n\n")
		fmt.Fprint(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")

		flusher.Flush()
	}))
	defer server.Close()

	provider, err := providers.NewAnthropic(
		providers.WithAPIKey("test-key"),
		providers.WithBaseURL(server.URL),
	)
	if err != nil {
		t.Fatal(err)
	}

	messages := []llm.Message{
		{Role: llm.RoleUser, Content: []llm.ContentPart{llm.TextPart{Type: "text", Text: "Get weather for NYC and LA"}}},
	}

	eventChan, err := provider.StreamMessages(context.Background(), messages, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}

	var toolCalls []llm.ToolCallEvent
	for event := range eventChan {
		switch e := event.(type) {
		case llm.ToolCallEvent:
			toolCalls = append(toolCalls, e)
		case llm.StreamErrorEvent:
			t.Fatalf("Stream error: %v", e.Error)
		}
	}

	if len(toolCalls) != 2 {
		t.Fatalf("Expected 2 tool calls, got %d", len(toolCalls))
	}

	// Check first tool call
	if toolCalls[0].ToolCallID != "tool-1" {
		t.Errorf("Expected tool call ID 'tool-1', got '%s'", toolCalls[0].ToolCallID)
	}
	if toolCalls[0].ToolName != "get_weather" {
		t.Errorf("Expected tool name 'get_weather', got '%s'", toolCalls[0].ToolName)
	}

	// Check second tool call
	if toolCalls[1].ToolCallID != "tool-2" {
		t.Errorf("Expected tool call ID 'tool-2', got '%s'", toolCalls[1].ToolCallID)
	}
}

func TestOpenAIToolResultMessageFormat(t *testing.T) {
	// Test that tool result messages are properly formatted for OpenAI API
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request body
		var reqBody map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
			t.Error(err)
			return
		}

		messages, ok := reqBody["messages"].([]interface{})
		if !ok {
			t.Fatal("Expected messages array")
		}

		// Should have: system (optional), user message, assistant with tool_calls, tool result
		if len(messages) < 3 {
			t.Fatalf("Expected at least 3 messages, got %d", len(messages))
		}

		// Find the tool result message (role: "tool")
		var foundToolResult bool
		for _, m := range messages {
			msg, ok := m.(map[string]interface{})
			if !ok {
				continue
			}
			if msg["role"] == "tool" {
				foundToolResult = true
				if msg["tool_call_id"] == nil {
					t.Error("Tool result message should have tool_call_id")
				}
				if msg["content"] == nil {
					t.Error("Tool result message should have content")
				}
				break
			}
		}
		if !foundToolResult {
			t.Error("Expected to find a tool result message with role 'tool'")
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		flusher, _ := w.(http.Flusher)
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"OK\"},\"finish_reason\":\"stop\"}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer server.Close()

	provider, err := providers.NewOpenAI(
		providers.WithOpenAIAPIKey("test-key"),
		providers.WithOpenAIBaseURL(server.URL),
	)
	if err != nil {
		t.Fatal(err)
	}

	// Simulate a conversation with tool call and result
	messages := []llm.Message{
		{Role: llm.RoleUser, Content: []llm.ContentPart{llm.TextPart{Type: "text", Text: "Use the tool"}}},
		{Role: llm.RoleAssistant, Content: []llm.ContentPart{llm.ToolCallPart{
			Type:       "tool_use",
			ToolCallID: "call-123",
			ToolName:   "test_tool",
			Input:      json.RawMessage(`{"input": "value"}`),
		}}},
		{Role: llm.RoleTool, Content: []llm.ContentPart{llm.ToolResultPart{
			Type:       "tool_result",
			ToolCallID: "call-123",
			Output:     llm.ToolResultOutputText{Type: "text", Text: "Tool executed successfully"},
		}}},
	}

	eventChan, err := provider.StreamMessages(context.Background(), messages, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}

	for event := range eventChan {
		if err, ok := event.(llm.StreamErrorEvent); ok {
			t.Fatalf("Stream error: %v", err.Error)
		}
	}
}

func TestOpenAIMultiToolResultMessageFormat(t *testing.T) {
	// Test that multiple tool results in a single message are converted to separate API messages
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request body
		var reqBody map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
			t.Error(err)
			return
		}

		messages, ok := reqBody["messages"].([]interface{})
		if !ok {
			t.Fatal("Expected messages array")
		}

		// Should have: user, assistant with 2 tool_calls, 2 tool results
		// That's 4 messages minimum
		if len(messages) < 4 {
			t.Fatalf("Expected at least 4 messages, got %d", len(messages))
		}

		// Count tool result messages (role: "tool")
		var toolResultCount int
		var toolCallIDs []string
		for _, m := range messages {
			msg, ok := m.(map[string]interface{})
			if !ok {
				continue
			}
			if msg["role"] == "tool" {
				toolResultCount++
				if msg["tool_call_id"] == nil {
					t.Error("Tool result message should have tool_call_id")
				}
				if id, ok := msg["tool_call_id"].(string); ok {
					toolCallIDs = append(toolCallIDs, id)
				}
				if msg["content"] == nil {
					t.Error("Tool result message should have content")
				}
			}
		}

		if toolResultCount != 2 {
			t.Errorf("Expected 2 tool result messages, got %d", toolResultCount)
		}

		// Verify both tool call IDs are present
		foundCall1 := false
		foundCall2 := false
		for _, id := range toolCallIDs {
			if id == "call-1" {
				foundCall1 = true
			}
			if id == "call-2" {
				foundCall2 = true
			}
		}
		if !foundCall1 {
			t.Error("Expected tool result for call-1")
		}
		if !foundCall2 {
			t.Error("Expected tool result for call-2")
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		flusher, _ := w.(http.Flusher)
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Done\"},\"finish_reason\":\"stop\"}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer server.Close()

	provider, err := providers.NewOpenAI(
		providers.WithOpenAIAPIKey("test-key"),
		providers.WithOpenAIBaseURL(server.URL),
	)
	if err != nil {
		t.Fatal(err)
	}

	// Simulate a conversation with 2 tool calls and 2 results in a single tool message
	messages := []llm.Message{
		{Role: llm.RoleUser, Content: []llm.ContentPart{llm.TextPart{Type: "text", Text: "Run two tools"}}},
		{Role: llm.RoleAssistant, Content: []llm.ContentPart{
			llm.ToolCallPart{
				Type:       "tool_use",
				ToolCallID: "call-1",
				ToolName:   "tool_a",
				Input:      json.RawMessage(`{}`),
			},
			llm.ToolCallPart{
				Type:       "tool_use",
				ToolCallID: "call-2",
				ToolName:   "tool_b",
				Input:      json.RawMessage(`{}`),
			},
		}},
		{Role: llm.RoleTool, Content: []llm.ContentPart{
			llm.ToolResultPart{
				Type:       "tool_result",
				ToolCallID: "call-1",
				Output:     llm.ToolResultOutputText{Type: "text", Text: "Result A"},
			},
			llm.ToolResultPart{
				Type:       "tool_result",
				ToolCallID: "call-2",
				Output:     llm.ToolResultOutputText{Type: "text", Text: "Result B"},
			},
		}},
	}

	eventChan, err := provider.StreamMessages(context.Background(), messages, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}

	for event := range eventChan {
		if err, ok := event.(llm.StreamErrorEvent); ok {
			t.Fatalf("Stream error: %v", err.Error)
		}
	}
}

func TestAnthropicToolResultError(t *testing.T) {
	// Test that tool result errors are properly formatted
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request body
		var reqBody map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
			t.Error(err)
			return
		}

		messages, ok := reqBody["messages"].([]interface{})
		if !ok {
			t.Fatal("Expected messages array")
		}

		// Find tool result and check is_error field
		for _, m := range messages {
			msg, ok := m.(map[string]interface{})
			if !ok || msg["role"] != "user" {
				continue
			}
			content, ok := msg["content"].([]interface{})
			if !ok || len(content) == 0 {
				continue
			}
			block, ok := content[0].(map[string]interface{})
			if !ok || block["type"] != "tool_result" {
				continue
			}
			// Check is_error is true
			if block["is_error"] != true {
				t.Error("Expected is_error to be true for error tool result")
			}
			break
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		flusher, _ := w.(http.Flusher)
		fmt.Fprint(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")
		fmt.Fprint(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"I see the error.\"}}\n\n")
		fmt.Fprint(w, "event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n")
		fmt.Fprint(w, "event: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"input_tokens\":10,\"output_tokens\":5}}\n\n")
		fmt.Fprint(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
		flusher.Flush()
	}))
	defer server.Close()

	provider, err := providers.NewAnthropic(
		providers.WithAPIKey("test-key"),
		providers.WithBaseURL(server.URL),
	)
	if err != nil {
		t.Fatal(err)
	}

	// Conversation with error tool result
	messages := []llm.Message{
		{Role: llm.RoleUser, Content: []llm.ContentPart{llm.TextPart{Type: "text", Text: "Use the tool"}}},
		{Role: llm.RoleAssistant, Content: []llm.ContentPart{llm.ToolCallPart{
			Type:       "tool_use",
			ToolCallID: "tool-123",
			ToolName:   "test_tool",
			Input:      json.RawMessage(`{}`),
		}}},
		{Role: llm.RoleTool, Content: []llm.ContentPart{llm.ToolResultPart{
			Type:       "tool_result",
			ToolCallID: "tool-123",
			Output:     llm.ToolResultOutputError{Type: "error", Error: "Something went wrong"},
		}}},
	}

	eventChan, err := provider.StreamMessages(context.Background(), messages, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}

	for event := range eventChan {
		if err, ok := event.(llm.StreamErrorEvent); ok {
			t.Fatalf("Stream error: %v", err.Error)
		}
	}
}
