package providers_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/llm/providers"
)

// newMockSSEServer creates an httptest.Server that responds with SSE events.
// The writeFn callback receives the response writer after common headers are
// set; it is responsible for writing the event data.  The server is
// automatically closed when the test finishes via t.Cleanup.
//
// Tests that need to verify request headers should use httptest.NewServer
// directly instead.
func newMockSSEServer(t *testing.T, writeFn func(w io.Writer)) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("response writer does not support flushing")
		}
		writeFn(w)
		flusher.Flush()
	}))
	t.Cleanup(server.Close)
	return server
}

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
	provider, err := providers.NewAnthropic(providers.BaseConfig{
		APIKey:  "test-key",
		BaseURL: server.URL,
	})
	if err != nil {
		t.Fatalf("Failed to create provider: %v", err)
	}

	// Stream messages
	messages := testMsg(llm.RoleUser, &llm.TextPart{Text: "Hi"})

	events, err := provider.StreamMessages(context.Background(), messages, nil, "You are helpful", "")
	if err != nil {
		t.Fatalf("Failed to stream: %v", err)
	}

	// Collect events
	var textReceived string
	var usageReceived *llm.Usage

	for event := range events {
		if e, ok := event.(llm.TextDeltaEvent); ok {
			textReceived += e.Delta
		} else if e, ok := event.(llm.StepCompleteEvent); ok {
			usageReceived = &e.Usage
		}
	}

	// Verify
	if textReceived != "Hello world" {
		t.Errorf("Expected 'Hello world', got '%s'", textReceived)
	}

	if usageReceived == nil {
		t.Error("No usage received")
	} else if usageReceived.InputTokens != 10 || usageReceived.OutputTokens != 15 {
		t.Errorf("Unexpected usage: %+v", usageReceived)
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
	provider, err := providers.NewOpenAI(providers.BaseConfig{
		APIKey:  "test-key",
		BaseURL: server.URL,
	})
	if err != nil {
		t.Fatalf("Failed to create provider: %v", err)
	}

	// Stream messages
	messages := testMsg(llm.RoleUser, &llm.TextPart{Text: "Hi"})

	events, err := provider.StreamMessages(context.Background(), messages, nil, "You are helpful", "")
	if err != nil {
		t.Fatalf("Failed to stream: %v", err)
	}

	// Collect events
	var textReceived string

	for event := range events {
		if e, ok := event.(llm.TextDeltaEvent); ok {
			textReceived += e.Delta
		}
	}

	// Verify
	if textReceived != "Hello there!" {
		t.Errorf("Expected 'Hello there!', got '%s'", textReceived)
	}
}

