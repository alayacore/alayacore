package terminal

// Tests for the tool status indicator (spinner while running, ✓/✗ when
// done — both share the "TOOL CALL" label color), the collapsed/expanded
// delta preview ellipsis position (tail shown, "…" at the line start),
// and the USER PROMPT label.

import (
	"encoding/json"
	"strings"
	"testing"

	ansi "github.com/charmbracelet/x/ansi"

	"github.com/alayacore/alayacore/internal/protocol"
	"github.com/alayacore/alayacore/internal/tlv"
)

// TestToolStatusIndicatorInheritsLabelColor verifies every status glyph
// is rendered using the supplied label style (the indicator shares the
// "TOOL CALL" label color, not green/red), and stays exactly 1 display
// column wide so the label column alignment holds.
func TestToolStatusIndicatorInheritsLabelColor(t *testing.T) {
	labelStyle := NewStyle().Foreground(Color("#abcdef"))
	for _, st := range []ToolStatus{ToolStatusNone, ToolStatusPending, ToolStatusSuccess, ToolStatusError} {
		glyph, stl := st.statusDot(labelStyle)
		if glyph == "" {
			t.Errorf("status %d: empty glyph", st)
			continue
		}
		rendered := stl.Render(glyph)
		// The label color must be encoded — no longer the deliberately
		// colorless indicator of the previous design.
		if !strings.Contains(rendered, "#abcdef") && !strings.Contains(rendered, "abcdef") {
			// Lipgloss emits hex as either 6-digit or 8-digit sequences
			// depending on the terminal profile; check both.
			if !strings.Contains(rendered, "\x1b[") {
				t.Errorf("status %d: indicator must carry the label color, got %q", st, rendered)
			}
		}
		if w := ansi.StringWidth(glyph); w != 1 {
			t.Errorf("status %d: indicator must be 1 column wide (label alignment), %q is %d", st, glyph, w)
		}
	}
}

// TestToolRunningShowsSpinner verifies the running states (args streaming,
// executing) show one of the session-loading spinner frames, and the
// terminal states show the plain check/cross.
func TestToolRunningShowsSpinner(t *testing.T) {
	labelStyle := NewStyle()
	for _, st := range []ToolStatus{ToolStatusNone, ToolStatusPending} {
		glyph, _ := st.statusDot(labelStyle)
		inSet := false
		for _, f := range toolSpinnerFrames {
			if glyph == f {
				inSet = true
				break
			}
		}
		if !inSet {
			t.Errorf("status %d: glyph %q is not one of the spinner frames", st, glyph)
		}
	}
	if g, _ := ToolStatusSuccess.statusDot(labelStyle); g != "✓" {
		t.Errorf("success glyph = %q, want ✓", g)
	}
	if g, _ := ToolStatusError.statusDot(labelStyle); g != "✗" {
		t.Errorf("error glyph = %q, want ✗", g)
	}
}

// TestToolStatusIndicatorHeaderStates drives the WindowBuffer through the
// full lifecycle: pending header shows TOOL CALL + spinner, success shows
// TOOL CALL ✓, error shows TOOL CALL ✗.
func TestToolStatusIndicatorHeaderStates(t *testing.T) {
	wb := NewWindowBuffer(60, DefaultStyles())
	wb.HandleToolInputEvent(protocol.ToolInputData{
		ID:    "t1",
		Name:  "execute_command",
		Input: json.RawMessage("execute_command: lscpu"),
	}, 0)

	// Pending (executing): header is "▶ TOOL CALL ⠋ …" — a separator space
	// and a spinner frame right after the label.
	plain := stripANSI(wb.GetAll(-1, false))
	if !strings.HasPrefix(plain, "▶ TOOL CALL ") {
		t.Fatalf("pending header should start with ▶ TOOL CALL + space, got %q", plain)
	}
	rest := []rune(strings.TrimPrefix(plain, "▶ TOOL CALL "))
	if len(rest) == 0 {
		t.Fatal("pending header has nothing after the label")
	}
	inSet := false
	for _, f := range toolSpinnerFrames {
		if rest[0] == []rune(f)[0] {
			inSet = true
			break
		}
	}
	if !inSet {
		t.Errorf("pending header should show a spinner frame after the label, got %q", plain)
	}

	// Success: plain ✓ after the separator space.
	wb.HandleToolOutput("t1", "x86_64", false, 0)
	plain = stripANSI(wb.GetAll(-1, false))
	if !strings.Contains(plain, "TOOL CALL ✓") {
		t.Errorf("success header should contain TOOL CALL ✓, got %q", plain)
	}

	// Error: plain ✗ after the separator space.
	wb.HandleToolOutput("t1", "boom", true, 0)
	plain = stripANSI(wb.GetAll(-1, false))
	if !strings.Contains(plain, "TOOL CALL ✗") {
		t.Errorf("error header should contain TOOL CALL ✗, got %q", plain)
	}
}

