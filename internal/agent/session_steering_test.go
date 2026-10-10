package agent

// Tests for steering: a prompt that arrives while a task is running is spliced
// into that turn at its next step boundary, instead of being refused.
//
// What these pin, in the order the design depends on them:
//
//   - TestSteeringInjectedAtStepBoundary      — the splice lands after the tool
//     result, the words ride that step's delta into Contents exactly once, and
//     the history ID the adapter was shown resolves there (no dangling ID).
//   - TestSteeringSplicedPartIsAUserMessage   — the spliced part is a user part
//     at the instant the request carrying it is built (an empty role fails the
//     whole API request), so the role is set at the splice, not at the commit.
//   - TestSteeringLeftoverDeliveredAsNextPrompt — a turn that ends without a
//     next step still delivers the words: they become the next prompt.
//   - TestSteeringSurvivesFailedStep          — the regression guard: a step
//     whose request fails before producing anything must neither leave a
//     dangling ID nor lose the input. The batch goes back to the queue and is
//     delivered as the next prompt.
//   - TestSteeringNotTakenBySummarizeCall     — a summarize call (an internal
//     helper of a turn) must not drain the queue.
//   - TestSteeringCommitIsOnceOnly            — a committed batch is not
//     re-numbered or re-published by later steps.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/tlv"
)

// steeringStep is one scripted provider call: the text it streams, the tool
// call it makes, a gate the *request* waits on before the stream is returned
// (the shape of a request in flight), and an optional pre-stream failure.
type steeringStep struct {
	text     string
	toolCall *llm.ToolInputPart
	preWait  <-chan struct{}
	failErr  error
}

// steeringProvider is a scripted provider that records the history every call
// was sent on, so a test can assert on what the model actually saw.
//
// It records two views of each call, and the difference matters: calls holds the
// history slice (whose elements are the live part pointers, so a later
// commitSteering mutates what a test reads back), while rolesAtSend captures
// each part's role at the instant the call was issued — the moment a real
// provider serializes its wire messages. A check on calls alone cannot see what
// role a part was sent with.
type steeringProvider struct {
	mu    sync.Mutex
	steps []steeringStep
	calls [][]llm.ContentPart
	roles [][]llm.MessageRole
}

func (p *steeringProvider) StreamMessages(ctx context.Context, history []llm.ContentPart, _ []llm.ToolDefinition, _, _ string) (iter.Seq2[llm.StreamEvent, error], error) {
	p.mu.Lock()
	i := len(p.calls)
	if i >= len(p.steps) {
		p.mu.Unlock()
		return nil, fmt.Errorf("steeringProvider: unexpected call %d", i+1)
	}
	step := p.steps[i]
	cp := make([]llm.ContentPart, len(history))
	copy(cp, history)
	p.calls = append(p.calls, cp)
	roles := make([]llm.MessageRole, len(history))
	for j, part := range history {
		roles[j] = part.GetRole()
	}
	p.roles = append(p.roles, roles)
	p.mu.Unlock()

	// A request in flight: the call is recorded (so a test can see what it was
	// sent on), then the "transport" blocks. A request issued on a canceled
	// context never reaches the provider — this is what a real transport does,
	// and the canceled-step tests depend on it.
	if step.preWait != nil {
		<-step.preWait
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if step.failErr != nil {
		return nil, step.failErr
	}
	return func(yield func(llm.StreamEvent, error) bool) {
		if step.text != "" {
			if !yield(llm.TextDeltaEvent{Delta: step.text, Key: "block:0"}, nil) {
				return
			}
			if !yield(llm.TextCompleteEvent{Key: "block:0"}, nil) {
				return
			}
		}
		if step.toolCall != nil {
			if !yield(llm.ToolInputStartEvent{ID: step.toolCall.ID, Name: step.toolCall.Name, Key: "block:1"}, nil) {
				return
			}
			if !yield(llm.ToolInputDeltaEvent{ID: step.toolCall.ID, Delta: string(step.toolCall.Input), Key: "block:1"}, nil) {
				return
			}
			if !yield(llm.ToolInputCompleteEvent{ID: step.toolCall.ID, Key: "block:1"}, nil) {
				return
			}
		}
		yield(llm.StepCompleteEvent{Usage: llm.Usage{InputTokens: 10, OutputTokens: 5}}, nil)
	}, nil
}

func (p *steeringProvider) SetReasoningLevel(_ int)                       {}
func (p *steeringProvider) SetReasoningConfigs(_ map[int]json.RawMessage) {}
func (p *steeringProvider) SetVideoConfig(_ int, _ int)                   {}

// request returns the history the n-th provider call (0-based) was sent on.
//
// Its elements are the live part pointers, so a field a later step mutates — the
// steering part's HistoryID, stamped at the commit — reads back at its committed
// value rather than its sent value. That mutation is also why a role that went
// out as "" is invisible here: a commit used to set it to "user" only after the
// request had gone. Use rolesAtSend for the roles as they actually went out.
func (p *steeringProvider) request(n int) []llm.ContentPart {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls[n]
}

// rolesAtSend returns each part's role as it was when the n-th call (0-based)
// was issued — the moment a real provider serializes its wire messages, and the
// only moment a roleless part is observable at all.
func (p *steeringProvider) rolesAtSend(n int) []llm.MessageRole {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.roles[n]
}

func (p *steeringProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.calls)
}

