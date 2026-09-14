package terminal

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/protocol"
	"github.com/alayacore/alayacore/internal/tlv"
)

// familyEmoji is the ZWJ family sequence 👨‍👩‍👧‍👦 — a multi-codepoint
// grapheme cluster used to verify takeCells / tailCells never split it
// mid-cluster. 7 codepoints (4 emoji + 3 ZWJ), 1 grapheme, 2 display cols.
const familyEmoji = "\U0001F468\u200D\U0001F469\u200D\U0001F467\u200D\U0001F466"

func TestFoldedToolCollapsedLine(t *testing.T) {
	wb := NewWindowBuffer(80, DefaultStyles())

	// Create a tool window with VERY long content that would wrap to many
	// lines if expanded. The collapsed form must be a single truncated line.
	longContent := strings.Repeat("This is a test sentence that will wrap. ", 12)
	wb.HandleToolInputEvent(protocol.ToolInputData{ID: "tool123", Name: "test_tool", Input: json.RawMessage(longContent)}, 0)

	// Folded by default — render the window
	rendered := wb.GetAll(-1, false)

	// Collapsed form: exactly one line, starting with the fold marker.
	renderedLines := strings.Split(rendered, "\n")
	if len(renderedLines) != 1 {
		t.Fatalf("Collapsed tool window should be a single line, got %d lines:\n%s", len(renderedLines), rendered)
	}
	plain := stripANSI(rendered)
	if !strings.HasPrefix(plain, foldArrow) {
		t.Errorf("Collapsed line should start with the fold marker, got %q", plain)
	}
	if !strings.Contains(plain, "TOOL") || !strings.Contains(plain, "test_tool") {
		t.Errorf("Collapsed line should contain TOOL + tool name, got %q", plain)
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
	if !strings.HasPrefix(stripANSI(rendered), foldArrow) {
		t.Error("Collapsed line must not contain box borders")
	}
	if !strings.Contains(stripANSI(rendered), "TOOL CALL") || !strings.Contains(stripANSI(rendered), "edit_file") {
		t.Errorf("Collapsed diff should show TOOL CALL + tool name, got %q", stripANSI(rendered))
	}
}

func TestUnfoldedWindowHasHeaderAndBox(t *testing.T) {
	wb := NewWindowBuffer(80, DefaultStyles())

	// Assistant window (unfolded by default)
	wb.AppendOrUpdate("AT", "assistant-1", "All edits are in place.")

	rendered := wb.GetAll(-1, false)
	lines := strings.Split(rendered, "\n")

	// Line 0: the window's own line (marker + label + timestamp); then the
	// content. No closing line — the next window's own line ends this one.
	if len(lines) != 2 {
		t.Fatalf("Expanded window should be 2 lines (window line + content), got %d:\n%s", len(lines), rendered)
	}
	ownLine := stripANSI(lines[0])
	if !strings.HasPrefix(ownLine, "- ASSISTANT") {
		t.Errorf("Window line should carry the ASSISTANT label, got %q", ownLine)
	}
	if w := cellWidth(ownLine); w != 80 {
		t.Errorf("Window line width = %d, want 80 (timestamp right-aligned): %q", w, ownLine)
	}
	if !strings.Contains(lines[1], "All edits are in place.") {
		t.Errorf("Content missing: %q", lines[1])
	}
}

func TestFoldedSystemWindowLabels(t *testing.T) {
	wb := NewWindowBuffer(80, DefaultStyles())

	// System notify (SN) — folded by default, shows SYSTEM NOTIFY label.
	wb.AppendOrUpdate("SN", "sys-1", "nothing to cancel")
	rendered := wb.GetAll(-1, false)
	plain := stripANSI(rendered)
	if !strings.HasPrefix(plain, foldArrow+" SYSTEM NOTIFY") {
		t.Errorf("System notify window should start with the fold marker + 'SYSTEM NOTIFY', got %q", plain)
	}
	if !strings.Contains(plain, "nothing to cancel") {
		t.Errorf("System notify window should show content after label, got %q", plain)
	}

	// System error (SE) — folded by default, shows SYSTEM ERROR label.
	wb.AppendOrUpdate("SE", "sys-2", "something failed")
	rendered = wb.GetAll(-1, false)
	lines := strings.Split(rendered, "\n")
	plain = stripANSI(lines[len(lines)-1])
	if !strings.HasPrefix(plain, foldArrow+" SYSTEM ERROR") {
		t.Errorf("System error window should start with the fold marker + 'SYSTEM ERROR', got %q", plain)
	}
	if !strings.Contains(plain, "something failed") {
		t.Errorf("System error window should show content after label, got %q", plain)
	}
}

func TestExpandedSystemWindowHeaderLabels(t *testing.T) {
	wb := NewWindowBuffer(80, DefaultStyles())

	// Unfold both system windows: the labeled top rules must show SYSTEM
	// NOTIFY / SYSTEM ERROR.
	wb.AppendOrUpdate("SN", "sys-1", "nothing to cancel")
	wb.AppendOrUpdate("SE", "sys-2", "something failed")
	wb.ToggleFold(0)
	wb.ToggleFold(1)

	rendered := wb.GetAll(-1, false)
	lines := strings.Split(rendered, "\n")

	if !strings.HasPrefix(stripANSI(lines[0]), "- SYSTEM NOTIFY") {
		t.Errorf("Expanded notify window line should carry 'SYSTEM NOTIFY', got %q", stripANSI(lines[0]))
	}
	// SE window: window line + content = 2 lines, so its own line is at
	// index 2.
	if !strings.HasPrefix(stripANSI(lines[2]), "- SYSTEM ERROR") {
		t.Errorf("Expanded error window line should carry 'SYSTEM ERROR', got %q", stripANSI(lines[2]))
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

	// Labels other than SYSTEM ERROR are bold + Styles.Label (muted by
	// default), and the content summary behind them is muted — a different
	// style on purpose, which is what lets the cursor highlight recolor
	// the label without touching the summary.
	labelBold := styles.Label.Bold(true)

	findLine := func(label string) string {
		for _, l := range lines {
			if strings.HasPrefix(stripANSI(l), foldArrow+" "+label) {
				return l
			}
		}
		t.Fatalf("no collapsed line for label %q in:\n%s", label, rendered)
		return ""
	}

	// REASONING: label plain bold (no italic, no color); content summary
	// muted (uniform with all collapsed window types).
	line := findLine("REASONING")
	if !strings.Contains(line, labelBold.Render(padLabel("REASONING"))) {
		t.Errorf("REASONING label should be plain bold (no color): %q", line)
	}
	if !strings.Contains(stripANSI(line), "thinking about the plan") {
		t.Errorf("REASONING content summary should be present: %q", line)
	}
	if !strings.Contains(line, styles.System.Render("thinking about the plan")) {
		t.Errorf("REASONING content summary should be muted: %q", line)
	}

	// ASSISTANT: label plain bold, content muted.
	line = findLine("ASSISTANT")
	if !strings.Contains(line, labelBold.Render(padLabel("ASSISTANT"))) {
		t.Errorf("ASSISTANT label should be plain bold (no color): %q", line)
	}
	if !strings.Contains(stripANSI(line), "all edits are in place") {
		t.Errorf("ASSISTANT content summary should be present: %q", line)
	}
	if !strings.Contains(line, styles.System.Render("all edits are in place")) {
		t.Errorf("ASSISTANT content summary should be muted: %q", line)
	}

	// SYSTEM NOTIFY: label keeps the label color (muted).
	line = findLine("SYSTEM NOTIFY")
	if !strings.Contains(line, labelBold.Render(padLabel("SYSTEM NOTIFY"))) {
		t.Errorf("SYSTEM NOTIFY label should keep the label (muted) color: %q", line)
	}
	if !strings.Contains(line, styles.System.Render("nothing to cancel")) {
		t.Errorf("SYSTEM NOTIFY content summary should be muted: %q", line)
	}

	// SYSTEM ERROR: label keeps its Error (red) color + bold (uniform with
	// every other label), content muted.
	line = findLine("SYSTEM ERROR")
	if !strings.Contains(line, styles.Error.Bold(true).Render(padLabel("SYSTEM ERROR"))) {
		t.Errorf("SYSTEM ERROR label should keep the Error (red) color: %q", line)
	}
	if !strings.Contains(line, styles.System.Render("session failed")) {
		t.Errorf("SYSTEM ERROR content summary should be muted: %q", line)
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

	// "TOOL CALL" is plain bold (the label color), the status indicator
	// shares the label color so they read as a single colored unit, the
	// tool name is bold + muted (so it stands out from the arguments), and
	// the arguments after the name stay muted (no bold).
	//
	// The name's style is the SAME one the expanded row uses, which in this
	// register is the same bytes as the label's — so the assertion below
	// cannot tell the two apart. What pins the name's style as its own is
	// TestToolNameIsIdenticalFoldedAndExpanded, which compares the two fold
	// states under the cursor, where they differ.
	if !strings.Contains(rendered, styles.Label.Bold(true).Render("TOOL CALL")) {
		t.Errorf("TOOL CALL label should be plain bold (the label color): %q", rendered)
	}
	if strings.Contains(rendered, styles.Tool.Render("TOOL CALL")) {
		t.Errorf("TOOL CALL label should not use the Tool color: %q", rendered)
	}
	if !strings.Contains(rendered, styles.ToolContent.Bold(true).Render("edit_file")) {
		t.Errorf("tool name should be bold + muted: %q", rendered)
	}
	if !strings.Contains(rendered, styles.ToolContent.Render(" /tmp/style.css")) {
		t.Errorf("arguments (with leading space) should be muted (not bold): %q", rendered)
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

	// Collapse the user and assistant windows (both start expanded).
	wb.ToggleFold(0)
	wb.ToggleFold(2)

	rendered := wb.GetAll(-1, false)
	lines := strings.Split(rendered, "\n")

	// Every collapsed line: content must start at the same display column
	// (arrow + space + CollapsedLabelWidth) — labels are left-justified to a
	// fixed column so USER/REASONING/ASSISTANT/… content aligns.
	wantCol := 2 + CollapsedLabelWidth
	for _, l := range lines {
		plain := stripANSI(l)
		if !strings.HasPrefix(plain, foldArrow) {
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
		w := cellWidth(string(r))
		if col < limit {
			col += w
			continue
		}
		return col // first character beyond the label column
	}
	return col
}

func TestFoldedToolStatusIndicatorNoReplacementChar(t *testing.T) {
	// Regression: the status indicator (✓, spinner) is multi-byte UTF-8 —
	// byte-slicing it produced a broken first byte rendered as U+FFFD (�).
	styles := DefaultStyles()
	wb := NewWindowBuffer(80, styles)

	wb.HandleToolInputEvent(protocol.ToolInputData{
		ID:    "t1",
		Name:  "execute_command",
		Input: json.RawMessage("execute_command: lscpu"),
	}, 1)
	wb.HandleToolOutput("t1", "x86_64", false, 1)

	// Collapsed (default for tools) — success shows the plain ✓.
	rendered := wb.GetAll(-1, false)
	plain := stripANSI(rendered)
	if strings.Contains(rendered, "\uFFFD") {
		t.Errorf("collapsed line contains replacement char: %q", rendered)
	}
	if !strings.Contains(plain, "TOOL CALL ✓") {
		t.Errorf("collapsed line should contain TOOL CALL + success check, got %q", plain)
	}
	// Content column must be exactly "arrow + space" + label column.
	if c := contentColumn(plain); c != 2+CollapsedLabelWidth {
		t.Errorf("collapsed TOOL content column = %d, want %d: %q", c, 2+CollapsedLabelWidth, plain)
	}

	// Expanded.
	wb.ToggleFold(0)
	rendered = wb.GetAll(-1, false)
	if strings.Contains(rendered, "\uFFFD") {
		t.Errorf("expanded header contains replacement char: %q", rendered)
	}
	if !strings.Contains(stripANSI(rendered), "TOOL CALL ✓") {
		t.Errorf("expanded header should contain TOOL CALL + success check, got %q", stripANSI(rendered))
	}
}

func TestTakeHeadAndTakeTailClusterAware(t *testing.T) {
	// takeCells and tailCells must drop whole grapheme clusters rather
	// than splitting individual runes — multi-codepoint glyphs like ZWJ
	// emoji, combining marks, and variation selectors would render as
	// U+FFFD if cut mid-cluster.
	tests := []struct {
		name  string
		input string
		width int
		head  string
		tail  string
	}{
		{
			name:  "single-width ascii",
			input: "abcdefgh",
			width: 3,
			head:  "abc",
			tail:  "fgh",
		},
		{
			name:  "CJK: each char is 2 cols, never split mid-rune",
			input: "中文内容",
			width: 3,
			head:  "中", // "中"=2 cols fits, "中"+"文"=4 > 3 → drop "文"
			tail:  "容", // drop "文"+"中" → "容" (2 cols fits 3)
		},
		{
			name:  "CJK with too-narrow width: cluster dropped, not split",
			input: "中文",
			width: 1,
			head:  "", // neither CJK char fits in 1 col
			tail:  "",
		},
		{
			name:  "ZWJ family cluster: kept whole or dropped whole",
			input: "a" + familyEmoji + "b", // "a" + 👨‍👩‍👧‍👦 + "b" — 3 clusters: 1, 2, 1 cols
			width: 1,
			head:  "a", // family cluster is 2 cols, doesn't fit in 1 → dropped
			tail:  "b", // family cluster dropped
		},
		{
			name:  "ZWJ family cluster fits when width allows",
			input: "a" + familyEmoji + "b",
			width: 3,
			head:  "a" + familyEmoji, // 1+2=3 cols, "b" would be 4 → drop
			tail:  familyEmoji + "b", // 2+1=3 cols, "a" would be 4 → drop
		},
		{
			// The case the two width tables used to disagree on: a keycap
			// ("1" + U+FE0F + U+20E3) is 1 cell to uniseg and 2 to
			// displaywidth. Cutting on one table against a budget from the
			// other filled a 4-cell budget with 5 cells.
			name:  "keycap: cut and measured by the same table",
			input: "\u0031\uFE0F\u20E3 abcd",
			width: 4,
			head:  "1️⃣ a", // keycap(2) + " "(1) + "a"(1) = 4; "b" would be 5
			tail:  "abcd",  // 7 total; drop keycap(2) then " "(1) → exactly 4
		},
		{
			name:  "combining acute: e + combining mark kept together",
			input: "e\u0301o", // é (e + combining acute U+0301) then o
			width: 1,
			head:  "e\u0301", // é is 1 col (combining), fits; drop "o"
			tail:  "o",       // tail: "o" (1 col) fits, "é" would push to 2
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := takeCells(tt.input, tt.width)
			if h != tt.head {
				t.Errorf("takeCells(%q, %d) = %q, want %q", tt.input, tt.width, h, tt.head)
			}
			if w := cellWidth(h); w > tt.width {
				t.Errorf("takeCells width = %d, want <= %d: %q", w, tt.width, h)
			}
			tl := tailCells(tt.input, tt.width)
			if tl != tt.tail {
				t.Errorf("tailCells(%q, %d) = %q, want %q", tt.input, tt.width, tl, tt.tail)
			}
			if w := cellWidth(tl); w > tt.width {
				t.Errorf("tailCells width = %d, want <= %d: %q", w, tt.width, tl)
			}
			// Critical: no replacement characters (U+FFFD) from cluster splits.
			if strings.Contains(h, "\uFFFD") {
				t.Errorf("takeCells produced replacement char (cluster split): %q", h)
			}
			if strings.Contains(tl, "\uFFFD") {
				t.Errorf("tailCells produced replacement char (cluster split): %q", tl)
			}
		})
	}
}

func TestTailParts(t *testing.T) {
	// tailParts returns the tail portion (no leading "…") and a truncated
	// flag. Used by callers that want to style the marker themselves
	// (e.g. dim vs muted).
	tests := []struct {
		name      string
		content   string
		maxWidth  int
		wantTail  string
		wantTrunc bool
	}{
		{name: "fits entirely", content: "hello", maxWidth: 20, wantTail: "hello", wantTrunc: false},
		{name: "needs truncation", content: "aaaaaaaaaa bbbbbbbbbb cccccccccc", maxWidth: 15, wantTail: "bbbb cccccccccc", wantTrunc: true},
		{name: "zero width", content: "hello", maxWidth: 0, wantTail: "", wantTrunc: false},
		{name: "single column", content: "hello", maxWidth: 1, wantTail: "", wantTrunc: false},
		{name: "CJK multibyte safe", content: "中文内容测试尾部", maxWidth: 8, wantTail: "测试尾部", wantTrunc: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tail, trunc := tailParts(tt.content, tt.maxWidth)
			if tail != tt.wantTail {
				t.Errorf("tailParts(%q, %d) tail = %q, want %q", tt.content, tt.maxWidth, tail, tt.wantTail)
			}
			if trunc != tt.wantTrunc {
				t.Errorf("tailParts(%q, %d) truncated = %v, want %v", tt.content, tt.maxWidth, trunc, tt.wantTrunc)
			}
		})
	}
}

func TestHeadAndTailParts(t *testing.T) {
	// headAndTailParts returns head and tail separately plus a truncated
	// flag. When not truncated, head is the full content and tail is empty.
	tests := []struct {
		name      string
		content   string
		maxWidth  int
		wantHead  string
		wantTail  string
		wantTrunc bool
	}{
		{
			name:     "fits entirely",
			content:  "hello",
			maxWidth: 20,
			wantHead: "hello", wantTail: "", wantTrunc: false,
		},
		{
			name:     "needs head+tail",
			content:  "aaaaaaaaaa bbbbbbbbbb cccccccccc",
			maxWidth: 15,
			// headWidth = 6, tailWidth = 8
			wantHead: "aaaaaa", wantTail: "cccccccc", wantTrunc: true,
		},
		{
			name:     "single column: head only",
			content:  "hello",
			maxWidth: 1,
			wantHead: "h", wantTail: "", wantTrunc: true,
		},
		{
			name:     "two columns: head only (no room for ellipsis)",
			content:  "hello",
			maxWidth: 2,
			wantHead: "he", wantTail: "", wantTrunc: true,
		},
		{
			name:     "zero width",
			content:  "hello",
			maxWidth: 0,
			wantHead: "", wantTail: "", wantTrunc: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			head, tail, trunc := headAndTailParts(tt.content, tt.maxWidth)
			if head != tt.wantHead {
				t.Errorf("headAndTailParts(%q, %d) head = %q, want %q", tt.content, tt.maxWidth, head, tt.wantHead)
			}
			if tail != tt.wantTail {
				t.Errorf("headAndTailParts(%q, %d) tail = %q, want %q", tt.content, tt.maxWidth, tail, tt.wantTail)
			}
			if trunc != tt.wantTrunc {
				t.Errorf("headAndTailParts(%q, %d) truncated = %v, want %v", tt.content, tt.maxWidth, trunc, tt.wantTrunc)
			}
		})
	}
}

func TestFoldedTextWindowEllipsisIsDimmed(t *testing.T) {
	// The truncation marker ("…") in collapsed text summaries is rendered
	// with the dim color (styles.Status), not muted (styles.System).
	// This visually separates the marker from the actual content. Applies
	// to both head+tail ("…middle…") and tail-only ("…tail") summaries.
	styles := DefaultStyles()
	wb := NewWindowBuffer(80, styles)

	// AT (assistant) — uses headAndTailParts, middle "…" is dimmed.
	longText := strings.Repeat("beginning of answer ", 3) + " and then some more content ending"
	wb.AppendOrUpdate(tlv.TagAssistantT, "a1", longText)
	wb.ToggleFold(0)
	atRendered := wb.GetAll(-1, false)
	atPlain := stripANSI(atRendered)
	if !strings.Contains(atPlain, "…") {
		t.Fatalf("AT collapsed summary should contain the middle ellipsis: %q", atPlain)
	}
	// Find the "…" in the rendered output and verify it carries the dim
	// color codes (status style), not the muted ones.
	if !containsDimEllipsis(atRendered, styles) {
		t.Errorf("AT middle ellipsis should carry the dim (Status) color, got %q", atRendered)
	}

	// SN (system notify) — uses tailParts, leading "…" is dimmed.
	wb2 := NewWindowBuffer(40, styles)
	wb2.AppendOrUpdate("SN", "n1", strings.Repeat("very long system notification that will need truncation ", 3))
	snRendered := wb2.GetAll(-1, false)
	snPlain := stripANSI(snRendered)
	if !strings.HasPrefix(snPlain, foldArrow+" SYSTEM NOTIFY") || !strings.Contains(snPlain, "…") {
		t.Fatalf("SN collapsed summary should start with the leading ellipsis: %q", snPlain)
	}
	if !containsDimEllipsis(snRendered, styles) {
		t.Errorf("SN leading ellipsis should carry the dim (Status) color, got %q", snRendered)
	}
}

// containsDimEllipsis checks that the first "…" character in the
// rendered ANSI string is wrapped in the Status (dim) color — not the
// System (muted) color used by the surrounding content. We do this by
// rendering "…" with each style and seeing which escape codes appear
// before it in the actual output.
func containsDimEllipsis(rendered string, styles *Styles) bool {
	dimStyled := styles.Status.Render("…")

	// Find the position of the first "…" character (3 UTF-8 bytes).
	idx := strings.Index(rendered, "…")
	if idx < 0 {
		return false
	}

	// Look for the SGR sequence that styles this "…": the last "\x1b["
	// that opens a sequence ending with 'm' before idx.
	escStart := strings.LastIndex(rendered[:idx], "\x1b[")
	if escStart < 0 {
		return false
	}
	mIdx := strings.IndexByte(rendered[escStart:], 'm')
	if mIdx < 0 {
		return false
	}
	prefix := rendered[escStart : escStart+mIdx+1]

	// Does this prefix match the dim style's prefix? If yes, the "…" is
	// dim — the desired behavior.
	return strings.HasPrefix(dimStyled, prefix)
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

func TestFoldedTextWindowHeadAndTailUpdatesOnDelta(t *testing.T) {
	// The whole point: as deltas arrive, the collapsed summary keeps the
	// head (topic) stable and updates the tail (latest content). The
	// truncation marker sits in the MIDDLE — never at the line start
	// (that's tailParts's behavior, used for streaming-tail content).
	styles := DefaultStyles()
	wb := NewWindowBuffer(80, styles)

	// Simulate a streaming assistant window: long content so the collapsed
	// summary truncates and shows head + "…" + tail.
	wb.AppendOrUpdate(tlv.TagAssistantT, "a1", strings.Repeat("beginning of answer ", 3))
	wb.AppendOrUpdate(tlv.TagAssistantT, "a1", " and then some more content ending")
	wb.ToggleFold(0) // AT is expanded by default; fold it for the summary test

	rendered := wb.GetAll(-1, false)
	plain := stripANSI(rendered)
	if !strings.Contains(plain, "some more content ending") {
		t.Errorf("collapsed summary should reflect the latest delta, got %q", plain)
	}
	if !strings.Contains(plain, "beginning of answer") {
		t.Errorf("collapsed summary should keep the head (topic), got %q", plain)
	}
	// The middle ellipsis sits between head and tail — the line does NOT
	// begin with "…" (that's tailParts's behavior).
	if strings.HasPrefix(plain, foldArrow+" ASSISTANT       …") {
		t.Errorf("collapsed summary must not start with a leading ellipsis: %q", plain)
	}
	if !strings.Contains(plain, "…") {
		t.Errorf("collapsed summary should contain the middle ellipsis, got %q", plain)
	}
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
	return cellWidth(s)
}

// TestFoldedTextHeadAndTailEqualWeight: regression test for a bug where
// the head of a collapsed text window (REASONING / ASSISTANT / SN / SE)
// was rendered with bold+muted styling while the tail was rendered with
// muted-only styling — inconsistent visual weight between the two halves
// of "head + … + tail". The label is bold by design; the head and tail
// must match each other.
func TestFoldedTextHeadAndTailEqualWeight(t *testing.T) {
	styles := DefaultStyles()
	wb := NewWindowBuffer(80, styles)

	longText := "this is the start of a very long reasoning that exceeds the available width for sure"
	wb.AppendOrUpdate(tlv.TagAssistantR, "r1", longText)

	rendered := wb.GetAll(-1, false)
	plain := stripANSI(rendered)
	idx := strings.Index(plain, "…")
	if idx < 0 {
		t.Fatalf("expected head + … + tail, got %q", plain)
	}
	// Find the byte positions of "this is" (start of head) and "for sure"
	// (end of tail) in the RAW rendered string — that's where the styled
	// segments begin/end. ANSI codes count as bytes; we work on the raw
	// string, not the stripped plain.
	headStart := strings.Index(rendered, "this is")
	tailEnd := strings.Index(rendered, "for sure") + len("for sure")
	if headStart < 0 || tailEnd <= headStart {
		t.Fatalf("could not locate head/tail in rendered output: %q", rendered)
	}

	// Extract the SGR (if any) right before the head and right before the
	// tail. They must be identical (both muted-only — no bold attribute).
	headPrefix := ansiSGRBefore(rendered, headStart)
	tailPrefix := ansiSGRBefore(rendered, tailEnd)
	if headPrefix != tailPrefix {
		t.Errorf("head and tail must have the same styling\n"+
			"head SGR before: %q\n"+
			"tail SGR before: %q\n"+
			"rendered: %q", headPrefix, tailPrefix, rendered)
	}
	// And neither should carry the bold attribute (1).
	if strings.Contains(headPrefix, ";1;") || strings.HasPrefix(headPrefix, "1;") ||
		strings.Contains(headPrefix, "[1") || headPrefix == "[1m" {
		t.Errorf("head must NOT be bold (label bold should not bleed into content); SGR=%q", headPrefix)
	}
}

// ansiSGRBefore returns the SGR parameter sequence that immediately
// precedes the given byte position in the rendered output (i.e., the
// most recent "\x1b[...m" before pos). Returns "" if none.
func ansiSGRBefore(rendered string, pos int) string {
	// Find the last SGR ending at or before pos.
	for i := pos - 1; i >= 0; i-- {
		if rendered[i] != 'm' {
			continue
		}
		// Walk back to the matching ESC.
		j := i - 1
		for j >= 1 && !(rendered[j-1] == 0x1b && rendered[j] == '[') {
			j--
		}
		if j >= 1 && rendered[j-1] == 0x1b && rendered[j] == '[' {
			return rendered[j+1 : i]
		}
	}
	return ""
}
