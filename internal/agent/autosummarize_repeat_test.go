package agent

// Regression tests for repeated auto-summarization (--auto-summarize).
//
// The threshold check lives in shouldAutoSummarize(), called at the start of
// every runTaskNormal. There is no "already summarized" flag, and a successful
// summarize resets ContextTokens to the summary size, so it can grow back over
// the threshold and trigger again. These tests pin that down so a future change
// cannot silently turn auto-summarize into a once-per-session action.
//
//   - TestAutoSummarizeTriggersRepeatedly      — three over-threshold tasks,
//     each must summarize (drives runTaskNormal + handleTaskEvent by hand).
//   - TestAutoSummarizeEndToEnd_RealRunLoop    — the real run() loop, several
//     growing prompts through the input pipe, summarizes on each crossing.
//   - TestAutoSummarizeStallWhenUsageOmitted   — documents the one way it can
//     *look* one-shot: a provider that stops reporting usage leaves
//     ContextTokens frozen at the last value (here, the summary size).

import (
	"context"
	"encoding/json"
	"io"
	"iter"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/tlv"
)

// recordProvider records every request's history and counts how many of them
// carried the summarize prompt. Usage is reported as the real history size
// (like a provider would), so the drop after a summarize is observable.
//
// omitUsageAfterSummarize models OpenAI-compatible providers (see
// docs/context-tracking.md, GLM-5.1) that intermittently omit the `usage`
// field: once a summarize request has been seen, usage is reported as zero.
type recordProvider struct {
	mu             sync.Mutex
	calls          [][]llm.ContentPart
	summarizeCount int
	omitUsage      bool
	silentAfter    bool // once a summarize is seen, stop reporting usage
}

func (m *recordProvider) StreamMessages(_ context.Context, history []llm.ContentPart, _ []llm.ToolDefinition, _, _ string) (iter.Seq2[llm.StreamEvent, error], error) {
	isSummarize := false
	for _, p := range history {
		if tp, ok := p.(*llm.TextPart); ok && tp.Text == summarizePrompt {
			isSummarize = true
			break
		}
	}

	m.mu.Lock()
	cp := make([]llm.ContentPart, len(history))
	copy(cp, history)
	m.calls = append(m.calls, cp)
	omit := m.omitUsage
	if isSummarize {
		m.summarizeCount++
		if m.silentAfter {
			// The summarize answer reports usage; everything after is silent.
			m.omitUsage = true
		}
	}
	m.mu.Unlock()

	var in int64
	for _, p := range history {
		if tp, ok := p.(*llm.TextPart); ok {
			in += int64(len(tp.Text))
		}
	}
	if omit {
		in = 0
	}

	text := "ANS"
	if isSummarize {
		text = "SUM"
	}
	return func(yield func(llm.StreamEvent, error) bool) {
		if !yield(llm.TextDeltaEvent{Delta: text, Key: "block:0"}, nil) {
			return
		}
		if !yield(llm.TextCompleteEvent{Key: "block:0"}, nil) {
			return
		}
		yield(llm.StepCompleteEvent{Usage: llm.Usage{InputTokens: in, OutputTokens: 5}}, nil)
	}, nil
}

func (m *recordProvider) SetReasoningLevel(_ int)                       {}
func (m *recordProvider) SetReasoningConfigs(_ map[int]json.RawMessage) {}
func (m *recordProvider) SetVideoConfig(_ int, _ int)                   {}

func (m *recordProvider) summarizeCalls() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.summarizeCount
}

// runOneTask starts a normal task and drains its events + result the way run()
// does, returning after handleTaskDone.
func runOneTask(t *testing.T, s *Session, parts []llm.ContentPart) {
	t.Helper()
	s.taskResultCh = make(chan []llm.ContentPart, 1)
	go s.runTaskNormal(context.Background(), parts)
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev := <-s.taskEventCh:
			s.handleTaskEvent(ev)
		case contents := <-s.taskResultCh:
			s.drainAndHandleDone(t, contents)
			return
		case <-deadline:
			t.Fatal("timed out waiting for task")
		}
	}
}

func newAutoSummarizeTestSession(provider llm.Provider, limit int64, tokens int64) *Session {
	return &Session{
		sessionConfig: sessionConfig{
			modelService:  &modelService{agent: llm.NewAgent(llm.AgentConfig{Provider: provider, MaxSteps: 10})},
			SessionConfig: SessionConfig{NoDelta: true, AutoSummarize: 65},
		},
		sharedState: sharedState{
			ContextLimit:  limit,
			ContextTokens: tokens,
		},
		runState: runState{
			taskEventCh: make(chan taskEvent, 20),
		},
	}
}

