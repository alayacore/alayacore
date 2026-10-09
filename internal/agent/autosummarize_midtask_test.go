package agent

import (
	"context"
	"encoding/json"
	"io"
	"iter"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alayacore/alayacore/internal/llm"
)

// midTaskProvider drives a task that grows past the threshold on its first
// step, then summarizes when asked:
//   - a normal step asks for a tool (so the loop continues) and reports a
//     context size over the threshold;
//   - the summarize request (its history carries the summarize prompt) answers
//     with a summary;
//   - the step after compaction (its history starts with "Continue") answers
//     with the plain final text.
type midTaskProvider struct {
	mu             sync.Mutex
	summarizeCount int
	requests       [][]llm.ContentPart
}

func (m *midTaskProvider) StreamMessages(_ context.Context, history []llm.ContentPart, _ []llm.ToolDefinition, _, _ string) (iter.Seq2[llm.StreamEvent, error], error) {
	hasSummarize, hasContinue := false, false
	for _, p := range history {
		tp, ok := p.(*llm.TextPart)
		if !ok {
			continue
		}
		switch tp.Text {
		case summarizePrompt:
			hasSummarize = true
		case "Continue":
			hasContinue = true
		}
	}
	m.mu.Lock()
	if hasSummarize {
		m.summarizeCount++
	}
	cp := make([]llm.ContentPart, len(history))
	copy(cp, history)
	m.requests = append(m.requests, cp)
	m.mu.Unlock()

	return func(yield func(llm.StreamEvent, error) bool) {
		switch {
		case hasSummarize:
			if !yield(llm.TextDeltaEvent{Delta: "SUMMARY OF EARLIER WORK", Key: "block:0"}, nil) {
				return
			}
			yield(llm.StepCompleteEvent{Usage: llm.Usage{InputTokens: 100, OutputTokens: 5}}, nil)
		case hasContinue:
			if !yield(llm.TextDeltaEvent{Delta: "done", Key: "block:0"}, nil) {
				return
			}
			yield(llm.StepCompleteEvent{Usage: llm.Usage{InputTokens: 20, OutputTokens: 5}}, nil)
		default:
			if !yield(llm.TextDeltaEvent{Delta: "working", Key: "block:0"}, nil) {
				return
			}
			yield(llm.ToolInputStartEvent{ID: "c1", Name: "t", Key: "block:1"}, nil)
			yield(llm.ToolInputDeltaEvent{ID: "c1", Delta: `{}`, Key: "block:1"}, nil)
			yield(llm.ToolInputCompleteEvent{ID: "c1", Key: "block:1"}, nil)
			yield(llm.StepCompleteEvent{Usage: llm.Usage{InputTokens: 100, OutputTokens: 5}, StopReason: "tool_use"}, nil)
		}
	}, nil
}

func (m *midTaskProvider) SetReasoningLevel(_ int)                       {}
func (m *midTaskProvider) SetReasoningConfigs(_ map[int]json.RawMessage) {}
func (m *midTaskProvider) SetVideoConfig(_ int, _ int)                   {}

func (m *midTaskProvider) summarizeCalls() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.summarizeCount
}

func (m *midTaskProvider) lastRequest() []llm.ContentPart {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.requests) == 0 {
		return nil
	}
	return m.requests[len(m.requests)-1]
}

// compactFailProvider asks for a tool on its first normal step (pushing the
// context over the threshold), answers a summarize request with nothing (so
// summarization fails), then finishes on the next normal step.
type compactFailProvider struct {
	mu             sync.Mutex
	normalCalls    int
	summarizeCount int
}

