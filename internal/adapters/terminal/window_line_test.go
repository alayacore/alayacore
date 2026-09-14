package terminal

// Tests for a window's own line — row 0, the only chrome a window spends:
//
//	+ REASONING       The user says "my os" — unclear. …
//	- REASONING                              2026/09/14 16:32:07 +08:00
//
// What is pinned here is the geometry the rest of the frame depends on, not
// a preference about looks: the row must never exceed the window width (the
// frame is emitted in raw passthrough mode and a row 1 cell too wide wraps
// into a second terminal row, shifting everything below it), the label must
// land in the same column in both fold states, and the timestamp column
// must be exactly as wide as the layout reserves because it is
// right-aligned.
//
// The row's *contents* (which label a window type uses, how a summary is
// truncated) are pinned by wrap_indicator_test.go and
// tool_status_indicator_test.go; this file is about the shape they sit in.

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/alayacore/alayacore/internal/protocol"
	"github.com/alayacore/alayacore/internal/tlv"
)

// pinnedTime is the arrival stamp the tests below give their windows. It is
// set explicitly because Window.CreatedAt is read from the clock when the
// adapter creates a window: a test that compares rendered rows must not
// depend on when it runs.
var pinnedTime = time.Date(2026, 9, 14, 16, 32, 0, 0, time.Local)

// pinCreatedAt stamps every window in the buffer, the way the adapter does
// when it receives a frame.
func pinCreatedAt(wb *WindowBuffer) {
	for i := 0; i < wb.WindowCount(); i++ {
		wb.WindowAt(i).CreatedAt = pinnedTime
	}
}

// TestTimeStampWidthIsPinned verifies the header's timestamp column against
// the layout constant: the row is right-aligned to the window edge, so a
// format whose formatted width drifted by a cell would move every timestamp
// on screen (and misalign the transcript) without any other test failing.
//
// The shape is pinned with the width, because the width is only stable
// while the shape is: "2026/09/14 18:30:07 +08:00" — seconds, and a numeric
// UTC offset rather than a zone name ("CST" is three different zones) or a
// zone-less local time (which would say nothing about which clock it is,
// and would silently change meaning in a terminal whose TZ differs from the
// session's). The cases below cover a positive offset, a negative one, UTC
// and single-digit fields.
func TestTimeStampWidthIsPinned(t *testing.T) {
	shape := regexp.MustCompile(`^\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2} [+-]\d{2}:\d{2}$`)
	for _, tm := range []time.Time{
		pinnedTime,
		time.Date(2006, 1, 2, 3, 4, 0, 0, time.Local),                       // single-digit month/day/hour
		time.Date(2026, 12, 31, 23, 59, 0, 0, time.UTC),                     // last minute of a year, UTC
		time.Date(2026, 12, 31, 23, 59, 0, 0, time.FixedZone("X", -5*3600)), // west of Greenwich
	} {
		got := tm.Format(timeStampLayout)
		if cellWidth(got) != timeStampWidth {
			t.Errorf("%q formats to %d cells, layout reserves %d", got, cellWidth(got), timeStampWidth)
		}
		if !shape.MatchString(got) {
			t.Errorf("%q is not the layout's shape %s: seconds and a numeric offset, nothing more",
				got, shape)
		}
	}
}

