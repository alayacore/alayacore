package terminal

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/protocol"
	"github.com/alayacore/alayacore/internal/theme"
	"github.com/alayacore/alayacore/internal/tlv"
)

// encodeTestTLV encodes a TLV frame for test input. Test payloads are
// tiny and never exceed maxMessageSize, so the encode error is ignored.
func encodeTestTLV(tag, value string) []byte {
	msg, _ := tlv.EncodeTLV(tag, value)
	return msg
}

// ============================================================================
// Text Delta Coalescing
// ============================================================================

func TestTextDeltaCoalescing(t *testing.T) {
	// Three "At" deltas for the same window should result in a single
	// window with accumulated content "hello world".
	out := NewTerminalOutput(NewStyles(theme.DefaultTheme()))
	out.SetWindowWidth(80)

	// Simulate three text deltas for window "5".
	writeAt(out, "5", "he")
	writeAt(out, "5", "llo wor")
	writeAt(out, "5", "ld")

	// Flush pending deltas.
	out.mu.Lock()
	out.flushPendingDeltas()
	out.mu.Unlock()

	// Verify: one window with merged content.
	wb := out.WindowBuffer()
	if wb.WindowCount() != 1 {
		t.Fatalf("expected 1 window, got %d", wb.WindowCount())
	}
	got := wb.WindowAt(0).RawContent()
	want := "hello world"
	if got != want {
		t.Errorf("expected content %q, got %q", want, got)
	}
}

func TestTextDeltaCoalescingMultipleWindows(t *testing.T) {
	// Deltas for different windows should be accumulated separately.
	out := NewTerminalOutput(NewStyles(theme.DefaultTheme()))
	out.SetWindowWidth(80)

	writeAt(out, "5", "Hello")
	writeAr(out, "3", "Reasoning...")
	writeAt(out, "5", " World")

	out.mu.Lock()
	out.flushPendingDeltas()
	out.mu.Unlock()

	wb := out.WindowBuffer()
	if wb.WindowCount() != 2 {
		t.Fatalf("expected 2 windows, got %d", wb.WindowCount())
	}

	// Find windows by ID (map iteration order is non-deterministic).
	foundText := false
	foundReasoning := false
	for i := 0; i < wb.WindowCount(); i++ {
		w := wb.WindowAt(i)
		switch w.ID {
		case "5":
			foundText = true
			if w.Tag() != tlv.TagAssistantT {
				t.Errorf("text window expected tag AT, got %s", w.Tag())
			}
			if w.RawContent() != "Hello World" {
				t.Errorf("text window expected %q, got %q", "Hello World", w.RawContent())
			}
		case "3":
			foundReasoning = true
			if w.Tag() != tlv.TagAssistantR {
				t.Errorf("reasoning window expected tag AR, got %s", w.Tag())
			}
			if w.RawContent() != "Reasoning..." {
				t.Errorf("reasoning window expected %q, got %q", "Reasoning...", w.RawContent())
			}
		default:
			t.Errorf("unexpected window ID: %q", w.ID)
		}
	}
	if !foundText {
		t.Error("text window (ID '5') not found")
	}
	if !foundReasoning {
		t.Error("reasoning window (ID '3') not found")
	}
}

func TestTextDeltaCreatesWindowOnFirstDelta(t *testing.T) {
	// The first "At" delta for a new window ID should create the window
	// (via AppendOrUpdate) when flushed.
	out := NewTerminalOutput(NewStyles(theme.DefaultTheme()))
	out.SetWindowWidth(80)

	writeAt(out, "42", "first chunk")

	out.mu.Lock()
	out.flushPendingDeltas()
	out.mu.Unlock()

	wb := out.WindowBuffer()
	if wb.WindowCount() != 1 {
		t.Fatalf("expected 1 window, got %d", wb.WindowCount())
	}

	if !wb.HasWindow("42") {
		t.Error("window with ID '42' should exist")
	}
	got := wb.WindowAt(0).RawContent()
	if got != "first chunk" {
		t.Errorf("expected %q, got %q", "first chunk", got)
	}
}