func TestToolCallStreaming(t *testing.T) {
	// Test that tool calls are properly streamed
	server := newMockSSEServer(t, func(w io.Writer) {
		// Send tool call
		toolCall := map[string]any{
			"choices": []any{
				map[string]any{
					"delta": map[string]any{
						"tool_calls": []any{
							map[string]any{
								"id":   "call-123",
								"type": "function",
								"function": map[string]any{
									"name":      "test_tool",
									"arguments": "{\"arg\":\"value\"}",
								},
							},
						},
					},
				},
			},
		}
		data, err := json.Marshal(toolCall)
		if err != nil {
			t.Errorf("Failed to marshal toolCall: %v", err)
			return
		}
		fmt.Fprintf(w, "data: %s\n\n", string(data))
		fmt.Fprint(w, "data: [DONE]\n\n")
	})

	provider, err := providers.NewOpenAI(providers.BaseConfig{
		APIKey:  "test",
		BaseURL: server.URL,
	})
	if err != nil {
		t.Fatal(err)
	}

	messages := testMsg(llm.RoleUser, &llm.TextPart{Text: "test"})

	events, err := provider.StreamMessages(context.Background(), messages, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}

	var toolCalls []llm.ToolInputPart
	toolNames := make(map[string]string)
	for event := range events {
		if e, ok := event.(llm.ToolInputStartEvent); ok {
			toolNames[e.ID] = e.Name
		}
		if tc, ok := event.(llm.ToolInputCompleteEvent); ok {
			toolCalls = append(toolCalls, llm.ToolInputPart{ID: tc.ID, Name: toolNames[tc.ID], Input: tc.Input})
		}
	}

	if len(toolCalls) != 1 {
		t.Fatalf("Expected 1 tool call, got %d", len(toolCalls))
	}

	if toolCalls[0].Name != "test_tool" {
		t.Errorf("Expected tool name 'test_tool', got '%s'", toolCalls[0].Name)
	}

	if toolCalls[0].ID != "call-123" {
		t.Errorf("Expected tool call ID 'call-123', got '%s'", toolCalls[0].ID)
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

func TestToolCallStreamingRawJSONArguments(t *testing.T) {
	// Regression test: some OpenAI-compatible providers send tool call
	// arguments as raw JSON objects (not JSON-string-encoded literals).
	// unquoteToolArg must fall back to the raw bytes instead of dropping
	// the fragment — otherwise every tool call from such providers would
	// complete with an empty input.
	server := newMockSSEServer(t, func(w io.Writer) {
		fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call-raw\",\"type\":\"function\",\"function\":{\"name\":\"read_file\",\"arguments\":\"\"}}]}}]}\n\n")
		// Raw JSON object chunk (no surrounding quotes).
		fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"\",\"function\":{\"arguments\":{\"path\":\"/tmp/f.txt\"}}}]}}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	})

	provider, err := providers.NewOpenAI(providers.BaseConfig{
		APIKey:  "test",
		BaseURL: server.URL,
	})
	if err != nil {
		t.Fatal(err)
	}

	events, err := provider.StreamMessages(context.Background(), testMsg(llm.RoleUser, &llm.TextPart{Text: "read /tmp/f.txt"}), nil, "", "")
	if err != nil {
		t.Fatal(err)
	}

	var toolCalls []llm.ToolInputPart
	toolNames := make(map[string]string)
	for event := range events {
		if e, ok := event.(llm.ToolInputStartEvent); ok {
			toolNames[e.ID] = e.Name
		}
		if tc, ok := event.(llm.ToolInputCompleteEvent); ok {
			toolCalls = append(toolCalls, llm.ToolInputPart{ID: tc.ID, Name: toolNames[tc.ID], Input: tc.Input})
		}
	}

	if len(toolCalls) != 1 {
		t.Fatalf("Expected 1 tool call, got %d", len(toolCalls))
	}

	// The raw-JSON fragment must be preserved, not dropped.
	var args struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(toolCalls[0].Input, &args); err != nil {
		t.Fatalf("Failed to unmarshal tool call arguments: %v (input was: %s)", err, toolCalls[0].Input)
	}
	if args.Path != "/tmp/f.txt" {
		t.Errorf("Expected path '/tmp/f.txt', got %q (input was: %s)", args.Path, toolCalls[0].Input)
	}
}

func TestToolCallStreamingChunked(t *testing.T) {
	// Test that tool calls with chunked arguments are properly accumulated
	// This simulates Qwen and other providers that split arguments across multiple deltas
	// Important: subsequent chunks have empty "id" but correct "index"
	server := newMockSSEServer(t, func(w io.Writer) {
		// First chunk: name + id + index (arguments empty)
		fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call-456\",\"type\":\"function\",\"function\":{\"name\":\"execute_command\",\"arguments\":\"\"}}]}}]}\n\n")
		// Subsequent chunks: arguments are raw JSON fragments (not quoted strings)
		// The API sends the JSON object being built up piece by piece
		fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"\",\"function\":{\"arguments\":\"{\\\"command\\\": \\\"uname -a\\\"}\"}}]}}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	})

	provider, err := providers.NewOpenAI(providers.BaseConfig{
		APIKey:  "test",
		BaseURL: server.URL,
	})
	if err != nil {
		t.Fatal(err)
	}

	messages := testMsg(llm.RoleUser, &llm.TextPart{Text: "Run uname -a"})

	events, err := provider.StreamMessages(context.Background(), messages, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}

	var toolCalls []llm.ToolInputPart
	toolNames := make(map[string]string)
	for event := range events {
		if e, ok := event.(llm.ToolInputStartEvent); ok {
			toolNames[e.ID] = e.Name
		}
		if tc, ok := event.(llm.ToolInputCompleteEvent); ok {
			toolCalls = append(toolCalls, llm.ToolInputPart{ID: tc.ID, Name: toolNames[tc.ID], Input: tc.Input})
		}
	}

	if len(toolCalls) != 1 {
		t.Fatalf("Expected 1 tool call, got %d", len(toolCalls))
	}

	if toolCalls[0].Name != "execute_command" {
		t.Errorf("Expected tool name 'execute_command', got '%s'", toolCalls[0].Name)
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

func TestToolCallStreamingWithNullArguments(t *testing.T) {
	// Regression test: some providers send "arguments": null as a no-op chunk.
	// This should be silently ignored, not appended to the accumulated arguments.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		flusher, _ := w.(http.Flusher)

		// First chunk: name + id + index
		fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call-789\",\"type\":\"function\",\"function\":{\"name\":\"read_file\",\"arguments\":\"\"}}]}}]}\n\n")
		// Second chunk: valid arguments
		fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"\",\"function\":{\"arguments\":\"{\\\"path\\\": \\\"README.md\\\"}\"}}]}}]}\n\n")
		// Third chunk: provider sends null as a no-op
		fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"\",\"function\":{\"arguments\":null}}]}}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")

		flusher.Flush()
	}))
	defer server.Close()

	provider, err := providers.NewOpenAI(providers.BaseConfig{
		APIKey:  "test",
		BaseURL: server.URL,
	})
	if err != nil {
		t.Fatal(err)
	}

	messages := testMsg(llm.RoleUser, &llm.TextPart{Text: "Read README"})

	events, err := provider.StreamMessages(context.Background(), messages, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}

	var toolCalls []llm.ToolInputPart
	toolNames := make(map[string]string)
	for event := range events {
		if e, ok := event.(llm.ToolInputStartEvent); ok {
			toolNames[e.ID] = e.Name
		}
		if tc, ok := event.(llm.ToolInputCompleteEvent); ok {
			toolCalls = append(toolCalls, llm.ToolInputPart{ID: tc.ID, Name: toolNames[tc.ID], Input: tc.Input})
		}
	}

	if len(toolCalls) != 1 {
		t.Fatalf("Expected 1 tool call, got %d", len(toolCalls))
	}

	// Verify arguments are valid JSON without trailing "null"
	var args struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(toolCalls[0].Input, &args); err != nil {
		t.Fatalf("Failed to unmarshal tool call arguments: %v (input was: %s)", err, toolCalls[0].Input)
	}
	if args.Path != "README.md" {
		t.Errorf("Expected path 'README.md', got '%s'", args.Path)
	}
}