func (m *compactFailProvider) StreamMessages(_ context.Context, history []llm.ContentPart, _ []llm.ToolDefinition, _, _ string) (iter.Seq2[llm.StreamEvent, error], error) {
	hasSummarize := false
	for _, p := range history {
		if tp, ok := p.(*llm.TextPart); ok && tp.Text == summarizePrompt {
			hasSummarize = true
		}
	}
	m.mu.Lock()
	if hasSummarize {
		m.summarizeCount++
	} else {
		m.normalCalls++
	}
	n := m.normalCalls
	m.mu.Unlock()

	return func(yield func(llm.StreamEvent, error) bool) {
		switch {
		case hasSummarize:
			// No text, no tool call: summarizeContents rejects this.
			yield(llm.StepCompleteEvent{Usage: llm.Usage{InputTokens: 100, OutputTokens: 0}}, nil)
		case n == 1:
			if !yield(llm.TextDeltaEvent{Delta: "working", Key: "block:0"}, nil) {
				return
			}
			yield(llm.ToolInputStartEvent{ID: "c1", Name: "t", Key: "block:1"}, nil)
			yield(llm.ToolInputDeltaEvent{ID: "c1", Delta: `{}`, Key: "block:1"}, nil)
			yield(llm.ToolInputCompleteEvent{ID: "c1", Key: "block:1"}, nil)
			yield(llm.StepCompleteEvent{Usage: llm.Usage{InputTokens: 100, OutputTokens: 5}, StopReason: "tool_use"}, nil)
		default:
			if !yield(llm.TextDeltaEvent{Delta: "recovered", Key: "block:0"}, nil) {
				return
			}
			yield(llm.StepCompleteEvent{Usage: llm.Usage{InputTokens: 20, OutputTokens: 5}}, nil)
		}
	}, nil
}

func (m *compactFailProvider) SetReasoningLevel(_ int)                       {}
func (m *compactFailProvider) SetReasoningConfigs(_ map[int]json.RawMessage) {}
func (m *compactFailProvider) SetVideoConfig(_ int, _ int)                   {}

func (m *compactFailProvider) summarizeCalls() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.summarizeCount
}

// normalRequestCount counts the requests that were not summarize requests. A
// turn that fails to compact ends before its next normal step, so this stays at
// 1 mid-task and 0 at the task start.
func (m *compactFailProvider) normalRequestCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.normalCalls
}

// TestAutoSummarizeMidTaskFailureEndsTheTurn: a failed mid-task summarization
// ends the turn. Carrying on would send the oversized request the user's
// --auto-summarize threshold exists to prevent, so the step after the failed
// compaction is never issued — and the uncompressed history survives intact, so
// :continue resumes from it rather than from a truncated one.
func TestAutoSummarizeMidTaskFailureEndsTheTurn(t *testing.T) {
	provider := &compactFailProvider{}
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
			SessionConfig: SessionConfig{NoDelta: true, AutoSummarize: 65, Output: io.Discard, SessionFile: filepath.Join(t.TempDir(), "s.alaya")},
		},
		sharedState: sharedState{
			ContextLimit:  100,
			ContextTokens: 10,
		},
		runState: runState{
			taskEventCh: make(chan taskEvent, 20),
		},
	}
	session.Contents = []llm.ContentPart{
		&llm.TextPart{Text: "hi", ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleUser}},
	}

	// runOneTask has its own 5s deadline and calls t.Fatal from the test
	// goroutine, so a hang here fails the test rather than blocking forever.
	runOneTask(t, session, []llm.ContentPart{&llm.TextPart{Text: "do it"}})

	if got := provider.summarizeCalls(); got != 1 {
		t.Fatalf("summarizeCalls = %d, want 1 attempt", got)
	}
	if got := provider.normalRequestCount(); got != 1 {
		t.Fatalf("normalCalls = %d, want 1 (the turn ends instead of sending the next step)", got)
	}
	if containsText(session.Contents, "recovered") {
		t.Fatalf("the step after the failed compaction was issued: %v", texts(session.Contents))
	}
	if !containsText(session.Contents, "working") {
		t.Fatalf("failed compaction should leave the uncompressed history: %v", texts(session.Contents))
	}
}