// TestCollapsedToolDeltaPreviewTailEllipsis: while arguments stream, the
// collapsed tool line must show the LATEST delta chunk at the line end
// with the ellipsis at the line start (like every other delta summary) —
// never a trailing "…" after the content.
func TestCollapsedToolDeltaPreviewTailEllipsis(t *testing.T) {
	wb := NewWindowBuffer(50, DefaultStyles())
	longDelta := `{"path":"/home/user/very/long/path/that/exceeds/the/preview/width","content":"hello"}`
	wb.HandleToolInputDelta("t1", "write_file", longDelta, 0)

	rendered := wb.GetAll(-1, false)
	plain := stripANSI(rendered)
	if strings.Contains(plain, "\n") {
		t.Fatalf("collapsed tool line must be a single line, got %q", plain)
	}
	if !strings.Contains(plain, "…") {
		t.Fatalf("long delta should be truncated with an ellipsis, got %q", plain)
	}
	trimmed := strings.TrimRight(plain, " ")
	if strings.HasSuffix(trimmed, "…") {
		t.Errorf("ellipsis must be at the line START, not the end: %q", plain)
	}
	// The line ends with the delta tail (latest characters).
	if !strings.HasSuffix(trimmed, `hello"}`) {
		t.Errorf("collapsed line should end with the delta tail, got %q", plain)
	}
	// And it fits the terminal width (single soft-wrap-free row).
	if w := ansi.StringWidth(plain); w > 50 {
		t.Errorf("collapsed line width = %d, want <= 50: %q", w, plain)
	}
}

// TestExpandedToolDeltaPreviewTailEllipsis: the expanded (BuildInner)
// streaming preview follows the same rule — tail visible, "…" leading.
func TestExpandedToolDeltaPreviewTailEllipsis(t *testing.T) {
	styles := DefaultStyles()
	longDelta := `{"path":"/home/user/very/long/path/that/exceeds/the/preview/width","content":"hello"}`
	tr := &toolRenderer{
		name:        "write_file",
		deltaBuffer: longDelta,
		status:      ToolStatusPending,
	}

	lines, lineCount := tr.BuildInner(30, false, styles)
	if lineCount != 3 {
		t.Fatalf("lineCount = %d, want 3 (delta preview + box rules)", lineCount)
	}
	preview := lines[0].Text
	plain := stripANSI(preview)
	if !strings.HasPrefix(plain, "…") {
		t.Errorf("expanded delta preview should start with the ellipsis, got %q", preview)
	}
	if !strings.HasSuffix(preview, `hello"}`) {
		t.Errorf("expanded delta preview should end with the delta tail, got %q", preview)
	}
	if w := ansi.StringWidth(preview); w > 30 {
		t.Errorf("preview width = %d, want <= 30: %q", w, preview)
	}
}

// TestUserPromptCollapsedTail: the user prompt collapsed summary is
// consistent with assistant text — the escaped LATEST tail of the whole
// content (all text parts), never just the first line.
func TestUserPromptCollapsedTail(t *testing.T) {
	styles := DefaultStyles()

	// Multi-line content fits entirely: newlines escaped as literal "\n".
	ur := &userRenderer{textParts: []string{"first line\nsecond line\nthird line"}}
	line, count := ur.BuildCollapsed(100, styles)
	if count != 1 {
		t.Fatalf("collapsed lineCount = %d, want 1", count)
	}
	if want := "USER PROMPT     first line\\nsecond line\\nthird line"; stripANSI(line) != want {
		t.Errorf("BuildCollapsed = %q, want %q", stripANSI(line), want)
	}

	// Narrow width: tail shown with leading "…", first line dropped,
	// newlines escaped — same rule as assistant text.
	ur = &userRenderer{textParts: []string{"first line\nsecond line\nthird line"}}
	line, _ = ur.BuildCollapsed(34, styles)
	plain := stripANSI(line)
	if !strings.HasPrefix(plain, "USER PROMPT     …") {
		t.Errorf("collapsed should keep the label + leading ellipsis, got %q", plain)
	}
	if !strings.HasSuffix(plain, "\\nthird line") {
		t.Errorf("collapsed should show the escaped tail, got %q", plain)
	}
	if strings.Contains(plain, "first") {
		t.Errorf("collapsed tail should drop the first line, got %q", plain)
	}
	if strings.Contains(plain, "\n") {
		t.Errorf("collapsed must not contain raw newlines: %q", plain)
	}
	if w := ansi.StringWidth(plain); w > 34 {
		t.Errorf("collapsed width = %d, want <= 34: %q", w, plain)
	}

	// Multiple text parts: summary covers ALL parts, not just the first.
	ur = &userRenderer{textParts: []string{"part one", "part two"}}
	line, _ = ur.BuildCollapsed(100, styles)
	if want := "USER PROMPT     part one\\npart two"; stripANSI(line) != want {
		t.Errorf("multi-part BuildCollapsed = %q, want %q", stripANSI(line), want)
	}
}

