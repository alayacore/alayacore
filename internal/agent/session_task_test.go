package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/protocol"
	"github.com/alayacore/alayacore/internal/wire"
)

type stepResponse struct {
	text      string
	toolCalls []llm.ToolInputPart
	failErr   error
}

type mockProviderStepFail struct {
	responses []stepResponse
	callCount int
}

func (m *mockProviderStepFail) StreamMessages(_ context.Context, _ []llm.ContentPart, _ []llm.ToolDefinition, _, _ string) (iter.Seq2[llm.StreamEvent, error], error) {
	if m.callCount >= len(m.responses) {
		return nil, errors.New("mock: unexpected call beyond configured responses")
	}
	resp := m.responses[m.callCount]
	m.callCount++
	if resp.failErr != nil {
		return nil, resp.failErr
	}
	return func(yield func(llm.StreamEvent, error) bool) {
		// A stub provider states only what the wire carried. llm.Agent assembles
		// the step's record from these events, so a fake cannot describe the task
		// in terms the events do not support.
		if resp.text != "" {
			if !yield(llm.TextDeltaEvent{Delta: resp.text, Key: "block:0"}, nil) {
				return
			}
			if !yield(llm.TextCompleteEvent{Key: "block:0"}, nil) {
				return
			}
		}
		for i, tc := range resp.toolCalls {
			key := fmt.Sprintf("block:%d", i+1) // 0 is the text block
			if !yield(llm.ToolInputStartEvent{ID: tc.ID, Name: tc.Name, Key: key}, nil) {
				return
			}
			if !yield(llm.ToolInputDeltaEvent{ID: tc.ID, Delta: string(tc.Input), Key: key}, nil) {
				return
			}
			if !yield(llm.ToolInputCompleteEvent{ID: tc.ID, Key: key}, nil) {
				return
			}
		}
		yield(llm.StepCompleteEvent{
			Usage: llm.Usage{InputTokens: 10, OutputTokens: 20},
		}, nil)
	}, nil
}

func (m *mockProviderStepFail) SetReasoningLevel(_ int)                       {}
func (m *mockProviderStepFail) SetReasoningConfigs(_ map[int]json.RawMessage) {}

func TestRunTaskPreservesPartialResultsOnError(t *testing.T) {
	provider := &mockProviderStepFail{
		responses: []stepResponse{
			{text: "Step 1.", toolCalls: []llm.ToolInputPart{{ID: "c1", Name: "t", Input: []byte(`{}`)}}},
			{text: "Step 2.", toolCalls: []llm.ToolInputPart{{ID: "c2", Name: "t", Input: []byte(`{}`)}}},
			{failErr: errors.New("provider fail on step 3")},
		},
	}
	agent := llm.NewAgent(llm.AgentConfig{
		Provider: provider,
		Tools: []llm.Tool{{
			Definition: llm.ToolDefinition{Name: "t", Description: "test", Schema: []byte(`{"type":"object"}`)},
			Execute:    func(_ context.Context, _ json.RawMessage) ([]llm.ContentPart, error) { return nil, nil },
		}},
		MaxSteps: 10,
	})
	session := &Session{
		sessionConfig: sessionConfig{
			modelService:  &modelService{agent: agent},
			SessionConfig: SessionConfig{NoDelta: true},
		},
		sharedState: sharedState{
			histCounter:  200,
			outputBroken: atomic.Bool{},
		},
		runState: runState{
			taskEventCh: make(chan taskEvent, 20),
		},
	}
	// runTaskNormal sends results to taskResultCh — read it.
	session.taskResultCh = make(chan []llm.ContentPart, 1)
	session.Contents = []llm.ContentPart{
		&llm.TextPart{Text: "hi", ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleUser}},
		&llm.TextPart{Text: "hello", ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleAssistant}},
	}
	session.runTaskNormal(context.Background(), []llm.ContentPart{
		&llm.TextPart{Text: "do it"},
	})
	result := <-session.taskResultCh

	assistantCount := 0
	toolCallCount := 0
	for _, p := range result {
		if _, ok := p.(*llm.ToolInputPart); ok {
			toolCallCount++
		}
		if p.GetRole() == llm.RoleAssistant {
			assistantCount++
		}
	}
	if assistantCount < 3 {
		t.Fatalf("expected >=3 assistant parts (prev + 2 steps), got %d", assistantCount)
	}
	if toolCallCount != 2 {
		t.Fatalf("expected 2 tool calls, got %d", toolCallCount)
	}
}

func TestDoAutoSummarizePreservesContentsOnError(t *testing.T) {
	// Summarization failed immediately (no successful step).
	provider := &mockProviderStepFail{
		responses: []stepResponse{
			{failErr: errors.New("provider failed on summarization")},
		},
	}
	agent := llm.NewAgent(llm.AgentConfig{
		Provider: provider,
		MaxSteps: 1,
	})
	session := &Session{
		sessionConfig: sessionConfig{
			modelService:  &modelService{agent: agent},
			SessionConfig: SessionConfig{NoDelta: true},
		},
		sharedState: sharedState{
			histCounter:  300,
			outputBroken: atomic.Bool{},
		},
		runState: runState{
			taskEventCh: make(chan taskEvent, 20),
		},
	}
	contents := []llm.ContentPart{
		&llm.TextPart{Text: "existing content", ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleUser}},
	}
	result, err := session.doAutoSummarize(context.Background(), contents)
	if err == nil {
		t.Fatal("doAutoSummarize returned nil error on a failed summarization")
	}

	if len(result) == 0 {
		t.Fatal("doAutoSummarize returned empty on error")
	}
	if tp, ok := result[0].(*llm.TextPart); !ok || tp.Text != "existing content" {
		t.Errorf("expected original content preserved, got %v", result[0])
	}
}

