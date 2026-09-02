package agent

// Tests for per-step content publishing (stepFinishEvent.NewParts,
// promptPartsEvent, contentsReplacedEvent):
//
//   - TestStepPublishDeltas               — run() accumulates Contents from
//     per-step deltas; user prompt is published at task start; final
//     taskResultCh replacement converges.
//   - TestRunTaskSummarize_NoTransientPollution — summarization internals
//     (prompt/response) must not leak into Contents; only the final
//     [Continue, summary] replacement appears.
//   - TestDoAutoSummarizePublishesReplacement — mid-task auto-summarize
//     publishes a replacement event (success only), then the task
//     continues publishing the user prompt and step deltas.
//   - TestSaveDuringStreaming_EndToEnd   — :save while a task is mid-run
//     (run under -race) persists the completed steps and nothing more;
//     the task-completion auto-save then contains everything.

import (
	"context"
	"encoding/json"
	"io"
	"iter"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/protocol"
	"github.com/alayacore/alayacore/internal/tlv"
)

// containsText reports whether any TextPart in parts contains s.
func containsText(parts []llm.ContentPart, s string) bool {
	for _, p := range parts {
		if tp, ok := p.(*llm.TextPart); ok && strings.Contains(tp.Text, s) {
			return true
		}
	}
	return false
}

func TestStepPublishDeltas(t *testing.T) {
	provider := &mockProviderStepFail{
		responses: []stepResponse{
			{text: "Step 1.", toolCalls: []llm.ToolInputPart{{ID: "c1", Name: "t", Input: []byte(`{}`)}}},
			{text: "Step 2."},
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
	session.taskResultCh = make(chan []llm.ContentPart, 1)
	session.Contents = []llm.ContentPart{
		&llm.TextPart{Text: "hi", ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleUser}},
		&llm.TextPart{Text: "hello", ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleAssistant}},
	}

	go session.runTaskNormal(context.Background(), []llm.ContentPart{
		&llm.TextPart{Text: "do it"},
	})

	deadline := time.After(5 * time.Second)
	steps := 0
	for {
		select {
		case ev := <-session.taskEventCh:
			session.handleTaskEvent(ev)
			switch e := ev.(type) {
			case promptPartsEvent:
				if len(e.Parts) != 1 || e.Parts[0].GetRole() != llm.RoleUser {
					t.Fatalf("promptPartsEvent = %#v, want one user part", e.Parts)
				}
				if !containsText(session.Contents, "do it") {
					t.Fatalf("user prompt missing from Contents after promptPartsEvent: %v", session.Contents)
				}
			case stepFinishEvent:
				steps++
				switch steps {
				case 1:
					if !containsText(session.Contents, "Step 1.") {
						t.Fatalf("step 1 delta missing from Contents: %v", session.Contents)
					}
				case 2:
					if !containsText(session.Contents, "Step 2.") {
						t.Fatalf("step 2 delta missing from Contents: %v", session.Contents)
					}
				default:
					t.Fatalf("unexpected stepFinishEvent #%d", steps)
				}
			}
		case contents := <-session.taskResultCh:
			// Drain pending events first, like run()'s flushPendingEvents:
			// the final replacement must be applied last.
			session.drainAndHandleDone(t, contents)
			for _, want := range []string{"hi", "hello", "do it", "Step 1.", "Step 2."} {
				if !containsText(session.Contents, want) {
					t.Fatalf("final Contents missing %q: %v", want, session.Contents)
				}
			}
			return
		case <-deadline:
			t.Fatal("timed out waiting for task events/result")
		}
	}
}

