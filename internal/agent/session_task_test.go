package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/protocol"
	"github.com/alayacore/alayacore/internal/wire"
	"github.com/alayacore/alayacore/internal/wiretest"
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

// cutStreamProvider answers the first request by streaming part of an answer and
// then losing the connection, and answers the continuation — the request whose
// history carries the boundary's "Continue" — in full. It is the shape a session
// sees when a provider goes away mid-answer, the one that used to end the task
// with nothing but "unexpected EOF" on screen.
type cutStreamProvider struct {
	calls    int
	requests [][]llm.ContentPart
}

func (p *cutStreamProvider) StreamMessages(_ context.Context, history []llm.ContentPart, _ []llm.ToolDefinition, _, _ string) (iter.Seq2[llm.StreamEvent, error], error) {
	p.calls++
	p.requests = append(p.requests, append([]llm.ContentPart(nil), history...))
	continued := false
	for _, part := range history {
		if tp, ok := part.(*llm.TextPart); ok && tp.Text == "Continue" {
			continued = true
		}
	}
	return func(yield func(llm.StreamEvent, error) bool) {
		if !continued {
			if !yield(llm.TextDeltaEvent{Delta: "half an answer", Key: "block:0"}, nil) {
				return
			}
			yield(nil, io.ErrUnexpectedEOF)
			return
		}
		if !yield(llm.TextDeltaEvent{Delta: "the rest", Key: "block:0"}, nil) {
			return
		}
		if !yield(llm.TextCompleteEvent{Key: "block:0"}, nil) {
			return
		}
		yield(llm.StepCompleteEvent{Usage: llm.Usage{InputTokens: 10, OutputTokens: 5}}, nil)
	}, nil
}

func (p *cutStreamProvider) SetReasoningLevel(_ int)                       {}
func (p *cutStreamProvider) SetReasoningConfigs(_ map[int]json.RawMessage) {}

// smTexts returns the text of every SM frame of one type in captured output, in
// write order.
func smTexts(t *testing.T, out, msgType string) []string {
	t.Helper()
	var texts []string
	r := strings.NewReader(out)
	for {
		tag, value, err := wiretest.NextFrame(r)
		if err != nil {
			return texts
		}
		if tag != wire.TagSystemMsg {
			continue
		}
		env, err := protocol.ParseSystemMsg(value)
		if err != nil || env.Type != msgType {
			continue
		}
		var m struct {
			Text string `json:"text"`
		}
		if json.Unmarshal(env.Data, &m) != nil {
			continue
		}
		texts = append(texts, m.Text)
	}
}

// A cut stream mid-task is resumed without the user: the salvage stays in
// history, the step boundary appends the same "Continue" :continue would — with a
// history ID, echoed like any part added at a boundary — and the task finishes
// instead of failing. This is the session half of llm.Agent.resumeStep, and the
// half that makes the continuation a well-formed request rather than prefill.
func TestRunTaskResumesACutStream(t *testing.T) {
	provider := &cutStreamProvider{}
	agent := llm.NewAgent(llm.AgentConfig{Provider: provider, MaxSteps: 10})
	out := &syncOutput{}
	session := &Session{
		sessionConfig: sessionConfig{
			modelService:  &modelService{agent: agent},
			SessionConfig: SessionConfig{NoDelta: true, Output: out},
		},
		sharedState: sharedState{histCounter: 500, outputBroken: atomic.Bool{}},
		runState:    runState{taskEventCh: make(chan taskEvent, 20)},
	}
	session.Contents = []llm.ContentPart{
		&llm.TextPart{Text: "hi", ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleUser}},
	}

	runOneTask(t, session, []llm.ContentPart{&llm.TextPart{Text: "do it"}})

	if provider.calls != 2 {
		t.Fatalf("provider calls = %d, want 2 (the cut step, then its continuation)", provider.calls)
	}
	got := make([]string, 0, len(session.Contents))
	var continueID uint64
	for _, p := range session.Contents {
		tp, ok := p.(*llm.TextPart)
		if !ok {
			continue
		}
		got = append(got, string(p.GetRole())+":"+tp.Text)
		if tp.Text == "Continue" {
			continueID = p.GetHistoryID()
		}
	}
	want := "user:hi,user:do it,assistant:half an answer,user:Continue,assistant:the rest"
	if joined := strings.Join(got, ","); joined != want {
		t.Fatalf("contents after the resume = %s, want %s", joined, want)
	}
	if countText(session.Contents, "half an answer") != 1 {
		t.Errorf("the partial answer appears %d times, want 1 (a resume keeps it, it does not re-send it)",
			countText(session.Contents, "half an answer"))
	}
	// The Continue part is a part like any other: numbered, and echoed under the
	// ID the adapter now holds a window for.
	if continueID == 0 {
		t.Fatal("the Continue part was never numbered")
	}
	var echoed bool
	for _, id := range echoedUserIDs(t, out.String()) {
		if id == wireID(continueID) {
			echoed = true
		}
	}
	if !echoed {
		t.Errorf("the adapter was never shown the Continue part (id %d)", continueID)
	}

	// Announced before the wait, because words the user did not type have to be
	// explained where they appear; and no failure, because the task landed.
	notices := smTexts(t, out.String(), string(protocol.MsgTypeNotify))
	var announced bool
	for _, n := range notices {
		if strings.HasPrefix(n, "provider stream was cut off — retrying in ") {
			announced = true
		}
	}
	if !announced {
		t.Errorf("notices = %v, want one announcing the resume", notices)
	}
	if errs := smTexts(t, out.String(), string(protocol.MsgTypeError)); len(errs) != 0 {
		t.Errorf("errors = %v, want none: the continuation finished the task", errs)
	}
}
