package agent

// The status bar's spinner is driven solely by the SM task frame's in_progress
// flag (terminal/spinner.go, terminal/tui_status.go). These tests drive the
// real run() loop for each task entry point and assert the session already
// reports in_progress while the task's first request is genuinely in flight
// (parked inside the provider) — the window beginTask's announcement exists to
// cover; see that function for why step boundaries alone do not:
//
//   - :summarize                      → runTaskSummarize
//   - normal prompt + auto-summarize  → runTaskNormal
//   - :continue  + auto-summarize     → runTaskContinue

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"iter"
	"sync"
	"testing"
	"time"

	"github.com/alayacore/alayacore/internal/commands"
	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/protocol"
	"github.com/alayacore/alayacore/internal/tlv"
)

// parkFirstProvider parks inside the first request until release is closed,
// then answers; later requests answer immediately. The first request is the
// slow one a user actually waits on: the :summarize round trip, or the
// task-start auto-summarize in front of a prompt.
type parkFirstProvider struct {
	started chan struct{}
	release chan struct{}

	mu    sync.Mutex
	calls int
}

func newParkFirstProvider() *parkFirstProvider {
	return &parkFirstProvider{started: make(chan struct{}), release: make(chan struct{})}
}

func (p *parkFirstProvider) StreamMessages(ctx context.Context, _ []llm.ContentPart, _ []llm.ToolDefinition, _, _ string) (iter.Seq2[llm.StreamEvent, error], error) {
	p.mu.Lock()
	p.calls++
	first := p.calls == 1
	p.mu.Unlock()
	if first {
		close(p.started)
	}
	return func(yield func(llm.StreamEvent, error) bool) {
		if first {
			select {
			case <-p.release:
			case <-ctx.Done():
				yield(nil, ctx.Err())
				return
			}
		}
		if !yield(llm.TextDeltaEvent{Delta: "Summary.", Key: "block:0"}, nil) {
			return
		}
		if !yield(llm.TextCompleteEvent{Key: "block:0"}, nil) {
			return
		}
		yield(llm.StepCompleteEvent{Usage: llm.Usage{InputTokens: 10, OutputTokens: 3}}, nil)
	}, nil
}

func (p *parkFirstProvider) SetReasoningLevel(_ int)                       {}
func (p *parkFirstProvider) SetReasoningConfigs(_ map[int]json.RawMessage) {}
func (p *parkFirstProvider) SetVideoConfig(_ int, _ int)                   {}

// TestSummarizeReportsInProgressWhileInFlight covers the runTaskSummarize
// entry point: a :summarize emits no opening step event, so only the
// task-acceptance broadcast can flip the status bar to in_progress.
func TestSummarizeReportsInProgressWhileInFlight(t *testing.T) {
	provider := newParkFirstProvider()
	output, w := startStatusTestSession(t, provider, SessionConfig{NoDelta: true, MaxSteps: 10}, nil, 1000, 0)

	writeCI(t, w, "s1", commands.CommandNameSummarize)

	waitParked(t, provider)
	assertInProgress(t, output, true)
	close(provider.release)
	waitTaskCompleted(t, output)
}

// TestTaskStartAutoSummarizeReportsInProgressWhileInFlight covers the
// runTaskNormal (prompt) and runTaskContinue (:continue) entry points in the
// one state where they too have no opening step event: the task-start
// auto-summarize runs before the turn's first step.
func TestTaskStartAutoSummarizeReportsInProgressWhileInFlight(t *testing.T) {
	cases := []struct {
		name          string
		send          func(t *testing.T, w io.Writer)
		wantCommandID string
	}{
		{
			name: "normal_prompt",
			send: func(t *testing.T, w io.Writer) {
				t.Helper()
				if err := tlv.WriteTLV(w, tlv.TagUserT, tlv.WrapID("1", "do it")); err != nil {
					t.Fatalf("write UT: %v", err)
				}
				if err := tlv.WriteTLV(w, tlv.TagUserEnd, ""); err != nil {
					t.Fatalf("write UE: %v", err)
				}
			},
			// A prompt was not started by a command, so no frame may carry a
			// (possibly stale) command_id.
			wantCommandID: "",
		},
		{
			name: "continue",
			send: func(t *testing.T, w io.Writer) {
				t.Helper()
				writeCI(t, w, "c1", commands.CommandNameContinue)
			},
			wantCommandID: "c1",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Over the 65% auto-summarize threshold, so the task's first
			// request is the auto-summarize, not a step of the real turn.
			contents := []llm.ContentPart{
				&llm.TextPart{Text: "old user", ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleUser}},
				&llm.TextPart{Text: "old assistant", ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleAssistant}},
			}
			provider := newParkFirstProvider()
			output, w := startStatusTestSession(t, provider,
				SessionConfig{NoDelta: true, MaxSteps: 10, AutoSummarize: 65},
				contents, 1000, 700)

			tc.send(t, w)

			waitParked(t, provider)
			assertInProgress(t, output, true)

			// command_id is the task's own: the announce frame reads it from
			// activeTask (empty for a prompt), never the previous task's.
			frames := taskFrames(t, output.String())
			gotCommandID := ""
			if n := len(frames); n > 0 {
				gotCommandID = frames[n-1].commandID
			}
			if gotCommandID != tc.wantCommandID {
				t.Fatalf("task frames carry command_id %q, want %q; output:\n%s",
					gotCommandID, tc.wantCommandID, output.String())
			}

			close(provider.release)
			waitTaskCompleted(t, output)
		})
	}
}