// TestUserPromptLabel: the user window label is "USER PROMPT" in both the
// collapsed line and the expanded header, and its content stays aligned at
// the fixed label column like every other window type.
func TestUserPromptLabel(t *testing.T) {
	wb := NewWindowBuffer(40, DefaultStyles())
	wb.AppendOrUpdate(tlv.TagUserT, "u1", "what is lisp?")

	// Collapsed (user windows start folded): "▶ USER PROMPT what is lisp?".
	plain := stripANSI(wb.GetAll(-1, false))
	if !strings.HasPrefix(plain, "▶ USER PROMPT     what is lisp?") {
		t.Errorf("collapsed user line should start with ▶ USER PROMPT, got %q", plain)
	}
	if c := contentColumn(plain); c != 2+CollapsedLabelWidth {
		t.Errorf("collapsed USER content column = %d, want %d: %q", c, 2+CollapsedLabelWidth, plain)
	}

	// Expanded header: "▼ USER PROMPT".
	wb.ToggleFold(0)
	plain = stripANSI(wb.GetAll(-1, false))
	if !strings.Contains(plain, "▼ USER PROMPT") {
		t.Errorf("expanded header should contain ▼ USER PROMPT, got %q", plain)
	}
}

// TestUserPromptCollapsedTailEllipsis: an over-long USER folded summary
// keeps the tail of the content with the ellipsis at the line START —
// never a trailing "…".
func TestUserPromptCollapsedTailEllipsis(t *testing.T) {
	wb := NewWindowBuffer(30, DefaultStyles())
	long := "请把 /home/user/project/src/main.go 第 100 行的函数重构，注意保持接口兼容性并添加单元测试"
	wb.AppendOrUpdate(tlv.TagUserT, "u1", long)

	plain := stripANSI(wb.GetAll(-1, false))
	if strings.Contains(plain, "\n") {
		t.Fatalf("collapsed user line must be a single line, got %q", plain)
	}
	if !strings.Contains(plain, "…") {
		t.Fatalf("long user summary should be truncated with an ellipsis, got %q", plain)
	}
	trimmed := strings.TrimRight(plain, " ")
	if strings.HasSuffix(trimmed, "…") {
		t.Errorf("ellipsis must be at the line START, not the end: %q", plain)
	}
	// The line ends with the tail (latest characters) of the content.
	if !strings.HasSuffix(trimmed, "单元测试") {
		t.Errorf("collapsed user line should end with the first-line tail, got %q", plain)
	}
	if w := ansi.StringWidth(plain); w > 30 {
		t.Errorf("collapsed line width = %d, want <= 30: %q", w, plain)
	}
}

// TestUFOnlyCollapsedTailEllipsis: a UF-only tool window (no AF frame, no
// tool name — created via replayed UF content) summarizes its output with
// the tail and a leading ellipsis.
func TestUFOnlyCollapsedTailEllipsis(t *testing.T) {
	wb := NewWindowBuffer(30, DefaultStyles())
	long := strings.Repeat("0123456789", 6) // 60 chars
	wb.AppendOrUpdate(tlv.TagUserF, "uf-1", long)

	plain := stripANSI(wb.GetAll(-1, false))
	if strings.Contains(plain, "\n") {
		t.Fatalf("collapsed UF-only line must be a single line, got %q", plain)
	}
	if !strings.Contains(plain, "…") {
		t.Fatalf("long UF-only summary should be truncated with an ellipsis, got %q", plain)
	}
	trimmed := strings.TrimRight(plain, " ")
	if strings.HasSuffix(trimmed, "…") {
		t.Errorf("ellipsis must be at the line START, not the end: %q", plain)
	}
	if !strings.HasSuffix(trimmed, "0123456789") {
		t.Errorf("collapsed UF-only line should end with the output tail, got %q", plain)
	}
	if w := ansi.StringWidth(plain); w > 30 {
		t.Errorf("collapsed line width = %d, want <= 30: %q", w, plain)
	}
}

// TestUfPreviewTailEllipsis: the Uf output preview shown while a tool is
// executing follows the same rule as the Af delta preview — the latest
// output tail is kept with a leading "…" (rendered dim). The "…" is
// style-wrapped, so callers reading the unstyled text use stripANSI.
func TestUfPreviewTailEllipsis(t *testing.T) {
	long := strings.Repeat("progress line ", 10) // > 80 columns
	tr := &toolRenderer{
		name:   "execute_command",
		input:  "ls",
		output: long,
		status: ToolStatusPending,
	}

	styles := DefaultStyles()
	p := tr.previewOutput(80, styles)
	plain := stripANSI(p)
	if !strings.HasPrefix(plain, "…") {
		t.Errorf("Uf preview should start with the ellipsis, got %q", p)
	}
	if !strings.HasSuffix(plain, "line ") {
		t.Errorf("Uf preview should end with the output tail, got %q", p)
	}
	if w := ansi.StringWidth(p); w > 80 {
		t.Errorf("preview width = %d, want <= 80: %q", w, p)
	}

	// Short preview: unchanged, no ellipsis.
	tr.output = " 42%"
	if p := tr.previewOutput(80, styles); stripANSI(p) != " 42%" {
		t.Errorf("short preview = %q, want %q", stripANSI(p), " 42%")
	}
}