// TestWindowLineNeverExceedsWidth is the invariant the raw-passthrough frame
// depends on: whatever the width, row 0 measures at most the window width —
// and, when a timestamp is shown, exactly the width (it is right-aligned to
// the edge).
func TestWindowLineNeverExceedsWidth(t *testing.T) {
	for _, width := range []int{1, 2, 5, 12, 20, 40, 129} {
		styles := DefaultStyles()
		w := NewWindow("at-1", tlv.TagAssistantT, styles)
		w.Visible = true
		w.CreatedAt = pinnedTime
		w.AppendContent("hello")
		w.Render(width, false, styles, false)

		if len(w.cache.lines) == 0 {
			t.Fatalf("width %d: no border lines", width)
		}
		row := w.cache.lines[0]
		if row.Cont {
			t.Errorf("width %d: a window's own line must start a new original row (Cont=false)", width)
		}
		got := cellWidth(row.Text)
		if got > width {
			t.Errorf("width %d: row 0 measures %d cells: %q", width, got, stripANSI(row.Text))
		}
		if strings.Contains(stripANSI(row.Text), "\n") {
			t.Errorf("width %d: row 0 contains a hard newline: %q", width, stripANSI(row.Text))
		}
		plain := stripANSI(row.Text)
		if !strings.HasPrefix(plain, unfoldArrow) {
			t.Errorf("width %d: row 0 should open with the open marker: %q", width, plain)
		}
		if width <= collapsedPrefixWidth {
			// Not even room for the separator: the marker alone.
			continue
		}
		if !strings.HasPrefix(plain, unfoldArrow+" ") {
			t.Errorf("width %d: row 0 should open with the marker + space: %q", width, plain)
		}
		if width < 8 {
			// No room for the full label: it is cut, marker first.
			if !strings.Contains(plain, "A") {
				t.Errorf("width %d: row 0 should keep what it can of the label: %q", width, plain)
			}
			continue
		}
		wantTimestamp := got == width
		hasTimestamp := strings.Contains(plain, pinnedTime.Format(timeStampLayout))
		if wantTimestamp != hasTimestamp {
			t.Errorf("width %d: row of %d cells with timestamp=%v — a timestamp is shown exactly when the row fills the width: %q",
				width, got, hasTimestamp, plain)
		}
		if hasTimestamp && !strings.HasSuffix(plain, pinnedTime.Format(timeStampLayout)) {
			t.Errorf("width %d: the timestamp must end the row (right-aligned): %q", width, plain)
		}
	}
}

// TestWindowLineTimestampYieldsToTheLabel pins the priority order: the
// label identifies the window, the timestamp is metadata, so on a narrow
// terminal the timestamp is dropped rather than the label being squeezed —
// and a long tool header keeps its name.
//
// The two widths are derived from the row's geometry rather than written
// down, so this test keeps meaning what it says when the timestamp's width
// changes: marker + label + one gap cell + the timestamp column, one cell
// short of which the timestamp must be gone and at which it must be there.
func TestWindowLineTimestampYieldsToTheLabel(t *testing.T) {
	styles := DefaultStyles()
	// The tool label is "TOOL CALL ⠋" padded to the label column, then the
	// name.
	label := padLabel(toolLabelWithIndicator("✓")) + "execute_command"
	fits := collapsedPrefixWidth + cellWidth(label) + 1 + timeStampWidth

	open := func(width int) string {
		wb := NewWindowBuffer(width, styles)
		wb.HandleToolInputEvent(protocol.ToolInputData{
			ID:    "t1",
			Name:  "execute_command",
			Input: []byte("execute_command: lscpu"),
		}, 0)
		pinCreatedAt(wb)
		wb.ToggleFold(0) // tool windows start folded; open it
		return stripANSI(wb.GetAll(-1, false))
	}

	plain := open(fits - 1)
	if !strings.HasPrefix(plain, unfoldArrow+" TOOL CALL") {
		t.Errorf("the label must survive: %q", firstRow(plain))
	}
	if !strings.Contains(firstRow(plain), "execute_command") {
		t.Errorf("the tool name must survive even with no room for the timestamp: %q", firstRow(plain))
	}
	if strings.Contains(firstRow(plain), pinnedTime.Format(timeStampLayout)) {
		t.Errorf("a timestamp must not be squeezed in beside a full tool header: %q", firstRow(plain))
	}

	// One cell wider and it fits.
	plain = open(fits)
	if !strings.HasSuffix(firstRow(plain), pinnedTime.Format(timeStampLayout)) {
		t.Errorf("at %d cells the timestamp fits and must be shown: %q", fits, firstRow(plain))
	}
	if got := cellWidth(firstRow(plain)); got != fits {
		t.Errorf("row with a timestamp = %d cells, want %d (right-aligned to the edge): %q",
			got, fits, firstRow(plain))
	}
}

