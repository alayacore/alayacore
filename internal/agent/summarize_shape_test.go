package agent

// The summarize request is an ordinary LLM call, so the model can answer it the
// way it can answer any other: with reasoning only, with an answer before the
// reasoning that produced it, or with nothing. The first two carry the summary
// text the caller needs if it is read from the right place; the textless ones
// get one retry. These tests pin all three.

import (
	"context"
	"encoding/json"
	"iter"
	"strings"
	"sync"
	"testing"

	"github.com/alayacore/alayacore/internal/llm"
)

// summaryStep is one scripted answer to a summarize call — one agent step. A
// step can call a tool, which makes the loop run another step.
type summaryStep struct {
	text      string
	reasoning string
	// textFirst streams the text before the reasoning. Providers that declare
	// no block positions (the default here) leave the record in arrival order,
	// so this is how a response whose answer precedes its reasoning is built.
	textFirst bool
	toolCall  bool
}

// scriptedSummarizer answers each StreamMessages call with the next scripted
// step, repeating the last one when the script runs out, and records the
// histories it was sent.
type scriptedSummarizer struct {
	mu        sync.Mutex
	steps     []summaryStep
	histories [][]llm.ContentPart
}

func (p *scriptedSummarizer) StreamMessages(_ context.Context, history []llm.ContentPart, _ []llm.ToolDefinition, _, _ string) (iter.Seq2[llm.StreamEvent, error], error) {
	p.mu.Lock()
	cp := make([]llm.ContentPart, len(history))
	copy(cp, history)
	p.histories = append(p.histories, cp)
	idx := len(p.histories) - 1
	if idx >= len(p.steps) {
		idx = len(p.steps) - 1
	}
	step := p.steps[idx]
	p.mu.Unlock()

	return func(yield func(llm.StreamEvent, error) bool) {
		reasoning := func() bool {
			if step.reasoning == "" {
				return true
			}
			return yield(llm.ReasoningDeltaEvent{Delta: step.reasoning, Key: "reasoning"}, nil)
		}
		text := func() bool {
			if step.text == "" {
				return true
			}
			return yield(llm.TextDeltaEvent{Delta: step.text, Key: "text"}, nil)
		}
		if step.textFirst {
			if !text() || !reasoning() {
				return
			}
		} else {
			if !reasoning() || !text() {
				return
			}
		}
		if step.toolCall {
			if !yield(llm.ToolInputStartEvent{ID: "c1", Name: "t", Key: "tool"}, nil) {
				return
			}
			if !yield(llm.ToolInputDeltaEvent{ID: "c1", Delta: `{}`, Key: "tool"}, nil) {
				return
			}
			if !yield(llm.ToolInputCompleteEvent{ID: "c1", Key: "tool"}, nil) {
				return
			}
			yield(llm.StepCompleteEvent{Usage: llm.Usage{InputTokens: 10, OutputTokens: 5}, StopReason: "tool_use"}, nil)
			return
		}
		yield(llm.StepCompleteEvent{Usage: llm.Usage{InputTokens: 10, OutputTokens: 5}}, nil)
	}, nil
}

func (p *scriptedSummarizer) SetReasoningLevel(_ int)                       {}
func (p *scriptedSummarizer) SetReasoningConfigs(_ map[int]json.RawMessage) {}

func (p *scriptedSummarizer) calls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.histories)
}

func (p *scriptedSummarizer) lastHistory() []llm.ContentPart {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.histories) == 0 {
		return nil
	}
	return p.histories[len(p.histories)-1]
}

// summarizeShapeSession builds the smallest session a summarize needs: an agent
// over provider, and the event channel sendEvent writes to.
func summarizeShapeSession(provider llm.Provider) *Session {
	agent := llm.NewAgent(llm.AgentConfig{
		Provider: provider,
		Tools: []llm.Tool{{
			Definition: llm.ToolDefinition{Name: "t", Description: "test", Schema: []byte(`{"type":"object"}`)},
			Execute: func(_ context.Context, _ json.RawMessage) ([]llm.ContentPart, error) {
				return []llm.ContentPart{&llm.TextPart{Text: "tool output", ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleTool}}}, nil
			},
		}},
		MaxSteps: 5,
	})
	return &Session{
		sessionConfig: sessionConfig{
			modelService:  &modelService{agent: agent},
			SessionConfig: SessionConfig{NoDelta: true},
		},
		sharedState: sharedState{histCounter: 100},
		runState:    runState{taskEventCh: make(chan taskEvent, 20)},
	}
}

func summarizeInput() []llm.ContentPart {
	return []llm.ContentPart{
		&llm.TextPart{Text: "original", ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleUser}},
	}
}