func TestAnthropicToolCallStreaming(t *testing.T) {
	// Test that tool calls are properly streamed from Anthropic
	server := newMockSSEServer(t, func(w io.Writer) {
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
	})

	provider, err := providers.NewAnthropic(providers.BaseConfig{
		APIKey:  "test",
		BaseURL: server.URL,
	})
	if err != nil {
		t.Fatal(err)
	}

	messages := testMsg(llm.RoleUser, &llm.TextPart{Text: "What's the weather?"})

	events, err := provider.StreamMessages(context.Background(), messages, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}

	var toolCalls []llm.ToolInputPart
	var stepComplete *llm.StepCompleteEvent
	toolNames := make(map[string]string)
	for event := range events {
		if e, ok := event.(llm.ToolInputStartEvent); ok {
			toolNames[e.ID] = e.Name
		}
		if e, ok := event.(llm.ToolInputCompleteEvent); ok {
			toolCalls = append(toolCalls, llm.ToolInputPart{ID: e.ID, Name: toolNames[e.ID], Input: e.Input})
		} else if e, ok := event.(llm.StepCompleteEvent); ok {
			stepComplete = &e
		}
	}

	if len(toolCalls) != 1 {
		t.Fatalf("Expected 1 tool call, got %d", len(toolCalls))
	}

	if toolCalls[0].Name != "get_weather" {
		t.Errorf("Expected tool name 'get_weather', got '%s'", toolCalls[0].Name)
	}

	if toolCalls[0].ID != "toolu_123" {
		t.Errorf("Expected tool call ID 'toolu_123', got '%s'", toolCalls[0].ID)
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

func TestToolInputStartEventOpenAI(t *testing.T) {
	// Verify ToolInputStartEvent is emitted before ToolInputPart for OpenAI.
	// This allows the UI to show a tool window immediately when the tool name
	// is known, before the potentially large arguments finish streaming.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)

		// Chunk 1: tool name + id (empty arguments)
		fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call-999\",\"type\":\"function\",\"function\":{\"name\":\"write_file\",\"arguments\":\"\"}}]}}]}\n\n")
		flusher.Flush()
		// Chunk 2: arguments part 1
		fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"{\\\"path\\\":\\\"/tmp/f.txt\\\",\"}}]}}]}\n\n")
		flusher.Flush()
		// Chunk 3: arguments part 2
		fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"\\\"content\\\":\\\"hello\\\"}\"}}]}}]}\n\n")
		flusher.Flush()
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer server.Close()

	provider, err := providers.NewOpenAI(providers.BaseConfig{
		APIKey:  "test",
		BaseURL: server.URL,
	})
	if err != nil {
		t.Fatal(err)
	}

	events, err := provider.StreamMessages(context.Background(), testMsg(llm.RoleUser, &llm.TextPart{Text: "test"}), nil, "", "")
	if err != nil {
		t.Fatal(err)
	}

	var order []string
	toolNames := make(map[string]string)
	for event := range events {
		switch e := event.(type) {
		case llm.ToolInputStartEvent:
			toolNames[e.ID] = e.Name
			order = append(order, fmt.Sprintf("start(%s)", e.Name))
		case llm.ToolInputCompleteEvent:
			order = append(order, fmt.Sprintf("complete(%s)", toolNames[e.ID]))
		}
	}

	if len(order) < 2 {
		t.Fatalf("Expected at least 2 events (start + complete), got %d: %v", len(order), order)
	}
	if order[0] != "start(write_file)" {
		t.Errorf("Expected first event to be start(write_file), got %s", order[0])
	}
	if order[len(order)-1] != "complete(write_file)" {
		t.Errorf("Expected last event to be complete(write_file), got %s", order[len(order)-1])
	}
}

func TestToolInputStartEventAnthropic(t *testing.T) {
	// Verify ToolInputStartEvent is emitted before ToolInputPart for Anthropic.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)

		fmt.Fprint(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"toolu_abc\",\"name\":\"write_file\"}}\n\n")
		flusher.Flush()
		fmt.Fprint(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"path\\\":\\\"/tmp/f.txt\\\",\"}}\n\n")
		flusher.Flush()
		fmt.Fprint(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"\\\"content\\\":\\\"hello\\\"}\"}}\n\n")
		flusher.Flush()
		fmt.Fprint(w, "event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n")
		flusher.Flush()
		fmt.Fprint(w, "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"},\"usage\":{\"input_tokens\":10,\"output_tokens\":5}}\n\n")
		fmt.Fprint(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
		flusher.Flush()
	}))
	defer server.Close()

	provider, err := providers.NewAnthropic(providers.BaseConfig{
		APIKey:  "test",
		BaseURL: server.URL,
	})
	if err != nil {
		t.Fatal(err)
	}

	events, err := provider.StreamMessages(context.Background(), testMsg(llm.RoleUser, &llm.TextPart{Text: "test"}), nil, "", "")
	if err != nil {
		t.Fatal(err)
	}

	var order []string
	toolNames := make(map[string]string)
	for event := range events {
		switch e := event.(type) {
		case llm.ToolInputStartEvent:
			toolNames[e.ID] = e.Name
			order = append(order, fmt.Sprintf("start(%s)", e.Name))
		case llm.ToolInputCompleteEvent:
			order = append(order, fmt.Sprintf("complete(%s)", toolNames[e.ID]))
		}
	}

	if len(order) < 2 {
		t.Fatalf("Expected at least 2 events (start + complete), got %d: %v", len(order), order)
	}
	if order[0] != "start(write_file)" {
		t.Errorf("Expected first event to be start(write_file), got %s", order[0])
	}
	if order[len(order)-1] != "complete(write_file)" {
		t.Errorf("Expected last event to be complete(write_file), got %s", order[len(order)-1])
	}
}

func TestAnthropicReasoningStreaming(t *testing.T) {
	// Test that reasoning/thinking content is properly streamed
	server := newMockSSEServer(t, func(w io.Writer) {
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
	})

	provider, err := providers.NewAnthropic(providers.BaseConfig{
		APIKey:  "test",
		BaseURL: server.URL,
	})
	if err != nil {
		t.Fatal(err)
	}

	messages := testMsg(llm.RoleUser, &llm.TextPart{Text: "What is the answer?"})

	events, err := provider.StreamMessages(context.Background(), messages, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}

	var reasoningText string
	var textReceived string
	var stepComplete *llm.StepCompleteEvent

	for event := range events {
		if e, ok := event.(llm.TextDeltaEvent); ok {
			textReceived += e.Delta
		} else if e, ok := event.(llm.ReasoningDeltaEvent); ok {
			reasoningText += e.Delta
		} else if e, ok := event.(llm.StepCompleteEvent); ok {
			stepComplete = &e
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
	msg := stepComplete.Contents
	if len(msg) != 2 {
		t.Fatalf("Expected 2 content parts, got %d", len(msg))
	}

	// First should be reasoning
	if reasonPart, ok := msg[0].(*llm.ReasoningPart); !ok {
		t.Error("First content part should be ReasoningPart")
	} else if reasonPart.Text != "Let me think..." {
		t.Errorf("Reasoning text mismatch: %s", reasonPart.Text)
	}

	// Second should be text
	if textPart, ok := msg[1].(*llm.TextPart); !ok {
		t.Error("Second content part should be TextPart")
	} else if textPart.Text != "The answer is 42." {
		t.Errorf("Text mismatch: %s", textPart.Text)
	}
}

func TestAnthropicThinkingOmittedMode(t *testing.T) {
	// Test the "display: omitted" mode where the thinking block has no
	// thinking_delta events — just a signature_delta then immediately stop.
	// Text streaming begins right after the thinking block closes.

	server := newMockSSEServer(t, func(w io.Writer) {
		// Thinking block: no thinking_delta, just stop
		fmt.Fprint(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"thinking\",\"thinking\":\"\"}}\n\n")
		fmt.Fprint(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"signature_delta\",\"signature\":\"abc123\"}}\n\n")
		fmt.Fprint(w, "event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n")

		// Text block streams immediately after
		fmt.Fprint(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")
		fmt.Fprint(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"text_delta\",\"text\":\"The GCD is 21.\"}}\n\n")
		fmt.Fprint(w, "event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":1}\n\n")

		fmt.Fprint(w, "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":10}}\n\n")
		fmt.Fprint(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	})

	provider, err := providers.NewAnthropic(providers.BaseConfig{
		APIKey:  "test",
		BaseURL: server.URL,
	})
	if err != nil {
		t.Fatal(err)
	}

	events, err := provider.StreamMessages(context.Background(), testMsg(llm.RoleUser, &llm.TextPart{Text: "GCD of 1071 and 462?"}), nil, "", "")
	if err != nil {
		t.Fatal(err)
	}

	var (
		reasoningParts int
		textParts      int
	)
	for event := range events {
		if e, ok := event.(llm.StepCompleteEvent); ok {
			for _, part := range e.Contents {
				switch p := part.(type) {
				case *llm.ReasoningPart:
					reasoningParts++
					_ = p
				case *llm.TextPart:
					textParts++
				}
			}
		}
	}

	if reasoningParts != 1 {
		t.Errorf("Expected 1 reasoning part, got %d", reasoningParts)
	}
	if textParts != 1 {
		t.Errorf("Expected 1 text part, got %d", textParts)
	}
}

func TestAnthropicAPIError(t *testing.T) {
	// Test API error handling
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error": {"message": "Invalid API key"}}`))
	}))
	defer server.Close()

	provider, err := providers.NewAnthropic(providers.BaseConfig{
		APIKey:  "invalid-key",
		BaseURL: server.URL,
	})
	if err != nil {
		t.Fatal(err)
	}

	messages := testMsg(llm.RoleUser, &llm.TextPart{Text: "Hi"})

	_, err = provider.StreamMessages(context.Background(), messages, nil, "", "")
	if err == nil {
		t.Error("Expected error for invalid API key")
	}
}

