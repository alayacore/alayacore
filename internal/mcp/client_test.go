package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// mockTransport implements Transport for testing.
type mockTransport struct {
	mu        sync.Mutex
	requests  []jsonrpcRequest
	responses []json.RawMessage
	index     atomic.Int32
	done      chan struct{}
	debug     bytes.Buffer
	// sendErr, when set, is what Send reports: the transport-level failure a
	// debug log is supposed to record without failing the handshake.
	sendErr error
}

func newMockTransport(responses []json.RawMessage) *mockTransport {
	return &mockTransport{
		responses: responses,
		done:      make(chan struct{}),
	}
}

func (m *mockTransport) Send(ctx context.Context, req jsonrpcRequest) error {
	_ = ctx
	m.mu.Lock()
	m.requests = append(m.requests, req)
	m.mu.Unlock()
	return m.sendErr
}

// DebugWriter returns the captured debug log, so a test can read back what the
// client recorded. Always non-nil: debug logging is what these tests exercise.
func (m *mockTransport) DebugWriter() io.Writer { return m }

// Write makes the mock itself the log sink; the mutex keeps it race-free when a
// transport goroutine writes while a test reads.
func (m *mockTransport) Write(p []byte) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.debug.Write(p)
}

func (m *mockTransport) debugLog() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.debug.String()
}

func (m *mockTransport) SendReceive(ctx context.Context, req jsonrpcRequest) (json.RawMessage, error) {
	// Store the request like Send does.
	m.mu.Lock()
	m.requests = append(m.requests, req)
	idx := int(m.index.Add(1) - 1)
	if idx >= len(m.responses) {
		m.mu.Unlock()
		<-ctx.Done()
		return nil, ctx.Err()
	}
	raw := m.responses[idx]
	m.mu.Unlock()

	// Unwrap JSON-RPC envelope to extract the result.
	var resp jsonrpcResponse
	if err := json.Unmarshal(raw, &resp); err == nil {
		if resp.Error != nil {
			return nil, &RPCError{
				Code:    resp.Error.Code,
				Message: resp.Error.Message,
				Data:    resp.Error.Data,
			}
		}
		return resp.Result, nil
	}
	return raw, nil
}

func (m *mockTransport) Receive(ctx context.Context) (json.RawMessage, error) {
	idx := int(m.index.Add(1) - 1)
	if idx >= len(m.responses) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return m.responses[idx], nil
}

func (m *mockTransport) Close() error {
	close(m.done)
	return nil
}

func (m *mockTransport) Done() <-chan struct{} {
	return m.done
}

func TestClientInitialize(t *testing.T) {
	// Mock MCP initialize response.
	initResult := InitializeResult{
		ProtocolVersion: "2025-11-25",
		Capabilities: ServerCapabilities{
			Tools: &ServerToolCapabilities{},
		},
		ServerInfo: ImplementationInfo{
			Name:    "test-server",
			Version: "1.0.0",
		},
	}
	initData, _ := json.Marshal(jsonrpcResponse{
		JSONRPC: "2.0",
		ID:      requestID("1"),
		Result:  mustMarshal(initResult),
	})

	client := NewClient(ServerConfig{Name: "test"})
	client.adapter = NewAdapterV20251125()
	client.storeTransport(newMockTransport([]json.RawMessage{initData}))

	ctx := context.Background()
	if _, err := client.adapter.Handshake(ctx, client); err != nil {
		t.Fatalf("doInitialize() error = %v", err)
	}

	if client.serverInfo.Name != "test-server" {
		t.Errorf("ServerInfo.Name = %q, want %q", client.serverInfo.Name, "test-server")
	}
	if client.capabilities.Tools == nil {
		t.Error("capabilities.Tools is nil, expected non-nil")
	}
}