// ============================================================================
// Tool Delta Coalescing
// ============================================================================

func TestToolDeltaCoalescing(t *testing.T) {
	// Three "Af" deltas for the same tool call should be merged into
	// a single HandleToolInputDelta call with concatenated JSON.
	out := NewTerminalOutput(NewStyles(theme.DefaultTheme()))
	out.SetWindowWidth(80)

	writeAf(out, "1", "call_123", `{"location":`)
	writeAf(out, "1", "call_123", `"San Francisco`)
	writeAf(out, "1", "call_123", `"}`)

	out.mu.Lock()
	out.flushPendingDeltas()
	out.mu.Unlock()

	wb := out.WindowBuffer()
	if wb.WindowCount() != 1 {
		t.Fatalf("expected 1 window, got %d", wb.WindowCount())
	}

	// Verify the tool delta was accumulated correctly by checking
	// the tool info contains the merged input.
	ti := wb.WindowAt(0).ToolInfo()
	if ti == nil {
		t.Fatal("expected ToolInfo, got nil")
	}
	// The pending preview should show the merged delta string.
	got := wb.WindowAt(0).RawDelta()
	want := `{"location":"San Francisco"}`
	if got != want {
		t.Errorf("expected delta %q, got %q", want, got)
	}
}

func TestToolDeltaWithNameResolution(t *testing.T) {
	// When an AF (start) frame arrives before Af frames, the tool name
	// should be resolved from the existing window.
	out := NewTerminalOutput(NewStyles(theme.DefaultTheme()))
	out.SetWindowWidth(80)

	// Write an AF start frame to create the window with tool name.
	writeAFStart(out, "1", "call_456", "read_file")

	// Write Af deltas.
	writeAf(out, "1", "call_456", `{"path":"/tmp/`)
	writeAf(out, "1", "call_456", `foo.txt"}`)

	out.mu.Lock()
	out.flushPendingDeltas()
	out.mu.Unlock()

	wb := out.WindowBuffer()
	if wb.WindowCount() != 1 {
		t.Fatalf("expected 1 window, got %d", wb.WindowCount())
	}

	ti := wb.WindowAt(0).ToolInfo()
	if ti == nil {
		t.Fatal("expected ToolInfo, got nil")
	}
	if ti.Name != "read_file" {
		t.Errorf("expected tool name 'read_file', got %q", ti.Name)
	}
	got := wb.WindowAt(0).RawDelta()
	if got != `{"path":"/tmp/foo.txt"}` {
		t.Errorf("expected delta %q, got %q", `{"path":"/tmp/foo.txt"}`, got)
	}
}

// ============================================================================
// Flush Trigger Tests
// ============================================================================

func TestDeltaFlushOnCompleteFrame(t *testing.T) {
	// When an authoritative AT frame arrives after At deltas,
	// the deltas should be flushed before the AT is processed.
	out := NewTerminalOutput(NewStyles(theme.DefaultTheme()))
	out.SetWindowWidth(80)

	writeAt(out, "5", "stream")
	writeAt(out, "5", "ing ")
	writeAT(out, "5", "streaming complete") // authoritative frame

	// The writeColored handler for AT first calls flushPendingDeltas(),
	// so the window should have the authoritative content (since AT
	// replaces it only if window didn't exist — but window was created
	// by the flush, so AT is skipped). The accumulated deltas should
	// be "streaming " (the merged deltas).
	wb := out.WindowBuffer()
	if wb.WindowCount() != 1 {
		t.Fatalf("expected 1 window, got %d", wb.WindowCount())
	}
	got := wb.WindowAt(0).RawContent()
	// The AT frame is skipped because the window already exists
	// (created by flush). So content is the merged deltas.
	if got != "streaming " {
		t.Errorf("expected content %q, got %q", "streaming ", got)
	}
}