func TestAnthropicRefusalStopReason(t *testing.T) {
	// Test Anthropic refusal stop reason handling
	server := newMockSSEServer(t, func(w io.Writer) {
		// Send message_start
		fmt.Fprint(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_123\",\"role\":\"assistant\",\"content\":[]}}\n\n")
		// Send text content
		fmt.Fprint(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")
		fmt.Fprint(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"I cannot\"}}\n\n")
		// Send refusal stop reason
		fmt.Fprint(w, "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"refusal\"}}\n\n")
	})

	provider, err := providers.NewAnthropic(providers.BaseConfig{
		APIKey:  "test-key",
		BaseURL: server.URL,
	})
	if err != nil {
		t.Fatal(err)
	}

	messages := testMsg(llm.RoleUser, &llm.TextPart{Text: "Hi"})

	events, err := provider.StreamMessages(context.Background(), messages, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}

	// Collect events - should get an error
	var gotError bool
	for _, iterErr := range events {
		if iterErr != nil {
			gotError = true
		}
	}

	if !gotError {
		t.Error("Expected StreamErrorEvent for refusal stop reason")
	}
}

func TestAnthropicUnknownStopReason(t *testing.T) {
	// Test Anthropic unknown stop reason handling
	server := newMockSSEServer(t, func(w io.Writer) {
		// Send message_start
		fmt.Fprint(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_123\",\"role\":\"assistant\",\"content\":[]}}\n\n")
		// Send text content
		fmt.Fprint(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")
		fmt.Fprint(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Some text\"}}\n\n")
		// Send unknown stop reason
		fmt.Fprint(w, "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"unknown_reason\"}}\n\n")
	})

	provider, err := providers.NewAnthropic(providers.BaseConfig{
		APIKey:  "test-key",
		BaseURL: server.URL,
	})
	if err != nil {
		t.Fatal(err)
	}

	messages := testMsg(llm.RoleUser, &llm.TextPart{Text: "Hi"})

	events, err := provider.StreamMessages(context.Background(), messages, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}

	// Collect events - should get an error
	var gotError bool
	for _, iterErr := range events {
		if iterErr != nil {
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
			server := newMockSSEServer(t, func(w io.Writer) {
				// Send message_start
				fmt.Fprint(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_123\",\"role\":\"assistant\",\"content\":[]}}\n\n")
				// Send text content
				fmt.Fprint(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")
				fmt.Fprint(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Response\"}}\n\n")
				fmt.Fprint(w, "event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n")
				// Send valid stop reason
				fmt.Fprintf(w, "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"%s\"},\"usage\":{\"output_tokens\":10}}\n\n", reason)
				fmt.Fprint(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
			})

			provider, err := providers.NewAnthropic(providers.BaseConfig{
				APIKey:  "test-key",
				BaseURL: server.URL,
			})
			if err != nil {
				t.Fatal(err)
			}

			messages := testMsg(llm.RoleUser, &llm.TextPart{Text: "Hi"})

			events, err := provider.StreamMessages(context.Background(), messages, nil, "", "")
			if err != nil {
				t.Fatal(err)
			}

			// Collect events - should NOT get an error
			var gotError bool
			var gotStepComplete bool
			var gotStopReason string
			for event, iterErr := range events {
				if iterErr != nil {
					gotError = true
				}
				if e, ok := event.(llm.StepCompleteEvent); ok {
					gotStepComplete = true
					gotStopReason = e.StopReason
				}
			}

			if gotError {
				t.Errorf("Should not get error for valid stop reason '%s'", reason)
			}
			if !gotStepComplete {
				t.Errorf("Expected StepCompleteEvent for valid stop reason '%s'", reason)
			}
			if gotStopReason != reason {
				t.Errorf("Expected StopReason=%q, got %q", reason, gotStopReason)
			}
		})
	}
}

func TestOpenAIContentFilter(t *testing.T) {
	// Test OpenAI content_filter finish reason handling
	server := newMockSSEServer(t, func(w io.Writer) {
		// Send partial content
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Some content\"}}]}\n\n")
		// Then content_filter
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"\"},\"finish_reason\":\"content_filter\"}]}\n\n")
	})

	provider, err := providers.NewOpenAI(providers.BaseConfig{
		APIKey:  "test-key",
		BaseURL: server.URL,
	})
	if err != nil {
		t.Fatal(err)
	}

	messages := testMsg(llm.RoleUser, &llm.TextPart{Text: "Hi"})

	events, err := provider.StreamMessages(context.Background(), messages, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}

	// Collect events - should get an error
	var gotError bool
	for _, iterErr := range events {
		if iterErr != nil {
			gotError = true
		}
	}

	if !gotError {
		t.Error("Expected StreamErrorEvent for content_filter finish reason")
	}
}

func TestOpenAILengthFinishReason(t *testing.T) {
	// Test OpenAI length finish reason (should NOT error - it's valid)
	server := newMockSSEServer(t, func(w io.Writer) {
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Truncated\"},\"finish_reason\":\"length\"}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	})

	provider, err := providers.NewOpenAI(providers.BaseConfig{
		APIKey:  "test-key",
		BaseURL: server.URL,
	})
	if err != nil {
		t.Fatal(err)
	}

	messages := testMsg(llm.RoleUser, &llm.TextPart{Text: "Hi"})

	events, err := provider.StreamMessages(context.Background(), messages, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}

	// Collect events - should NOT get an error at provider level
	var gotError bool
	var gotStepComplete bool
	var gotStopReason string
	for event, iterErr := range events {
		if iterErr != nil {
			gotError = true
		}
		if e, ok := event.(llm.StepCompleteEvent); ok {
			gotStepComplete = true
			gotStopReason = e.StopReason
		}
	}

	if gotError {
		t.Error("Should not get error for 'length' finish reason at provider level")
	}
	if !gotStepComplete {
		t.Error("Expected StepCompleteEvent for 'length' finish reason")
	}
	if gotStopReason != "length" {
		t.Errorf("Expected StopReason=%q, got %q", "length", gotStopReason)
	}
}

func TestOpenAIAPIError(t *testing.T) {
	// Test OpenAI API error handling
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error": {"message": "Rate limit exceeded"}}`))
	}))
	defer server.Close()

	provider, err := providers.NewOpenAI(providers.BaseConfig{
		APIKey:  "test-key",
		BaseURL: server.URL,
	})
	if err != nil {
		t.Fatal(err)
	}

	messages := testMsg(llm.RoleUser, &llm.TextPart{Text: "Hi"})

	_, err = provider.StreamMessages(context.Background(), messages, nil, "", "")
	if err == nil {
		t.Error("Expected error for rate limit")
	}
}

