package agent

// End to end for the tool-calling mode: the model.conf line, the parsed model,
// the request body the provider builds, and the order the tools actually ran
// in.
//
// The hops in between are single lines of wiring in model_service.go, and a
// dropped one is invisible to the per-layer tests — the option would parse
// correctly, the provider would send the right field, the agent would order
// calls, and the three would simply never be connected to each other. That is
// the failure this file exists to catch.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alayacore/alayacore/internal/llm"
)

// twoCallsServer answers the first request with two tool calls and every later
// one with text, so the agent loop terminates. It records each request body.
//
// The two calls are streamed in the wrong order on purpose — the fragment for
// `beta` (declared index 1) arrives before the one for `alpha` (index 0). Serial
// mode must run them in the order the model named, which is not the order the
// transport happened to deliver.
type twoCallsServer struct {
	mu     sync.Mutex
	bodies []map[string]any
	seen   int
	t      *testing.T
}

func (s *twoCallsServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		s.t.Errorf("decoding request body: %v", err)
		return
	}
	s.mu.Lock()
	s.bodies = append(s.bodies, body)
	s.seen++
	first := s.seen == 1
	s.mu.Unlock()

	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	if !first {
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
		return
	}
	fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":1,\"id\":\"c_beta\",\"type\":\"function\",\"function\":{\"name\":\"beta\",\"arguments\":\"{}\"}}]}}]}\n\n")
	fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"c_alpha\",\"type\":\"function\",\"function\":{\"name\":\"alpha\",\"arguments\":\"{}\"}}]}}]}\n\n")
	fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\n")
	fmt.Fprint(w, "data: [DONE]\n\n")
}

func (s *twoCallsServer) firstBody(t *testing.T) map[string]any {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.bodies) == 0 {
		t.Fatal("the provider never reached the server")
	}
	return s.bodies[0]
}

// toolTracer logs when each tool begins and ends, and how many were inside at
// once — which is what distinguishes the two modes observably.
type toolTracer struct {
	mu     sync.Mutex
	events []string
	inside int
	maxIn  int
}

// span records one tool's begin and end, with a deliberate duration between
// them so overlapping runs are distinguishable from serialized ones.
func (tt *toolTracer) span(name string) {
	tt.mu.Lock()
	tt.inside++
	if tt.inside > tt.maxIn {
		tt.maxIn = tt.inside
	}
	tt.events = append(tt.events, "enter:"+name)
	tt.mu.Unlock()

	time.Sleep(15 * time.Millisecond)

	tt.mu.Lock()
	tt.events = append(tt.events, "exit:"+name)
	tt.inside--
	tt.mu.Unlock()
}

func (tt *toolTracer) trace() string {
	tt.mu.Lock()
	defer tt.mu.Unlock()
	return strings.Join(tt.events, " ")
}

func (tt *toolTracer) peakConcurrency() int {
	tt.mu.Lock()
	defer tt.mu.Unlock()
	return tt.maxIn
}

// runTurn takes a model.conf block, builds the provider and agent from it the
// way a model switch does, and runs one turn against the stub server.
func runTurn(t *testing.T, confText string) (firstBody map[string]any, tracer *toolTracer) {
	t.Helper()

	server := &twoCallsServer{t: t}
	srv := httptest.NewServer(server)
	t.Cleanup(srv.Close)

	text := fmt.Sprintf("%s\nbase_url: %q\n", confText, srv.URL)
	models, errs := parseModelList(text, "model.conf")
	if len(errs) != 0 {
		t.Fatalf("model.conf parse errors: %v", errs)
	}
	if len(models) != 1 {
		t.Fatalf("expected 1 model, got %d", len(models))
	}

	tracer = &toolTracer{}
	tools := make([]llm.Tool, 0, 2)
	for _, name := range []string{"alpha", "beta"} {
		toolName := name
		tools = append(tools, llm.Tool{
			Definition: llm.ToolDefinition{Name: toolName, Schema: json.RawMessage(`{"type":"object"}`)},
			Execute: func(_ context.Context, _ json.RawMessage) ([]llm.ContentPart, error) {
				tracer.span(toolName)
				return []llm.ContentPart{&llm.TextPart{Text: "did " + toolName}}, nil
			},
		})
	}

	ms := newModelService(nil, nil)
	_, agent, err := ms.createProviderAndAgent(&models[0], tools, "", "", 0)
	if err != nil {
		t.Fatalf("createProviderAndAgent: %v", err)
	}

	if _, err := agent.Stream(context.Background(), []llm.ContentPart{
		&llm.TextPart{Text: "go", ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleUser}},
	}, llm.StreamCallbacks{}); err != nil {
		t.Fatalf("Stream: %v", err)
	}

	return server.firstBody(t), tracer
}

// Configuring `serial_tool_calls: true` must reach both places the option means:
// the request states it (in Chat Completions' own positive spelling, the single
// inversion point), and the calls run one at a time in the order the model named
// them — which is `alpha` first even though `beta` streamed first.
func TestSerialToolCallsWiredFromConfigThroughToExecution(t *testing.T) {
	body, tracer := runTurn(t, "name: \"m\"\nprotocol_type: \"openai\"\napi_key: \"k\"\nmodel_name: \"model-a\"\nserial_tool_calls: true")

	if got := body["parallel_tool_calls"]; got != false {
		t.Errorf(`request parallel_tool_calls = %#v, want false — serial_tool_calls parsed but never reached the provider`, got)
	}
	if want := "enter:alpha exit:alpha enter:beta exit:beta"; tracer.trace() != want {
		t.Errorf("execution trace:\n got %q\nwant %q", tracer.trace(), want)
	}
	if got := tracer.peakConcurrency(); got != 1 {
		t.Errorf("peak concurrent tools = %d, want 1", got)
	}
}

// The same path unconfigured must keep the behavior every existing model.conf
// has: the request says parallel out loud, and the calls still overlap. Without
// this case the serial test above could pass because everything went serial.
func TestParallelToolCallsStayParallelWhenNotConfigured(t *testing.T) {
	body, tracer := runTurn(t, "name: \"m\"\nprotocol_type: \"openai\"\napi_key: \"k\"\nmodel_name: \"model-a\"")

	if got := body["parallel_tool_calls"]; got != true {
		t.Errorf(`request parallel_tool_calls = %#v, want true: an unset option means the historical behavior, stated explicitly rather than left to the server's default`, got)
	}
	if got := tracer.peakConcurrency(); got < 2 {
		t.Errorf("peak concurrent tools = %d, want 2 — the default mode stopped overlapping (trace %q)", got, tracer.trace())
	}
}