// steeringSession builds a session that is already Ready with an initialized
// agent, so a promoted leftover really starts its next task through beginTask —
// exactly as it does under run().
func steeringSession(t *testing.T, output *syncOutput, provider llm.Provider, tools []llm.Tool, contents []llm.ContentPart) *Session {
	t.Helper()

	agent := llm.NewAgent(llm.AgentConfig{Provider: provider, Tools: tools, MaxSteps: 10})
	sessionCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	s := &Session{
		sessionConfig: sessionConfig{
			modelService: &modelService{agent: agent, provider: provider},
			SessionConfig: SessionConfig{
				Output:   output,
				NoDelta:  true,
				MaxSteps: 10,
			},
		},
		sharedState: sharedState{
			sessionCtx:    sessionCtx,
			sessionCancel: cancel,
			histCounter:   200,
			outputBroken:  atomic.Bool{},
		},
		runState: runState{
			Contents:     contents,
			taskEventCh:  make(chan taskEvent, 64),
			taskResultCh: make(chan []llm.ContentPart, 1),
			cancelReqCh:  make(chan chan bool, 1),
		},
	}
	s.state.Store(int32(SessionReady))
	return s
}

// driveRun plays run()'s event loop for a test: it applies task events, commits
// each finished task through handleTaskDone, and keeps going while that commit
// started another task (the leftover-steering promotion). It returns once
// nothing is in flight.
func driveRun(t *testing.T, s *Session) {
	t.Helper()
	for {
		select {
		case ev := <-s.taskEventCh:
			s.handleTaskEvent(ev)
		case contents := <-s.taskResultCh:
			s.handleTaskDone(contents)
			if s.activeTask == nil {
				return
			}
		case <-time.After(5 * time.Second):
			t.Fatal("driveRun timed out waiting for task events")
		}
	}
}

// echoedUserIDs returns, in order, the history IDs of the UT frames written to
// the adapter — the IDs a client would hold windows for.
func echoedUserIDs(t *testing.T, out string) []string {
	t.Helper()
	var ids []string
	r := strings.NewReader(out)
	for {
		tag, value, err := tlv.ReadTLV(r)
		if err != nil {
			return ids
		}
		if tag != tlv.TagUserT {
			continue
		}
		if id, _, ok := tlv.UnwrapID(value); ok {
			ids = append(ids, id)
		}
	}
}

// indexOfSteering returns the index of the steering text in parts, or -1.
func indexOfSteering(parts []llm.ContentPart) int {
	for i, p := range parts {
		if tp, ok := p.(*llm.TextPart); ok && tp.Text == steeringText {
			return i
		}
	}
	return -1
}

func indexOfToolOutput(parts []llm.ContentPart) int {
	for i, p := range parts {
		if _, ok := p.(*llm.ToolOutputPart); ok {
			return i
		}
	}
	return -1
}

// summary renders parts compactly for failure messages: the pointers a %v prints
// say nothing about what was actually in the history.
func summary(parts []llm.ContentPart) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, p := range parts {
		if i > 0 {
			b.WriteString(", ")
		}
		switch v := p.(type) {
		case *llm.TextPart:
			fmt.Fprintf(&b, "%s(%q)", v.Role, v.Text)
		case *llm.ToolInputPart:
			fmt.Fprintf(&b, "tool_use(%s)", v.Name)
		case *llm.ToolOutputPart:
			fmt.Fprintf(&b, "tool_result(%s)", v.ID)
		default:
			b.WriteString("?")
		}
	}
	b.WriteByte(']')
	return b.String()
}

const steeringText = "actually, use Y instead"

// The splice lands after the tool result the model just asked for, reaches the
// model in the next step's request, and enters Contents exactly once — with the
// history ID the adapter was shown, so a :fork on that ID resolves.
func TestSteeringInjectedAtStepBoundary(t *testing.T) {
	output := &syncOutput{}
	toolStarted := make(chan struct{})
	releaseTool := make(chan struct{})

	provider := &steeringProvider{steps: []steeringStep{
		{text: "Let me check.", toolCall: &llm.ToolInputPart{ID: "c1", Name: "t", Input: []byte(`{}`)}},
		{text: "Done, using Y."},
	}}
	tool := llm.Tool{
		Definition: llm.ToolDefinition{Name: "t", Description: "test", Schema: []byte(`{"type":"object"}`)},
		Execute: func(_ context.Context, _ json.RawMessage) ([]llm.ContentPart, error) {
			close(toolStarted)
			<-releaseTool
			return []llm.ContentPart{&llm.TextPart{Text: "the tool result"}}, nil
		},
	}

	s := steeringSession(t, output, provider, []llm.Tool{tool}, []llm.ContentPart{
		&llm.TextPart{Text: "earlier", ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleUser}},
	})

	go s.runTaskNormal(context.Background(), []llm.ContentPart{&llm.TextPart{Text: "do it"}})

	// The tool is running: this is the window a steering prompt arrives in.
	<-toolStarted
	if !s.queueSteering([]llm.ContentPart{&llm.TextPart{Text: steeringText}}) {
		t.Fatal("queueSteering refused a prompt that fits")
	}
	close(releaseTool)

	driveRun(t, s)

	// The next step's request must carry the words, after the tool result the
	// model asked for — never between a call and its answer.
	req := provider.request(1)
	if got := indexOfSteering(req); got < 0 {
		t.Fatalf("steering never reached the model; step 2 request = %s", summary(req))
	} else if tool := indexOfToolOutput(req); got != tool+1 {
		t.Fatalf("steering at %d, tool result at %d: the words must come straight after the result", got, tool)
	}

	// Contents: once, after the tool result, before the answer to it.
	inContents := 0
	for _, p := range s.Contents {
		if tp, ok := p.(*llm.TextPart); ok && tp.Text == steeringText {
			inContents++
		}
	}
	if inContents != 1 {
		t.Fatalf("steering appears %d times in Contents, want exactly 1: %s", inContents, summary(s.Contents))
	}
	idxSteer, idxTool := indexOfSteering(s.Contents), indexOfToolOutput(s.Contents)
	if idxTool < 0 || idxSteer != idxTool+1 {
		t.Fatalf("Contents order wrong: steering at %d, tool result at %d", idxSteer, idxTool)
	}
	if next := s.Contents[idxSteer+1]; next.GetRole() != llm.RoleAssistant {
		t.Fatalf("part after the steering = %v, want the assistant's answer", next)
	}

	// The ID the adapter was shown is the ID Contents holds. This is the
	// property the two-phase commit exists for.
	ids := echoedUserIDs(t, output.String())
	if len(ids) != 2 { // the prompt, then the steering
		t.Fatalf("echoed user IDs = %v, want [prompt, steering]", ids)
	}
	steerID := s.Contents[idxSteer].GetHistoryID()
	if want := fmt.Sprintf("%d", steerID); ids[1] != want {
		t.Fatalf("adapter was shown ID %q for the steering, Contents holds %d", ids[1], steerID)
	}
}

