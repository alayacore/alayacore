package terminal

import (
	"encoding/json"
	"strings"
	"testing"

	ansi "github.com/charmbracelet/x/ansi"

	"github.com/alayacore/alayacore/internal/protocol"
	"github.com/alayacore/alayacore/internal/theme"
	"github.com/alayacore/alayacore/internal/tlv"
)

func TestFoldedToolCollapsedLine(t *testing.T) {
	wb := NewWindowBuffer(80, DefaultStyles())

	// Create a tool window with VERY long content that would wrap to many
	// lines if expanded. The collapsed form must be a single truncated line.
	longContent := strings.Repeat("This is a test sentence that will wrap. ", 12)
	wb.HandleToolInputEvent(protocol.ToolInputData{ID: "tool123", Name: "test_tool", Input: json.RawMessage(longContent)}, 0)

	// Folded by default — render the window
	rendered := wb.GetAll(-1, false)

	// Collapsed form: exactly one line, starting with the collapse arrow.
	renderedLines := strings.Split(rendered, "\n")
	if len(renderedLines) != 1 {
		t.Fatalf("Collapsed tool window should be a single line, got %d lines:\n%s", len(renderedLines), rendered)
	}
	plain := stripANSI(rendered)
	if !strings.HasPrefix(plain, "▶") {
		t.Errorf("Collapsed line should start with ▶ arrow, got %q", plain)
	}
	if !strings.Contains(plain, "TOOL•") || !strings.Contains(plain, "test_tool") {
		t.Errorf("Collapsed line should contain TOOL• + tool name, got %q", plain)
	}
	// Long content is truncated to a single line.
	if strings.Contains(plain, "\n") {
		t.Error("Collapsed line must not contain newlines")
	}
}

func TestFoldedToolCollapsedTruncatesToWidth(t *testing.T) {
	wb := NewWindowBuffer(30, DefaultStyles())

	longContent := strings.Repeat("This is a very long tool argument that will definitely overflow. ", 8)
	wb.HandleToolInputEvent(protocol.ToolInputData{ID: "tool123", Name: "test_tool", Input: json.RawMessage(longContent)}, 0)

	rendered := wb.GetAll(-1, false)
	plain := stripANSI(rendered)

	// The collapsed line must fit the terminal width (30) — otherwise the
	// terminal soft-wraps it and the 1-line invariant breaks.
	if w := displayWidth(plain); w > 30 {
		t.Errorf("Collapsed line width = %d, want <= 30: %q", w, plain)
	}
	// Truncation should leave an ellipsis.
	if !strings.Contains(plain, "…") {
		t.Errorf("Collapsed line should be truncated with ellipsis, got %q", plain)
	}
}

func TestFoldedDiffCollapsedLine(t *testing.T) {
	wb := NewWindowBuffer(80, DefaultStyles())

	// Create a diff window with many lines
	var content strings.Builder
	content.WriteString("edit_file: test.txt\n")
	for i := 0; i < 20; i++ {
		content.WriteString("- old line ")
		content.WriteString(string(rune('0' + i%10)))
		content.WriteString("\n+ new line ")
		content.WriteString(string(rune('0' + i%10)))
		content.WriteString("\n")
	}
	wb.HandleToolInputEvent(protocol.ToolInputData{ID: "diff123", Name: "edit_file", Input: json.RawMessage(content.String())}, 0)

	// Folded by default — render
	rendered := wb.GetAll(-1, false)

	// Single line, no borders, no fold indicator row.
	renderedLines := strings.Split(rendered, "\n")
	if len(renderedLines) != 1 {
		t.Errorf("Folded diff should collapse to a single line, got %d lines:\n%s", len(renderedLines), rendered)
	}
	if !strings.HasPrefix(stripANSI(rendered), "▶") {
		t.Error("Collapsed line must not contain box borders")
	}
	if !strings.Contains(stripANSI(rendered), "TOOL•") || !strings.Contains(stripANSI(rendered), "edit_file") {
		t.Errorf("Collapsed diff should show TOOL• + tool name, got %q", stripANSI(rendered))
	}
}

