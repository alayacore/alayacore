package terminal

// Tests for the tick-driven tool spinner refresh:
//
//   - toolSpinnerFrameAt: the extracted wall-clock frame function (frame
//     advances every 150ms, wraps after 10 frames) — the unit seam that
//     makes the refresh tests deterministic without real waits.
//   - WindowBuffer.InvalidateRunningToolSpinners: only executing tool
//     windows (ToolStatusPending) are invalidated; finished tools and
//     text windows are untouched; idle buffers stay clean.
//   - Terminal.handleDisplayRefresh: end-to-end — a pending tool window
//     with zero deltas advances its header spinner across ticks, while a
//     display with no executing tool keeps the idle-tick skip behavior
//     (no re-render, no dirtying).

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/alayacore/alayacore/internal/protocol"
	"github.com/alayacore/alayacore/internal/tlv"
)

// TestToolSpinnerFrameAt locks the wall-clock frame math: one frame per
// 150ms slot, exact boundaries, and cycle wrap after 10 frames.
func TestToolSpinnerFrameAt(t *testing.T) {
	base := time.UnixMilli(0)
	for i := 0; i < len(toolSpinnerFrames); i++ {
		got := toolSpinnerFrameAt(base.Add(time.Duration(i) * 150 * time.Millisecond))
		if got != toolSpinnerFrames[i] {
			t.Errorf("frame at +%dms = %q, want %q", i*150, got, toolSpinnerFrames[i])
		}
	}
	// Cycle wraps: 1500ms == 10 slots → frame 0 again.
	if got := toolSpinnerFrameAt(base.Add(1500 * time.Millisecond)); got != toolSpinnerFrames[0] {
		t.Errorf("frame at +1500ms = %q, want %q (cycle wrap)", got, toolSpinnerFrames[0])
	}
	// Inside one slot the frame is stable; across a boundary it changes.
	if a, b := toolSpinnerFrameAt(base), toolSpinnerFrameAt(base.Add(149*time.Millisecond)); a != b {
		t.Errorf("frames within one 150ms slot must match: %q != %q", a, b)
	}
	if a, b := toolSpinnerFrameAt(base), toolSpinnerFrameAt(base.Add(150*time.Millisecond)); a == b {
		t.Errorf("frames across a 150ms boundary must differ: %q == %q", a, b)
	}
}

// TestInvalidateRunningToolSpinnersInvalidatesPendingOnly: only executing
// tool windows (ToolStatusPending) get their border invalidated; finished
// tools and plain text windows keep their cached borders.
func TestInvalidateRunningToolSpinnersInvalidatesPendingOnly(t *testing.T) {
	wb := NewWindowBuffer(60, DefaultStyles())
	wb.HandleToolInputEvent(protocol.ToolInputData{
		ID:    "running",
		Name:  "execute_command",
		Input: json.RawMessage("sleep 60"),
	}, 0)
	wb.HandleToolInputEvent(protocol.ToolInputData{
		ID:    "done",
		Name:  "read_file",
		Input: json.RawMessage("x"),
	}, 0)
	wb.HandleToolOutput("done", "content", false, 0)
	wb.AppendOrUpdate(tlv.TagAssistantT, "w1", "hello")

	// Render once so every border cache is populated/valid.
	wb.GetAll(-1, false)

	idxRunning, ok := wb.LookupID("running")
	if !ok {
		t.Fatal("running window missing")
	}
	idxDone, ok := wb.LookupID("done")
	if !ok {
		t.Fatal("done window missing")
	}
	idxText, ok := wb.LookupID("w1")
	if !ok {
		t.Fatal("text window missing")
	}

	if !wb.InvalidateRunningToolSpinners() {
		t.Fatal("expected true with a pending tool window")
	}
	if !wb.IsDirty() {
		t.Fatal("invalidation must mark the buffer dirty")
	}
	if wb.WindowAt(idxRunning).border.valid {
		t.Error("pending tool window border must be invalidated")
	}
	if !wb.WindowAt(idxDone).border.valid {
		t.Error("finished tool window border must stay valid")
	}
	if !wb.WindowAt(idxText).border.valid {
		t.Error("text window border must stay valid")
	}
}