func TestDeltaCoalescingPreservesOrder(t *testing.T) {
	// Deltas should be concatenated in the order they arrive.
	out := NewTerminalOutput(NewStyles(theme.DefaultTheme()))
	out.SetWindowWidth(80)

	writeAt(out, "7", "a")
	writeAt(out, "7", "b")
	writeAt(out, "7", "c")
	writeAt(out, "7", "d")
	writeAt(out, "7", "e")

	out.mu.Lock()
	out.flushPendingDeltas()
	out.mu.Unlock()

	wb := out.WindowBuffer()
	// Should have 1 window since all deltas are for same ID.
	if wb.WindowCount() != 1 {
		t.Fatalf("expected 1 window, got %d", wb.WindowCount())
	}
	got := wb.WindowAt(0).RawContent()
	if got != "abcde" {
		t.Errorf("expected 'abcde', got %q", got)
	}
}

func TestToolDeltaCoalescingPreservesOrder(t *testing.T) {
	// Tool JSON fragments should be concatenated in order.
	out := NewTerminalOutput(NewStyles(theme.DefaultTheme()))
	out.SetWindowWidth(80)

	writeAf(out, "1", "call_999", `{"a":1,`)
	writeAf(out, "1", "call_999", `"b":2}`)

	out.mu.Lock()
	out.flushPendingDeltas()
	out.mu.Unlock()

	wb := out.WindowBuffer()
	got := wb.WindowAt(0).RawDelta()
	if got != `{"a":1,"b":2}` {
		t.Errorf("expected %q, got %q", `{"a":1,"b":2}`, got)
	}
}

// ============================================================================
// Mixed Delta Interleaving
// ============================================================================

func TestTextAndToolDeltaInterleaving(t *testing.T) {
	// Text and tool deltas for different windows/tools should not interfere.
	// Map iteration order is non-deterministic, so we check by window ID
	// rather than buffer position.
	out := NewTerminalOutput(NewStyles(theme.DefaultTheme()))
	out.SetWindowWidth(80)

	writeAt(out, "5", "Hello")
	writeAf(out, "5", "call_111", `{"x":`)
	writeAt(out, "5", " World")
	writeAf(out, "5", "call_111", `1}`)
	writeAr(out, "3", "thinking")
	writeAr(out, "3", " hard")

	out.mu.Lock()
	out.flushPendingDeltas()
	out.mu.Unlock()

	wb := out.WindowBuffer()

	// Should have 3 windows: text (AT), tool (AF), reasoning (AR).
	if wb.WindowCount() != 3 {
		t.Fatalf("expected 3 windows, got %d", wb.WindowCount())
	}

	// Find windows by ID (map iteration order is non-deterministic).
	foundText := false
	foundReasoning := false
	foundTool := false
	for i := 0; i < wb.WindowCount(); i++ {
		w := wb.WindowAt(i)
		switch w.Tag() {
		case tlv.TagAssistantT:
			foundText = true
			if w.RawContent() != "Hello World" {
				t.Errorf("text content: expected %q, got %q", "Hello World", w.RawContent())
			}
		case tlv.TagAssistantR:
			foundReasoning = true
			if w.RawContent() != "thinking hard" {
				t.Errorf("reasoning content: expected %q, got %q", "thinking hard", w.RawContent())
			}
		case tlv.TagAssistantF:
			foundTool = true
			if w.RawDelta() != `{"x":1}` {
				t.Errorf("tool delta: expected %q, got %q", `{"x":1}`, w.RawDelta())
			}
			ti := w.ToolInfo()
			if ti == nil {
				t.Error("expected tool info for tool window")
			}
		}
	}
	if !foundText {
		t.Error("no text window found")
	}
	if !foundReasoning {
		t.Error("no reasoning window found")
	}
	if !foundTool {
		t.Error("no tool window found")
	}
}