func TestOpenAINetworkError(t *testing.T) {
	// Test OpenAI network_error finish reason handling
	server := newMockSSEServer(t, func(w io.Writer) {
		// Send some content first
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Partial\"}}]}\n\n")
		// Then send network_error
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"\"},\"finish_reason\":\"network_error\"}]}\n\n")
	})

	provider, err := providers.NewOpenAI(providers.BaseConfig{
		APIKey:  "test-key",
		BaseURL: server.URL,
	})
	if err != nil {
		t.Fatal(err)
	}

	messages := testMsg(llm.RoleUser, &llm.TextPart{Text: "Hi"})

	events, err := provider.StreamMessages(context.Background(), messages, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}

	// Collect events - should get an error
	var gotError bool
	for _, iterErr := range events {
		if iterErr != nil {
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
		var reqBody map[string]any
		if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
			t.Error(err)
			return
		}

		messages, ok := reqBody["messages"].([]any)
		if !ok || len(messages) == 0 {
			t.Error("Expected messages array")
		}

		// First message should be system
		firstMsg, ok := messages[0].(map[string]any)
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

	provider, err := providers.NewOpenAI(providers.BaseConfig{
		APIKey:  "test-key",
		BaseURL: server.URL,
	})
	if err != nil {
		t.Fatal(err)
	}

	messages := testMsg(llm.RoleUser, &llm.TextPart{Text: "Hi"})

	events, err := provider.StreamMessages(context.Background(), messages, nil, "You are helpful", "")
	if err != nil {
		t.Fatal(err)
	}

	for range events {
		// Drain the channel
	}
}