func TestAutoSummarizeTriggersRepeatedly(t *testing.T) {
	provider := &recordProvider{}
	session := newAutoSummarizeTestSession(provider, 100, 70) // 70 >= 65% of 100
	session.Contents = []llm.ContentPart{
		&llm.TextPart{Text: "old user", ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleUser}},
		&llm.TextPart{Text: "old assistant", ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleAssistant}},
	}

	for round := 1; round <= 3; round++ {
		runOneTask(t, session, []llm.ContentPart{&llm.TextPart{Text: "p" + string(rune('0'+round))}})
		if got := provider.summarizeCalls(); got != round {
			t.Fatalf("round %d: summarizeCalls = %d, want %d (auto-summarize must fire every time it is over threshold)",
				round, got, round)
		}
		if round == 1 && session.ContextTokens >= 65 {
			t.Fatalf("summarize left ContextTokens=%d over the threshold; it could never trigger again", session.ContextTokens)
		}
		// The conversation grows back over the threshold.
		session.ContextTokens = 70
	}
}

func TestAutoSummarizeEndToEnd_RealRunLoop(t *testing.T) {
	output := &syncOutput{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r, w := io.Pipe()
	defer w.Close()

	provider := &recordProvider{}
	agent := llm.NewAgent(llm.AgentConfig{Provider: provider, MaxSteps: 10})

	s := &Session{
		sessionConfig: sessionConfig{
			modelService: &modelService{agent: agent, provider: provider, contextLimit: 1000},
			SessionConfig: SessionConfig{
				Input:         r,
				Output:        output,
				NoDelta:       true,
				MaxSteps:      10,
				AutoSummarize: 65,
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

	// Each prompt is longer than the 650-token threshold, so every task after
	// the first is over the threshold when it starts.
	long := strings.Repeat("x", 800)
	for i := 1; i <= 3; i++ {
		if err := tlv.WriteTLV(w, tlv.TagUserT, tlv.WrapID("1", long)); err != nil {
			t.Fatalf("write UT: %v", err)
		}
		if err := tlv.WriteTLV(w, tlv.TagUserEnd, ""); err != nil {
			t.Fatalf("write UE: %v", err)
		}
		// Wait for this task's answer before submitting the next prompt;
		// otherwise it would be refused as BUSY. Task 1 answers once, tasks 2
		// and 3 each add a summarize pass plus an answer.
		want := i
		deadline := time.Now().Add(3 * time.Second)
		for strings.Count(output.String(), "ANS") < want {
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting for task %d; output:\n%s", i, output.String())
			}
			time.Sleep(10 * time.Millisecond)
		}
	}

	if got := provider.summarizeCalls(); got < 2 {
		t.Fatalf("auto-summarize fired %d times over 3 growing prompts, want >= 2", got)
	}
	t.Logf("auto-summarize fired %d times over 3 growing prompts", provider.summarizeCalls())

	_ = w.Close()
	select {
	case <-s.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("session did not finish")
	}
}

// TestAutoSummarizeStallWhenUsageOmitted documents the one observed way
// auto-summarize can appear to be once-only: the summarize response reports
// usage (resetting ContextTokens to the summary size) but the provider then
// stops reporting usage, so ContextTokens is frozen at that small value and
// never reaches the threshold again even as the real conversation grows.
func TestAutoSummarizeStallWhenUsageOmitted(t *testing.T) {
	provider := &recordProvider{silentAfter: true}
	session := newAutoSummarizeTestSession(provider, 100, 70)
	session.Contents = []llm.ContentPart{
		&llm.TextPart{Text: "old user", ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleUser}},
		&llm.TextPart{Text: strings.Repeat("y", 200), ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleAssistant}},
	}

	runOneTask(t, session, []llm.ContentPart{&llm.TextPart{Text: "p1"}})
	if got := provider.summarizeCalls(); got != 1 {
		t.Fatalf("round 1: summarizeCalls = %d, want 1", got)
	}
	t.Logf("after round 1 (usage then silent): ContextTokens=%d", session.ContextTokens)

	// The real conversation keeps growing, but no usage is reported, so the
	// observed token count cannot move.
	session.Contents = append(session.Contents, &llm.TextPart{
		Text: strings.Repeat("z", 400), ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleUser},
	})
	runOneTask(t, session, []llm.ContentPart{&llm.TextPart{Text: "p2"}})

	if got := provider.summarizeCalls(); got != 1 {
		t.Fatalf("summarizeCalls = %d; this test documents the stall, but usage was reported", got)
	}
	t.Logf("round 2 did NOT summarize: ContextTokens stayed at %d (threshold 65) while the real history grew", session.ContextTokens)
}