// TestAutoSummarizeTaskStartFailureSkipsTheTurn: the same rule at the task
// start. The compaction runs before the user's parts are appended, so a failure
// ends the turn with the prompt never sent and the history untouched.
func TestAutoSummarizeTaskStartFailureSkipsTheTurn(t *testing.T) {
	provider := &compactFailProvider{}
	agent := llm.NewAgent(llm.AgentConfig{Provider: provider, MaxSteps: 10})
	session := &Session{
		sessionConfig: sessionConfig{
			modelService:  &modelService{agent: agent},
			SessionConfig: SessionConfig{NoDelta: true, AutoSummarize: 65, Output: io.Discard},
		},
		sharedState: sharedState{ContextLimit: 100, ContextTokens: 70}, // over the 65% threshold
		runState:    runState{taskEventCh: make(chan taskEvent, 20)},
	}
	session.Contents = []llm.ContentPart{
		&llm.TextPart{Text: "hi", ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleUser}},
	}

	runOneTask(t, session, []llm.ContentPart{&llm.TextPart{Text: "do it"}})

	if got := provider.summarizeCalls(); got != 1 {
		t.Fatalf("summarizeCalls = %d, want 1 (task-start summarize did not fire)", got)
	}
	if got := provider.normalRequestCount(); got != 0 {
		t.Fatalf("normalCalls = %d, want 0 (the prompt must not be sent after a failed compaction)", got)
	}
	if containsText(session.Contents, "do it") {
		t.Fatalf("the user's prompt was appended despite the failed compaction: %v", texts(session.Contents))
	}
	if !containsText(session.Contents, "hi") {
		t.Fatalf("the history should be preserved: %v", texts(session.Contents))
	}
}

func texts(parts []llm.ContentPart) []string {
	var out []string
	for _, p := range parts {
		if tp, ok := p.(*llm.TextPart); ok {
			out = append(out, string(tp.Role)+":"+tp.Text)
		}
	}
	return out
}

// TestAutoSummarizeRunsAtContinueStart pins that :continue — a turn like any
// other — runs the task-start auto-summarize, not only the mid-task one. Before
// that check, a retried turn whose history was already over the threshold would
// send its first request uncompressed.
func TestAutoSummarizeRunsAtContinueStart(t *testing.T) {
	provider := &midTaskProvider{}
	agent := llm.NewAgent(llm.AgentConfig{Provider: provider, MaxSteps: 10})
	session := &Session{
		sessionConfig: sessionConfig{
			modelService:  &modelService{agent: agent},
			SessionConfig: SessionConfig{NoDelta: true, AutoSummarize: 65, Output: io.Discard},
		},
		sharedState: sharedState{ContextLimit: 100, ContextTokens: 70}, // over the 65% threshold
		runState:    runState{taskEventCh: make(chan taskEvent, 20), taskResultCh: make(chan []llm.ContentPart, 1)},
	}
	session.Contents = []llm.ContentPart{
		&llm.TextPart{Text: "hi", ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleUser}},
		&llm.TextPart{Text: "partial", ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleAssistant}},
	}

	go session.runTaskContinue(context.Background())
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev := <-session.taskEventCh:
			session.handleTaskEvent(ev)
		case contents := <-session.taskResultCh:
			session.drainAndHandleDone(t, contents)
			if got := provider.summarizeCalls(); got != 1 {
				t.Fatalf("summarizeCalls = %d, want 1 (task-start summarize on :continue)", got)
			}
			if !containsText(session.Contents, "SUMMARY OF EARLIER WORK") {
				t.Fatalf("final contents missing the summary: %v", texts(session.Contents))
			}
			if !containsText(session.Contents, "done") {
				t.Fatalf("final contents missing the resumed answer: %v", texts(session.Contents))
			}
			return
		case <-deadline:
			t.Fatal("timed out waiting for :continue task")
		}
	}
}