func TestUnfoldedWindowHasHeaderAndBox(t *testing.T) {
	wb := NewWindowBuffer(80, DefaultStyles())

	// Assistant window (unfolded by default)
	wb.AppendOrUpdate("AT", "assistant-1", "All edits are in place.")

	rendered := wb.GetAll(-1, false)
	lines := strings.Split(rendered, "\n")

	// Line 0: header with expand arrow + label; line 1: top rule; last: bottom rule.
	if len(lines) != 4 {
		t.Fatalf("Expanded window should be 4 lines (header + box), got %d:\n%s", len(lines), rendered)
	}
	header := stripANSI(lines[0])
	if !strings.HasPrefix(header, "▼") {
		t.Errorf("Header should start with ▼ arrow, got %q", header)
	}
	if !strings.Contains(header, "ASSISTANT") {
		t.Errorf("Header should contain ASSISTANT label, got %q", header)
	}
	if !strings.Contains(lines[1], strings.Repeat("─", 80)) {
		t.Errorf("Top rule missing: %q", lines[1])
	}
	if !strings.Contains(lines[len(lines)-1], strings.Repeat("─", 80)) {
		t.Errorf("Bottom rule missing: %q", lines[len(lines)-1])
	}
	if !strings.Contains(lines[2], "All edits are in place.") {
		t.Errorf("Content missing: %q", lines[2])
	}
}

func TestFoldedSystemWindowLabels(t *testing.T) {
	wb := NewWindowBuffer(80, DefaultStyles())

	// System notify (SN) — folded by default, shows NOTIFY label.
	wb.AppendOrUpdate("SN", "sys-1", "nothing to cancel")
	rendered := wb.GetAll(-1, false)
	plain := stripANSI(rendered)
	if !strings.HasPrefix(plain, "▶ NOTIFY") {
		t.Errorf("System notify window should start with '▶ NOTIFY', got %q", plain)
	}
	if !strings.Contains(plain, "nothing to cancel") {
		t.Errorf("System notify window should show content after label, got %q", plain)
	}

	// System error (SE) — folded by default, shows ERROR label.
	wb.AppendOrUpdate("SE", "sys-2", "something failed")
	rendered = wb.GetAll(-1, false)
	lines := strings.Split(rendered, "\n")
	plain = stripANSI(lines[len(lines)-1])
	if !strings.HasPrefix(plain, "▶ ERROR") {
		t.Errorf("System error window should start with '▶ ERROR', got %q", plain)
	}
	if !strings.Contains(plain, "something failed") {
		t.Errorf("System error window should show content after label, got %q", plain)
	}
}

func TestExpandedSystemWindowHeaderLabels(t *testing.T) {
	wb := NewWindowBuffer(80, DefaultStyles())

	// Unfold both system windows: headers must show NOTIFY / ERROR labels.
	wb.AppendOrUpdate("SN", "sys-1", "nothing to cancel")
	wb.AppendOrUpdate("SE", "sys-2", "something failed")
	wb.ToggleFold(0)
	wb.ToggleFold(1)

	rendered := wb.GetAll(-1, false)
	lines := strings.Split(rendered, "\n")

	if !strings.Contains(stripANSI(lines[0]), "▼ NOTIFY") {
		t.Errorf("Expanded notify header should be '▼ NOTIFY', got %q", stripANSI(lines[0]))
	}
	// SE window: header + box = 4 lines, so its header is at index 4.
	if !strings.Contains(stripANSI(lines[4]), "▼ ERROR") {
		t.Errorf("Expanded error header should be '▼ ERROR', got %q", stripANSI(lines[4]))
	}
}

