package terminal

// Tests for the tool status indicator (spinner while running, colorless
// ✓/✗ when done), the collapsed/expanded delta preview ellipsis position
// (tail shown, "…" at the line start), and the USER PROMPT label.

import (
	"encoding/json"
	"strings"
	"testing"

	ansi "github.com/charmbracelet/x/ansi"

	"github.com/alayacore/alayacore/internal/protocol"
	"github.com/alayacore/alayacore/internal/tlv"
)

// TestToolStatusIndicatorColorless verifies every status glyph renders
// without any ANSI styling (the spinner and ✓/✗ replace the old colored
// dots) and stays exactly 1 display column wide so the label column
// alignment holds.
func TestToolStatusIndicatorColorless(t *testing.T) {
	styles := DefaultStyles()
	for _, st := range []ToolStatus{ToolStatusNone, ToolStatusPending, ToolStatusSuccess, ToolStatusError} {
		glyph, stl := st.statusDot(styles)
		if glyph == "" {
			t.Errorf("status %d: empty glyph", st)
			continue
		}
		rendered := stl.Render(glyph)
		if strings.Contains(rendered, "\x1b") {
			t.Errorf("status %d: indicator must be colorless, got %q", st, rendered)
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
	styles := DefaultStyles()
	for _, st := range []ToolStatus{ToolStatusNone, ToolStatusPending} {
		glyph, _ := st.statusDot(styles)
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
	if g, _ := ToolStatusSuccess.statusDot(styles); g != "✓" {
		t.Errorf("success glyph = %q, want ✓", g)
	}
	if g, _ := ToolStatusError.statusDot(styles); g != "✗" {
		t.Errorf("error glyph = %q, want ✗", g)
	}
}

// TestToolStatusIndicatorHeaderStates drives the WindowBuffer through the
// full lifecycle: pending header shows TOOLUSE + spinner, success shows
// TOOLUSE ✓, error shows TOOLUSE ✗.
func TestToolStatusIndicatorHeaderStates(t *testing.T) {
	wb := NewWindowBuffer(60, DefaultStyles())
	wb.HandleToolInputEvent(protocol.ToolInputData{
		ID:    "t1",
		Name:  "execute_command",
		Input: json.RawMessage("execute_command: lscpu"),
	}, 0)

	// Pending (executing): header is "▶ TOOLUSE ⠋ …" — a separator space
	// and a spinner frame right after the label.
	plain := stripANSI(wb.GetAll(-1, false))
	if !strings.HasPrefix(plain, "▶ TOOLUSE ") {
		t.Fatalf("pending header should start with ▶ TOOLUSE + space, got %q", plain)
	}
	rest := []rune(strings.TrimPrefix(plain, "▶ TOOLUSE "))
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
	if !strings.Contains(plain, "TOOLUSE ✓") {
		t.Errorf("success header should contain TOOLUSE ✓, got %q", plain)
	}

	// Error: plain ✗ after the separator space.
	wb.HandleToolOutput("t1", "boom", true, 0)
	plain = stripANSI(wb.GetAll(-1, false))
	if !strings.Contains(plain, "TOOLUSE ✗") {
		t.Errorf("error header should contain TOOLUSE ✗, got %q", plain)
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
	if !strings.HasPrefix(preview, "…") {
		t.Errorf("expanded delta preview should start with the ellipsis, got %q", preview)
	}
	if !strings.HasSuffix(preview, `hello"}`) {
		t.Errorf("expanded delta preview should end with the delta tail, got %q", preview)
	}
	if w := ansi.StringWidth(preview); w > 30 {
		t.Errorf("preview width = %d, want <= 30: %q", w, preview)
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
	if !strings.HasPrefix(plain, "▶ USER PROMPT what is lisp?") {
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