// TestInvalidateRunningToolSpinnersMultiplePending verifies multiple
// executing windows escalate the dirty index to a full rebuild (the
// markDirty sentinel logic) instead of corrupting the single-window index.
func TestInvalidateRunningToolSpinnersMultiplePending(t *testing.T) {
	wb := NewWindowBuffer(60, DefaultStyles())
	wb.HandleToolInputEvent(protocol.ToolInputData{
		ID:    "a",
		Name:  "execute_command",
		Input: json.RawMessage("sleep 1"),
	}, 0)
	wb.HandleToolInputEvent(protocol.ToolInputData{
		ID:    "b",
		Name:  "search_content",
		Input: json.RawMessage("x"),
	}, 0)
	wb.GetAll(-1, false)

	if !wb.InvalidateRunningToolSpinners() {
		t.Fatal("expected true with pending tool windows")
	}
	if wb.dirtyIndex != dirtyFullRebuild {
		t.Errorf("dirtyIndex = %d, want dirtyFullRebuild (%d)", wb.dirtyIndex, dirtyFullRebuild)
	}
}

// TestInvalidateRunningToolSpinnersIdleNoop: no executing tool → the call
// is a no-op: returns false and leaves the buffer clean, preserving the
// idle-tick 100% skip behavior.
func TestInvalidateRunningToolSpinnersIdleNoop(t *testing.T) {
	// Empty buffer.
	empty := NewWindowBuffer(60, DefaultStyles())
	empty.GetAll(-1, false)
	if empty.InvalidateRunningToolSpinners() {
		t.Error("empty buffer must not report an invalidation")
	}
	if empty.IsDirty() {
		t.Error("empty buffer must stay clean")
	}

	// Finished tools + text windows only.
	wb := NewWindowBuffer(60, DefaultStyles())
	wb.HandleToolInputEvent(protocol.ToolInputData{
		ID:    "done",
		Name:  "read_file",
		Input: json.RawMessage("x"),
	}, 0)
	wb.HandleToolOutput("done", "content", false, 0)
	wb.HandleToolInputEvent(protocol.ToolInputData{
		ID:    "failed",
		Name:  "execute_command",
		Input: json.RawMessage("boom"),
	}, 0)
	wb.HandleToolOutput("failed", "err", true, 0)
	wb.AppendOrUpdate(tlv.TagAssistantT, "w1", "hello")
	wb.GetAll(-1, false)

	if wb.InvalidateRunningToolSpinners() {
		t.Error("no executing tool → must not report an invalidation")
	}
	if wb.IsDirty() {
		t.Error("no executing tool → buffer must stay clean")
	}
}

// TestDisplayRefreshAdvancesSpinnerWithoutDeltas is the end-to-end fix:
// a pending tool window, zero deltas, two ticks 200ms apart (past one
// 150ms spinner slot) — the header glyph must advance.
func TestDisplayRefreshAdvancesSpinnerWithoutDeltas(t *testing.T) {
	out := NewTerminalOutput(DefaultStyles())
	out.WindowBuffer().HandleToolInputEvent(protocol.ToolInputData{
		ID:    "t1",
		Name:  "execute_command",
		Input: json.RawMessage("sleep 60"),
	}, 0)
	m := newTerminalForUpdateStatusTest(out)
	m.display = m.display.updateContent()

	g1 := toolSpinnerGlyph(t, stripANSI(m.display.lastContent))
	if g1 == "" {
		t.Fatalf("expected a spinner glyph in the rendered content: %q", stripANSI(m.display.lastContent))
	}

	// No deltas arrive; only the wall clock advances past one spinner slot.
	time.Sleep(200 * time.Millisecond)
	m, _ = m.handleDisplayRefresh()

	g2 := toolSpinnerGlyph(t, stripANSI(m.display.lastContent))
	if g2 == "" {
		t.Fatal("spinner disappeared after refresh")
	}
	if g2 == g1 {
		t.Errorf("spinner did not advance without deltas: %q == %q", g1, g2)
	}
}