// TestAutoSummarizeCompactsMidTask is the whole point of the feature: a single
// long turn whose context crosses the threshold must summarize part-way through
// — not only at the start of a turn — and then keep running on the summary.
func TestAutoSummarizeCompactsMidTask(t *testing.T) {
	provider := &midTaskProvider{}
	output := &syncOutput{}
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
			modelService: &modelService{agent: agent},
			// SessionFile set so the pre-summarize backup actually runs: it is
			// the path that reads the context size from the task goroutine, and
			// this test is where a race on that read would surface (-race).
			// Output captures the frames so the test can check the trailing
			// "Continue" is echoed to the adapter.
			SessionConfig: SessionConfig{NoDelta: true, AutoSummarize: 65, Output: output,
				SessionFile: filepath.Join(t.TempDir(), "s.alaya")},
		},
		sharedState: sharedState{
			ContextLimit:  100, // 65% = 65
			ContextTokens: 10,  // below threshold: the task-start check stays quiet
		},
		runState: runState{
			taskEventCh: make(chan taskEvent, 20),
		},
	}
	session.Contents = []llm.ContentPart{
		&llm.TextPart{Text: "hi", ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleUser}},
	}

	runOneTask(t, session, []llm.ContentPart{&llm.TextPart{Text: "do it"}})

	if got := provider.summarizeCalls(); got != 1 {
		t.Fatalf("summarizeCalls = %d, want 1 (mid-task compaction did not fire)", got)
	}
	if !containsText(session.Contents, "SUMMARY OF EARLIER WORK") {
		t.Fatalf("final contents missing the summary: %v", texts(session.Contents))
	}
	if !containsText(session.Contents, "done") {
		t.Fatalf("final contents missing the resumed answer: %v", texts(session.Contents))
	}
	if containsText(session.Contents, "working") {
		t.Fatalf("pre-compaction content survived into the final history: %v", texts(session.Contents))
	}

	// The request after compaction must end on a user turn. Ending on the
	// assistant summary would be read as prefill ("continue this assistant
	// turn"), not as a fresh turn to resume.
	tail := provider.lastRequest()
	if len(tail) == 0 || tail[len(tail)-1].GetRole() != llm.RoleUser {
		t.Fatalf("post-compaction request ends on %v, want a user turn", tail)
	}

	// The trailing "Continue" is part of the session's Contents, so the adapter
	// must have been shown it too — otherwise it would appear only after a
	// session reload, and the reloaded conversation would differ from the live
	// one.
	if !strings.Contains(output.String(), "Continue") {
		t.Fatalf("trailing Continue was not echoed to the adapter; output:\n%s", output.String())
	}

	// The summarize prompt is the one part the agent builds that no record
	// holds: the callers discard it, and the summary replaces the history it was
	// appended to. So it is not echoed as a user turn — an ID the adapter was
	// shown but Contents does not hold is one :fork cannot resolve.
	firstLine, _, _ := strings.Cut(summarizePrompt, "\n")
	if strings.Contains(output.String(), firstLine) {
		t.Fatalf("the summarize prompt was echoed as a user turn; output:\n%s", output.String())
	}
}

// TestAutoSummarizeMidTaskSkippedBelowThreshold guards the other side: when the
// step's context stays under the threshold, no mid-task summarize happens.
func TestAutoSummarizeMidTaskSkippedBelowThreshold(t *testing.T) {
	provider := &midTaskProvider{}
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
			SessionConfig: SessionConfig{NoDelta: true, AutoSummarize: 65},
		},
		sharedState: sharedState{
			ContextLimit:  10000, // 65% = 6500, far above the mock's ~105
			ContextTokens: 10,
		},
		runState: runState{
			taskEventCh: make(chan taskEvent, 20),
		},
	}
	session.Contents = []llm.ContentPart{
		&llm.TextPart{Text: "hi", ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleUser}},
	}

	runOneTask(t, session, []llm.ContentPart{&llm.TextPart{Text: "do it"}})

	if got := provider.summarizeCalls(); got != 0 {
		t.Fatalf("summarizeCalls = %d, want 0 (context never crossed the threshold)", got)
	}
	if !containsText(session.Contents, "working") {
		t.Fatalf("uncompacted content missing: %v", texts(session.Contents))
	}
}