// TestSummarizeRetriesWhenTheModelWritesNoText is the failure the retry exists
// for: the model reasons its way to the answer and stops before writing it. The
// retry re-sends the original request with one part saying the last reply
// carried no text, so it cannot outgrow the request that just fit.
func TestSummarizeRetriesWhenTheModelWritesNoText(t *testing.T) {
	provider := &scriptedSummarizer{steps: []summaryStep{
		{reasoning: "The user wants a summary. Now write it."},
		{text: "SUMMARY"},
	}}
	session := summarizeShapeSession(provider)

	result, err := session.doAutoSummarize(context.Background(), summarizeInput())
	if err != nil {
		t.Fatalf("doAutoSummarize: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("result has %d parts, want the request and the summary: %#v", len(result), result)
	}
	if tp, ok := result[1].(*llm.TextPart); !ok || tp.Text != "SUMMARY" {
		t.Errorf("result[1] = %#v, want the retried summary", result[1])
	}
	if got := provider.calls(); got != 2 {
		t.Fatalf("summarize requests = %d, want 2 (the response that wrote no text, then the retry)", got)
	}

	// The retry is the original request again — the conversation, the summarize
	// prompt, then one part saying the last reply carried no text. It must NOT
	// carry the failed attempt: that response would make the retry larger than
	// the request that just fit.
	history := provider.lastHistory()
	want := len(summarizeInput()) + 2
	if len(history) != want {
		t.Fatalf("retry history has %d parts, want %d (the conversation, the prompt, the corrective part): %#v",
			len(history), want, history)
	}
	prompt, ok := history[len(history)-2].(*llm.TextPart)
	if !ok || prompt.Role != llm.RoleUser || prompt.Text != summarizePrompt {
		t.Errorf("retry history's prompt = %#v, want the summarize prompt", history[len(history)-2])
	}
	last, ok := history[len(history)-1].(*llm.TextPart)
	if !ok || last.Role != llm.RoleUser || last.Text != summarizeRetryPrompt {
		t.Errorf("retry history ends with %#v, want the corrective part", history[len(history)-1])
	}
	for _, p := range history {
		if _, ok := p.(*llm.ReasoningPart); ok {
			t.Error("retry history carries the failed attempt's reasoning; the retry must not grow past the request that fit")
		}
	}
}

// TestSummarizeAcceptsTextBeforeReasoning covers a response whose answer comes
// before the reasoning that produced it. Block layout is the provider's to
// declare, and a provider that declares none leaves the record in arrival
// order; the summary is in the response either way, so it must not be thrown
// away for not being the final part.
func TestSummarizeAcceptsTextBeforeReasoning(t *testing.T) {
	provider := &scriptedSummarizer{steps: []summaryStep{
		{text: "SUMMARY", reasoning: "Wrapping up.", textFirst: true},
	}}
	session := summarizeShapeSession(provider)

	result, err := session.doAutoSummarize(context.Background(), summarizeInput())
	if err != nil {
		t.Fatalf("doAutoSummarize: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("result has %d parts, want the request and the summary: %#v", len(result), result)
	}
	if tp, ok := result[1].(*llm.TextPart); !ok || tp.Text != "SUMMARY" {
		t.Errorf("result[1] = %#v, want the summary that preceded its reasoning", result[1])
	}
	if got := provider.calls(); got != 1 {
		t.Errorf("summarize requests = %d, want 1 (the text was in the response)", got)
	}
}

// TestSummarizeDoesNotAcceptWhitespaceText: a text block that is only
// whitespace is not a summary — accepting it would replace the conversation
// with nothing — so the response counts as textless and the retry gets its chance.
func TestSummarizeDoesNotAcceptWhitespaceText(t *testing.T) {
	provider := &scriptedSummarizer{steps: []summaryStep{
		{text: "  \n\t", reasoning: "Formatting only."},
		{text: "SUMMARY"},
	}}
	session := summarizeShapeSession(provider)

	result, err := session.doAutoSummarize(context.Background(), summarizeInput())
	if err != nil {
		t.Fatalf("doAutoSummarize: %v", err)
	}
	if tp, ok := result[1].(*llm.TextPart); !ok || tp.Text != "SUMMARY" {
		t.Errorf("result[1] = %#v, want the retried summary, not whitespace", result[1])
	}
	if got := provider.calls(); got != 2 {
		t.Errorf("summarize requests = %d, want 2 (whitespace is not a summary)", got)
	}
}

// TestSummarizeIgnoresTextFromAnEarlierStep: a summarize runs the same
// multi-step loop as a task, so a step that calls a tool may emit text before
// doing so. That preamble is not the summary, and a final step that wrote no
// text must not promote it to one.
func TestSummarizeIgnoresTextFromAnEarlierStep(t *testing.T) {
	provider := &scriptedSummarizer{steps: []summaryStep{
		{text: "an earlier step's text", toolCall: true},
		{reasoning: "Round 22 done. Now write it."},
		{text: "SUMMARY"},
	}}
	session := summarizeShapeSession(provider)

	result, err := session.doAutoSummarize(context.Background(), summarizeInput())
	if err != nil {
		t.Fatalf("doAutoSummarize: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("result has %d parts, want the request and the summary: %#v", len(result), result)
	}
	if tp, ok := result[1].(*llm.TextPart); !ok || tp.Text != "SUMMARY" {
		t.Errorf("result[1] = %#v, want the retried summary, not the earlier step's chatter", result[1])
	}
	if got := provider.calls(); got != 3 {
		t.Errorf("summarize requests = %d, want 3 (tool step, reasoning-only step, retry)", got)
	}
}

// TestSummarizeGivesUpAfterOneRetry: the retry is bounded. A model that answers
// with reasoning twice still fails closed — the task ends rather than continuing
// on the uncompressed history — and the original contents come back untouched.
func TestSummarizeGivesUpAfterOneRetry(t *testing.T) {
	provider := &scriptedSummarizer{steps: []summaryStep{
		{reasoning: "Still thinking."},
	}}
	session := summarizeShapeSession(provider)

	result, err := session.doAutoSummarize(context.Background(), summarizeInput())
	if err == nil {
		t.Fatal("doAutoSummarize returned nil error when both attempts wrote no text")
	}
	if !strings.Contains(err.Error(), "no text") {
		t.Errorf("error = %q, want the no-text failure", err)
	}
	if got := provider.calls(); got != 2 {
		t.Errorf("summarize requests = %d, want 2 (one retry, not a loop)", got)
	}
	if len(result) != 1 {
		t.Fatalf("expected the original contents back, got %#v", result)
	}
	if tp, ok := result[0].(*llm.TextPart); !ok || tp.Text != "original" {
		t.Errorf("result[0] = %#v, want the original content", result[0])
	}
}