// The part a splice adds must be a well-formed user message by the time the
// request is built. The providers group the history by role and write that role
// onto each wire message (llm.GroupByRole → openaiConvertContents), so a part
// whose role is still the empty string goes out as an empty-role message and a
// real API rejects the whole request — the 422 "unknown variant" error that
// names a message index rather than the steering that caused it.
//
// The role is therefore given at the splice (onBeforeSend), not at the commit:
// the commit runs at the step's OnStepFinish, after the request has already
// gone. This test reads the roles captured at send time — request(n) holds the
// same part pointers and would show the committed role instead.
func TestSteeringSplicedPartIsAUserMessage(t *testing.T) {
	output := &syncOutput{}
	toolStarted := make(chan struct{})
	releaseTool := make(chan struct{})

	provider := &steeringProvider{steps: []steeringStep{
		{text: "Let me check.", toolCall: &llm.ToolInputPart{ID: "c1", Name: "t", Input: []byte(`{}`)}},
		{text: "Done."},
	}}
	tool := llm.Tool{
		Definition: llm.ToolDefinition{Name: "t", Description: "test", Schema: []byte(`{"type":"object"}`)},
		Execute: func(_ context.Context, _ json.RawMessage) ([]llm.ContentPart, error) {
			close(toolStarted)
			<-releaseTool
			return []llm.ContentPart{&llm.TextPart{Text: "the tool result"}}, nil
		},
	}
	s := steeringSession(t, output, provider, []llm.Tool{tool}, []llm.ContentPart{
		&llm.TextPart{Text: "earlier", ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleUser}},
	})

	go s.runTaskNormal(context.Background(), []llm.ContentPart{&llm.TextPart{Text: "do it"}})
	<-toolStarted
	if !s.queueSteering([]llm.ContentPart{&llm.TextPart{Text: steeringText}}) {
		t.Fatal("queueSteering refused a prompt that fits")
	}
	close(releaseTool)

	driveRun(t, s)

	roles := provider.rolesAtSend(1)
	idx := indexOfSteering(provider.request(1))
	if idx < 0 {
		t.Fatalf("steering never reached the model: %s", summary(provider.request(1)))
	}
	if got := roles[idx]; got != llm.RoleUser {
		t.Errorf("the spliced part went out with role %q, want %q — a real provider rejects an empty role", got, llm.RoleUser)
	}
	// One roleless part fails the entire request, so none may be roleless.
	for i, role := range roles {
		if role == "" {
			t.Errorf("request part %d went out with an empty role: %v", i, roles)
		}
	}
}

// A turn that ends without another step (the model answered without calling a
// tool) still delivers the words: they become the next prompt.
func TestSteeringLeftoverDeliveredAsNextPrompt(t *testing.T) {
	output := &syncOutput{}
	stepOneMayFinish := make(chan struct{})

	provider := &steeringProvider{steps: []steeringStep{
		{text: "First answer.", preWait: stepOneMayFinish},
		{text: "Second answer."},
	}}
	s := steeringSession(t, output, provider, nil, []llm.ContentPart{
		&llm.TextPart{Text: "earlier", ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleUser}},
	})

	go s.runTaskNormal(context.Background(), []llm.ContentPart{&llm.TextPart{Text: "do it"}})

	// Wait until step 1 is past its OnBeforeSend (the stream is in flight), so
	// the words cannot be spliced into step 1 — they can only be leftover.
	waitFor(t, func() bool { return provider.callCount() == 1 }, "step 1 to start")
	if !s.queueSteering([]llm.ContentPart{&llm.TextPart{Text: steeringText}}) {
		t.Fatal("queueSteering refused a prompt that fits")
	}
	close(stepOneMayFinish)

	driveRun(t, s)

	// The first turn had no tool call, so it ended before a boundary existed;
	// the words must have become the next prompt rather than being dropped.
	if n := provider.callCount(); n != 2 {
		t.Fatalf("provider calls = %d, want 2 (the turn, then the promoted prompt)", n)
	}
	req := provider.request(1)
	if idx := indexOfSteering(req); idx < 0 {
		t.Fatalf("the promoted prompt did not carry the words: %s", summary(req))
	} else if idx != len(req)-1 {
		t.Fatalf("the words are not the trailing user turn: %s", summary(req))
	}

	count := 0
	for _, p := range s.Contents {
		if tp, ok := p.(*llm.TextPart); ok && tp.Text == steeringText {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("steering appears %d times in Contents, want exactly 1: %s", count, summary(s.Contents))
	}
}