func TestAnthropicWithTools(t *testing.T) {
	// Test that tools are properly sent to Anthropic API
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request body
		var reqBody map[string]any
		if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
			t.Error(err)
			return
		}

		tools, ok := reqBody["tools"].([]any)
		if !ok || len(tools) != 1 {
			t.Error("Expected 1 tool in request")
		}

		tool, ok := tools[0].(map[string]any)
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

	provider, err := providers.NewAnthropic(providers.BaseConfig{
		APIKey:  "test-key",
		BaseURL: server.URL,
	})
	if err != nil {
		t.Fatal(err)
	}

	messages := testMsg(llm.RoleUser, &llm.TextPart{Text: "Use the tool"})

	tools := []llm.ToolDefinition{
		{
			Name:        "test_tool",
			Description: "A test tool",
			Schema:      json.RawMessage(`{"type":"object","properties":{"input":{"type":"string"}}}`),
		},
	}

	events, err := provider.StreamMessages(context.Background(), messages, tools, "", "")
	if err != nil {
		t.Fatal(err)
	}

	for range events {
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

	provider, err := providers.NewOpenAI(providers.BaseConfig{
		APIKey:  "test",
		BaseURL: server.URL,
	})
	if err != nil {
		t.Fatal(err)
	}

	messages := testMsg(llm.RoleUser, &llm.TextPart{Text: "Calculate"})

	events, err := provider.StreamMessages(context.Background(), messages, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}

	var reasoningText string
	var textReceived string
	var stepComplete *llm.StepCompleteEvent

	for event := range events {
		if e, ok := event.(llm.TextDeltaEvent); ok {
			textReceived += e.Delta
		} else if e, ok := event.(llm.ReasoningDeltaEvent); ok {
			reasoningText += e.Delta
		} else if e, ok := event.(llm.StepCompleteEvent); ok {
			stepComplete = &e
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

	msg := stepComplete.Contents
	if len(msg) < 2 {
		t.Fatalf("Expected at least 2 content parts (reasoning + text), got %d", len(msg))
	}

	// First should be reasoning
	if rp, ok := msg[0].(*llm.ReasoningPart); !ok {
		t.Error("First content part should be ReasoningPart")
	} else if rp.Text != "Analyzing... computing..." {
		t.Errorf("Reasoning text mismatch: %s", rp.Text)
	}

	// Second should be text
	if tp, ok := msg[1].(*llm.TextPart); !ok {
		t.Error("Second content part should be TextPart")
	} else if tp.Text != "Result: 123." {
		t.Errorf("Text mismatch: %s", tp.Text)
	}
}

func TestOpenAITextWithToolCallsConversion(t *testing.T) {
	// Test that text content is preserved when converting assistant messages
	// with tool calls back to wire format (for multi-turn tool call chains).
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request body preserves text content alongside tool calls
		var reqBody map[string]any
		if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
			t.Error(err)
			return
		}

		messages, ok := reqBody["messages"].([]any)
		if !ok {
			t.Fatal("Expected messages array")
		}

		// Find the assistant message with tool calls (should be second message)
		var assistantMsg map[string]any
		for _, msg := range messages {
			m, ok := msg.(map[string]any)
			if ok && m["role"] == "assistant" {
				assistantMsg = m
				break
			}
		}

		if assistantMsg == nil {
			t.Fatal("Expected assistant message")
		}

		// Verify text content is preserved
		content, hasContent := assistantMsg["content"]
		if !hasContent || content == nil {
			t.Error("CRITICAL BUG: Assistant message content is nil! Text is lost when tool calls are present")
		} else if contentStr, ok := content.(string); ok {
			if contentStr != "Let me check that." {
				t.Errorf("Expected text 'Let me check that.', got '%s'", contentStr)
			}
		} else {
			t.Errorf("Expected content to be string, got %T", content)
		}

		// Verify tool calls are present
		toolCalls, hasToolCalls := assistantMsg["tool_calls"]
		if !hasToolCalls || toolCalls == nil {
			t.Error("Expected tool_calls to be present")
		}

		// Send minimal response
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Done.\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":5}}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	provider, err := providers.NewOpenAI(providers.BaseConfig{
		APIKey:  "test",
		BaseURL: server.URL,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Simulate a multi-turn conversation where assistant returned text + tool call
	messages := testMsg(llm.RoleUser, &llm.TextPart{Text: "Check the weather"})
	messages = append(messages, testMsg(llm.RoleAssistant,
		&llm.TextPart{Text: "Let me check that."},
		&llm.ToolInputPart{ID: "call_123", Name: "get_weather", Input: json.RawMessage(`{"location":"SF"}`)},
	)...)
	messages = append(messages, testMsg(llm.RoleTool,
		&llm.ToolOutputPart{ID: "call_123", Output: []llm.ContentPart{&llm.TextPart{Text: "Sunny, 72°F"}}},
	)...)

	events, err := provider.StreamMessages(context.Background(), messages, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}

	// Drain events
	for range events {
	}
}

func TestAnthropicToolResultMessageFormat(t *testing.T) {
	// Test that tool result messages are properly formatted for Anthropic API
	// Tool results must be in a "user" role message, not "tool" role
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request body
		var reqBody map[string]any
		if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
			t.Error(err)
			return
		}

		messages, ok := reqBody["messages"].([]any)
		if !ok {
			t.Fatal("Expected messages array")
		}

		// Should have: user message, assistant with tool_use, user with tool_result
		if len(messages) < 3 {
			t.Fatalf("Expected at least 3 messages, got %d", len(messages))
		}

		// Check assistant message has tool_use
		assistantMsg, ok := messages[1].(map[string]any)
		if !ok || assistantMsg["role"] != "assistant" {
			t.Fatal("Expected second message to be assistant")
		}

		// Check tool result message is "user" role (not "tool")
		toolResultMsg, ok := messages[2].(map[string]any)
		if !ok {
			t.Fatal("Expected third message to be an object")
		}
		if toolResultMsg["role"] != "user" {
			t.Errorf("Expected tool result message role to be 'user', got '%v'", toolResultMsg["role"])
		}

		// Check content has tool_result type
		content, ok := toolResultMsg["content"].([]any)
		if !ok || len(content) == 0 {
			t.Fatal("Expected tool result message to have content")
		}
		firstContent, ok := content[0].(map[string]any)
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

	provider, err := providers.NewAnthropic(providers.BaseConfig{
		APIKey:  "test-key",
		BaseURL: server.URL,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Simulate a conversation with tool call and result
	messages := testMsg(llm.RoleUser, &llm.TextPart{Text: "Use the tool"})
	messages = append(messages, testMsg(llm.RoleAssistant,
		&llm.ToolInputPart{ID: "tool-123", Name: "test_tool", Input: json.RawMessage(`{"input": "value"}`)},
	)...)
	messages = append(messages, testMsg(llm.RoleTool,
		&llm.ToolOutputPart{ID: "tool-123", Output: []llm.ContentPart{&llm.TextPart{Text: "Tool executed successfully"}}},
	)...)

	events, err := provider.StreamMessages(context.Background(), messages, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}

	for _, iterErr := range events {
		if iterErr != nil {
			t.Fatalf("Stream error: %v", iterErr)
		}
	}
}

func TestAnthropicMultiToolCall(t *testing.T) {
	// Test multiple tool calls in a single response
	server := newMockSSEServer(t, func(w io.Writer) {
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
	})

	provider, err := providers.NewAnthropic(providers.BaseConfig{
		APIKey:  "test-key",
		BaseURL: server.URL,
	})
	if err != nil {
		t.Fatal(err)
	}

	messages := testMsg(llm.RoleUser, &llm.TextPart{Text: "Get weather for NYC and LA"})

	events, err := provider.StreamMessages(context.Background(), messages, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}

	var toolCalls []llm.ToolInputPart
	toolNames := make(map[string]string)
	for event := range events {
		if e, ok := event.(llm.ToolInputStartEvent); ok {
			toolNames[e.ID] = e.Name
		}
		if e, ok := event.(llm.ToolInputCompleteEvent); ok {
			toolCalls = append(toolCalls, llm.ToolInputPart{ID: e.ID, Name: toolNames[e.ID], Input: e.Input})
		}
	}

	if len(toolCalls) != 2 {
		t.Fatalf("Expected 2 tool calls, got %d", len(toolCalls))
	}

	// Check first tool call
	if toolCalls[0].ID != "tool-1" {
		t.Errorf("Expected tool call ID 'tool-1', got '%s'", toolCalls[0].ID)
	}
	if toolCalls[0].Name != "get_weather" {
		t.Errorf("Expected tool name 'get_weather', got '%s'", toolCalls[0].Name)
	}

	// Check second tool call
	if toolCalls[1].ID != "tool-2" {
		t.Errorf("Expected tool call ID 'tool-2', got '%s'", toolCalls[1].ID)
	}
}

func TestOpenAIToolResultMessageFormat(t *testing.T) {
	// Test that tool result messages are properly formatted for OpenAI API
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request body
		var reqBody map[string]any
		if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
			t.Error(err)
			return
		}

		messages, ok := reqBody["messages"].([]any)
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
			msg, ok := m.(map[string]any)
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

	provider, err := providers.NewOpenAI(providers.BaseConfig{
		APIKey:  "test-key",
		BaseURL: server.URL,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Simulate a conversation with tool call and result
	messages := testMsg(llm.RoleUser, &llm.TextPart{Text: "Use the tool"})
	messages = append(messages, testMsg(llm.RoleAssistant,
		&llm.ToolInputPart{ID: "call-123", Name: "test_tool", Input: json.RawMessage(`{"input": "value"}`)},
	)...)
	messages = append(messages, testMsg(llm.RoleTool,
		&llm.ToolOutputPart{ID: "call-123", Output: []llm.ContentPart{&llm.TextPart{Text: "Tool executed successfully"}}},
	)...)

	events, err := provider.StreamMessages(context.Background(), messages, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}

	for _, iterErr := range events {
		if iterErr != nil {
			t.Fatalf("Stream error: %v", iterErr)
		}
	}
}

func TestOpenAIMultiToolResultMessageFormat(t *testing.T) {
	// Test that multiple tool results in a single message are converted to separate API messages
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request body
		var reqBody map[string]any
		if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
			t.Error(err)
			return
		}

		messages, ok := reqBody["messages"].([]any)
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
			msg, ok := m.(map[string]any)
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

	provider, err := providers.NewOpenAI(providers.BaseConfig{
		APIKey:  "test-key",
		BaseURL: server.URL,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Simulate a conversation with 2 tool calls and 2 results in a single tool message
	messages := testMsg(llm.RoleUser, &llm.TextPart{Text: "Run two tools"})
	messages = append(messages, testMsg(llm.RoleAssistant,
		&llm.ToolInputPart{ID: "call-1", Name: "tool_a", Input: json.RawMessage(`{}`)},
		&llm.ToolInputPart{ID: "call-2", Name: "tool_b", Input: json.RawMessage(`{}`)},
	)...)
	messages = append(messages, testMsg(llm.RoleTool,
		&llm.ToolOutputPart{ID: "call-1", Output: []llm.ContentPart{&llm.TextPart{Text: "Result A"}}},
		&llm.ToolOutputPart{ID: "call-2", Output: []llm.ContentPart{&llm.TextPart{Text: "Result B"}}},
	)...)

	events, err := provider.StreamMessages(context.Background(), messages, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}

	for _, iterErr := range events {
		if iterErr != nil {
			t.Fatalf("Stream error: %v", iterErr)
		}
	}
}

func TestAnthropicToolResultError(t *testing.T) {
	// Test that tool result errors are properly formatted
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request body
		var reqBody map[string]any
		if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
			t.Error(err)
			return
		}

		messages, ok := reqBody["messages"].([]any)
		if !ok {
			t.Fatal("Expected messages array")
		}

		// Find tool result and check is_error field
		for _, m := range messages {
			msg, ok := m.(map[string]any)
			if !ok || msg["role"] != "user" {
				continue
			}
			content, ok := msg["content"].([]any)
			if !ok || len(content) == 0 {
				continue
			}
			block, ok := content[0].(map[string]any)
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

	provider, err := providers.NewAnthropic(providers.BaseConfig{
		APIKey:  "test-key",
		BaseURL: server.URL,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Conversation with error tool result
	messages := testMsg(llm.RoleUser, &llm.TextPart{Text: "Use the tool"})
	messages = append(messages, testMsg(llm.RoleAssistant,
		&llm.ToolInputPart{ID: "tool-123", Name: "test_tool", Input: json.RawMessage(`{}`)},
	)...)
	messages = append(messages, testMsg(llm.RoleTool,
		&llm.ToolOutputPart{ID: "tool-123", Output: []llm.ContentPart{&llm.TextPart{Text: "Something went wrong"}}, IsError: true},
	)...)

	events, err := provider.StreamMessages(context.Background(), messages, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}

	for _, iterErr := range events {
		if iterErr != nil {
			t.Fatalf("Stream error: %v", iterErr)
		}
	}
}

// ============================================================================
// Anthropic output_config tests
// ============================================================================

// TestAnthropicOutputConfigOff verifies that output_config is NOT sent
// when reasoning is disabled (level 0).
func TestAnthropicOutputConfigOff(t *testing.T) {
	var requestBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			t.Error(err)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		fmt.Fprint(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"role\":\"assistant\",\"content\":[]}}\n\n")
		fmt.Fprint(w, "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"}}\n\n")
		fmt.Fprint(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
		flusher.Flush()
	}))
	defer server.Close()

	provider, err := providers.NewAnthropic(providers.BaseConfig{
		APIKey:  "test-key",
		BaseURL: server.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	provider.SetReasoningLevel(0)

	messages := testMsg(llm.RoleUser, &llm.TextPart{Text: "Hi"})
	events, err := provider.StreamMessages(context.Background(), messages, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}
	for range events {
	}

	if requestBody == nil {
		t.Fatal("No request body captured")
	}

	// Should NOT have output_config when reasoning is off
	if _, ok := requestBody["output_config"]; ok {
		t.Error("output_config should NOT be present when reasoning is disabled (level 0)")
	}

	// thinking should be disabled
	thinking, ok := requestBody["thinking"].(map[string]any)
	if !ok {
		t.Fatal("thinking field should be present")
	}
	if thinking["type"] != "disabled" {
		t.Errorf("expected thinking.type=disabled, got %v", thinking["type"])
	}
}

// TestAnthropicOutputConfigNormal verifies output_config.effort is "high"
// when reasoning is set to level 1 (normal).
func TestAnthropicOutputConfigNormal(t *testing.T) {
	var requestBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			t.Error(err)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		fmt.Fprint(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"role\":\"assistant\",\"content\":[]}}\n\n")
		fmt.Fprint(w, "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"}}\n\n")
		fmt.Fprint(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
		flusher.Flush()
	}))
	defer server.Close()

	provider, err := providers.NewAnthropic(providers.BaseConfig{
		APIKey:  "test-key",
		BaseURL: server.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	provider.SetReasoningLevel(1)

	messages := testMsg(llm.RoleUser, &llm.TextPart{Text: "Hi"})
	events, err := provider.StreamMessages(context.Background(), messages, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}
	for range events {
	}

	if requestBody == nil {
		t.Fatal("No request body captured")
	}

	oc, ok := requestBody["output_config"].(map[string]any)
	if !ok {
		t.Fatal("output_config should be present when reasoning is enabled (level 1)")
	}
	if oc["effort"] != "high" {
		t.Errorf("expected output_config.effort='high', got %v", oc["effort"])
	}

	thinking, ok := requestBody["thinking"].(map[string]any)
	if !ok {
		t.Fatal("thinking field should be present")
	}
	if thinking["type"] != "enabled" {
		t.Errorf("expected thinking.type=enabled, got %v", thinking["type"])
	}
}

// TestAnthropicOutputConfigMax verifies output_config.effort is "max"
// when reasoning is set to level 2 (max).
func TestAnthropicOutputConfigMax(t *testing.T) {
	var requestBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			t.Error(err)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		fmt.Fprint(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"role\":\"assistant\",\"content\":[]}}\n\n")
		fmt.Fprint(w, "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"}}\n\n")
		fmt.Fprint(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
		flusher.Flush()
	}))
	defer server.Close()

	provider, err := providers.NewAnthropic(providers.BaseConfig{
		APIKey:  "test-key",
		BaseURL: server.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	provider.SetReasoningLevel(2)

	messages := testMsg(llm.RoleUser, &llm.TextPart{Text: "Hi"})
	events, err := provider.StreamMessages(context.Background(), messages, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}
	for range events {
	}

	if requestBody == nil {
		t.Fatal("No request body captured")
	}

	oc, ok := requestBody["output_config"].(map[string]any)
	if !ok {
		t.Fatal("output_config should be present when reasoning is enabled (level 2)")
	}
	if oc["effort"] != "max" {
		t.Errorf("expected output_config.effort='max', got %v", oc["effort"])
	}

	thinking, ok := requestBody["thinking"].(map[string]any)
	if !ok {
		t.Fatal("thinking field should be present")
	}
	if thinking["type"] != "enabled" {
		t.Errorf("expected thinking.type=enabled, got %v", thinking["type"])
	}
}

// ============================================================================
// OpenAI reasoning_effort tests
// ============================================================================

// TestOpenAIReasoningEffortOff verifies that reasoning_effort is absent
// and thinking.type=disabled when reasoning is disabled (level 0).
func TestOpenAIReasoningEffortOff(t *testing.T) {
	var requestBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			t.Error(err)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"OK\"},\"finish_reason\":\"stop\"}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	provider, err := providers.NewOpenAI(providers.BaseConfig{
		APIKey:  "test-key",
		BaseURL: server.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	provider.SetReasoningLevel(0)

	messages := testMsg(llm.RoleUser, &llm.TextPart{Text: "Hi"})
	events, err := provider.StreamMessages(context.Background(), messages, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}
	for range events {
	}

	if requestBody == nil {
		t.Fatal("No request body captured")
	}

	// Should NOT have reasoning_effort when reasoning is off
	if _, ok := requestBody["reasoning_effort"]; ok {
		t.Error("reasoning_effort should NOT be present when reasoning is disabled (level 0)")
	}

	// thinking should be disabled
	thinking, ok := requestBody["thinking"].(map[string]any)
	if !ok {
		t.Fatal("thinking field should be present")
	}
	if thinking["type"] != "disabled" {
		t.Errorf("expected thinking.type=disabled, got %v", thinking["type"])
	}
}

// TestOpenAIReasoningEffortNormal verifies reasoning_effort="high"
// when reasoning is set to level 1 (normal).
func TestOpenAIReasoningEffortNormal(t *testing.T) {
	var requestBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			t.Error(err)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"OK\"},\"finish_reason\":\"stop\"}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	provider, err := providers.NewOpenAI(providers.BaseConfig{
		APIKey:  "test-key",
		BaseURL: server.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	provider.SetReasoningLevel(1)

	messages := testMsg(llm.RoleUser, &llm.TextPart{Text: "Hi"})
	events, err := provider.StreamMessages(context.Background(), messages, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}
	for range events {
	}

	if requestBody == nil {
		t.Fatal("No request body captured")
	}

	re, ok := requestBody["reasoning_effort"].(string)
	if !ok {
		t.Fatal("reasoning_effort should be present when reasoning is enabled (level 1)")
	}
	if re != "high" {
		t.Errorf("expected reasoning_effort='high', got %q", re)
	}

	thinking, ok := requestBody["thinking"].(map[string]any)
	if !ok {
		t.Fatal("thinking field should be present")
	}
	if thinking["type"] != "enabled" {
		t.Errorf("expected thinking.type=enabled, got %v", thinking["type"])
	}
}

// TestOpenAIReasoningEffortMax verifies reasoning_effort="xhigh"
// when reasoning is set to level 2 (max). OpenAI uses "xhigh" for max
// (unlike Anthropic which uses "max").
func TestOpenAIReasoningEffortMax(t *testing.T) {
	var requestBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			t.Error(err)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"OK\"},\"finish_reason\":\"stop\"}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	provider, err := providers.NewOpenAI(providers.BaseConfig{
		APIKey:  "test-key",
		BaseURL: server.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	provider.SetReasoningLevel(2)

	messages := testMsg(llm.RoleUser, &llm.TextPart{Text: "Hi"})
	events, err := provider.StreamMessages(context.Background(), messages, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}
	for range events {
	}

	if requestBody == nil {
		t.Fatal("No request body captured")
	}

	re, ok := requestBody["reasoning_effort"].(string)
	if !ok {
		t.Fatal("reasoning_effort should be present when reasoning is enabled (level 2)")
	}
	if re != "xhigh" {
		t.Errorf("expected reasoning_effort='xhigh', got %q", re)
	}

	thinking, ok := requestBody["thinking"].(map[string]any)
	if !ok {
		t.Fatal("thinking field should be present")
	}
	if thinking["type"] != "enabled" {
		t.Errorf("expected thinking.type=enabled, got %v", thinking["type"])
	}
}