func TestClientListTools(t *testing.T) {
	toolsResult := ListToolsResult{
		Tools: []Tool{
			{Name: "tool1", Description: "First tool", InputSchema: json.RawMessage(`{"type":"object"}`)},
			{Name: "tool2", Description: "Second tool", InputSchema: json.RawMessage(`{"type":"object"}`)},
		},
	}
	toolsData, _ := json.Marshal(jsonrpcResponse{
		JSONRPC: "2.0", ID: requestID("2"),
		Result: mustMarshal(toolsResult),
	})

	client := NewClient(ServerConfig{Name: "test"})
	client.state.Store(int32(StateReady))
	client.storeTransport(newMockTransport([]json.RawMessage{toolsData}))

	// Need to set init state to skip initialize for this test.
	client.capabilities.Tools = &ServerToolCapabilities{}

	ctx := context.Background()
	tools, err := client.ListTools(ctx)
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}

	if len(tools) != 2 {
		t.Fatalf("got %d tools, want 2", len(tools))
	}
	if tools[0].Name != "tool1" {
		t.Errorf("tools[0].Name = %q, want %q", tools[0].Name, "tool1")
	}
	if tools[1].Name != "tool2" {
		t.Errorf("tools[1].Name = %q, want %q", tools[1].Name, "tool2")
	}
}

func TestClientCallTool(t *testing.T) {
	callResult := CallToolResult{
		Content: []ToolContent{
			{Type: "text", Text: "result data"},
		},
	}
	callData, _ := json.Marshal(jsonrpcResponse{
		JSONRPC: "2.0", ID: requestID("1"),
		Result: mustMarshal(callResult),
	})

	client := NewClient(ServerConfig{Name: "test"})
	client.state.Store(int32(StateReady))
	client.storeTransport(newMockTransport([]json.RawMessage{callData}))

	ctx := context.Background()
	result, err := client.CallTool(ctx, "my_tool", json.RawMessage(`{"arg":"val"}`))
	if err != nil {
		t.Fatalf("CallTool() error = %v", err)
	}

	if len(result.Content) != 1 {
		t.Fatalf("Content length = %d, want 1", len(result.Content))
	}
	if result.Content[0].Text != "result data" {
		t.Errorf("Content[0].Text = %q, want %q", result.Content[0].Text, "result data")
	}
}

func TestClientCallTool_ResultTypeInputRequired(t *testing.T) {
	// Server returns resultType: "input_required" — client must reject.
	resp := jsonrpcResponse{
		JSONRPC: "2.0",
		ID:      requestID("1"),
		Result:  json.RawMessage(`{"resultType":"input_required","inputRequests":[],"content":[]}`),
	}
	data, _ := json.Marshal(resp)

	client := NewClient(ServerConfig{Name: "test"})
	client.adapter = NewAdapterV20260728()
	client.state.Store(int32(StateReady))
	client.storeTransport(newMockTransport([]json.RawMessage{data}))

	_, err := client.CallTool(context.Background(), "my_tool", nil)
	if err == nil {
		t.Fatal("expected error for resultType=input_required, got nil")
	}
	if !strings.Contains(err.Error(), "input_required") {
		t.Errorf("error should mention input_required, got: %v", err)
	}
	if !strings.Contains(err.Error(), "multi-round-trip") {
		t.Errorf("error should mention multi-round-trip, got: %v", err)
	}
}

func TestClientCallTool_ResultTypeUnrecognized(t *testing.T) {
	// Server returns unknown resultType — client must reject.
	resp := jsonrpcResponse{
		JSONRPC: "2.0",
		ID:      requestID("1"),
		Result:  json.RawMessage(`{"resultType":"foobar","content":[{"type":"text","text":"hi"}]}`),
	}
	data, _ := json.Marshal(resp)

	client := NewClient(ServerConfig{Name: "test"})
	client.adapter = NewAdapterV20260728()
	client.state.Store(int32(StateReady))
	client.storeTransport(newMockTransport([]json.RawMessage{data}))

	_, err := client.CallTool(context.Background(), "my_tool", nil)
	if err == nil {
		t.Fatal("expected error for unrecognized resultType, got nil")
	}
	if !strings.Contains(err.Error(), "foobar") {
		t.Errorf("error should mention foobar, got: %v", err)
	}
}

func TestClientCallTool_ResultTypeComplete(t *testing.T) {
	// Server returns resultType: "complete" — normal success.
	resp := jsonrpcResponse{
		JSONRPC: "2.0",
		ID:      requestID("1"),
		Result:  json.RawMessage(`{"resultType":"complete","content":[{"type":"text","text":"ok"}]}`),
	}
	data, _ := json.Marshal(resp)

	client := NewClient(ServerConfig{Name: "test"})
	client.adapter = NewAdapterV20260728()
	client.state.Store(int32(StateReady))
	client.storeTransport(newMockTransport([]json.RawMessage{data}))

	result, err := client.CallTool(context.Background(), "my_tool", nil)
	if err != nil {
		t.Fatalf("CallTool() error = %v", err)
	}
	if len(result.Content) != 1 || result.Content[0].Text != "ok" {
		t.Errorf("unexpected result: %+v", result)
	}
}