// TestDisplayRefreshIdleSkipsRender: with no executing tool, the idle
// tick must not re-render — the display content stays byte-identical
// (the perf contract of the 100% idle skip).
func TestDisplayRefreshIdleSkipsRender(t *testing.T) {
	out := NewTerminalOutput(DefaultStyles())
	out.WindowBuffer().AppendOrUpdate(tlv.TagAssistantT, "w1", "hello")
	m := newTerminalForUpdateStatusTest(out)
	m.display = m.display.updateContent()
	before := m.display.lastContent

	m, _ = m.handleDisplayRefresh()

	if m.display.lastContent != before {
		t.Errorf("idle refresh must not re-render: content changed\nbefore: %q\nafter:  %q", before, m.display.lastContent)
	}
}

// TestDisplayRefreshIdleSkipsRenderWithFinishedTool: a completed tool
// (✓/✗) must not keep the spinner refresh alive — once a tool finishes,
// idle ticks resume the skip behavior.
func TestDisplayRefreshIdleSkipsRenderWithFinishedTool(t *testing.T) {
	out := NewTerminalOutput(DefaultStyles())
	wb := out.WindowBuffer()
	wb.HandleToolInputEvent(protocol.ToolInputData{
		ID:    "t1",
		Name:  "execute_command",
		Input: json.RawMessage("echo hi"),
	}, 0)
	wb.HandleToolOutput("t1", "hi", false, 0)
	m := newTerminalForUpdateStatusTest(out)
	m.display = m.display.updateContent()
	before := m.display.lastContent

	m, _ = m.handleDisplayRefresh()

	if m.display.lastContent != before {
		t.Errorf("finished tool must not keep the spinner refresh alive: content changed")
	}
}

// toolSpinnerGlyph extracts the spinner frame right after the "TOOL CALL "
// label from stripped (plain) rendered content. Returns "" if absent.
func toolSpinnerGlyph(t *testing.T, content string) string {
	t.Helper()
	const label = "TOOL CALL "
	i := strings.Index(content, label)
	if i < 0 {
		return ""
	}
	rest := content[i+len(label):]
	for _, f := range toolSpinnerFrames {
		if strings.HasPrefix(rest, f) {
			return f
		}
	}
	return ""
}

// BenchmarkInvalidateRunningToolSpinnersIdle measures the per-tick cost of
// the spinner refresh scan when no tool is executing (100 history windows)
// — the idle-tick overhead the perf contract cares about: it must stay
// allocation-free and sub-µs so the 100% idle skip behavior is preserved.
func BenchmarkInvalidateRunningToolSpinnersIdle(b *testing.B) {
	wb := NewWindowBuffer(120, DefaultStyles())
	for i := 0; i < 100; i++ {
		wb.AppendOrUpdate(tlv.TagAssistantT, fmt.Sprintf("w%d", i), "hello world")
	}
	wb.GetAll(-1, false)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if wb.InvalidateRunningToolSpinners() {
			b.Fatal("idle buffer must not report invalidation")
		}
	}
}

// BenchmarkInvalidateRunningToolSpinnersPending measures the per-tick cost
// while a tool executes (one pending window among 100 history windows) —
// the cost is only paid for the duration of tool execution.
func BenchmarkInvalidateRunningToolSpinnersPending(b *testing.B) {
	wb := NewWindowBuffer(120, DefaultStyles())
	for i := 0; i < 100; i++ {
		wb.AppendOrUpdate(tlv.TagAssistantT, fmt.Sprintf("w%d", i), "hello world")
	}
	wb.HandleToolInputEvent(protocol.ToolInputData{
		ID:    "tool",
		Name:  "execute_command",
		Input: json.RawMessage("sleep 60"),
	}, 0)
	wb.GetAll(-1, false)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if !wb.InvalidateRunningToolSpinners() {
			b.Fatal("pending tool must be invalidated")
		}
	}
}