// A turn that does not land takes its steering with it. The words were a
// modification of that turn; with the turn gone there is nothing for them to
// modify, so they are dropped (and said so) rather than re-sent — re-sending
// would either spend a turn the user did not ask for or fold the correction into
// whatever they type next.
//
// This is also where the dangling-ID hazard would have lived under a naive
// design: the batch was spliced into a request that never produced anything, so
// if it had been echoed at injection the adapter would hold an ID that no
// Contents entry resolves.
func TestSteeringDroppedWhenTurnFails(t *testing.T) {
	output := &syncOutput{}
	toolStarted := make(chan struct{})
	releaseTool := make(chan struct{})

	provider := &steeringProvider{steps: []steeringStep{
		{text: "Let me check.", toolCall: &llm.ToolInputPart{ID: "c1", Name: "t", Input: []byte(`{}`)}},
		{failErr: errors.New("provider stream failed: 429 rate limited")},
	}}
	tool := llm.Tool{
		Definition: llm.ToolDefinition{Name: "t", Description: "test", Schema: []byte(`{"type":"object"}`)},
		Execute: func(_ context.Context, _ json.RawMessage) ([]llm.ContentPart, error) {
			close(toolStarted)
			<-releaseTool
			return []llm.ContentPart{&llm.TextPart{Text: "the tool result"}}, nil
		},
	}

	s := steeringSession(t, output, provider, []llm.Tool{tool}, []llm.ContentPart{
		&llm.TextPart{Text: "earlier", ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleUser}},
	})
	go s.runTaskNormal(context.Background(), []llm.ContentPart{&llm.TextPart{Text: "do it"}})

	// Hold the tool open so the words are queued before step 2's boundary —
	// otherwise this tests the never-spliced case below.
	<-toolStarted
	if !s.queueSteering([]llm.ContentPart{&llm.TextPart{Text: steeringText}}) {
		t.Fatal("queueSteering refused a prompt that fits")
	}
	close(releaseTool)

	driveRun(t, s)

	// The words did reach the step that then failed: that is what makes this a
	// test of the discard rather than of "never got there".
	if indexOfSteering(provider.request(1)) < 0 {
		t.Fatalf("steering was not spliced into the step that then failed: %s", summary(provider.request(1)))
	}
	// Dropped: not in history, not echoed, not re-sent.
	for _, p := range s.Contents {
		if tp, ok := p.(*llm.TextPart); ok && tp.Text == steeringText {
			t.Fatalf("steering survived a failed turn into Contents: %s", summary(s.Contents))
		}
	}
	if strings.Contains(output.String(), steeringText) {
		t.Fatalf("steering was echoed to the adapter despite the turn failing: %q", output.String())
	}
	if n := provider.callCount(); n != 2 {
		t.Fatalf("provider calls = %d, want 2 — a failed turn must not re-send its steering", n)
	}
	if parts := s.takeSteering(); len(parts) != 0 {
		t.Fatalf("the queue holds %d parts after a failed turn, want none", len(parts))
	}
	// And the user is told, because the words are gone from everywhere else.
	if !strings.Contains(output.String(), "steering dropped") {
		t.Errorf("the discard was silent: %q", output.String())
	}
}

// The case the queue drain cannot cover: the words arrive while the request that
// then fails is already in flight, so no boundary ever takes them. They must go
// with the turn all the same — otherwise they would run on as a turn of their
// own, which is exactly the "spent a turn nobody asked for" shape.
func TestSteeringQueuedDuringFailingRequestIsDropped(t *testing.T) {
	output := &syncOutput{}
	requestInFlight := make(chan struct{})

	provider := &steeringProvider{steps: []steeringStep{
		{text: "Step 1.", toolCall: &llm.ToolInputPart{ID: "c1", Name: "t", Input: []byte(`{}`)}},
		{text: "never reached", preWait: requestInFlight, failErr: errors.New("provider stream failed: 400")},
	}}
	tool := llm.Tool{
		Definition: llm.ToolDefinition{Name: "t", Description: "test", Schema: []byte(`{"type":"object"}`)},
		Execute: func(_ context.Context, _ json.RawMessage) ([]llm.ContentPart, error) {
			return []llm.ContentPart{&llm.TextPart{Text: "the tool result"}}, nil
		},
	}
	s := steeringSession(t, output, provider, []llm.Tool{tool}, []llm.ContentPart{
		&llm.TextPart{Text: "earlier", ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleUser}},
	})
	go s.runTaskNormal(context.Background(), []llm.ContentPart{&llm.TextPart{Text: "do it"}})

	// Step 2's request is in flight; its boundary has already passed, so the
	// words stay in the queue.
	waitFor(t, func() bool { return provider.callCount() == 2 }, "the second request to be in flight")
	if !s.queueSteering([]llm.ContentPart{&llm.TextPart{Text: steeringText}}) {
		t.Fatal("queueSteering refused a prompt that fits")
	}
	close(requestInFlight)

	driveRun(t, s)

	if indexOfSteering(provider.request(1)) >= 0 {
		t.Fatalf("the in-flight request cannot have carried words typed after it: %s", summary(provider.request(1)))
	}
	for _, p := range s.Contents {
		if tp, ok := p.(*llm.TextPart); ok && tp.Text == steeringText {
			t.Fatalf("steering of a failed turn survived into Contents: %s", summary(s.Contents))
		}
	}
	if n := provider.callCount(); n != 2 {
		t.Fatalf("provider calls = %d, want 2 — the words must not run on as their own turn", n)
	}
	if !strings.Contains(output.String(), "steering dropped") {
		t.Errorf("the discard was silent: %q", output.String())
	}
}