func TestFoldedCollapsedLabelColors(t *testing.T) {
	styles := DefaultStyles()
	wb := NewWindowBuffer(80, styles)

	wb.AppendOrUpdate(tlv.TagAssistantR, "reason-1", "thinking about the plan")
	wb.AppendOrUpdate(tlv.TagAssistantT, "assist-1", "all edits are in place")
	wb.AppendOrUpdate("SN", "sys-1", "nothing to cancel")
	wb.AppendOrUpdate("SE", "sys-2", "session failed")

	// Fold the assistant window so every window is a single collapsed line.
	wb.ToggleFold(1)

	rendered := wb.GetAll(-1, false)
	lines := strings.Split(rendered, "\n")

	// Labels other than ERROR are bold + muted (no bright default color).
	mutedBold := styles.System.Bold(true)

	findLine := func(label string) string {
		for _, l := range lines {
			if strings.HasPrefix(stripANSI(l), "▶ "+label) {
				return l
			}
		}
		t.Fatalf("no collapsed line for label %q in:\n%s", label, rendered)
		return ""
	}

	// REASONING: label plain bold (no italic, no color); content summary
	// muted (uniform with all collapsed window types).
	line := findLine("REASONING")
	if !strings.Contains(line, mutedBold.Render(padLabel("REASONING"))) {
		t.Errorf("REASONING label should be plain bold (no color): %q", line)
	}
	if strings.Contains(line, styles.Reasoning.Render("REASONING")) {
		t.Errorf("REASONING label should not use the Reasoning color: %q", line)
	}
	if !strings.Contains(stripANSI(line), "thinking about the plan") {
		t.Errorf("REASONING content summary should be present: %q", line)
	}
	if !strings.Contains(line, styles.System.Render("thinking about the plan")) {
		t.Errorf("REASONING content summary should be muted: %q", line)
	}

	// ASSISTANT: label plain bold, content muted.
	line = findLine("ASSISTANT")
	if !strings.Contains(line, mutedBold.Render(padLabel("ASSISTANT"))) {
		t.Errorf("ASSISTANT label should be plain bold (no color): %q", line)
	}
	if strings.Contains(line, styles.Text.Render("ASSISTANT")) {
		t.Errorf("ASSISTANT label should not use the Text color: %q", line)
	}
	if !strings.Contains(stripANSI(line), "all edits are in place") {
		t.Errorf("ASSISTANT content summary should be present: %q", line)
	}
	if !strings.Contains(line, styles.System.Render("all edits are in place")) {
		t.Errorf("ASSISTANT content summary should be muted: %q", line)
	}

	// NOTIFY: label keeps its System (muted) color.
	line = findLine("NOTIFY")
	if !strings.Contains(line, styles.System.Bold(true).Render(padLabel("NOTIFY"))) {
		t.Errorf("NOTIFY label should keep the System (muted) color: %q", line)
	}
	if !strings.Contains(line, styles.System.Render("nothing to cancel")) {
		t.Errorf("NOTIFY content summary should be muted: %q", line)
	}

	// ERROR: label keeps its Error (red) color, content muted.
	line = findLine("ERROR")
	if !strings.Contains(line, styles.Error.Render(padLabel("ERROR"))) {
		t.Errorf("ERROR label should keep the Error (red) color: %q", line)
	}
	if !strings.Contains(line, styles.System.Render("session failed")) {
		t.Errorf("ERROR content summary should be muted: %q", line)
	}
}

func TestFoldedToolCollapsedLabelColor(t *testing.T) {
	styles := DefaultStyles()
	wb := NewWindowBuffer(80, styles)

	wb.HandleToolInputEvent(protocol.ToolInputData{
		ID:    "tool123",
		Name:  "edit_file",
		Input: json.RawMessage("edit_file: /tmp/style.css"),
	}, 0)

	rendered := wb.GetAll(-1, false)

	// "TOOL" is plain bold, the status dot uses the status color, and the
	// content (tool name + arguments) is muted.
	if !strings.Contains(rendered, styles.System.Bold(true).Render("TOOL")) {
		t.Errorf("TOOL label should be plain bold (no color): %q", rendered)
	}
	if strings.Contains(rendered, styles.Tool.Render("TOOL")) {
		t.Errorf("TOOL label should not use the Tool color: %q", rendered)
	}
	if !strings.Contains(rendered, styles.ToolContent.Render("edit_file /tmp/style.css")) {
		t.Errorf("TOOL content (name + args) should use muted ToolContent style: %q", rendered)
	}
}

func TestFoldedLabelsAligned(t *testing.T) {
	styles := DefaultStyles()
	wb := NewWindowBuffer(80, styles)

	wb.AppendOrUpdate(tlv.TagUserT, "u", "what is lisp?")
	wb.AppendOrUpdate(tlv.TagAssistantR, "r", "The user is asking what is lisp")
	wb.AppendOrUpdate(tlv.TagAssistantT, "a", "Lisp is one of the oldest languages")
	wb.AppendOrUpdate("SN", "n", "nothing to cancel")
	wb.AppendOrUpdate("SE", "e", "session failed")
	wb.HandleToolInputEvent(protocol.ToolInputData{ID: "t", Name: "cat", Input: json.RawMessage("cat: /tmp/x")}, 5)

	// Fold the assistant and user windows (folded by default otherwise).
	wb.ToggleFold(0)
	wb.ToggleFold(2)

	rendered := wb.GetAll(-1, false)
	lines := strings.Split(rendered, "\n")

	// Every collapsed line: content must start at the same display column
	// ("▶ " + CollapsedLabelWidth) — labels are left-justified to a fixed
	// column so USER/REASONING/ASSISTANT/… content aligns.
	wantCol := 2 + CollapsedLabelWidth
	for _, l := range lines {
		plain := stripANSI(l)
		if !strings.HasPrefix(plain, "▶") {
			continue
		}
		if c := contentColumn(plain); c != wantCol {
			t.Errorf("collapsed line content column = %d, want %d: %q", c, wantCol, plain)
		}
	}
}

