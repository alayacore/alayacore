package providers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestAnthropicPromptCacheFullFlow tests that prompt_cache=true results in
// top-level cache_control field in the API request (automatic caching)
func TestAnthropicPromptCacheFullFlow(t *testing.T) {
	var lastRequest []byte

	// Create test server to capture the request
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("Failed to read request body: %v", err)
		}
		lastRequest = body

		// Return SSE stream with minimal response
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":10}}}\n\n"))
		_, _ = w.Write([]byte("event: message_stop\ndata: {}\n\n"))
	}))
	defer server.Close()

	// Create provider with prompt cache enabled
	provider, err := NewAnthropic(
		WithAPIKey("test-key"),
		WithBaseURL(server.URL),
		WithAnthropicModel("claude-3-5-sonnet-20241022"),
		WithPromptCache(true),
	)
	if err != nil {
		t.Fatalf("Failed to create provider: %v", err)
	}

	// Make streaming request
	events, err := provider.StreamMessages(
		context.Background(),
		nil, // messages
		nil, // tools
		"Default system prompt that is long enough to meet the 1024 token minimum requirement for caching to work properly in Anthropic's API",
		"Extra system prompt",
	)
	if err != nil {
		t.Fatalf("Failed to stream messages: %v", err)
	}

	// Drain the channel
	for _, _ = range events {
	}

	// Parse the captured request
	var req map[string]interface{}
	if err := json.Unmarshal(lastRequest, &req); err != nil {
		t.Fatalf("Failed to parse request: %v", err)
	}

	// Verify top-level cache_control exists
	cacheControl, ok := req["cache_control"].(map[string]interface{})
	if !ok {
		t.Fatal("Expected top-level cache_control field for automatic caching")
	}

	if cacheControl["type"] != "ephemeral" {
		t.Errorf("Expected cache_control type 'ephemeral', got %v", cacheControl["type"])
	}

	// Verify system messages do NOT have cache_control (automatic caching handles it)
	system, ok := req["system"].([]interface{})
	if !ok {
		t.Fatal("Expected system to be an array")
	}

	if len(system) != 2 {
		t.Fatalf("Expected 2 system messages, got %d", len(system))
	}

	// Verify system messages don't have individual cache_control
	for i, msg := range system {
		m := msg.(map[string]interface{})
		if _, hasCache := m["cache_control"]; hasCache {
			t.Errorf("System message %d should NOT have cache_control in automatic caching mode", i)
		}
	}

	t.Logf("Request structure verified: top-level cache_control for automatic caching")
}

// TestAnthropicPromptCacheDisabled tests that prompt_cache=false does NOT add cache_control
func TestAnthropicPromptCacheDisabled(t *testing.T) {
	var lastRequest []byte

	// Create test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("Failed to read request body: %v", err)
		}
		lastRequest = body

		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":10}}}\n\n"))
		_, _ = w.Write([]byte("event: message_stop\ndata: {}\n\n"))
	}))
	defer server.Close()

	// Create provider with prompt cache DISABLED
	provider, err := NewAnthropic(
		WithAPIKey("test-key"),
		WithBaseURL(server.URL),
		WithAnthropicModel("claude-3-5-sonnet-20241022"),
		WithPromptCache(false),
	)
	if err != nil {
		t.Fatalf("Failed to create provider: %v", err)
	}

	// Make streaming request
	events, err := provider.StreamMessages(
		context.Background(),
		nil,
		nil,
		"System prompt",
		"",
	)
	if err != nil {
		t.Fatalf("Failed to stream messages: %v", err)
	}

	// Drain the channel
	for _, _ = range events {
	}

	// Parse the captured request
	var req map[string]interface{}
	if err := json.Unmarshal(lastRequest, &req); err != nil {
		t.Fatalf("Failed to parse request: %v", err)
	}

	// Verify NO top-level cache_control
	if _, hasCache := req["cache_control"]; hasCache {
		t.Error("Expected NO top-level cache_control when prompt_cache=false")
	}

	// Verify system messages do NOT have cache_control
	system := req["system"].([]interface{})
	first := system[0].(map[string]interface{})

	if _, hasCache := first["cache_control"]; hasCache {
		t.Error("Expected NO cache_control on system message when prompt_cache=false")
	}

	t.Logf("Verified: no cache_control when prompt_cache=false")
}