// The turn that never started. When the task-start auto-summarize fails,
// runTaskNormal returns before the prompt is even appended — no step, no
// processPrompt — so the words typed while it ran are dropped by the call site,
// not by processPrompt's defer. Same rule, same notice, and exactly one of them:
// the summarize call's own discard has nothing left to report by then.
func TestSteeringDroppedWhenTaskStartSummarizeFails(t *testing.T) {
	output := &syncOutput{}
	summarizeInFlight := make(chan struct{})

	provider := &steeringProvider{steps: []steeringStep{
		{preWait: summarizeInFlight, failErr: errors.New("provider stream failed: 400")},
	}}
	s := steeringSession(t, output, provider, nil, []llm.ContentPart{
		&llm.TextPart{Text: "history", ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleUser}},
	})
	s.AutoSummarize, s.ContextLimit, s.ContextTokens = 100, 10, 10

	go s.runTaskNormal(context.Background(), []llm.ContentPart{&llm.TextPart{Text: "do it"}})

	// The auto-summarize is in flight; this is when the user types.
	waitFor(t, func() bool { return provider.callCount() == 1 }, "the auto-summarize request to be in flight")
	if !s.queueSteering([]llm.ContentPart{&llm.TextPart{Text: steeringText}}) {
		t.Fatal("queueSteering refused a prompt that fits")
	}
	close(summarizeInFlight)

	driveRun(t, s)

	if parts := s.takeSteering(); len(parts) != 0 {
		t.Fatalf("a failed task-start summarize left %d parts queued, want none", len(parts))
	}
	if n := strings.Count(output.String(), "steering dropped"); n != 1 {
		t.Fatalf("expected exactly one discard notice, got %d: %q", n, output.String())
	}
	if n := provider.callCount(); n != 1 {
		t.Fatalf("provider calls = %d, want 1 — the words must not run on as their own turn", n)
	}
	for _, p := range s.Contents {
		if tp, ok := p.(*llm.TextPart); ok && tp.Text == steeringText {
			t.Fatalf("steering survived into Contents: %s", summary(s.Contents))
		}
	}
}

// The take side needs no rule of its own either: a summarize call is simply
// never given the step-boundary hook (processPrompt installs it for a turn
// only), so this test's queue stays where it is. Nothing in the steering code
// asks whether the call is a summarize.
//
// The take side does still need to *not happen*, and the reason is not
// bookkeeping: a summarize's history is a copy that either replaces the
// conversation or is thrown away, so words spliced into it would be consumed by
// a request whose result nobody keeps — vanished, with nothing to report,
// because from the queue's point of view they were delivered.
func TestSteeringDroppedWhenSummarizeFails(t *testing.T) {
	output := &syncOutput{}
	provider := &steeringProvider{steps: []steeringStep{
		{failErr: errors.New("provider stream failed: 400")},
	}}
	s := steeringSession(t, output, provider, nil, nil)

	if !s.queueSteering([]llm.ContentPart{&llm.TextPart{Text: steeringText}}) {
		t.Fatal("queueSteering refused a prompt that fits")
	}

	history := []llm.ContentPart{&llm.TextPart{Text: "history", ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleUser}}}
	if _, _, err := s.processPrompt(context.Background(), history, summarizeCall); err == nil {
		t.Fatal("processPrompt(summarizeCall) should have failed")
	}

	if parts := s.takeSteering(); len(parts) != 0 {
		t.Fatalf("a failed summarize left %d parts queued, want none", len(parts))
	}
	if !strings.Contains(output.String(), "steering dropped") {
		t.Errorf("the discard was silent: %q", output.String())
	}
	if indexOfSteering(provider.request(0)) >= 0 {
		t.Fatalf("the summarize request carried the user's words: %s", summary(provider.request(0)))
	}
}

// A summarize call is not a turn, and nothing in the steering code has to know
// that: processPrompt installs the step-boundary hook for a turn only, and a
// summarize never gets it. So the queue is not reachable from one — and the
// reason that matters is not bookkeeping: a summarize's history is a copy that
// either replaces the conversation or is thrown away, so words spliced into it
// would be consumed by a request whose result nobody keeps. They would vanish
// with nothing to report, because from the queue's point of view they were
// delivered.
//
// A summarize runs the *same* multi-step tool loop as a turn, so this has to
// hold for every step of it. The test scripts a two-step summarize whose first
// step calls a tool, which is where the words would land if the hook were
// installed: after the summarize prompt and between the model's own tool calls.
//
// And when a summarize fails, what was queued during it is dropped — the same
// rule as a failed turn, with the same notice (see
// TestSteeringDroppedWhenSummarizeFails).
func TestSteeringNotTakenBySummarizeCall(t *testing.T) {
	toolRan := make(chan struct{})
	provider := &steeringProvider{steps: []steeringStep{
		{text: "Let me look at something first.", toolCall: &llm.ToolInputPart{ID: "s1", Name: "t", Input: []byte(`{}`)}},
		{text: "a summary"},
	}}
	tool := llm.Tool{
		Definition: llm.ToolDefinition{Name: "t", Description: "test", Schema: []byte(`{"type":"object"}`)},
		Execute: func(_ context.Context, _ json.RawMessage) ([]llm.ContentPart, error) {
			close(toolRan)
			return []llm.ContentPart{&llm.TextPart{Text: "what the tool saw"}}, nil
		},
	}
	s := steeringSession(t, &syncOutput{}, provider, []llm.Tool{tool}, nil)

	if !s.queueSteering([]llm.ContentPart{&llm.TextPart{Text: steeringText}}) {
		t.Fatal("queueSteering refused a prompt that fits")
	}

	history := []llm.ContentPart{&llm.TextPart{Text: "history", ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleUser}}}
	if _, _, err := s.processPrompt(context.Background(), history, summarizeCall); err != nil {
		t.Fatalf("processPrompt(summarizeCall): %v", err)
	}

	select {
	case <-toolRan:
	default:
		t.Fatal("the summarize never ran its tool step — this test is not exercising the multi-step case")
	}
	if n := provider.callCount(); n != 2 {
		t.Fatalf("summarize ran %d steps, want 2 (tool call, then the summary)", n)
	}
	for step := 0; step < 2; step++ {
		if indexOfSteering(provider.request(step)) >= 0 {
			t.Fatalf("summarize step %d carried the user's steering words: %s", step+1, summary(provider.request(step)))
		}
	}
	if parts := s.takeSteering(); len(parts) != 1 {
		t.Fatalf("the queue should still hold the words after a summarize, holds %d parts", len(parts))
	}
}