func TestRunTaskSummarize_NoTransientPollution(t *testing.T) {
	provider := &mockProviderStepFail{
		responses: []stepResponse{
			{text: "Summary of the conversation."},
		},
	}
	agent := llm.NewAgent(llm.AgentConfig{Provider: provider, MaxSteps: 10})
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
	session.taskResultCh = make(chan []llm.ContentPart, 1)
	session.Contents = []llm.ContentPart{
		&llm.TextPart{Text: "hi", ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleUser}},
		&llm.TextPart{Text: "hello", ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleAssistant}},
	}

	go session.runTaskSummarize(context.Background())

	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev := <-session.taskEventCh:
			session.handleTaskEvent(ev)
			// Summarization is a replacement operation: its internals
			// (prompt part, response deltas) must never touch Contents.
			switch e := ev.(type) {
			case stepFinishEvent:
				if len(e.NewParts) != 0 {
					t.Fatalf("summarize step published NewParts: %v", e.NewParts)
				}
			case promptPartsEvent:
				t.Fatalf("summarize published promptPartsEvent: %v", e.Parts)
			case contentsReplacedEvent:
				t.Fatalf("summarize published contentsReplacedEvent: %v", e.Contents)
			}
			if len(session.Contents) != 2 ||
				!containsText(session.Contents, "hi") || !containsText(session.Contents, "hello") {
				t.Fatalf("Contents mutated during summarize: %v", session.Contents)
			}
		case contents := <-session.taskResultCh:
			session.drainAndHandleDone(t, contents)
			if len(session.Contents) != 2 {
				t.Fatalf("final Contents = %v, want [Continue, summary]", session.Contents)
			}
			if tp, ok := session.Contents[0].(*llm.TextPart); !ok || tp.Text != "Continue" || tp.Role != llm.RoleUser {
				t.Fatalf("final[0] = %#v, want user Continue", session.Contents[0])
			}
			if tp, ok := session.Contents[1].(*llm.TextPart); !ok || !strings.Contains(tp.Text, "Summary") {
				t.Fatalf("final[1] = %#v, want summary text", session.Contents[1])
			}
			return
		case <-deadline:
			t.Fatal("timed out waiting for summarize task")
		}
	}
}

func TestDoAutoSummarizePublishesReplacement(t *testing.T) {
	provider := &mockProviderStepFail{
		responses: []stepResponse{
			{text: "Summary of the conversation."}, // auto-summarize (before user parts)
			{text: "Final answer."},                // the actual prompt
		},
	}
	agent := llm.NewAgent(llm.AgentConfig{Provider: provider, MaxSteps: 10})
	session := &Session{
		sessionConfig: sessionConfig{
			modelService:  &modelService{agent: agent},
			SessionConfig: SessionConfig{NoDelta: true, AutoSummarize: 100},
		},
		sharedState: sharedState{
			histCounter:   200,
			outputBroken:  atomic.Bool{},
			ContextTokens: 1000,
			ContextLimit:  1000,
		},
		runState: runState{
			taskEventCh: make(chan taskEvent, 20),
		},
	}
	session.taskResultCh = make(chan []llm.ContentPart, 1)
	session.Contents = []llm.ContentPart{
		&llm.TextPart{Text: "hi", ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleUser}},
		&llm.TextPart{Text: "hello", ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleAssistant}},
	}

	go session.runTaskNormal(context.Background(), []llm.ContentPart{
		&llm.TextPart{Text: "do it"},
	})

	// Expected event order from the task goroutine:
	//   stepFinishEvent (summarize, no NewParts)
	//   contentsReplacedEvent ([Continue, summary])
	//   promptPartsEvent   (user "do it")
	//   stepFinishEvent    (final answer, 1 NewPart)
	//   taskResultCh
	deadline := time.After(5 * time.Second)
	phase := 0
	for {
		select {
		case ev := <-session.taskEventCh:
			session.handleTaskEvent(ev)
			switch e := ev.(type) {
			case stepFinishEvent:
				switch phase {
				case 0: // summarize step
					if len(e.NewParts) != 0 {
						t.Fatalf("summarize step published NewParts: %v", e.NewParts)
					}
				case 2: // final answer step
					if len(e.NewParts) != 1 || !containsText(e.NewParts, "Final answer.") {
						t.Fatalf("final step NewParts = %v, want [Final answer.]", e.NewParts)
					}
				default:
					t.Fatalf("stepFinishEvent at unexpected phase %d", phase)
				}
			case contentsReplacedEvent:
				if phase != 0 {
					t.Fatalf("contentsReplacedEvent at phase %d", phase)
				}
				phase = 1
				if len(session.Contents) != 2 ||
					!containsText(session.Contents, "Continue") || !containsText(session.Contents, "Summary") {
					t.Fatalf("Contents after replacement = %v, want [Continue, summary]", session.Contents)
				}
			case promptPartsEvent:
				if phase != 1 {
					t.Fatalf("promptPartsEvent at phase %d", phase)
				}
				phase = 2
				if len(e.Parts) != 1 || !containsText(e.Parts, "do it") {
					t.Fatalf("promptPartsEvent = %#v, want user \"do it\"", e.Parts)
				}
				if !containsText(session.Contents, "do it") {
					t.Fatalf("user prompt missing after promptPartsEvent: %v", session.Contents)
				}
			}
		case contents := <-session.taskResultCh:
			session.drainAndHandleDone(t, contents)
			for _, want := range []string{"Continue", "Summary", "do it", "Final answer."} {
				if !containsText(session.Contents, want) {
					t.Fatalf("final Contents missing %q: %v", want, session.Contents)
				}
			}
			return
		case <-deadline:
			t.Fatal("timed out waiting for auto-summarize task")
		}
	}
}

