package llm_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/llm/factory"
)

// TestFullIntegration shows complete end-to-end usage
func TestFullIntegration(t *testing.T) {
	// Mock server simulating Anthropic API
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)

		// Simulate streaming response
		fmt.Fprint(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")
		fmt.Fprint(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"I'll help you with that.\"}}\n\n")
		fmt.Fprint(w, "event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n")
		fmt.Fprint(w, "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"input_tokens\":5,\"output_tokens\":10}}\n\n")
		fmt.Fprint(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
		flusher.Flush()
	}))
	defer server.Close()

	// 1. Create provider
	provider, err := factory.NewProvider(factory.ProviderConfig{
		Type:    "anthropic",
		APIKey:  "test-key",
		BaseURL: server.URL,
	})
	if err != nil {
		panic(err)
	}

	// 2. Define tools
	type EchoInput struct {
		Message string `json:"message" jsonschema:"required,description=Message to echo back"`
	}

	echoTool := llm.NewTool("echo", "Echo back a message").
		WithSchema(llm.GenerateSchema(EchoInput{})).
		WithExecute(func(ctx context.Context, input json.RawMessage) (llm.ToolResultOutput, error) {
			var params EchoInput
			if err := json.Unmarshal(input, &params); err != nil {
				return llm.NewTextErrorResponse("invalid input"), nil
			}
			return llm.NewTextResponse(fmt.Sprintf("Echo: %s", params.Message)), nil
		}).
		Build()

	// 3. Create agent
	agent := llm.NewAgent(llm.AgentConfig{
		Provider:     provider,
		Tools:        []llm.Tool{echoTool},
		SystemPrompt: "You are a helpful assistant.",
		MaxSteps:     5,
	})

	// 4. Create conversation
	messages := []llm.Message{
		llm.NewUserMessage("Hello!"),
	}

	// 5. Stream with callbacks
	result, err := agent.Stream(context.Background(), messages, llm.StreamCallbacks{
		OnTextDelta: func(delta string) error {
			fmt.Print(delta)
			return nil
		},
		OnReasoningDelta: func(delta string) error {
			fmt.Printf("[Thinking] %s", delta)
			return nil
		},
		OnToolCall: func(toolCallID, toolName string, input json.RawMessage) error {
			fmt.Printf("\n[Calling tool: %s]\n", toolName)
			return nil
		},
		OnStepStart: func(step int) error {
			fmt.Printf("\n=== Step %d ===\n", step)
			return nil
		},
		OnStepFinish: func(messages []llm.Message, usage llm.Usage) error {
			fmt.Printf("\n[Step complete: %d in, %d out tokens]\n",
				usage.InputTokens, usage.OutputTokens)
			return nil
		},
	})

	if err != nil {
		t.Fatal(err)
	}

	if result.Usage.InputTokens != 5 {
		t.Errorf("Expected 5 input tokens, got %d", result.Usage.InputTokens)
	}

	if len(result.Messages) == 0 {
		t.Error("Expected at least one message")
	}
}