// startStatusTestSession builds a session with its run() loop live and waits
// for the ready transition, returning the captured output and the write end of
// the input pipe. The session's run() goroutine is torn down by t.Cleanup
// closing that pipe.
func startStatusTestSession(t *testing.T, provider llm.Provider, cfg SessionConfig, contents []llm.ContentPart, contextLimit, contextTokens int64) (*syncOutput, io.Writer) {
	t.Helper()

	output := &syncOutput{}
	ctx, cancel := context.WithCancel(context.Background())
	r, w := io.Pipe()
	t.Cleanup(func() {
		_ = w.Close()
		cancel()
	})

	cfg.Input = r
	cfg.Output = output

	agent := llm.NewAgent(llm.AgentConfig{Provider: provider, MaxSteps: cfg.MaxSteps})
	s := &Session{
		sessionConfig: sessionConfig{
			modelService:  &modelService{agent: agent, provider: provider, contextLimit: contextLimit},
			SessionConfig: cfg,
		},
		runState: runState{
			Contents:     contents,
			taskEventCh:  make(chan taskEvent, 64),
			taskResultCh: make(chan []llm.ContentPart, 1),
			cancelReqCh:  make(chan chan bool, 1),
		},
		sharedState: sharedState{
			sessionCtx:    ctx,
			sessionCancel: cancel,
			confirmChs:    make(map[string]chan bool),
			ContextLimit:  contextLimit,
			ContextTokens: contextTokens,
		},
		runDoneCh: make(chan struct{}),
	}
	s.mcpService = newMCPService(nil, output)
	s.Start()

	// Commands and prompts are refused until the session is ready.
	waitForState(t, s, SessionReady)
	return output, w
}

func writeCI(t *testing.T, w io.Writer, id, name string) {
	t.Helper()
	data, err := json.Marshal(protocol.CmdMsg{ID: id, Name: name})
	if err != nil {
		t.Fatalf("marshal CI: %v", err)
	}
	if err := tlv.WriteTLV(w, tlv.TagCommandIn, string(data)); err != nil {
		t.Fatalf("write CI: %v", err)
	}
}

// waitParked blocks until the provider is serving its first (parked) request.
func waitParked(t *testing.T, provider *parkFirstProvider) {
	t.Helper()
	select {
	case <-provider.started:
	case <-time.After(3 * time.Second):
		t.Fatal("the task's first request never reached the provider")
	}
}

// assertInProgress checks the last task-status frame written so far.
func assertInProgress(t *testing.T, output *syncOutput, want bool) {
	t.Helper()
	frames := taskFrames(t, output.String())
	if len(frames) == 0 || frames[len(frames)-1].inProgress != want {
		t.Fatalf("expected in_progress:%v while in flight; task frames = %v; output:\n%s",
			want, frames, output.String())
	}
}

// waitTaskCompleted waits until the last task-status frame reports idle. The
// session's goroutine is torn down by the session builder's t.Cleanup (closing
// the input pipe ends run()).
func waitTaskCompleted(t *testing.T, output *syncOutput) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		frames := taskFrames(t, output.String())
		if len(frames) > 0 && !frames[len(frames)-1].inProgress {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("task never reported completion; task frames = %v; output:\n%s", frames, output.String())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// taskFrame is the subset of an SM "task" frame these tests read.
type taskFrame struct {
	inProgress bool
	commandID  string
}

// taskFrames parses every SM "task" frame out of the captured output
// (concatenated TLV frames) in write order. A partial trailing frame —
// possible while the session is mid-write — ends the scan.
func taskFrames(t *testing.T, output string) []taskFrame {
	t.Helper()
	var frames []taskFrame
	r := bytes.NewReader([]byte(output))
	for {
		tag, value, err := tlv.ReadTLV(r)
		if err != nil {
			break
		}
		if tag != tlv.TagSystemMsg {
			continue
		}
		env, err := protocol.ParseSystemMsg(value)
		if err != nil || env.Type != string(protocol.MsgTypeTask) {
			continue
		}
		var m struct {
			InProgress bool   `json:"in_progress"`
			CommandID  string `json:"command_id"`
		}
		if json.Unmarshal(env.Data, &m) != nil {
			continue
		}
		frames = append(frames, taskFrame{inProgress: m.InProgress, commandID: m.CommandID})
	}
	return frames
}