// ============================================================================
// Empty / Edge Cases
// ============================================================================

func TestFlushWithNoPendingDeltas(t *testing.T) {
	// flushPendingDeltas with empty maps should be a no-op.
	out := NewTerminalOutput(NewStyles(theme.DefaultTheme()))
	out.mu.Lock()
	out.flushPendingDeltas() // should not panic
	out.mu.Unlock()

	wb := out.WindowBuffer()
	if wb.WindowCount() != 0 {
		t.Errorf("expected 0 windows, got %d", wb.WindowCount())
	}
}

func TestEmptyDeltaContent(t *testing.T) {
	// Empty delta content should not create visible windows.
	out := NewTerminalOutput(NewStyles(theme.DefaultTheme()))
	out.SetWindowWidth(80)

	writeAt(out, "5", "")
	writeAt(out, "5", "")

	out.mu.Lock()
	out.flushPendingDeltas()
	out.mu.Unlock()

	wb := out.WindowBuffer()
	if wb.WindowCount() != 1 {
		t.Fatalf("expected 1 window (invisible), got %d", wb.WindowCount())
	}
	// Window exists but is invisible (no visible content).
	if wb.WindowAt(0).Visible {
		t.Error("expected window to be invisible for empty content")
	}
}

// ============================================================================
// Flush + DrainDirty integration
// ============================================================================

func TestFlushThenDrainDirty(t *testing.T) {
	out := NewTerminalOutput(NewStyles(theme.DefaultTheme()))
	out.SetWindowWidth(80)

	writeAt(out, "5", "pending content")

	// FlushPendingDeltas moves accumulated content to WindowBuffer.
	out.FlushPendingDeltas()

	wb := out.WindowBuffer()
	if wb.WindowCount() != 1 {
		t.Fatalf("expected 1 window after FlushPendingDeltas, got %d", wb.WindowCount())
	}
	if wb.WindowAt(0).RawContent() != "pending content" {
		t.Errorf("expected content %q, got %q", "pending content", wb.WindowAt(0).RawContent())
	}

	// DrainDirty returns true because FlushPendingDeltas set the dirty flag.
	if !out.DrainDirty() {
		t.Error("expected DrainDirty to return true after flush")
	}

	// Second call should return false (no new data).
	if out.DrainDirty() {
		t.Error("expected DrainDirty to return false after first call")
	}
}

func TestDrainDirtyIsPureQuery(t *testing.T) {
	// DrainDirty should NOT flush pending deltas — only the TUI tick
	// loop calls FlushPendingDeltas before DrainDirty.
	out := NewTerminalOutput(NewStyles(theme.DefaultTheme()))
	out.SetWindowWidth(80)

	// Write a non-delta frame to set dirty.
	var buf strings.Builder
	buf.Write(encodeTestTLV(tlv.TagUserF, tlv.WrapID("1", `{"id":"call_1","content":[],"is_error":false}`)))
	_, err := out.Write([]byte(buf.String()))
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	// DrainDirty should return true (dirty was set by non-delta frame)...
	// but NOT flush any pending deltas (there are none, so this is just
	// a sanity check that DrainDirty is a pure CAS).
	if !out.DrainDirty() {
		t.Error("expected DrainDirty to return true")
	}

	// Second call should return false (no new data).
	if out.DrainDirty() {
		t.Error("expected DrainDirty to return false after first call")
	}

	// Writing only delta frames should NOT set dirty.
	wb := out.WindowBuffer()
	prevCount := wb.WindowCount()

	writeAt(out, "5", "pending content")

	// DrainDirty should return false (deltas don't set dirty).
	if out.DrainDirty() {
		t.Error("expected DrainDirty to return false (deltas don't set dirty)")
	}

	// WindowBuffer should be unchanged.
	if wb.WindowCount() != prevCount {
		t.Errorf("expected %d windows (unchanged), got %d", prevCount, wb.WindowCount())
	}
}