// TestAgentMultiTurnWithTools tests multi-turn conversation with tool calls
// This simulates the session pattern where messages are accumulated across queries
func TestAgentMultiTurnWithTools(t *testing.T) {
	// Track how many times the API is called
	callCount := 0

	// Mock server that returns tool call on first request, text on second
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)

		if callCount == 1 {
			// First call: return tool call
			fmt.Fprint(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"tool-1\",\"name\":\"echo\"}}\n\n")
			fmt.Fprint(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"message\\\":\\\"hello\\\"}\"}}\n\n")
			fmt.Fprint(w, "event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n")
			fmt.Fprint(w, "event: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"input_tokens\":10,\"output_tokens\":20}}\n\n")
			fmt.Fprint(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
		} else {
			// Second call: return text response
			// Verify the request has proper message structure
			var reqBody map[string]interface{}
			if err := json.NewDecoder(r.Body).Decode(&reqBody); err == nil {
				messages, ok := reqBody["messages"].([]interface{})
				if ok {
					// Should have: user, assistant with tool_use, user with tool_result
					if len(messages) < 3 {
						t.Errorf("Expected at least 3 messages on 2nd call, got %d", len(messages))
					}
					// Check that tool result is present with correct role
					for _, m := range messages {
						msg, ok := m.(map[string]interface{})
						if !ok {
							continue
						}
						content, ok := msg["content"].([]interface{})
						if !ok || len(content) == 0 {
							continue
						}
						block, ok := content[0].(map[string]interface{})
						if !ok {
							continue
						}
						if block["type"] == "tool_result" {
							// Tool result should be in a "user" role message for Anthropic
							if msg["role"] != "user" {
								t.Errorf("Tool result message should have role 'user', got '%v'", msg["role"])
							}
						}
					}
				}
			}

			fmt.Fprint(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")
			fmt.Fprint(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Done!\"}}\n\n")
			fmt.Fprint(w, "event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n")
			fmt.Fprint(w, "event: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"input_tokens\":30,\"output_tokens\":5}}\n\n")
			fmt.Fprint(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
		}
		flusher.Flush()
	}))
	defer server.Close()

	provider, err := factory.NewProvider(factory.ProviderConfig{
		Type:    "anthropic",
		APIKey:  "test-key",
		BaseURL: server.URL,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Define echo tool
	echoTool := llm.Tool{
		Definition: llm.ToolDefinition{
			Name:        "echo",
			Description: "Echo back a message",
			Schema:      json.RawMessage(`{"type":"object","properties":{"message":{"type":"string"}},"required":["message"]}`),
		},
		Execute: func(ctx context.Context, input json.RawMessage) (llm.ToolResultOutput, error) {
			return llm.ToolResultOutputText{Type: "text", Text: "Echoed!"}, nil
		},
	}

	agent := llm.NewAgent(llm.AgentConfig{
		Provider:     provider,
		Tools:        []llm.Tool{echoTool},
		SystemPrompt: "You are helpful.",
		MaxSteps:     5,
	})

	// First query - will trigger tool call
	allMessages := []llm.Message{
		{Role: llm.RoleUser, Content: []llm.ContentPart{llm.TextPart{Type: "text", Text: "Use echo"}}},
	}

	result, err := agent.Stream(context.Background(), allMessages, llm.StreamCallbacks{})
	if err != nil {
		t.Fatalf("First query failed: %v", err)
	}

	// Verify result has correct message structure
	if len(result.Messages) != 4 {
		t.Errorf("Expected 4 messages (user, assistant with tool, tool result, assistant response), got %d", len(result.Messages))
	}

	// Check message structure
	// [0] = user message
	// [1] = assistant with tool_use
	// [2] = tool result (role: tool)
	// [3] = assistant text response
	if result.Messages[0].Role != llm.RoleUser {
		t.Error("First message should be user")
	}
	if result.Messages[1].Role != llm.RoleAssistant {
		t.Error("Second message should be assistant")
	}
	if result.Messages[2].Role != llm.RoleTool {
		t.Error("Third message should be tool result")
	}
	if result.Messages[3].Role != llm.RoleAssistant {
		t.Error("Fourth message should be assistant")
	}

	// Now use result.Messages for a second query (simulating session behavior)
	allMessages = result.Messages
	allMessages = append(allMessages, llm.Message{
		Role:    llm.RoleUser,
		Content: []llm.ContentPart{llm.TextPart{Type: "text", Text: "Thanks!"}},
	})

	// Second query - should NOT fail with "tool call result does not follow tool call"
	_, err = agent.Stream(context.Background(), allMessages, llm.StreamCallbacks{})
	if err != nil {
		t.Fatalf("Second query failed (this is the bug we're testing): %v", err)
	}

	// Should have made 3 API calls: tool call, tool response, second query
	if callCount != 3 {
		t.Errorf("Expected 3 API calls, got %d", callCount)
	}
}

// TestAgentMultiTurnSequentialTools tests multiple sequential queries each with tool calls
func TestAgentMultiTurnSequentialTools(t *testing.T) {
	callCount := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)

		// Every call returns a tool call
		fmt.Fprintf(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"tool-%d\",\"name\":\"echo\"}}\n\n", callCount)
		fmt.Fprint(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{}\"}}\n\n")
		fmt.Fprint(w, "event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n")
		fmt.Fprint(w, "event: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"input_tokens\":10,\"output_tokens\":20}}\n\n")
		fmt.Fprint(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
		flusher.Flush()
	}))
	defer server.Close()

	provider, err := factory.NewProvider(factory.ProviderConfig{
		Type:    "anthropic",
		APIKey:  "test-key",
		BaseURL: server.URL,
	})
	if err != nil {
		t.Fatal(err)
	}

	echoTool := llm.Tool{
		Definition: llm.ToolDefinition{
			Name:        "echo",
			Description: "Echo",
			Schema:      json.RawMessage(`{"type":"object","properties":{}}`),
		},
		Execute: func(ctx context.Context, input json.RawMessage) (llm.ToolResultOutput, error) {
			return llm.ToolResultOutputText{Type: "text", Text: "ok"}, nil
		},
	}

	agent := llm.NewAgent(llm.AgentConfig{
		Provider: provider,
		Tools:    []llm.Tool{echoTool},
		MaxSteps: 5,
	})

	// First query
	allMessages := []llm.Message{
		{Role: llm.RoleUser, Content: []llm.ContentPart{llm.TextPart{Type: "text", Text: "Query 1"}}},
	}

	result, err := agent.Stream(context.Background(), allMessages, llm.StreamCallbacks{})
	if err != nil {
		t.Fatalf("Query 1 failed: %v", err)
	}

	// Second query using accumulated messages
	allMessages = result.Messages
	allMessages = append(allMessages, llm.Message{
		Role:    llm.RoleUser,
		Content: []llm.ContentPart{llm.TextPart{Type: "text", Text: "Query 2"}}},
	)

	result, err = agent.Stream(context.Background(), allMessages, llm.StreamCallbacks{})
	if err != nil {
		t.Fatalf("Query 2 failed: %v", err)
	}

	// Third query
	allMessages = result.Messages
	allMessages = append(allMessages, llm.Message{
		Role:    llm.RoleUser,
		Content: []llm.ContentPart{llm.TextPart{Type: "text", Text: "Query 3"}}},
	)

	_, err = agent.Stream(context.Background(), allMessages, llm.StreamCallbacks{})
	if err != nil {
		t.Fatalf("Query 3 failed: %v", err)
	}

	// Each query makes 2 API calls: tool call, then tool response
	// Query 1: 2 calls
	// Query 2: 2 calls (adds to accumulated messages)
	// Query 3: 2 calls (adds to accumulated messages)
	// Total: 6 calls... but actually agent keeps looping because tool keeps getting called
	// Let's just verify we got at least 3 queries worth
	if callCount < 6 {
		t.Errorf("Expected at least 6 API calls (3 queries * 2 calls each), got %d", callCount)
	}
}