func TestClientStateTransitions(t *testing.T) {
	client := NewClient(ServerConfig{Name: "test"})

	if client.State() != StateDisconnected {
		t.Errorf("initial state = %d, want %d", client.State(), StateDisconnected)
	}

	// Close without connecting.
	if err := client.Close(); err != nil {
		t.Errorf("Close() error = %v", err)
	}
}

func TestManagerEmpty(t *testing.T) {
	m := NewManager(nil)
	if m == nil {
		t.Fatal("NewManager(nil) returned nil")
	}

	if n := m.ActiveServerCount(); n != 0 {
		t.Errorf("ActiveServerCount() = %d, want 0", n)
	}

	if c := m.Clients(); len(c) != 0 {
		t.Errorf("Clients() = %d, want 0", len(c))
	}

	m.CloseAll()
}

func TestClientInitialize_VersionMismatch(t *testing.T) {
	// Server responds with a different protocol version — client must disconnect.
	initResult := InitializeResult{
		ProtocolVersion: "2024-11-05", // Different from our "2025-11-25"
		Capabilities: ServerCapabilities{
			Tools: &ServerToolCapabilities{},
		},
		ServerInfo: ImplementationInfo{
			Name:    "old-server",
			Version: "1.0.0",
		},
	}
	initData, _ := json.Marshal(jsonrpcResponse{
		JSONRPC: "2.0",
		ID:      requestID("1"),
		Result:  mustMarshal(initResult),
	})

	client := NewClient(ServerConfig{Name: "test"})
	client.adapter = NewAdapterV20251125()
	client.storeTransport(newMockTransport([]json.RawMessage{initData}))

	ctx := context.Background()
	_, err := client.adapter.Handshake(ctx, client)
	if err == nil {
		t.Fatal("expected error for version mismatch, got nil")
	}
	if !strings.Contains(err.Error(), "protocol version mismatch") {
		t.Errorf("expected 'protocol version mismatch' error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "2024-11-05") {
		t.Errorf("expected server version in error, got: %v", err)
	}
}

func TestToolsToAgentTools(t *testing.T) {
	// Create a manager (not connected, just for tool adapter).
	m := NewManager([]ServerConfig{
		{Name: "srv1", Command: "echo", Args: []string{"hello"}},
	})

	serverTools := map[string][]Tool{
		"srv1": {
			{Name: "greet", Description: "Say hello", InputSchema: json.RawMessage(`{"type":"object"}`)},
			{Name: "farewell", Description: "Say goodbye", InputSchema: json.RawMessage(`{"type":"object"}`)},
		},
	}

	agentTools := ToolsToAgentTools(serverTools, m)
	if len(agentTools) != 2 {
		t.Fatalf("got %d tools, want 2", len(agentTools))
	}

	// Check naming with prefix strategy.
	expectedNames := []string{"srv1_greet", "srv1_farewell"}
	for i, name := range expectedNames {
		if agentTools[i].Definition.Name != name {
			t.Errorf("agentTools[%d].Name = %q, want %q", i, agentTools[i].Definition.Name, name)
		}
	}

	// Check descriptions include server name.
	for _, tool := range agentTools {
		if tool.Definition.Description == "" {
			t.Errorf("tool %q has empty description", tool.Definition.Name)
		}
	}
}

// mustMarshal is a test helper that marshals v to JSON or panics.
func mustMarshal(v any) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return data
}