// ============================================================================
// Integration: End-to-end TLV write through the pipeline
// ============================================================================

func TestDeltaCoalescingThroughWrite(t *testing.T) {
	// Write TLV-encoded deltas through the Write() -> processBuffer() path.
	out := NewTerminalOutput(NewStyles(theme.DefaultTheme()))
	out.SetWindowWidth(80)

	// Single write with multiple TLV frames.
	var buf strings.Builder
	buf.Write(encodeTestTLV(tlv.TagAssistantTDelta, tlv.WrapID("1", "A")))
	buf.Write(encodeTestTLV(tlv.TagAssistantTDelta, tlv.WrapID("1", "B")))
	buf.Write(encodeTestTLV(tlv.TagAssistantTDelta, tlv.WrapID("1", "C")))
	buf.Write(encodeTestTLV(tlv.TagAssistantRDelta, tlv.WrapID("2", "think")))
	buf.Write(encodeTestTLV(tlv.TagAssistantRDelta, tlv.WrapID("2", "ing")))

	_, err := out.Write([]byte(buf.String()))
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	// After writeColored processes all frames, only non-delta frames
	// trigger a flush. Since all frames are deltas, nothing is flushed
	// to WindowBuffer yet. FlushPendingDeltas will flush them.
	out.FlushPendingDeltas()
	out.DrainDirty()

	wb := out.WindowBuffer()
	if wb.WindowCount() != 2 {
		t.Fatalf("expected 2 windows, got %d", wb.WindowCount())
	}

	// Find windows by ID (map iteration order is non-deterministic).
	for i := 0; i < wb.WindowCount(); i++ {
		w := wb.WindowAt(i)
		switch w.ID {
		case "1":
			if w.RawContent() != "ABC" {
				t.Errorf("text window: expected %q, got %q", "ABC", w.RawContent())
			}
		case "2":
			if w.RawContent() != "thinking" {
				t.Errorf("reasoning window: expected %q, got %q", "thinking", w.RawContent())
			}
		default:
			t.Errorf("unexpected window ID: %q", w.ID)
		}
	}
}

func TestDeltaFlushViaNonDeltaTag(t *testing.T) {
	// A non-delta tag (e.g. TagUserF) in the same write should trigger flush.
	out := NewTerminalOutput(NewStyles(theme.DefaultTheme()))
	out.SetWindowWidth(80)

	var buf strings.Builder
	buf.Write(encodeTestTLV(tlv.TagAssistantTDelta, tlv.WrapID("1", "some text")))
	buf.Write(encodeTestTLV(tlv.TagUserF, tlv.WrapID("1", `{"id":"call_1","content":[],"is_error":false}`)))

	_, err := out.Write([]byte(buf.String()))
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	// The TagUserF frame should have triggered flushPendingDeltas.
	wb := out.WindowBuffer()
	if wb.WindowCount() != 2 {
		t.Fatalf("expected 2 windows, got %d", wb.WindowCount())
	}
	if wb.WindowAt(0).RawContent() != "some text" {
		t.Errorf("expected %q, got %q", "some text", wb.WindowAt(0).RawContent())
	}
}

// ============================================================================
// Helpers
// ============================================================================

// writeAt writes a TagAssistantTDelta frame through the output pipeline.
func writeAt(out *outputWriter, id, content string) {
	data := encodeTestTLV(tlv.TagAssistantTDelta, tlv.WrapID(id, content))
	_, _ = out.Write(data)
}

// writeAr writes a TagAssistantRDelta frame through the output pipeline.
func writeAr(out *outputWriter, id, content string) {
	data := encodeTestTLV(tlv.TagAssistantRDelta, tlv.WrapID(id, content))
	_, _ = out.Write(data)
}

// writeAT writes a TagAssistantT (authoritative) frame through the output pipeline.
func writeAT(out *outputWriter, id, content string) {
	data := encodeTestTLV(tlv.TagAssistantT, tlv.WrapID(id, content))
	_, _ = out.Write(data)
}

