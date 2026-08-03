package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// xMcpHeaderTestServer returns a server that answers tools/list with one
// valid tool and one tool carrying an invalid x-mcp-header annotation
// (a `number`-typed parameter, which the spec forbids).
func xMcpHeaderTestServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := jsonrpcResponse{
			JSONRPC: "2.0",
			ID:      requestID("1"),
			Result: mustMarshal(ListToolsResult{
				ResultType: "complete",
				Tools: []Tool{
					{
						Name:        "good_tool",
						InputSchema: json.RawMessage(`{"type":"object","properties":{"region":{"type":"string","x-mcp-header":"Region"}}}`),
					},
					{
						Name:        "bad_tool",
						InputSchema: json.RawMessage(`{"type":"object","properties":{"a":{"type":"number","x-mcp-header":"Num"}}}`),
					},
				},
			}),
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
}

// newListToolsClient builds a Ready client over the given server URL with
// the given adapter and HTTP transport.
func newListToolsClient(t *testing.T, serverURL string, adapter Adapter) *Client {
	t.Helper()
	client := NewClient(ServerConfig{Name: "test", URL: serverURL})
	client.adapter = adapter
	tr := NewHTTPTransport(serverURL, "")
	tr.SetHTTPAdapter(adapter.(HTTPAdapter))
	client.storeTransport(tr)
	client.state.Store(int32(StateReady))
	return client
}

// TestClientListTools_RejectsInvalidXMcpHeader verifies that the
// 2026-07-28 client over Streamable HTTP excludes tools whose
// x-mcp-header annotations violate the spec constraints.
func TestClientListTools_RejectsInvalidXMcpHeader(t *testing.T) {
	srv := xMcpHeaderTestServer()
	defer srv.Close()

	client := newListToolsClient(t, srv.URL, NewAdapterV20260728())

	tools, err := client.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("ListTools returned %d tools, want 1 (invalid tool must be excluded)", len(tools))
	}
	if tools[0].Name != "good_tool" {
		t.Errorf("tool = %q, want %q", tools[0].Name, "good_tool")
	}
	if _, ok := client.toolsCache["bad_tool"]; ok {
		t.Error("bad_tool must not be in the tools cache")
	}
	if _, ok := client.toolsCache["good_tool"]; !ok {
		t.Error("good_tool must be in the tools cache")
	}
}

// TestClientListTools_LegacyKeepsInvalidHeaderTools verifies that legacy
// protocol versions do not reject tools based on x-mcp-header annotations
// (the annotation is ignored entirely for those versions).
func TestClientListTools_LegacyKeepsInvalidHeaderTools(t *testing.T) {
	srv := xMcpHeaderTestServer()
	defer srv.Close()

	client := newListToolsClient(t, srv.URL, NewAdapterV20251125())

	tools, err := client.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("ListTools returned %d tools, want 2 (legacy versions do not reject)", len(tools))
	}
}
