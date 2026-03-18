// Package providers_test contains integration tests for LLM providers.
// These tests require real API credentials and are skipped by default.
// Run with: go test -tags=integration ./internal/llm/providers/...
// Or set the environment variable RUN_INTEGRATION_TESTS=1
package providers_test

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/llm/factory"
)

var runIntegration = flag.Bool("integration", false, "run integration tests with real APIs")

func skipIfNoIntegration(t *testing.T) {
	if !*runIntegration && os.Getenv("RUN_INTEGRATION_TESTS") == "" {
		t.Skip("Skipping integration test. Use -integration flag or set RUN_INTEGRATION_TESTS=1")
	}
}

// TestAnthropicRealAPI tests the Anthropic-compatible provider with real API
func TestAnthropicRealAPI(t *testing.T) {
	skipIfNoIntegration(t)

	baseURL := os.Getenv("ANTHROPIC_TEST_BASE_URL")
	apiKey := os.Getenv("ANTHROPIC_TEST_API_KEY")
	model := os.Getenv("ANTHROPIC_TEST_MODEL")

	if baseURL == "" || apiKey == "" {
		t.Skip("Set ANTHROPIC_TEST_BASE_URL and ANTHROPIC_TEST_API_KEY to run this test")
	}

	if model == "" {
		model = "claude-3-5-sonnet-20241022"
	}

	provider, err := factory.NewProvider(factory.ProviderConfig{
		Type:    "anthropic",
		APIKey:  apiKey,
		BaseURL: baseURL,
		Model:   model,
	})
	if err != nil {
		t.Fatalf("Failed to create provider: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	messages := []llm.Message{
		{Role: llm.RoleUser, Content: []llm.ContentPart{llm.TextPart{Type: "text", Text: "Say 'Hello, world!' and nothing else."}}},
	}

	eventChan, err := provider.StreamMessages(ctx, messages, nil, "You are a helpful assistant. Be very brief.", "")
	if err != nil {
		t.Fatalf("Failed to stream messages: %v", err)
	}

	var textReceived string
	var stepComplete *llm.StepCompleteEvent

	for event := range eventChan {
		switch e := event.(type) {
		case llm.TextDeltaEvent:
			textReceived += e.Delta
		case llm.StepCompleteEvent:
			stepComplete = &e
		case llm.StreamErrorEvent:
			t.Fatalf("Stream error: %v", e.Error)
		}
	}

	if textReceived == "" {
		t.Error("Expected to receive some text")
	}

	t.Logf("Received text: %q", textReceived)

	if stepComplete == nil {
		t.Error("Expected StepCompleteEvent")
	} else {
		t.Logf("Usage: input=%d, output=%d", stepComplete.Usage.InputTokens, stepComplete.Usage.OutputTokens)
	}
}

// TestOpenAICompatibleRealAPI tests an OpenAI-compatible provider with real API
func TestOpenAICompatibleRealAPI(t *testing.T) {
	skipIfNoIntegration(t)

	baseURL := os.Getenv("OPENAI_COMPAT_TEST_BASE_URL")
	apiKey := os.Getenv("OPENAI_COMPAT_TEST_API_KEY")
	model := os.Getenv("OPENAI_COMPAT_TEST_MODEL")

	if baseURL == "" {
		t.Skip("Set OPENAI_COMPAT_TEST_BASE_URL to run this test")
	}

	provider, err := factory.NewProvider(factory.ProviderConfig{
		Type:    "openai",
		APIKey:  apiKey,
		BaseURL: baseURL,
		Model:   model,
	})
	if err != nil {
		t.Fatalf("Failed to create provider: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	messages := []llm.Message{
		{Role: llm.RoleUser, Content: []llm.ContentPart{llm.TextPart{Type: "text", Text: "Say 'Hello!' and nothing else."}}},
	}

	eventChan, err := provider.StreamMessages(ctx, messages, nil, "", "")
	if err != nil {
		t.Fatalf("Failed to stream messages: %v", err)
	}

	var textReceived string

	for event := range eventChan {
		switch e := event.(type) {
		case llm.TextDeltaEvent:
			textReceived += e.Delta
		case llm.StreamErrorEvent:
			t.Fatalf("Stream error: %v", e.Error)
		}
	}

	if textReceived == "" {
		t.Error("Expected to receive some text")
	}

	t.Logf("Received text: %q", textReceived)
}

// TestAnthropicRealToolCall tests tool calling with a real Anthropic-compatible API
func TestAnthropicRealToolCall(t *testing.T) {
	skipIfNoIntegration(t)

	baseURL := os.Getenv("ANTHROPIC_TEST_BASE_URL")
	apiKey := os.Getenv("ANTHROPIC_TEST_API_KEY")
	model := os.Getenv("ANTHROPIC_TEST_MODEL")

	if baseURL == "" || apiKey == "" {
		t.Skip("Set ANTHROPIC_TEST_BASE_URL and ANTHROPIC_TEST_API_KEY to run this test")
	}

	if model == "" {
		model = "claude-3-5-sonnet-20241022"
	}

	provider, err := factory.NewProvider(factory.ProviderConfig{
		Type:    "anthropic",
		APIKey:  apiKey,
		BaseURL: baseURL,
		Model:   model,
	})
	if err != nil {
		t.Fatalf("Failed to create provider: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Define a simple echo tool
	tools := []llm.ToolDefinition{
		{
			Name:        "echo",
			Description: "Echo back a message",
			Schema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"message": {"type": "string", "description": "Message to echo"}
				},
				"required": ["message"]
			}`),
		},
	}

	messages := []llm.Message{
		{Role: llm.RoleUser, Content: []llm.ContentPart{llm.TextPart{Type: "text", Text: "Use the echo tool to say 'test123'"}}},
	}

	eventChan, err := provider.StreamMessages(ctx, messages, tools, "You are a helpful assistant. Use tools when requested.", "")
	if err != nil {
		t.Fatalf("Failed to stream messages: %v", err)
	}

	var toolCalls []llm.ToolCallEvent
	var textReceived string

	for event := range eventChan {
		switch e := event.(type) {
		case llm.TextDeltaEvent:
			textReceived += e.Delta
		case llm.ToolCallEvent:
			toolCalls = append(toolCalls, e)
		case llm.StreamErrorEvent:
			t.Fatalf("Stream error: %v", e.Error)
		}
	}

	t.Logf("Text received: %q", textReceived)

	if len(toolCalls) == 0 {
		t.Error("Expected at least one tool call")
	} else {
		for _, tc := range toolCalls {
			t.Logf("Tool call: %s(%s)", tc.ToolName, string(tc.Input))
			if tc.ToolName != "echo" {
				t.Errorf("Expected tool name 'echo', got '%s'", tc.ToolName)
			}
		}
	}
}

// TestOpenAIRealAPI tests the OpenAI provider with real API
func TestOpenAIRealAPI(t *testing.T) {
	skipIfNoIntegration(t)

	baseURL := os.Getenv("OPENAI_TEST_BASE_URL")
	apiKey := os.Getenv("OPENAI_TEST_API_KEY")
	model := os.Getenv("OPENAI_TEST_MODEL")

	if baseURL == "" || apiKey == "" {
		t.Skip("Set OPENAI_TEST_BASE_URL and OPENAI_TEST_API_KEY to run this test")
	}

	if model == "" {
		model = "gpt-4o"
	}

	provider, err := factory.NewProvider(factory.ProviderConfig{
		Type:    "openai",
		APIKey:  apiKey,
		BaseURL: baseURL,
		Model:   model,
	})
	if err != nil {
		t.Fatalf("Failed to create provider: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	messages := []llm.Message{
		{Role: llm.RoleUser, Content: []llm.ContentPart{llm.TextPart{Type: "text", Text: "Say 'Hello, world!' and nothing else."}}},
	}

	eventChan, err := provider.StreamMessages(ctx, messages, nil, "You are a helpful assistant. Be very brief.", "")
	if err != nil {
		t.Fatalf("Failed to stream messages: %v", err)
	}

	var textReceived string

	for event := range eventChan {
		switch e := event.(type) {
		case llm.TextDeltaEvent:
			textReceived += e.Delta
		case llm.StreamErrorEvent:
			t.Fatalf("Stream error: %v", e.Error)
		}
	}

	if textReceived == "" {
		t.Error("Expected to receive some text")
	}

	t.Logf("Received text: %q", textReceived)
}

// TestAgentToolLoopRealAPI tests the full agent tool-calling loop with a real API
func TestAgentToolLoopRealAPI(t *testing.T) {
	skipIfNoIntegration(t)

	baseURL := os.Getenv("ANTHROPIC_TEST_BASE_URL")
	apiKey := os.Getenv("ANTHROPIC_TEST_API_KEY")
	model := os.Getenv("ANTHROPIC_TEST_MODEL")

	if baseURL == "" || apiKey == "" {
		t.Skip("Set ANTHROPIC_TEST_BASE_URL and ANTHROPIC_TEST_API_KEY to run this test")
	}

	if model == "" {
		model = "claude-3-5-sonnet-20241022"
	}

	provider, err := factory.NewProvider(factory.ProviderConfig{
		Type:    "anthropic",
		APIKey:  apiKey,
		BaseURL: baseURL,
		Model:   model,
	})
	if err != nil {
		t.Fatalf("Failed to create provider: %v", err)
	}

	// Define an echo tool
	echoTool := llm.Tool{
		Definition: llm.ToolDefinition{
			Name:        "echo",
			Description: "Echo back a message",
			Schema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"message": {"type": "string", "description": "Message to echo"}
				},
				"required": ["message"]
			}`),
		},
		Execute: func(ctx context.Context, input json.RawMessage) (llm.ToolResultOutput, error) {
			var params struct {
				Message string `json:"message"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return llm.ToolResultOutputError{Type: "error", Error: "invalid input"}, nil
			}
			return llm.ToolResultOutputText{Type: "text", Text: "Echo: " + params.Message}, nil
		},
	}

	// Create agent
	agent := llm.NewAgent(llm.AgentConfig{
		Provider:     provider,
		Tools:        []llm.Tool{echoTool},
		SystemPrompt: "You are a helpful assistant. When asked to use a tool, use it exactly as requested.",
		MaxSteps:     5,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	messages := []llm.Message{
		{Role: llm.RoleUser, Content: []llm.ContentPart{llm.TextPart{Type: "text", Text: "Use the echo tool to say 'hello world' and then tell me what the tool returned."}}},
	}

	var textReceived strings.Builder
	var toolCalls []string

	result, err := agent.Stream(ctx, messages, llm.StreamCallbacks{
		OnTextDelta: func(delta string) error {
			textReceived.WriteString(delta)
			return nil
		},
		OnToolCall: func(toolCallID, toolName string, input json.RawMessage) error {
			toolCalls = append(toolCalls, fmt.Sprintf("%s(%s)", toolName, string(input)))
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Agent stream failed: %v", err)
	}

	t.Logf("Text received: %q", textReceived.String())
	t.Logf("Tool calls: %v", toolCalls)
	t.Logf("Result messages: %d", len(result.Messages))
	t.Logf("Usage: input=%d, output=%d", result.Usage.InputTokens, result.Usage.OutputTokens)

	if len(toolCalls) == 0 {
		t.Error("Expected at least one tool call")
	}

	// Check that echo was called with the right message
	found := false
	for _, tc := range toolCalls {
		if strings.Contains(tc, "echo") && strings.Contains(tc, "hello world") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Expected echo tool call with 'hello world', got: %v", toolCalls)
	}

	// Check that the response mentions the tool result
	if !strings.Contains(strings.ToLower(textReceived.String()), "echo") {
		t.Logf("Warning: Response doesn't mention echo result, but this may be model behavior")
	}
}

// TestAgentMultiToolLoopRealAPI tests multiple sequential tool calls with a real API
func TestAgentMultiToolLoopRealAPI(t *testing.T) {
	skipIfNoIntegration(t)

	baseURL := os.Getenv("ANTHROPIC_TEST_BASE_URL")
	apiKey := os.Getenv("ANTHROPIC_TEST_API_KEY")
	model := os.Getenv("ANTHROPIC_TEST_MODEL")

	if baseURL == "" || apiKey == "" {
		t.Skip("Set ANTHROPIC_TEST_BASE_URL and ANTHROPIC_TEST_API_KEY to run this test")
	}

	if model == "" {
		model = "claude-3-5-sonnet-20241022"
	}

	provider, err := factory.NewProvider(factory.ProviderConfig{
		Type:    "anthropic",
		APIKey:  apiKey,
		BaseURL: baseURL,
		Model:   model,
	})
	if err != nil {
		t.Fatalf("Failed to create provider: %v", err)
	}

	// Define counter tool that increments
	counter := 0
	counterTool := llm.Tool{
		Definition: llm.ToolDefinition{
			Name:        "increment_counter",
			Description: "Increment a counter and return the new value",
			Schema: json.RawMessage(`{
				"type": "object",
				"properties": {},
				"required": []
			}`),
		},
		Execute: func(ctx context.Context, input json.RawMessage) (llm.ToolResultOutput, error) {
			counter++
			return llm.ToolResultOutputText{Type: "text", Text: fmt.Sprintf("Counter is now %d", counter)}, nil
		},
	}

	// Create agent
	agent := llm.NewAgent(llm.AgentConfig{
		Provider:     provider,
		Tools:        []llm.Tool{counterTool},
		SystemPrompt: "You are a helpful assistant. When asked to increment, call the increment_counter tool exactly as many times as requested.",
		MaxSteps:     10,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	messages := []llm.Message{
		{Role: llm.RoleUser, Content: []llm.ContentPart{llm.TextPart{Type: "text", Text: "Please increment the counter 3 times, then tell me the final value."}}},
	}

	var textReceived strings.Builder
	var toolCalls []string

	result, err := agent.Stream(ctx, messages, llm.StreamCallbacks{
		OnTextDelta: func(delta string) error {
			textReceived.WriteString(delta)
			return nil
		},
		OnToolCall: func(toolCallID, toolName string, input json.RawMessage) error {
			toolCalls = append(toolCalls, fmt.Sprintf("%s(%s)", toolName, string(input)))
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Agent stream failed: %v", err)
	}

	t.Logf("Text received: %q", textReceived.String())
	t.Logf("Tool calls: %v", toolCalls)
	t.Logf("Result messages: %d", len(result.Messages))
	t.Logf("Usage: input=%d, output=%d", result.Usage.InputTokens, result.Usage.OutputTokens)

	if len(toolCalls) < 3 {
		t.Errorf("Expected at least 3 tool calls, got %d", len(toolCalls))
	}

	// Check that counter reached 3
	if !strings.Contains(textReceived.String(), "3") {
		t.Logf("Warning: Response doesn't mention final counter value 3")
	}
}

// TestAgentSequentialQueriesWithTools tests multiple sequential queries with tool calls
// This reproduces the issue where the 2nd query fails after a tool call in the 1st query
func TestAgentSequentialQueriesWithTools(t *testing.T) {
	skipIfNoIntegration(t)

	baseURL := os.Getenv("ANTHROPIC_TEST_BASE_URL")
	apiKey := os.Getenv("ANTHROPIC_TEST_API_KEY")
	model := os.Getenv("ANTHROPIC_TEST_MODEL")

	if baseURL == "" || apiKey == "" {
		t.Skip("Set ANTHROPIC_TEST_BASE_URL and ANTHROPIC_TEST_API_KEY to run this test")
	}

	if model == "" {
		model = "claude-3-5-sonnet-20241022"
	}

	provider, err := factory.NewProvider(factory.ProviderConfig{
		Type:    "anthropic",
		APIKey:  apiKey,
		BaseURL: baseURL,
		Model:   model,
	})
	if err != nil {
		t.Fatalf("Failed to create provider: %v", err)
	}

	// Define an echo tool
	echoTool := llm.Tool{
		Definition: llm.ToolDefinition{
			Name:        "echo",
			Description: "Echo back a message",
			Schema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"message": {"type": "string", "description": "Message to echo"}
				},
				"required": ["message"]
			}`),
		},
		Execute: func(ctx context.Context, input json.RawMessage) (llm.ToolResultOutput, error) {
			var params struct {
				Message string `json:"message"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return llm.ToolResultOutputError{Type: "error", Error: "invalid input"}, nil
			}
			return llm.ToolResultOutputText{Type: "text", Text: "Echo: " + params.Message}, nil
		},
	}

	// Create agent
	agent := llm.NewAgent(llm.AgentConfig{
		Provider:     provider,
		Tools:        []llm.Tool{echoTool},
		SystemPrompt: "You are a helpful assistant.",
		MaxSteps:     10,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// First query - uses tool
	allMessages := []llm.Message{
		{Role: llm.RoleUser, Content: []llm.ContentPart{llm.TextPart{Type: "text", Text: "Use the echo tool to say 'first query'"}}},
	}

	var toolCalls []string
	result, err := agent.Stream(ctx, allMessages, llm.StreamCallbacks{
		OnToolCall: func(toolCallID, toolName string, input json.RawMessage) error {
			toolCalls = append(toolCalls, fmt.Sprintf("%s(%s)", toolName, string(input)))
			return nil
		},
	})
	if err != nil {
		t.Fatalf("First query failed: %v", err)
	}

	t.Logf("First query - Tool calls: %v", toolCalls)
	t.Logf("First query - Result messages: %d", len(result.Messages))

	if len(toolCalls) == 0 {
		t.Error("First query should have made a tool call")
	}

	// Use the returned messages for the next query
	allMessages = result.Messages

	// Second query - should also work
	allMessages = append(allMessages, llm.Message{
		Role:    llm.RoleUser,
		Content: []llm.ContentPart{llm.TextPart{Type: "text", Text: "Now use the echo tool to say 'second query'"}},
	})

	toolCalls = nil
	result, err = agent.Stream(ctx, allMessages, llm.StreamCallbacks{
		OnToolCall: func(toolCallID, toolName string, input json.RawMessage) error {
			toolCalls = append(toolCalls, fmt.Sprintf("%s(%s)", toolName, string(input)))
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Second query failed: %v", err)
	}

	t.Logf("Second query - Tool calls: %v", toolCalls)
	t.Logf("Second query - Result messages: %d", len(result.Messages))

	// Third query - simple text, no tool
	allMessages = result.Messages
	allMessages = append(allMessages, llm.Message{
		Role:    llm.RoleUser,
		Content: []llm.ContentPart{llm.TextPart{Type: "text", Text: "What is 2+2? Just answer with the number."}},
	})

	var textReceived strings.Builder
	_, err = agent.Stream(ctx, allMessages, llm.StreamCallbacks{
		OnTextDelta: func(delta string) error {
			textReceived.WriteString(delta)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Third query failed: %v", err)
	}

	t.Logf("Third query - Text received: %q", textReceived.String())
}

// TestOpenAICompatSequentialQueriesWithTools tests multiple sequential queries with tool calls
// using an OpenAI-compatible API
func TestOpenAICompatSequentialQueriesWithTools(t *testing.T) {
	skipIfNoIntegration(t)

	baseURL := os.Getenv("OPENAI_COMPAT_TEST_BASE_URL")
	apiKey := os.Getenv("OPENAI_COMPAT_TEST_API_KEY")
	model := os.Getenv("OPENAI_COMPAT_TEST_MODEL")

	if baseURL == "" {
		t.Skip("Set OPENAI_COMPAT_TEST_BASE_URL to run this test")
	}

	provider, err := factory.NewProvider(factory.ProviderConfig{
		Type:    "openai",
		APIKey:  apiKey,
		BaseURL: baseURL,
		Model:   model,
	})
	if err != nil {
		t.Fatalf("Failed to create provider: %v", err)
	}

	// Define an echo tool
	echoTool := llm.Tool{
		Definition: llm.ToolDefinition{
			Name:        "echo",
			Description: "Echo back a message",
			Schema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"message": {"type": "string", "description": "Message to echo"}
				},
				"required": ["message"]
			}`),
		},
		Execute: func(ctx context.Context, input json.RawMessage) (llm.ToolResultOutput, error) {
			var params struct {
				Message string `json:"message"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return llm.ToolResultOutputError{Type: "error", Error: "invalid input"}, nil
			}
			return llm.ToolResultOutputText{Type: "text", Text: "Echo: " + params.Message}, nil
		},
	}

	// Create agent
	agent := llm.NewAgent(llm.AgentConfig{
		Provider:     provider,
		Tools:        []llm.Tool{echoTool},
		SystemPrompt: "You are a helpful assistant.",
		MaxSteps:     10,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// First query - uses tool
	allMessages := []llm.Message{
		{Role: llm.RoleUser, Content: []llm.ContentPart{llm.TextPart{Type: "text", Text: "Use the echo tool to say 'first query'"}}},
	}

	var toolCalls []string
	result, err := agent.Stream(ctx, allMessages, llm.StreamCallbacks{
		OnToolCall: func(toolCallID, toolName string, input json.RawMessage) error {
			toolCalls = append(toolCalls, fmt.Sprintf("%s(%s)", toolName, string(input)))
			return nil
		},
	})
	if err != nil {
		t.Fatalf("First query failed: %v", err)
	}

	t.Logf("First query - Tool calls: %v", toolCalls)
	t.Logf("First query - Result messages: %d", len(result.Messages))

	if len(toolCalls) == 0 {
		t.Error("First query should have made a tool call")
	}

	// Use the returned messages for the next query
	allMessages = result.Messages

	// Second query - should also work
	allMessages = append(allMessages, llm.Message{
		Role:    llm.RoleUser,
		Content: []llm.ContentPart{llm.TextPart{Type: "text", Text: "Now use the echo tool to say 'second query'"}},
	})

	toolCalls = nil
	result, err = agent.Stream(ctx, allMessages, llm.StreamCallbacks{
		OnToolCall: func(toolCallID, toolName string, input json.RawMessage) error {
			toolCalls = append(toolCalls, fmt.Sprintf("%s(%s)", toolName, string(input)))
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Second query failed: %v", err)
	}

	t.Logf("Second query - Tool calls: %v", toolCalls)
	t.Logf("Second query - Result messages: %d", len(result.Messages))

	// Third query - simple text, no tool
	allMessages = result.Messages
	allMessages = append(allMessages, llm.Message{
		Role:    llm.RoleUser,
		Content: []llm.ContentPart{llm.TextPart{Type: "text", Text: "What is 2+2? Just answer with the number."}},
	})

	var textReceived strings.Builder
	_, err = agent.Stream(ctx, allMessages, llm.StreamCallbacks{
		OnTextDelta: func(delta string) error {
			textReceived.WriteString(delta)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Third query failed: %v", err)
	}

	t.Logf("Third query - Text received: %q", textReceived.String())
}
