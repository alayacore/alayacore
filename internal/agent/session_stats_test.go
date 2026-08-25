package agent

// Tests for per-step speed metric handling (stepStatsEvent) and the
// taskMsg broadcast fields (step_tps / ttft_ms). No task-level
// averaging is reported — only the latest step's simple end-to-end
// throughput.

import (
	"bytes"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alayacore/alayacore/internal/protocol"
	"github.com/alayacore/alayacore/internal/tlv"
)

// TestStepStatsBroadcast verifies that stepStatsEvent handling stores
// the latest step's speed/TTFT and that the taskMsg broadcast carries
// them.
func TestStepStatsBroadcast(t *testing.T) {
	var buf bytes.Buffer
	session := &Session{
		sessionConfig: sessionConfig{
			SessionConfig: SessionConfig{Output: &buf, MaxSteps: 10},
		},
		sharedState: sharedState{
			histCounter:  1,
			outputBroken: atomic.Bool{},
		},
		runState: runState{
			taskEventCh: make(chan taskEvent, 20),
		},
	}

	// Step 1 completes: 125 tok/s, TTFT 200ms.
	session.handleTaskEvent(stepStatsEvent{
		TokensPerSec: 125, TimeToFirstToken: 200 * time.Millisecond,
	})
	// Step 2 completes: 100 tok/s, TTFT 100ms — replaces step 1's values.
	session.handleTaskEvent(stepStatsEvent{
		TokensPerSec: 100, TimeToFirstToken: 100 * time.Millisecond,
	})

	session.sendTaskMsg()

	m := taskMsgFields{}
	readTaskMsg(t, &buf, &m)

	if m.StepTPS != 100 {
		t.Errorf("step_tps = %v, want 100 (latest step wins)", m.StepTPS)
	}
	if m.TTFTMS != 100 {
		t.Errorf("ttft_ms = %d, want 100", m.TTFTMS)
	}
	if m.InProgress {
		t.Error("in_progress = true, want false (no active task)")
	}
}

// TestStepStatsNoOutputClearsSpeed verifies that a step with no output
// tokens (TokensPerSec 0 — e.g. a tool-only step) clears the displayed
// speed: the status bar must not keep showing a stale value.
func TestStepStatsNoOutputClearsSpeed(t *testing.T) {
	var buf bytes.Buffer
	session := &Session{
		sessionConfig: sessionConfig{
			SessionConfig: SessionConfig{Output: &buf, MaxSteps: 10},
		},
		sharedState: sharedState{
			histCounter:  1,
			outputBroken: atomic.Bool{},
		},
		runState: runState{
			taskEventCh: make(chan taskEvent, 20),
		},
	}

	// A step with speed first.
	session.handleTaskEvent(stepStatsEvent{TokensPerSec: 125, TimeToFirstToken: 200 * time.Millisecond})
	// Then a step with no output tokens — e.g. a tool-only step.
	session.handleTaskEvent(stepStatsEvent{TokensPerSec: 0, TimeToFirstToken: 50 * time.Millisecond})

	session.sendTaskMsg()

	m := taskMsgFields{}
	readTaskMsg(t, &buf, &m)
	if m.StepTPS != 0 {
		t.Errorf("step_tps = %v, want 0 (no-output step clears speed)", m.StepTPS)
	}
	if m.TTFTMS != 50 {
		t.Errorf("ttft_ms = %d, want 50 (TTFT still reported)", m.TTFTMS)
	}
}

// TestTaskStartBroadcastClearsSpeed verifies that a new task's
// stepStartEvent(Step==1) clears the previous task's speed values so
// the start broadcast carries no stale data.
func TestTaskStartBroadcastClearsSpeed(t *testing.T) {
	var buf bytes.Buffer
	session := &Session{
		sessionConfig: sessionConfig{
			SessionConfig: SessionConfig{Output: &buf, MaxSteps: 10},
		},
		sharedState: sharedState{
			histCounter:  1,
			outputBroken: atomic.Bool{},
		},
		runState: runState{
			taskEventCh: make(chan taskEvent, 20),
		},
	}

	// Previous task had speed data.
	session.handleTaskEvent(stepStatsEvent{TokensPerSec: 125, TimeToFirstToken: 200 * time.Millisecond})

	// New task starts.
	buf.Reset()
	session.handleTaskEvent(stepStartEvent{Step: 1})
	session.sendTaskMsg()

	tag, value, err := tlv.ReadTLV(&buf)
	if err != nil {
		t.Fatalf("ReadTLV failed: %v", err)
	}
	if tag != tlv.TagSystemMsg {
		t.Fatalf("tag = %q, want system msg", tag)
	}
	env, err := protocol.ParseSystemMsg(value)
	if err != nil {
		t.Fatalf("ParseSystemMsg failed: %v", err)
	}
	for _, field := range []string{`"step_tps"`, `"ttft_ms"`} {
		if bytes.Contains(env.Data, []byte(field)) {
			t.Errorf("task start broadcast contains stale %s: %s", field, env.Data)
		}
	}
}

// TestTaskMsgSpeedFieldsAbsentWithoutSteps verifies that the speed fields
// stay absent (omitted) when no step has completed — the additive
// backward-compat guarantee for older adapters.
func TestTaskMsgSpeedFieldsAbsentWithoutSteps(t *testing.T) {
	var buf bytes.Buffer
	session := &Session{
		sessionConfig: sessionConfig{
			SessionConfig: SessionConfig{Output: &buf, MaxSteps: 10},
		},
		sharedState: sharedState{
			histCounter:  1,
			outputBroken: atomic.Bool{},
		},
		runState: runState{
			taskEventCh: make(chan taskEvent, 20),
		},
	}

	session.sendTaskMsg()

	tag, value, err := tlv.ReadTLV(&buf)
	if err != nil {
		t.Fatalf("ReadTLV failed: %v", err)
	}
	if tag != tlv.TagSystemMsg {
		t.Fatalf("tag = %q, want system msg", tag)
	}
	env, err := protocol.ParseSystemMsg(value)
	if err != nil {
		t.Fatalf("ParseSystemMsg failed: %v", err)
	}
	if string(env.Data) == "" {
		t.Fatal("task msg has no data")
	}
	for _, field := range []string{`"step_tps"`, `"ttft_ms"`} {
		if bytes.Contains(env.Data, []byte(field)) {
			t.Errorf("task msg contains %s before any step completed: %s", field, env.Data)
		}
	}
}

// readTaskMsg reads the next TLV frame, asserts it is an SM "task"
// message, and unmarshals its data into m.
func readTaskMsg(t *testing.T, buf *bytes.Buffer, m any) {
	t.Helper()
	tag, value, err := tlv.ReadTLV(buf)
	if err != nil {
		t.Fatalf("ReadTLV failed: %v", err)
	}
	if tag != tlv.TagSystemMsg {
		t.Fatalf("tag = %q, want system msg", tag)
	}
	env, err := protocol.ParseSystemMsg(value)
	if err != nil {
		t.Fatalf("ParseSystemMsg failed: %v", err)
	}
	if env.Type != string(protocol.MsgTypeTask) {
		t.Fatalf("type = %q, want %q", env.Type, protocol.MsgTypeTask)
	}
	if err := json.Unmarshal(env.Data, m); err != nil {
		t.Fatalf("unmarshal task msg failed: %v", err)
	}
}

// taskMsgFields is the subset of taskMsg fields asserted by these tests.
type taskMsgFields struct {
	InProgress bool    `json:"in_progress"`
	StepTPS    float64 `json:"step_tps"`
	TTFTMS     int64   `json:"ttft_ms"`
}
