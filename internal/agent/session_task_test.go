package agent

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"sync/atomic"
	"testing"

	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/protocol"
	"github.com/alayacore/alayacore/internal/tlv"
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
		if resp.text != "" {
			if !yield(llm.TextDeltaEvent{Delta: resp.text, Key: "block:0"}, nil) {
				return
			}
		}
		for i, tc := range resp.toolCalls {
			key := fmt.Sprintf("block:%d", i+1) // 0 is the text block
			if !yield(llm.ToolInputStartEvent{ID: tc.ID, Name: tc.Name, Key: key}, nil) {
				return
			}
			if !yield(llm.ToolInputCompleteEvent{ID: tc.ID, Input: tc.Input, Key: key}, nil) {
				return
			}
		}
		content := []llm.ContentPart{}
		if resp.text != "" {
			content = append(content, &llm.TextPart{
				Text:            resp.text,
				ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleAssistant, BlockKey: "block:0"},
			})
		}
		for i, tc := range resp.toolCalls {
			content = append(content, &llm.ToolInputPart{
				ID:              tc.ID,
				Name:            tc.Name,
				Input:           tc.Input,
				ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleAssistant, BlockKey: fmt.Sprintf("block:%d", i+1)},
			})
		}
		yield(llm.StepCompleteEvent{
			Contents: content,
			Usage:    llm.Usage{InputTokens: 10, OutputTokens: 20},
		}, nil)
	}, nil
}

func (m *mockProviderStepFail) SetReasoningLevel(_ int)                       {}
func (m *mockProviderStepFail) SetReasoningConfigs(_ map[int]json.RawMessage) {}
func (m *mockProviderStepFail) SetVideoConfig(_ int, _ int)                   {}

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
	result := session.doAutoSummarize(context.Background(), contents)

	if len(result) == 0 {
		t.Fatal("doAutoSummarize returned empty on error")
	}
	if tp, ok := result[0].(*llm.TextPart); !ok || tp.Text != "existing content" {
		t.Errorf("expected original content preserved, got %v", result[0])
	}
}

func TestDoAutoSummarizeBuildSummaryFails(t *testing.T) {
	// processPrompt succeeds (step 1 tool calls → loop continues,
	// step 2 empty → no tool calls → loop exits with success).
	// The response has no text, so summarizeContents returns an error.
	provider := &mockProviderStepFail{
		responses: []stepResponse{
			{toolCalls: []llm.ToolInputPart{{ID: "s1", Name: "t", Input: []byte(`{}`)}}},
			{}, // empty step — no text, no tool calls
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
	result := session.doAutoSummarize(context.Background(), contents)

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
	raw := buf.Bytes()
	if len(raw) < 6 {
		t.Fatalf("frame too short: %d bytes", len(raw))
	}
	tag := string(raw[0:2])
	if tag != tlv.TagUserFDelta {
		t.Fatalf("tag = %q, want %q", tag, tlv.TagUserFDelta)
	}
	length := int(binary.BigEndian.Uint32(raw[2:6]))
	value := string(raw[6 : 6+length])

	id, payload, ok := tlv.UnwrapID(value)
	if !ok {
		t.Fatalf("failed to unwrap frame value %q", value)
	}
	if id != "42" {
		t.Errorf("historyID = %q, want %q", id, "42")
	}
	var od protocol.ToolOutputDeltaData
	if err := json.Unmarshal([]byte(payload), &od); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if od.ID != "call_1" || od.Text != " 99%" {
		t.Errorf("payload = %+v, want id=call_1 text=\" 99%%\"", od)
	}
}