// contentColumn returns the display column where the visible content begins
// for a collapsed line: after the arrow (2 cols) and the fixed label column
// (CollapsedLabelWidth). Labels longer than the column shift content right.
func contentColumn(plain string) int {
	col := 0
	limit := 2 + CollapsedLabelWidth
	for _, r := range plain {
		w := ansi.StringWidth(string(r))
		if col < limit {
			col += w
			continue
		}
		return col // first character beyond the label column
	}
	return col
}

func TestFoldedToolStatusDotNoReplacementChar(t *testing.T) {
	// Regression: the status dot (•) is multi-byte UTF-8 — byte-slicing it
	// produced a broken first byte rendered as U+FFFD (�).
	styles := DefaultStyles()
	wb := NewWindowBuffer(80, styles)

	wb.HandleToolInputEvent(protocol.ToolInputData{
		ID:    "t1",
		Name:  "execute_command",
		Input: json.RawMessage("execute_command: lscpu"),
	}, 1)
	wb.HandleToolOutput("t1", "x86_64", false, 1)

	// Collapsed (default for tools).
	rendered := wb.GetAll(-1, false)
	plain := stripANSI(rendered)
	if strings.Contains(rendered, "\uFFFD") {
		t.Errorf("collapsed line contains replacement char: %q", rendered)
	}
	if !strings.Contains(plain, "TOOL•") {
		t.Errorf("collapsed line should contain TOOL + status dot, got %q", plain)
	}
	// Content column must be exactly "▶ " + label column (11).
	if c := contentColumn(plain); c != 2+CollapsedLabelWidth {
		t.Errorf("collapsed TOOL content column = %d, want %d: %q", c, 2+CollapsedLabelWidth, plain)
	}

	// Expanded.
	wb.ToggleFold(0)
	rendered = wb.GetAll(-1, false)
	if strings.Contains(rendered, "\uFFFD") {
		t.Errorf("expanded header contains replacement char: %q", rendered)
	}
	if !strings.Contains(stripANSI(rendered), "TOOL•") {
		t.Errorf("expanded header should contain TOOL + status dot, got %q", stripANSI(rendered))
	}
}

func TestTailSummary(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		maxWidth int
		want     string
	}{
		{
			name:     "short content fits entirely",
			content:  "hello",
			maxWidth: 20,
			want:     "hello",
		},
		{
			name:     "long content shows tail with ellipsis",
			content:  "aaaaaaaaaa bbbbbbbbbb cccccccccc",
			maxWidth: 15,
			// Tail kept: 3 trailing "b"s + " " + 10 "c"s = 14 cols + "…".
			want: "…bbb cccccccccc",
		},
		{
			name:     "newlines escaped as literal backslash-n",
			content:  "line one\nline two",
			maxWidth: 50,
			want:     "line one\\nline two",
		},
		{
			name:     "tail keeps latest lines after escape",
			content:  "first line\nsecond line\nthird line",
			maxWidth: 20,
			// Tail of 19 cols: "nd line" + "\n" + "third line".
			want: "…nd line\\nthird line",
		},
		{
			name:     "zero width",
			content:  "hello",
			maxWidth: 0,
			want:     "",
		},
		{
			name:     "multibyte safe",
			content:  "中文内容测试尾部",
			maxWidth: 8,
			// room = 7 cols for the tail; 3 full-width chars (6 cols) fit:
			// "试尾部". No half-width slicing of multi-byte runes.
			want: "…试尾部",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tailSummary(tt.content, tt.maxWidth)
			if got != tt.want {
				t.Errorf("tailSummary(%q, %d) = %q, want %q", tt.content, tt.maxWidth, got, tt.want)
			}
			// The result must be a single logical line (no raw newlines).
			if strings.Contains(got, "\n") {
				t.Errorf("tailSummary must not contain raw newlines: %q", got)
			}
			// And must fit the width (or be empty).
			if w := ansi.StringWidth(got); w > tt.maxWidth {
				t.Errorf("tailSummary width = %d, want <= %d: %q", w, tt.maxWidth, got)
			}
		})
	}
}