func TestSanitizeInputSchema_Valid(t *testing.T) {
	tests := []struct {
		name   string
		schema json.RawMessage
	}{
		{"empty object", json.RawMessage(`{"type":"object"}`)},
		{"with properties", json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"}},"required":["name"]}`)},
		{"with nested properties", json.RawMessage(`{"type":"object","properties":{"addr":{"type":"object","properties":{"city":{"type":"string"}}}}}`)},
		{"with array items", json.RawMessage(`{"type":"object","properties":{"tags":{"type":"array","items":{"type":"string"}}}}`)},
		{"with allOf", json.RawMessage(`{"type":"object","allOf":[{"type":"object"},{"required":["name"]}]}`)},
		{"with local $ref", json.RawMessage(`{"type":"object","properties":{"user":{"$ref":"#/$defs/User"}},"$defs":{"User":{"type":"object"}}}`)},
		{"with anyOf", json.RawMessage(`{"type":"object","properties":{"id":{"anyOf":[{"type":"string"},{"type":"integer"}]}}}`)},
		{"with additionalProperties", json.RawMessage(`{"type":"object","additionalProperties":{"type":"string"}}`)},
		{"no params (recommended)", json.RawMessage(`{"type":"object","additionalProperties":false}`)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := sanitizeInputSchema(tt.schema)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			// Must return the original bytes unchanged.
			if string(result) != string(tt.schema) {
				t.Errorf("returned schema differs from input:\ngot:  %s\nwant: %s", string(result), string(tt.schema))
			}
		})
	}
}

func TestSanitizeInputSchema_Invalid(t *testing.T) {
	tests := []struct {
		name        string
		schema      json.RawMessage
		containsErr string
	}{
		{
			name:        "empty schema",
			schema:      json.RawMessage(``),
			containsErr: "empty",
		},
		{
			name:        "null schema",
			schema:      json.RawMessage(`null`),
			containsErr: "root must be a JSON object",
		},
		{
			name:        "array schema",
			schema:      json.RawMessage(`["a","b"]`),
			containsErr: "root must be a JSON object",
		},
		{
			name:        "string schema",
			schema:      json.RawMessage(`"hello"`),
			containsErr: "root must be a JSON object",
		},
		{
			name:        "number schema",
			schema:      json.RawMessage(`42`),
			containsErr: "root must be a JSON object",
		},
		{
			name:        "external http $ref at root",
			schema:      json.RawMessage(`{"type":"object","$ref":"http://evil.com/schema"}`),
			containsErr: "external",
		},
		{
			name:        "external https $ref in property",
			schema:      json.RawMessage(`{"type":"object","properties":{"x":{"$ref":"https://internal.corp/schema"}}}`),
			containsErr: "external",
		},
		{
			name:        "external $ref in items",
			schema:      json.RawMessage(`{"type":"object","properties":{"items":{"type":"array","items":{"$ref":"http://attacker.net/s"}}}}`),
			containsErr: "external",
		},
		{
			name:        "external $ref in allOf",
			schema:      json.RawMessage(`{"type":"object","allOf":[{"$ref":"https://malicious.io/s"}]}`),
			containsErr: "external",
		},
		{
			name:        "external $ref in anyOf",
			schema:      json.RawMessage(`{"type":"object","anyOf":[{"type":"object"},{"$ref":"http://evil/s"}]}`),
			containsErr: "external",
		},
		{
			name:        "external $ref in oneOf",
			schema:      json.RawMessage(`{"type":"object","oneOf":[{"$ref":"http://evil/s"}]}`),
			containsErr: "external",
		},
		{
			name:        "external $ref in if/then",
			schema:      json.RawMessage(`{"type":"object","if":{"$ref":"http://evil/s"},"then":{"type":"object"}}`),
			containsErr: "external",
		},
		{
			name:        "external $ref in $defs",
			schema:      json.RawMessage(`{"$defs":{"X":{"$ref":"http://evil/s"}},"type":"object"}`),
			containsErr: "external",
		},
		{
			name:        "root type is string instead of object",
			schema:      json.RawMessage(`{"type":"string"}`),
			containsErr: "root type must be \"object\"",
		},
		{
			name:        "root type is number instead of object",
			schema:      json.RawMessage(`{"type":"number"}`),
			containsErr: "root type must be \"object\"",
		},
		{
			name:        "root has no type field",
			schema:      json.RawMessage(`{"properties":{"x":{"type":"string"}}}`),
			containsErr: "must have a 'type' field",
		},
		{
			name:        "root type is non-string value",
			schema:      json.RawMessage(`{"type":["object","string"]}`),
			containsErr: "root type must be \"object\"",
		},
		{
			name:        "deeply nested schema exceeds limit",
			schema:      buildDeepSchema(21),
			containsErr: "exceeds maximum depth",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := sanitizeInputSchema(tt.schema)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.containsErr) {
				t.Errorf("error %q does not contain %q", err.Error(), tt.containsErr)
			}
		})
	}
}