func TestDoAutoSummarizeBuildSummaryFails(t *testing.T) {
	// The summarize calls a tool and then ends with an empty step, so it writes
	// no text. The retry is a fresh request — it does not carry the failed
	// attempt — and is scripted empty too, so summarizeContents fails.
	provider := &mockProviderStepFail{
		responses: []stepResponse{
			{toolCalls: []llm.ToolInputPart{{ID: "s1", Name: "t", Input: []byte(`{}`)}}},
			{}, // empty step — no text, no tool calls
			{}, // the retry, empty too
		},
	}
	agent := llm.NewAgent(llm.AgentConfig{
		Provider: provider,
		Tools: []llm.Tool{{
			Definition: llm.ToolDefinition{Name: "t", Description: "test", Schema: []byte(`{"type":"object"}`)},
			Execute:    func(_ context.Context, _ json.RawMessage) ([]llm.ContentPart, error) { return nil, nil },
		}},
		MaxSteps: 5,
	})
	session := &Session{
		sessionConfig: sessionConfig{
			modelService:  &modelService{agent: agent},
			SessionConfig: SessionConfig{NoDelta: true},
		},
		sharedState: sharedState{
			histCounter:  400,
			outputBroken: atomic.Bool{},
		},
		runState: runState{
			taskEventCh: make(chan taskEvent, 20),
		},
	}
	contents := []llm.ContentPart{
		&llm.TextPart{Text: "original", ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleUser}},
	}
	result, err := session.doAutoSummarize(context.Background(), contents)
	if err == nil {
		t.Fatal("doAutoSummarize returned nil error when the summary produced no text")
	}

	// summarizeContents failed (no text) — original history preserved.
	if len(result) != 1 {
		t.Fatalf("expected 1 part (original history), got %d", len(result))
	}
	if tp, ok := result[0].(*llm.TextPart); !ok || tp.Text != "original" {
		t.Errorf("expected original content preserved, got %v", result[0])
	}
}

func TestDeltaWriterHandleToolOutputDelta(t *testing.T) {
	var buf bytes.Buffer
	dw := &deltaWriter{output: &buf}

	if err := dw.handleToolOutputDelta("call_1", " 99%", 42); err != nil {
		t.Fatalf("handleToolOutputDelta: %v", err)
	}

	// Verify the emitted frame is a Uf frame with the expected payload.
	tag, value, err := wire.Decode(buf.Bytes())
	if err != nil {
		t.Fatalf("decode the emitted frame: %v", err)
	}
	if tag != wire.TagUserFDelta {
		t.Fatalf("tag = %q, want %q", tag, wire.TagUserFDelta)
	}

	var od protocol.ToolOutputDeltaData
	if err := json.Unmarshal([]byte(value), &od); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if od.ID != "42" {
		t.Errorf("historyID = %q, want %q", od.ID, "42")
	}
	if od.CallID != "call_1" || od.Text != " 99%" {
		t.Errorf("payload = %+v, want call_id=call_1 text=\" 99%%\"", od)
	}
}

// panicProvider streams a reasoning block and an answer, then panics mid-flight
// — the shape of a bug in a provider's parser or in llm.Agent's own dispatch.
type panicProvider struct{}

func (panicProvider) StreamMessages(_ context.Context, _ []llm.ContentPart, _ []llm.ToolDefinition, _, _ string) (iter.Seq2[llm.StreamEvent, error], error) {
	return func(yield func(llm.StreamEvent, error) bool) {
		yield(llm.ReasoningDeltaEvent{Delta: "a thought", Key: "reasoning"}, nil)
		yield(llm.TextDeltaEvent{Delta: "half an answ", Key: "text"}, nil)
		panic("provider parser exploded")
	}, nil
}

func (panicProvider) SetReasoningLevel(_ int)                       {}
func (panicProvider) SetReasoningConfigs(_ map[int]json.RawMessage) {}

// The content a user watched stream in must survive a panic all the way to the
// session's history, not just to llm.Agent's return value. llm.Agent converts the
// panic into the step's error (see TestStreamSalvagesContentOnPanic); this covers
// the other half — that the failed step's parts still travel out through the task
// result, which is what handleTaskDone assigns to Contents and writes to the
// session file.
func TestRunTaskKeepsStreamedContentOnPanic(t *testing.T) {
	agent := llm.NewAgent(llm.AgentConfig{Provider: panicProvider{}, MaxSteps: 3})
	session := &Session{
		sessionConfig: sessionConfig{
			modelService:  &modelService{agent: agent},
			SessionConfig: SessionConfig{NoDelta: true},
		},
		sharedState: sharedState{histCounter: 100},
		runState:    runState{taskEventCh: make(chan taskEvent, 20)},
	}
	session.taskResultCh = make(chan []llm.ContentPart, 1)
	session.Contents = []llm.ContentPart{
		&llm.TextPart{Text: "hi", ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleUser}},
	}

	session.runTaskNormal(context.Background(), []llm.ContentPart{
		&llm.TextPart{Text: "do it", ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleUser}},
	})

	var got []string
	for _, p := range <-session.taskResultCh {
		switch v := p.(type) {
		case *llm.ReasoningPart:
			got = append(got, "reasoning:"+v.Text)
		case *llm.TextPart:
			if p.GetRole() == llm.RoleAssistant {
				got = append(got, "text:"+v.Text)
			}
		}
	}
	want := "reasoning:a thought,text:half an answ"
	if joined := strings.Join(got, ","); joined != want {
		t.Errorf("history after the panic = [%s], want [%s]", joined, want)
	}
}
