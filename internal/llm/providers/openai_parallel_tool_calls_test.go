package providers_test

// `parallel_tool_calls` is a Chat Completions field, and the one place the
// option is spelled positively: model.conf and every layer of alayacore spell it
// `serial_tool_calls`, whose absent form is the historical behavior. These tests
// pin the three rules that decide when the field appears: it is on every request
// that carries tools (never omitted, so a server never holds the mode by default),
// it carries the configured mode inverted exactly once, and it never appears on a
// protocol that has no such field.

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

// oneTool is enough to make the request a tool request.
func oneTool() []llm.ToolDefinition {
	return []llm.ToolDefinition{{Name: "read_file", Schema: json.RawMessage(`{"type":"object"}`)}}
}

// postOpenAI sends one request through a capturing server and returns the body
// the provider built. The response is a plain text turn: only the request is
// under test here.
func postOpenAI(t *testing.T, cfg providers.BaseConfig, tools []llm.ToolDefinition) map[string]any {
	t.Helper()

	bodyCh := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var got map[string]any
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			bodyCh <- nil
			t.Errorf("decoding request body: %v", err)
			return
		}
		bodyCh <- got

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	cfg.APIKey = "test-key"
	cfg.BaseURL = server.URL
	provider, err := providers.NewOpenAI(cfg)
	if err != nil {
		t.Fatal(err)
	}

	events, err := provider.StreamMessages(context.Background(),
		testMsg(llm.RoleUser, &llm.TextPart{Text: "hi"}), tools, "", "")
	if err != nil {
		t.Fatalf("StreamMessages: %v", err)
	}
	for range events {
	}

	select {
	case got := <-bodyCh:
		if got == nil {
			t.Fatal("no request body captured")
		}
		return got
	default:
		t.Fatal("the provider never reached the server")
		return nil
	}
}

// The mode is stated, not left to the endpoint. Unconfigured means parallel,
// and the request says so out loud rather than relying on a server default that
// varies between deployments (some OpenAI-compatible servers default this to
// off, which is invisible from here and reads as "the option does nothing").
func TestOpenAIRequestAlwaysCarriesParallelToolCalls(t *testing.T) {
	tools := oneTool()

	parallel := postOpenAI(t, providers.BaseConfig{}, tools)
	if got, ok := parallel["parallel_tool_calls"]; !ok {
		t.Error("field absent by default; it must be sent on every request with tools")
	} else if got != true {
		t.Errorf("default parallel_tool_calls = %#v, want true", got)
	}

	serial := postOpenAI(t, providers.BaseConfig{SerialToolCalls: true}, tools)
	if got, ok := serial["parallel_tool_calls"]; !ok {
		t.Error("field absent when serial is configured; the server cannot be expected to guess")
	} else if got != false {
		t.Errorf("configured parallel_tool_calls = %#v, want false", got)
	}
}

// With no tools there is nothing to batch, so the field has no meaning — and a
// strict endpoint would be right to reject it as describing a request with no
// tools. It travels with the tools block, which is the only place it can mean
// something.
func TestOpenAIRequestWithoutToolsCarriesNoParallelToolCalls(t *testing.T) {
	body := postOpenAI(t, providers.BaseConfig{}, nil)

	if _, ok := body["tools"]; ok {
		t.Error("tools present for a request with no tool definitions")
	}
	if _, ok := body["parallel_tool_calls"]; ok {
		t.Error(`parallel_tool_calls sent with no tools — it describes how calls may be batched, and there are none`)
	}
}

// Also with an explicit serial configuration, so the "no tools" rule is not
// merely the zero value passing by accident.
func TestOpenAIRequestWithoutToolsCarriesNoParallelToolCallsWhenSerial(t *testing.T) {
	body := postOpenAI(t, providers.BaseConfig{SerialToolCalls: true}, nil)
	if _, ok := body["parallel_tool_calls"]; ok {
		t.Errorf("parallel_tool_calls sent with no tools: %v", body["parallel_tool_calls"])
	}
}

// The Messages API has no such field. The setting still reaches that model —
// through the agent's serial driver, which is protocol-agnostic — but it must
// not be invented onto this wire, where a strict server would refuse the whole
// request for a field it does not define.
func TestAnthropicRequestNeverCarriesParallelToolCalls(t *testing.T) {
	for _, serial := range []bool{false, true} {
		bodyCh := make(chan map[string]any, 1)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var got map[string]any
			if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
				bodyCh <- nil
				return
			}
			bodyCh <- got
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
		}))

		provider, err := providers.NewAnthropic(providers.BaseConfig{
			APIKey:          "test-key",
			BaseURL:         server.URL,
			SerialToolCalls: serial,
		})
		if err != nil {
			server.Close()
			t.Fatal(err)
		}
		events, err := provider.StreamMessages(context.Background(),
			testMsg(llm.RoleUser, &llm.TextPart{Text: "hi"}), oneTool(), "", "")
		if err != nil {
			server.Close()
			t.Fatalf("StreamMessages: %v", err)
		}
		for range events {
		}
		body := <-bodyCh
		server.Close()

		if body == nil {
			t.Fatalf("serial=%v: no request body captured", serial)
		}
		if _, ok := body["parallel_tool_calls"]; ok {
			t.Errorf("serial=%v: Anthropic request carried parallel_tool_calls", serial)
		}
		// The tools themselves must still be offered — the mode orders their
		// execution, it does not withdraw them.
		if _, ok := body["tools"]; !ok {
			t.Errorf("serial=%v: Anthropic request dropped the tools", serial)
		}
	}
}