// buildDeepSchema creates a JSON Schema with nested depth levels of
// {"next":{"type":"object",...}} for testing depth limits.
func buildDeepSchema(depth int) json.RawMessage {
	if depth <= 0 {
		return json.RawMessage(`{"type":"object"}`)
	}
	inner := buildDeepSchema(depth - 1)
	data := fmt.Sprintf(`{"type":"object","properties":{"nested":%s}}`, string(inner))
	return json.RawMessage(data)
}

func TestSanitizeInputSchema_WithXMcpHeader(t *testing.T) {
	// x-mcp-header is a JSON Schema extension property.
	// It should be safely ignored (not rejected) by the sanitizer.
	schema := json.RawMessage(`{
		"type":"object",
		"properties":{
			"region":{
				"type":"string",
				"x-mcp-header":"Region"
			}
		},
		"required":["region"]
	}`)
	_, err := sanitizeInputSchema(schema)
	if err != nil {
		t.Fatalf("schema with x-mcp-header should be accepted, got error: %v", err)
	}
}

func TestSanitizeInputSchema_ExternalRefInEnum(t *testing.T) {
	// $ref values that are local (JSON pointer) should be accepted.
	schema := json.RawMessage(`{
		"type":"object",
		"properties":{
			"status":{
				"enum":["active","inactive"],
				"$ref":"#/$defs/Status"
			}
		}
	}`)
	_, err := sanitizeInputSchema(schema)
	if err != nil {
		t.Fatalf("local $ref should be accepted, got error: %v", err)
	}
}

func TestSanitizeInputSchema_NotRef(t *testing.T) {
	// "not" keyword should be traversed for $ref checking.
	schema := json.RawMessage(`{
		"type":"object",
		"not":{"$ref":"http://evil.com/schema"}
	}`)
	_, err := sanitizeInputSchema(schema)
	if err == nil {
		t.Fatal("expected error for external $ref in not, got nil")
	}
	if !strings.Contains(err.Error(), "external") {
		t.Errorf("error %q should mention external $ref", err.Error())
	}
}

func TestSanitizeInputSchema_AdditionalPropertiesObj(t *testing.T) {
	// additionalProperties as an object should be traversed.
	schema := json.RawMessage(`{
		"type":"object",
		"additionalProperties":{"$ref":"http://evil.com/s"}
	}`)
	_, err := sanitizeInputSchema(schema)
	if err == nil {
		t.Fatal("expected error for external $ref in additionalProperties, got nil")
	}
}

func TestToolsToAgentTools_SkipsInvalidSchema(t *testing.T) {
	m := NewManager([]ServerConfig{
		{Name: "srv1", Command: "echo", Args: []string{"hello"}},
	})

	serverTools := map[string][]Tool{
		"srv1": {
			{Name: "good", Description: "Valid tool", InputSchema: json.RawMessage(`{"type":"object"}`)},
			{Name: "bad", Description: "Invalid schema", InputSchema: json.RawMessage(`null`)},
			{Name: "evil", Description: "External ref", InputSchema: json.RawMessage(`{"$ref":"http://evil.com/s"}`)},
			{Name: "deep", Description: "Too deep", InputSchema: buildDeepSchema(21)},
			{Name: "also_good", Description: "Another valid tool", InputSchema: json.RawMessage(`{"type":"object","properties":{"x":{"type":"string"}}}`)},
		},
	}

	agentTools := ToolsToAgentTools(serverTools, m)
	if len(agentTools) != 2 {
		t.Fatalf("expected 2 valid tools (good, also_good), got %d", len(agentTools))
	}
	if agentTools[0].Definition.Name != "srv1_good" {
		t.Errorf("first tool name = %q, want %q", agentTools[0].Definition.Name, "srv1_good")
	}
	if agentTools[1].Definition.Name != "srv1_also_good" {
		t.Errorf("second tool name = %q, want %q", agentTools[1].Definition.Name, "srv1_also_good")
	}
}