// TestOpenAIMultiTurnWithTools tests multi-turn conversation with tool calls for OpenAI-compatible APIs
// This simulates the session pattern where messages are accumulated across queries
func TestOpenAIMultiTurnWithTools(t *testing.T) {
	callCount := 0

	// Mock server that returns tool call on first request, text on second
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)

		if callCount == 1 {
			// First call: return tool call
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call-123\",\"type\":\"function\",\"function\":{\"name\":\"echo\",\"arguments\":\"{\\\"message\\\":\\\"hello\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n")
			fmt.Fprint(w, "data: [DONE]\n\n")
		} else {
			// Second call: verify request has proper message structure
			var reqBody map[string]interface{}
			if err := json.NewDecoder(r.Body).Decode(&reqBody); err == nil {
				messages, ok := reqBody["messages"].([]interface{})
				if ok {
					// Should have: user, assistant with tool_calls, tool result
					if len(messages) < 3 {
						t.Errorf("Expected at least 3 messages on 2nd call, got %d", len(messages))
					}
					// Check that tool result is present with correct role
					for _, m := range messages {
						msg, ok := m.(map[string]interface{})
						if !ok {
							continue
						}
						// Tool result should have role "tool" for OpenAI
						if msg["role"] == "tool" {
							if msg["tool_call_id"] == nil {
								t.Error("Tool result message should have tool_call_id")
							}
						}
					}
				}
			}

			// Return text response
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Done!\"},\"finish_reason\":\"stop\"}]}\n\n")
			fmt.Fprint(w, "data: [DONE]\n\n")
		}
		flusher.Flush()
	}))
	defer server.Close()

	provider, err := factory.NewProvider(factory.ProviderConfig{
		Type:    "openai",
		APIKey:  "test-key",
		BaseURL: server.URL,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Define echo tool
	echoTool := llm.Tool{
		Definition: llm.ToolDefinition{
			Name:        "echo",
			Description: "Echo back a message",
			Schema:      json.RawMessage(`{"type":"object","properties":{"message":{"type":"string"}},"required":["message"]}`),
		},
		Execute: func(ctx context.Context, input json.RawMessage) (llm.ToolResultOutput, error) {
			return llm.ToolResultOutputText{Type: "text", Text: "Echoed!"}, nil
		},
	}

	agent := llm.NewAgent(llm.AgentConfig{
		Provider:     provider,
		Tools:        []llm.Tool{echoTool},
		SystemPrompt: "You are helpful.",
		MaxSteps:     5,
	})

	// First query - will trigger tool call
	allMessages := []llm.Message{
		{Role: llm.RoleUser, Content: []llm.ContentPart{llm.TextPart{Type: "text", Text: "Use echo"}}},
	}

	result, err := agent.Stream(context.Background(), allMessages, llm.StreamCallbacks{})
	if err != nil {
		t.Fatalf("First query failed: %v", err)
	}

	// Verify result has correct message structure
	if len(result.Messages) != 4 {
		t.Errorf("Expected 4 messages (user, assistant with tool, tool result, assistant response), got %d", len(result.Messages))
	}

	// Check message structure
	// [0] = user message
	// [1] = assistant with tool_use
	// [2] = tool result (role: tool)
	// [3] = assistant text response
	if result.Messages[0].Role != llm.RoleUser {
		t.Error("First message should be user")
	}
	if result.Messages[1].Role != llm.RoleAssistant {
		t.Error("Second message should be assistant")
	}
	if result.Messages[2].Role != llm.RoleTool {
		t.Error("Third message should be tool result")
	}
	if result.Messages[3].Role != llm.RoleAssistant {
		t.Error("Fourth message should be assistant")
	}

	// Now use result.Messages for a second query (simulating session behavior)
	allMessages = result.Messages
	allMessages = append(allMessages, llm.Message{
		Role:    llm.RoleUser,
		Content: []llm.ContentPart{llm.TextPart{Type: "text", Text: "Thanks!"}},
	})

	// Second query - should NOT fail with message order errors
	_, err = agent.Stream(context.Background(), allMessages, llm.StreamCallbacks{})
	if err != nil {
		t.Fatalf("Second query failed (this is the bug we're testing): %v", err)
	}

	// Should have made 3 API calls: tool call, tool response, second query
	if callCount != 3 {
		t.Errorf("Expected 3 API calls, got %d", callCount)
	}
}

// TestOpenAISequentialQueriesWithTools tests multiple sequential queries each with tool calls
func TestOpenAISequentialQueriesWithTools(t *testing.T) {
	callCount := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)

		// Every call returns a tool call
		fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call-%d\",\"type\":\"function\",\"function\":{\"name\":\"echo\",\"arguments\":\"{}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n", callCount)
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer server.Close()

	provider, err := factory.NewProvider(factory.ProviderConfig{
		Type:    "openai",
		APIKey:  "test-key",
		BaseURL: server.URL,
	})
	if err != nil {
		t.Fatal(err)
	}

	echoTool := llm.Tool{
		Definition: llm.ToolDefinition{
			Name:        "echo",
			Description: "Echo",
			Schema:      json.RawMessage(`{"type":"object","properties":{}}`),
		},
		Execute: func(ctx context.Context, input json.RawMessage) (llm.ToolResultOutput, error) {
			return llm.ToolResultOutputText{Type: "text", Text: "ok"}, nil
		},
	}

	agent := llm.NewAgent(llm.AgentConfig{
		Provider: provider,
		Tools:    []llm.Tool{echoTool},
		MaxSteps: 5,
	})

	// First query
	allMessages := []llm.Message{
		{Role: llm.RoleUser, Content: []llm.ContentPart{llm.TextPart{Type: "text", Text: "Query 1"}}},
	}

	result, err := agent.Stream(context.Background(), allMessages, llm.StreamCallbacks{})
	if err != nil {
		t.Fatalf("Query 1 failed: %v", err)
	}

	// Second query using accumulated messages
	allMessages = result.Messages
	allMessages = append(allMessages, llm.Message{
		Role:    llm.RoleUser,
		Content: []llm.ContentPart{llm.TextPart{Type: "text", Text: "Query 2"}},
	})

	result, err = agent.Stream(context.Background(), allMessages, llm.StreamCallbacks{})
	if err != nil {
		t.Fatalf("Query 2 failed: %v", err)
	}

	// Third query
	allMessages = result.Messages
	allMessages = append(allMessages, llm.Message{
		Role:    llm.RoleUser,
		Content: []llm.ContentPart{llm.TextPart{Type: "text", Text: "Query 3"}},
	})

	_, err = agent.Stream(context.Background(), allMessages, llm.StreamCallbacks{})
	if err != nil {
		t.Fatalf("Query 3 failed: %v", err)
	}

	// Each query makes 2 API calls: tool call, then tool response
	// Query 1: 2 calls
	// Query 2: 2 calls (adds to accumulated messages)
	// Query 3: 2 calls (adds to accumulated messages)
	// Total: 6 calls... but actually agent keeps looping because tool keeps getting called
	// Let's just verify we got at least 3 queries worth
	if callCount < 6 {
		t.Errorf("Expected at least 6 API calls (3 queries * 2 calls each), got %d", callCount)
	}
}
