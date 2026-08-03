package mcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"testing"
)

// newTestRequest builds a POST request with a context for EnrichRequest tests.
func newTestRequest(t *testing.T) *http.Request {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "https://example.com/mcp", nil)
	if err != nil {
		t.Fatal(err)
	}
	return req
}

// TestEnrichRequest_McpNameEncoding verifies the Mcp-Name header encoding
// rules: plain ASCII values are sent as-is; values that cannot be safely
// represented as plain ASCII header values use the Base64 sentinel format;
// values that already match the sentinel pattern are re-encoded to avoid
// ambiguity.
func TestEnrichRequest_McpNameEncoding(t *testing.T) {
	tests := []struct {
		name     string
		method   string
		params   string
		wantName string
	}{
		{
			name:     "plain ascii tool name",
			method:   "tools/call",
			params:   `{"name":"get_weather"}`,
			wantName: "get_weather",
		},
		{
			name:     "non-ascii tool name",
			method:   "tools/call",
			params:   `{"name":"获取天气"}`,
			wantName: "=?base64?" + base64.StdEncoding.EncodeToString([]byte("获取天气")) + "?=",
		},
		{
			name:     "resource uri with non-ascii",
			method:   "resources/read",
			params:   `{"uri":"file:///tmp/测试.txt"}`,
			wantName: "=?base64?" + base64.StdEncoding.EncodeToString([]byte("file:///tmp/测试.txt")) + "?=",
		},
		{
			name:     "prompt name with control char",
			method:   "prompts/get",
			params:   `{"name":"line1\nline2"}`,
			wantName: "=?base64?" + base64.StdEncoding.EncodeToString([]byte("line1\nline2")) + "?=",
		},
		{
			name:     "value matching sentinel pattern is re-encoded",
			method:   "tools/call",
			params:   `{"name":"=?base64?literal?="}`,
			wantName: "=?base64?" + base64.StdEncoding.EncodeToString([]byte("=?base64?literal?=")) + "?=",
		},
	}

	adapter := NewAdapterV20260728()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := newTestRequest(t)
			adapter.EnrichRequest(req, tt.method, json.RawMessage(tt.params))
			if got := req.Header.Get("Mcp-Name"); got != tt.wantName {
				t.Errorf("Mcp-Name = %q, want %q", got, tt.wantName)
			}
		})
	}
}

// TestEnrichRequest_McpNameAbsent verifies that methods other than
// tools/call, resources/read, and prompts/get do not set Mcp-Name.
func TestEnrichRequest_McpNameAbsent(t *testing.T) {
	adapter := NewAdapterV20260728()

	req := newTestRequest(t)
	adapter.EnrichRequest(req, "tools/list", json.RawMessage(`{}`))
	if got := req.Header.Get("Mcp-Name"); got != "" {
		t.Errorf("Mcp-Name = %q, want empty for tools/list", got)
	}
}

// TestEnrichRequest_ProtocolVersionHeader verifies MCP-Protocol-Version
// is set on every request and matches the adapter's version.
func TestEnrichRequest_ProtocolVersionHeader(t *testing.T) {
	adapter := NewAdapterV20260728()

	req := newTestRequest(t)
	adapter.EnrichRequest(req, "tools/call", json.RawMessage(`{"name":"get_weather"}`))
	if got := req.Header.Get("MCP-Protocol-Version"); got != protocolVersion20260728 {
		t.Errorf("MCP-Protocol-Version = %q, want %q", got, protocolVersion20260728)
	}
	if got := req.Header.Get("Mcp-Method"); got != "tools/call" {
		t.Errorf("Mcp-Method = %q, want %q", got, "tools/call")
	}
}