// writeAf writes a TagAssistantFDelta (tool delta) frame through the output pipeline.
func writeAf(out *outputWriter, historyID, toolID, delta string) {
	fd, _ := json.Marshal(protocol.ToolInputDeltaData{ID: toolID, Delta: delta})
	data := encodeTestTLV(tlv.TagAssistantFDelta, tlv.WrapID(historyID, string(fd)))
	_, _ = out.Write(data)
}

// writeUf writes a TagUserFDelta (tool result preview) frame through the output pipeline.
func writeUf(out *outputWriter, historyID, toolID, text string) {
	od, _ := json.Marshal(protocol.ToolOutputDeltaData{ID: toolID, Text: text})
	data := encodeTestTLV(tlv.TagUserFDelta, tlv.WrapID(historyID, string(od)))
	_, _ = out.Write(data)
}

// writeAFStart writes a TagAssistantF (tool start) frame with tool name.
func writeAFStart(out *outputWriter, historyID, toolID, toolName string) {
	fd, _ := json.Marshal(protocol.ToolInputData{ID: toolID, Name: toolName})
	data := encodeTestTLV(tlv.TagAssistantF, tlv.WrapID(historyID, string(fd)))
	_, _ = out.Write(data)
}

// ============================================================================
// Tool Output Delta (Uf) Preview
// ============================================================================

func TestToolOutputDeltaPreview(t *testing.T) {
	out := NewTerminalOutput(NewStyles(theme.DefaultTheme()))
	out.SetWindowWidth(80)

	// Tool call starts (AF), then preview snapshots arrive (Uf).
	writeAFStart(out, "1", "call_1", "execute_command")
	writeUf(out, "1", "call_1", " 42%")
	writeUf(out, "1", "call_1", " 99%")

	wb := out.WindowBuffer()
	if wb.WindowCount() != 1 {
		t.Fatalf("expected 1 window, got %d", wb.WindowCount())
	}
	tr, ok := wb.WindowAt(0).renderer.(*toolRenderer)
	if !ok {
		t.Fatal("expected toolRenderer")
	}
	// Latest snapshot wins (last-write-wins, no accumulation).
	if tr.output != " 99%" {
		t.Errorf("preview = %q, want %q", tr.output, " 99%")
	}
	if tr.status != ToolStatusPending {
		t.Errorf("status = %v, want Pending", tr.status)
	}

	// Authoritative UF overwrites the preview.
	buf := encodeTestTLV(tlv.TagUserF, tlv.WrapID("1",
		`{"id":"call_1","output":[{"type":"text","text":"done\n"}],"is_error":false}`))
	_, _ = out.Write(buf)

	tr2, _ := wb.WindowAt(0).renderer.(*toolRenderer)
	if tr2.output != "done\n" {
		t.Errorf("authoritative output = %q, want %q", tr2.output, "done\n")
	}
	if tr2.status != ToolStatusSuccess {
		t.Errorf("status = %v, want Success", tr2.status)
	}
}

func TestToolOutputDeltaNoPriorAF(t *testing.T) {
	// Uf arriving without a prior AF frame creates a placeholder window.
	out := NewTerminalOutput(NewStyles(theme.DefaultTheme()))
	out.SetWindowWidth(80)

	writeUf(out, "1", "call_9", "running...")

	wb := out.WindowBuffer()
	if wb.WindowCount() != 1 {
		t.Fatalf("expected 1 window, got %d", wb.WindowCount())
	}
	tr, ok := wb.WindowAt(0).renderer.(*toolRenderer)
	if !ok {
		t.Fatal("expected toolRenderer")
	}
	if tr.output != "running..." {
		t.Errorf("preview = %q, want %q", tr.output, "running...")
	}
	if tr.status != ToolStatusPending {
		t.Errorf("status = %v, want Pending", tr.status)
	}
}