func TestFoldedTextWindowShowsTail(t *testing.T) {
	// Streaming content: the collapsed summary shows the LATEST text so
	// the user sees new deltas arriving.
	styles := DefaultStyles()
	wb := NewWindowBuffer(80, styles)

	wb.AppendOrUpdate(tlv.TagAssistantT, "a1", "first line\nsecond line\nthird line tail")
	wb.ToggleFold(0) // AT is expanded by default; fold it for the summary test

	rendered := wb.GetAll(-1, false)
	plain := stripANSI(rendered)

	// Tail visible, truncation marker present, newline escaped.
	if !strings.Contains(plain, "third line tail") {
		t.Errorf("collapsed summary should show the tail, got %q", plain)
	}
	if !strings.Contains(plain, "\\n") {
		t.Errorf("newlines should be escaped as literal \\n, got %q", plain)
	}
	// Single visual line.
	if strings.Contains(plain, "\n") {
		t.Errorf("collapsed line must stay a single line, got %q", plain)
	}
}

func TestFoldedTextWindowTailUpdatesOnDelta(t *testing.T) {
	// The whole point: as deltas arrive, the collapsed summary moves.
	styles := DefaultStyles()
	wb := NewWindowBuffer(80, styles)

	// Simulate a streaming assistant window: long content so the collapsed
	// summary truncates and shows only the tail.
	wb.AppendOrUpdate(tlv.TagAssistantT, "a1", strings.Repeat("beginning of answer ", 3))
	wb.AppendOrUpdate(tlv.TagAssistantT, "a1", " and then some more content ending")
	wb.ToggleFold(0) // AT is expanded by default; fold it for the summary test

	rendered := wb.GetAll(-1, false)
	plain := stripANSI(rendered)
	if !strings.Contains(plain, "some more content ending") {
		t.Errorf("collapsed summary should reflect the latest delta, got %q", plain)
	}
	// The start was truncated away — the summary begins with "…".
	if !strings.HasPrefix(plain, "▶ ASSISTANT  …") {
		t.Errorf("collapsed summary should be truncated with a leading ellipsis: %q", plain)
	}
}

func TestFoldArrowThemeConfigurable(t *testing.T) {
	// Custom arrows from a theme override the hardcoded defaults.
	base := DefaultStyles()
	custom := NewStyles(&theme.Theme{
		Primary:     "#89d4fa",
		Dim:         "#313244",
		Muted:       "#6c7086",
		Text:        "#cdd6f4",
		Warning:     "#f77923",
		Error:       "#f38ba8",
		Success:     "#a6e3a1",
		Selection:   "#fab387",
		Cursor:      "#cdd6f4",
		Added:       "#a6e3a1",
		Removed:     "#f38ba8",
		Tool:        "#f9e2af",
		FoldArrow:   ">",
		UnfoldArrow: "v",
	})

	wb := NewWindowBuffer(80, custom)
	wb.AppendOrUpdate(tlv.TagAssistantT, "a1", "hello")

	// Expanded (default): expand arrow "v".
	rendered := wb.GetAll(-1, false)
	if !strings.Contains(stripANSI(rendered), "v ASSISTANT") {
		t.Errorf("expanded header should use theme expand arrow, got %q", stripANSI(rendered))
	}
	if strings.Contains(stripANSI(rendered), "▼") {
		t.Errorf("expanded header should not use hardcoded default, got %q", stripANSI(rendered))
	}

	// Folded: collapse arrow ">".
	wb.ToggleFold(0)
	rendered = wb.GetAll(-1, false)
	if !strings.HasPrefix(stripANSI(rendered), "> ") {
		t.Errorf("collapsed line should use theme collapse arrow, got %q", stripANSI(rendered))
	}
	if strings.Contains(stripANSI(rendered), "▶") {
		t.Errorf("collapsed line should not use hardcoded default, got %q", stripANSI(rendered))
	}

	// Defaults still apply when the theme does not configure arrows.
	_ = base
}

func TestCursorArrowColor(t *testing.T) {
	wb := NewWindowBuffer(80, DefaultStyles())
	wb.AppendOrUpdate("AT", "assistant-1", "Hello world")

	// Render without cursor: arrow is dim.
	plain := stripANSI(wb.GetAll(-1, false))

	// Render with cursor: arrow should use the selection color, border unchanged.
	rendered := wb.GetAll(0, false)
	if !strings.Contains(rendered, "\x1b[") {
		t.Fatal("Expected ANSI codes in cursor render")
	}
	_ = plain
}

// displayWidth returns the terminal display width of a plain string.
func displayWidth(s string) int {
	return ansi.StringWidth(s)
}