// The whole wire, end to end: a second prompt typed through the real input pump
// while the first one's tool is still running reaches the turn's next step
// boundary. Unit tests call runTaskNormal and queueSteering directly; this one
// goes through run(), handleInputMsg, submitPrompt and the auto-save, so a
// wiring mistake between those layers cannot pass unnoticed.
func TestSteeringEndToEndThroughInputPipe(t *testing.T) {
	output := &syncOutput{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r, w := io.Pipe()
	defer w.Close()

	savePath := filepath.Join(t.TempDir(), "session.alaya")
	toolStarted := make(chan struct{})
	releaseTool := make(chan struct{})

	provider := &steeringProvider{steps: []steeringStep{
		{text: "Let me check.", toolCall: &llm.ToolInputPart{ID: "c1", Name: "t", Input: []byte(`{}`)}},
		{text: "Done."},
	}}
	tool := llm.Tool{
		Definition: llm.ToolDefinition{Name: "t", Description: "test", Schema: []byte(`{"type":"object"}`)},
		Execute: func(_ context.Context, _ json.RawMessage) ([]llm.ContentPart, error) {
			close(toolStarted)
			<-releaseTool
			return []llm.ContentPart{&llm.TextPart{Text: "the tool result"}}, nil
		},
	}
	agent := llm.NewAgent(llm.AgentConfig{Provider: provider, Tools: []llm.Tool{tool}, MaxSteps: 10})

	s := &Session{
		sessionConfig: sessionConfig{
			modelService: &modelService{agent: agent, provider: provider},
			SessionConfig: SessionConfig{
				Input:       r,
				Output:      output,
				NoDelta:     true,
				MaxSteps:    10,
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

	writePrompt(t, w, "first prompt")
	// The window: step 1's tool is running, so step 2's boundary is still ahead.
	<-toolStarted
	writePrompt(t, w, "use Y instead")

	// The session acknowledged it as steering rather than refusing it as BUSY.
	waitFor(t, func() bool { return strings.Contains(output.String(), "steering queued") }, "the steering acknowledgement")

	close(releaseTool)

	// It reached the conversation: the auto-save at task end carries it, after
	// the tool result it was spliced behind.
	waitFor(t, func() bool { return fileContains(savePath, "use Y instead") }, "auto-save with the steered prompt")
	saved := readFile(t, savePath)
	if strings.Index(saved, "use Y instead") < strings.Index(saved, "the tool result") {
		t.Fatalf("the steered prompt must follow the tool result it was spliced behind:\n%s", saved)
	}
	// And it is echoed exactly once, so nothing downstream re-published it.
	if n := strings.Count(output.String(), "use Y instead"); n != 1 {
		t.Fatalf("the steered prompt was echoed %d times, want 1", n)
	}

	_ = w.Close()
	select {
	case <-s.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("session did not finish after the task completed")
	}
}

// writePrompt sends one complete user message: text then the UE flush.
func writePrompt(t *testing.T, w io.Writer, text string) {
	t.Helper()
	if err := tlv.WriteTLV(w, tlv.TagUserT, text); err != nil {
		t.Fatalf("write UT: %v", err)
	}
	if err := tlv.WriteTLV(w, tlv.TagUserEnd, ""); err != nil {
		t.Fatalf("write UE: %v", err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// Canceling stops the turn *and* what the turn was going to be told: leaving
// the queue in place would start a fresh task on its own the moment the
// canceled one unwinds.
func TestCancelDropsQueuedSteering(t *testing.T) {
	output := &syncOutput{}
	provider := &steeringProvider{steps: []steeringStep{{text: "never runs"}}}
	s := steeringSession(t, output, provider, nil, nil)
	s.activeTask = &taskHandle{cancel: func() {}}

	if !s.queueSteering([]llm.ContentPart{&llm.TextPart{Text: steeringText}}) {
		t.Fatal("queueSteering refused a prompt that fits")
	}
	if _, err := s.cancelTask(); err != nil {
		t.Fatalf("cancelTask() = %v, want success", err)
	}
	if parts := s.takeSteering(); len(parts) != 0 {
		t.Fatalf("cancel left %d parts queued, want none", len(parts))
	}
	// The drop is announced — the words are the user's, and they are gone.
	if !strings.Contains(output.String(), "steering dropped") {
		t.Errorf("canceling dropped queued steering without saying so: %q", output.String())
	}

	// And the task ending afterwards delivers nothing: no task starts by itself.
	s.activeTask = nil
	s.handleTaskDone(nil)
	if n := provider.callCount(); n != 0 {
		t.Fatalf("provider was called %d times after a cancel, want 0 — canceled steering must not start a turn", n)
	}
}

// The other half of that rule, and the failure mode a "drop on next task end"
// flag would have: a prompt typed *after* the cancel is a fresh intent and must
// survive. Cancel, then type what you actually wanted, is an ordinary sequence.
func TestCancelKeepsSteeringArrivingAfterIt(t *testing.T) {
	output := &syncOutput{}
	provider := &steeringProvider{steps: []steeringStep{{text: "the answer"}}}
	s := steeringSession(t, output, provider, nil, nil)
	s.activeTask = &taskHandle{cancel: func() {}}

	// Cancel with nothing queued.
	if _, err := s.cancelTask(); err != nil {
		t.Fatalf("cancelTask() = %v, want success", err)
	}
	if strings.Contains(output.String(), "steering dropped") {
		t.Error("nothing was queued, so nothing should have been reported dropped")
	}

	// The canceled task is still unwinding; the user types again.
	s.submitPrompt([]llm.ContentPart{&llm.TextPart{Text: "and use Y"}})

	// The task ends; what is queued now is post-cancel intent.
	s.activeTask = nil
	s.handleTaskDone(nil)
	driveRun(t, s)

	if n := provider.callCount(); n != 1 {
		t.Fatalf("provider calls = %d, want 1 (the prompt typed after the cancel)", n)
	}
	req := provider.request(0)
	last, ok := req[len(req)-1].(*llm.TextPart)
	if !ok || last.Text != "and use Y" {
		t.Fatalf("the post-cancel prompt must be delivered as the trailing user turn: %s", summary(req))
	}
}

// The half that is no longer in the queue: a batch the task goroutine had
// already spliced into a step goes back nowhere when that step is cut short by a
// cancel. Dropping only the queue would let this one through and restart the
// very task the user stopped.
//
// The window here is the one the queue drain cannot cover: the batch has been
// taken and the request is in flight, so there is nothing left for cancelTask to
// drain — the decision belongs to the task goroutine, which is what makes
// processPrompt's deferred restore the other half of the rule.
func TestCancelDropsBatchInFlight(t *testing.T) {
	output := &syncOutput{}
	toolStarted := make(chan struct{})
	releaseTool := make(chan struct{})
	requestInFlight := make(chan struct{})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	provider := &steeringProvider{steps: []steeringStep{
		{text: "Let me check.", toolCall: &llm.ToolInputPart{ID: "c1", Name: "t", Input: []byte(`{}`)}},
		{text: "never reached", preWait: requestInFlight},
	}}
	tool := llm.Tool{
		Definition: llm.ToolDefinition{Name: "t", Description: "test", Schema: []byte(`{"type":"object"}`)},
		Execute: func(_ context.Context, _ json.RawMessage) ([]llm.ContentPart, error) {
			close(toolStarted)
			<-releaseTool
			return []llm.ContentPart{&llm.TextPart{Text: "the tool result"}}, nil
		},
	}

	s := steeringSession(t, output, provider, []llm.Tool{tool}, []llm.ContentPart{
		&llm.TextPart{Text: "earlier", ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleUser}},
	})
	go s.runTaskNormal(ctx, []llm.ContentPart{&llm.TextPart{Text: "do it"}})

	// Step 1's tool is running, so the words arrive while step 1 is still in
	// flight — too late for step 1's boundary, in time for step 2's.
	<-toolStarted
	if !s.queueSteering([]llm.ContentPart{&llm.TextPart{Text: steeringText}}) {
		t.Fatal("queueSteering refused a prompt that fits")
	}
	close(releaseTool)

	// Step 2's request is now in flight with the words spliced in: they have
	// been taken from the queue, so there is nothing left for cancelTask to
	// drain. The user cancels here.
	waitFor(t, func() bool { return provider.callCount() == 2 }, "the second request to be in flight")
	cancel()
	close(requestInFlight)

	driveRun(t, s)

	// It was spliced into the step the cancel cut short — that is what makes
	// this the in-flight half rather than the queued one...
	if indexOfSteering(provider.request(1)) < 0 {
		t.Fatalf("the batch was never spliced into the canceled step: %s", summary(provider.request(1)))
	}
	// ...and it went nowhere: not in history, not echoed, not re-sent.
	for _, p := range s.Contents {
		if tp, ok := p.(*llm.TextPart); ok && tp.Text == steeringText {
			t.Fatalf("canceled steering survived into Contents: %s", summary(s.Contents))
		}
	}
	if strings.Contains(output.String(), steeringText) {
		t.Fatalf("canceled steering was echoed to the adapter: %q", output.String())
	}
	if n := provider.callCount(); n != 2 {
		t.Fatalf("provider calls = %d, want 2 — canceled steering must not be re-sent", n)
	}
}

// Compaction and steering in the same boundary, in that order. The order is not
// a preference: a compaction replaces the history wholesale, so a steering part
// appended before it would vanish from Contents while the adapter kept the ID it
// was shown. This pins the order, and with it the two things that follow —
// steering never lands in the summarize request, and a steering part that is
// spliced after a replacement keeps an ID that resolves.
func TestSteeringSplicedAfterCompactionInSameBoundary(t *testing.T) {
	output := &syncOutput{}
	toolStarted := make(chan struct{})
	releaseTool := make(chan struct{})

	provider := &steeringProvider{steps: []steeringStep{
		{text: "Step 1.", toolCall: &llm.ToolInputPart{ID: "c1", Name: "t", Input: []byte(`{}`)}},
		{text: "Summary of the conversation."}, // the compaction's summarize call
		{text: "Final."},                       // the real step 2
	}}
	tool := llm.Tool{
		Definition: llm.ToolDefinition{Name: "t", Description: "test", Schema: []byte(`{"type":"object"}`)},
		Execute: func(_ context.Context, _ json.RawMessage) ([]llm.ContentPart, error) {
			close(toolStarted)
			<-releaseTool
			return []llm.ContentPart{&llm.TextPart{Text: "the tool result"}}, nil
		},
	}
	s := steeringSession(t, output, provider, []llm.Tool{tool}, []llm.ContentPart{
		&llm.TextPart{Text: "earlier", ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleUser}},
	})
	// ContextTokens starts at 0, so the task-start check is skipped; step 1's
	// usage (15 tokens) pushes the task-local count over the threshold, and the
	// compaction fires at step 2's boundary — the same boundary the steering is
	// waiting for.
	s.AutoSummarize, s.ContextLimit = 100, 10

	go s.runTaskNormal(context.Background(), []llm.ContentPart{&llm.TextPart{Text: "do it"}})
	<-toolStarted
	if !s.queueSteering([]llm.ContentPart{&llm.TextPart{Text: steeringText}}) {
		t.Fatal("queueSteering refused a prompt that fits")
	}
	close(releaseTool)

	driveRun(t, s)

	if n := provider.callCount(); n != 3 {
		t.Fatalf("provider calls = %d, want 3 (step 1, the summarize, step 2)", n)
	}
	// The summarize request must not carry the words...
	if indexOfSteering(provider.request(1)) >= 0 {
		t.Fatalf("the summarize request carried the user's words: %s", summary(provider.request(1)))
	}
	// ...and the step after it must, at the tail, behind the "Continue" the
	// compaction ends on.
	step2 := provider.request(2)
	if idx := indexOfSteering(step2); idx != len(step2)-1 {
		t.Fatalf("steering must be the last thing the model reads after a compaction: %s", summary(step2))
	}
	last, ok := provider.request(2)[len(step2)-2].(*llm.TextPart)
	if !ok || last.Text != "Continue" {
		t.Fatalf("steering must be spliced behind the compaction's Continue turn: %s", summary(step2))
	}

	// Exactly once in Contents, and the ID the adapter was shown resolves there
	// — the whole point of splicing after the replacement rather than before it.
	count := 0
	for _, p := range s.Contents {
		if tp, ok := p.(*llm.TextPart); ok && tp.Text == steeringText {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("steering appears %d times in Contents, want exactly 1: %s", count, summary(s.Contents))
	}
	// The steering part's own ID must be one the adapter can resolve: that is
	// exactly what splicing it *after* the replacement buys, and the reason the
	// order is fixed rather than chosen.
	//
	// (Parts from before the compaction are a different story: a compaction
	// discards the history wholesale, and the adapter is never told, so every ID
	// it was shown earlier — the user's prompt included — stops resolving. That
	// is pre-existing, reachable with no steering involved, and out of this test's
	// scope.)
	var steerID uint64
	for _, p := range s.Contents {
		if tp, ok := p.(*llm.TextPart); ok && tp.Text == steeringText {
			steerID = p.GetHistoryID()
		}
	}
	advertised := false
	for _, id := range echoedUserIDs(t, output.String()) {
		if id == fmt.Sprintf("%d", steerID) {
			advertised = true
		}
	}
	if !advertised {
		t.Fatalf("the steering part (ID %d) is not the ID the adapter was shown — a compaction swallowed it: %s",
			steerID, summary(s.Contents))
	}
}

// A committed batch is committed once: later steps must not re-number or
// re-publish it.
func TestSteeringCommitIsOnceOnly(t *testing.T) {
	output := &syncOutput{}

	provider := &steeringProvider{steps: []steeringStep{
		{text: "Step 1.", toolCall: &llm.ToolInputPart{ID: "c1", Name: "t", Input: []byte(`{}`)}},
		{text: "Step 2.", toolCall: &llm.ToolInputPart{ID: "c2", Name: "t", Input: []byte(`{}`)}},
		{text: "Step 3."},
	}}
	tool := llm.Tool{
		Definition: llm.ToolDefinition{Name: "t", Description: "test", Schema: []byte(`{"type":"object"}`)},
		Execute: func(_ context.Context, _ json.RawMessage) ([]llm.ContentPart, error) {
			return []llm.ContentPart{&llm.TextPart{Text: "result"}}, nil
		},
	}

	s := steeringSession(t, output, provider, []llm.Tool{tool}, []llm.ContentPart{
		&llm.TextPart{Text: "earlier", ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleUser}},
	})

	// Queued before the run starts, so it is spliced at step 1's boundary and
	// must survive the two later steps unaltered.
	if !s.queueSteering([]llm.ContentPart{&llm.TextPart{Text: steeringText}}) {
		t.Fatal("queueSteering refused a prompt that fits")
	}

	go s.runTaskNormal(context.Background(), []llm.ContentPart{&llm.TextPart{Text: "do it"}})
	driveRun(t, s)

	count := 0
	var id uint64
	for _, p := range s.Contents {
		if tp, ok := p.(*llm.TextPart); ok && tp.Text == steeringText {
			count++
			id = p.GetHistoryID()
		}
	}
	if count != 1 {
		t.Fatalf("steering appears %d times in Contents, want exactly 1: %s", count, summary(s.Contents))
	}
	ids := echoedUserIDs(t, output.String())
	seen := 0
	for _, got := range ids {
		if got == fmt.Sprintf("%d", id) {
			seen++
		}
	}
	if seen != 1 {
		t.Fatalf("the steering was echoed %d times (IDs %v), want exactly 1", seen, ids)
	}
	// The splice must be in every later request too — it is part of the
	// conversation now, not a one-shot injection.
	for _, n := range []int{1, 2} {
		if indexOfSteering(provider.request(n)) < 0 {
			t.Fatalf("step %d request lost the steering: %v", n+1, provider.request(n))
		}
	}
}