// TestWindowLineWithoutATime: a window whose arrival time is unknown renders
// the marker and the label and nothing else — no reserved column, no
// placeholder, no zero date.
func TestWindowLineWithoutATime(t *testing.T) {
	styles := DefaultStyles()
	wb := NewWindowBuffer(80, styles)
	wb.AppendOrUpdate(tlv.TagAssistantT, "a1", "hello")
	wb.WindowAt(0).CreatedAt = time.Time{} // unknown (a Window built outside the adapter)

	plain := stripANSI(wb.GetAll(-1, false))
	if firstRow(plain) != unfoldArrow+" ASSISTANT" {
		t.Errorf("row 0 = %q, want the marker and the label with nothing after them", firstRow(plain))
	}
}

// TestWindowLineToolHeaderIsTheCollapsedHeader: an expanded tool window
// reuses the collapsed line's header layout — label, status indicator, then
// the tool name — so the same window reads the same way open or closed.
func TestWindowLineToolHeaderIsTheCollapsedHeader(t *testing.T) {
	styles := DefaultStyles()
	wb := NewWindowBuffer(80, styles)
	wb.HandleToolInputEvent(protocol.ToolInputData{
		ID:    "t1",
		Name:  "execute_command",
		Input: []byte("execute_command: lscpu"),
	}, 0)
	wb.HandleToolOutput("t1", "x86_64", false, 0)
	wb.ToggleFold(0)

	plain := stripANSI(wb.GetAll(-1, false))
	if !strings.HasPrefix(plain, unfoldArrow+" TOOL CALL ✓") {
		t.Errorf("expanded tool line should carry 'TOOL CALL ✓', got %q", firstRow(plain))
	}
	if !strings.Contains(wb.GetAll(-1, false), styles.ToolContent.Bold(true).Render("execute_command")) {
		t.Error("the tool name should be bold + muted, as in the collapsed line")
	}
}

// TestWindowLineColorIsTheDefaultExceptErrors pins the rule the line colors
// follow: EVERY window type is painted in the default label style — a user
// turn, a reasoning step, an answer and a tool call alike, because they are
// all conversation — and the error color is the one exception, because an
// error has to be recognizable at a glance. Whatever the type, the whole
// line takes that one style, marker to timestamp.
func TestWindowLineColorIsTheDefaultExceptErrors(t *testing.T) {
	defaultLine := func(s *Styles) Style { return s.Label.Bold(true) }
	errorLine := func(s *Styles) Style { return s.Error.Bold(true) }

	cases := []struct {
		name  string
		tag   string
		label string
		style func(*Styles) Style
	}{
		{"user prompt", tlv.TagUserT, "USER PROMPT", defaultLine},
		{"reasoning", tlv.TagAssistantR, "REASONING", defaultLine},
		{"assistant", tlv.TagAssistantT, "ASSISTANT", defaultLine},
		{"tool call", tlv.TagAssistantF, "TOOL CALL", defaultLine},
		{"system notify", TagWindowSN, "SYSTEM NOTIFY", defaultLine},
		{"system error", TagWindowSE, "SYSTEM ERROR", errorLine},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			styles := DefaultStyles()
			wb := NewWindowBuffer(80, styles)
			wb.AppendOrUpdate(tc.tag, "w1", "hello")
			pinCreatedAt(wb)
			if wb.WindowAt(0).Folded { // conversation starts expanded; scaffolding does not
				wb.ToggleFold(0)
			}
			if wb.WindowAt(0).Folded {
				t.Fatal("fixture: the window should be open")
			}

			row := firstRow(wb.GetAll(-1, false))
			if !strings.Contains(stripANSI(row), tc.label) {
				t.Fatalf("row = %q, want it to carry %q", stripANSI(row), tc.label)
			}
			st := tc.style(styles)
			for _, glyph := range []string{unfoldArrow, tc.label, pinnedTime.Format(timeStampLayout)} {
				if !strings.Contains(row, st.Render(glyph)) {
					t.Errorf("%q should be painted in the line's style: %q", glyph, row)
				}
			}
		})
	}
}