// drainAndHandleDone applies any remaining task events (mirroring run()'s
// flushPendingEvents) before the final replacement, so the taskResultCh
// contents always win regardless of select scheduling.
func (s *Session) drainAndHandleDone(t *testing.T, contents []llm.ContentPart) {
	t.Helper()
	for {
		select {
		case ev := <-s.taskEventCh:
			s.handleTaskEvent(ev)
		default:
			s.handleTaskDone(contents)
			return
		}
	}
}

// stepBlockingProvider yields step 1 (text + tool call), then blocks on
// release before yielding step 2.
type stepBlockingProvider struct {
	release   chan struct{}
	callCount int
}

func (m *stepBlockingProvider) StreamMessages(_ context.Context, _ []llm.ContentPart, _ []llm.ToolDefinition, _, _ string) (iter.Seq2[llm.StreamEvent, error], error) {
	m.callCount++
	if m.callCount == 1 {
		return func(yield func(llm.StreamEvent, error) bool) {
			if !yield(llm.TextDeltaEvent{Delta: "Step 1.", Key: "block:0"}, nil) {
				return
			}
			if !yield(llm.TextCompleteEvent{Text: "Step 1.", Key: "block:0"}, nil) {
				return
			}
			if !yield(llm.ToolInputStartEvent{ID: "c1", Name: "t", Key: "block:1"}, nil) {
				return
			}
			if !yield(llm.ToolInputCompleteEvent{ID: "c1", Input: []byte(`{}`), Key: "block:1"}, nil) {
				return
			}
			yield(llm.StepCompleteEvent{Contents: []llm.ContentPart{
				&llm.TextPart{Text: "Step 1.", ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleAssistant, BlockKey: "block:0"}},
				&llm.ToolInputPart{ID: "c1", Name: "t", Input: []byte(`{}`), ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleAssistant, BlockKey: "block:1"}},
			}, Usage: llm.Usage{InputTokens: 10, OutputTokens: 10}}, nil)
		}, nil
	}
	<-m.release
	return func(yield func(llm.StreamEvent, error) bool) {
		if !yield(llm.TextDeltaEvent{Delta: "Step 2.", Key: "block:0"}, nil) {
			return
		}
		if !yield(llm.TextCompleteEvent{Text: "Step 2.", Key: "block:0"}, nil) {
			return
		}
		yield(llm.StepCompleteEvent{Contents: []llm.ContentPart{
			&llm.TextPart{Text: "Step 2.", ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleAssistant, BlockKey: "block:0"}},
		}, Usage: llm.Usage{InputTokens: 10, OutputTokens: 10}}, nil)
	}, nil
}

func (m *stepBlockingProvider) SetReasoningLevel(_ int)                       {}
func (m *stepBlockingProvider) SetReasoningConfigs(_ map[int]json.RawMessage) {}
func (m *stepBlockingProvider) SetVideoConfig(_ int, _ int)                   {}

func TestSaveDuringStreaming_EndToEnd(t *testing.T) {
	output := &syncOutput{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r, w := io.Pipe()
	defer w.Close()

	savePath := filepath.Join(t.TempDir(), "session.alaya")

	provider := &stepBlockingProvider{release: make(chan struct{})}
	agent := llm.NewAgent(llm.AgentConfig{
		Provider: provider,
		Tools: []llm.Tool{{
			Definition: llm.ToolDefinition{Name: "t", Description: "test", Schema: []byte(`{"type":"object"}`)},
			Execute:    func(_ context.Context, _ json.RawMessage) ([]llm.ContentPart, error) { return nil, nil },
		}},
		MaxSteps: 10,
	})

	s := &Session{
		sessionConfig: sessionConfig{
			modelService: &modelService{agent: agent, provider: provider},
			SessionConfig: SessionConfig{
				Input:       r,
				Output:      output,
				NoDelta:     true,
				SessionFile: savePath,
			},
		},
		runState: runState{
			Contents:     make([]llm.ContentPart, 0),
			taskEventCh:  make(chan taskEvent, 64),
			taskResultCh: make(chan []llm.ContentPart, 1),
			cancelReqCh:  make(chan chan bool, 1),
		},
		sharedState: sharedState{
			sessionCtx:    ctx,
			sessionCancel: cancel,
			confirmChs:    make(map[string]chan bool),
		},
		runDoneCh: make(chan struct{}),
	}
	s.mcpService = newMCPService(nil, output)
	s.Start()

	// Deliver a prompt (what plainio/terseio do).
	if err := tlv.WriteTLV(w, tlv.TagUserT, tlv.WrapID("1", "hello")); err != nil {
		t.Fatalf("write UT: %v", err)
	}
	if err := tlv.WriteTLV(w, tlv.TagUserEnd, ""); err != nil {
		t.Fatalf("write UE: %v", err)
	}

	// Wait for the step 2 start marker: run() writes the current_step=2
	// task frame AFTER applying the step 1 delta (handleTaskEvent appends
	// NewParts before sendSystemInfo) and BEFORE the provider blocks in
	// step 2. So once this frame appears, a :save is guaranteed to
	// include step 1 and exclude step 2.
	waitFor(t, func() bool { return strings.Contains(output.String(), `"current_step":2`) }, "step 1 delta applied (step 2 start marker)")

	// :save mid-task → the file must contain the user prompt and step 1,
	// but NOT step 2 (the task is still running and blocked).
	cmd, err := json.Marshal(protocol.CmdMsg{ID: "s1", Name: "save", Input: savePath})
	if err != nil {
		t.Fatalf("marshal save cmd: %v", err)
	}
	if err := tlv.WriteTLV(w, tlv.TagCommandIn, string(cmd)); err != nil {
		t.Fatalf("write CI save: %v", err)
	}
	waitFor(t, func() bool { return fileContains(savePath, "Step 1.") }, "save written with step 1")
	if fileContains(savePath, "Step 2.") {
		t.Fatal("save contained step 2 before it was produced")
	}
	if !fileContains(savePath, "hello") {
		t.Fatal("save missing user prompt")
	}

	// Release step 2 → the task completes → auto-save contains everything.
	close(provider.release)
	waitFor(t, func() bool { return fileContains(savePath, "Step 2.") }, "auto-save with step 2")

	_ = w.Close()
	select {
	case <-s.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("session did not finish after task completion")
	}
}

func waitFor(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		if cond() {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for %s", what)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func fileContains(path, s string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return strings.Contains(string(b), s)
}
