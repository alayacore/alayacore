package terminal

import (
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/tlv"
)

// A window's rows and their '\n'-joined projection are separate work, and the
// viewport path only wants the rows: WindowBuffer.windowFragment clips
// cache.lines to what is on screen and WindowBuffer.ensureLineHeights reads
// only LineCount. Render is buildLines plus the join, so a caller that wants
// rows asks for rows.
//
// These hold that split from both sides: a frame that never asks for the
// string must not build it, and a frame that does must get exactly the rows
// joined — in either register, and never a projection outliving the rows it
// was built from.

// longBody is content an expanded window wraps into many rows: enough that
// joining them is the frame's largest single allocation, and more rows than
// any viewport below shows.
const longBody = "a line of assistant text that wraps at eighty columns\n"

// TestViewportFrameNeverJoinsTheWindow is the reason for the split. Before it,
// every rebuild joined the window's whole rendered text and the viewport path
// threw the string away — once per window per frame.
func TestViewportFrameNeverJoinsTheWindow(t *testing.T) {
	wb := NewWindowBuffer(80, DefaultStyles())
	wb.AppendOrUpdate(tlv.TagAssistantT, "msg", strings.Repeat(longBody, 200))
	w := wb.WindowAt(0)
	if w.Folded {
		t.Fatal("an assistant text window starts expanded; the test needs many rows")
	}

	wb.SetViewportPosition(10, 20)
	frame := wb.GetAll(0, false)

	if frame == "" {
		t.Fatal("the viewport frame is empty")
	}
	if len(w.cache.lines) < 100 {
		t.Fatalf("expected the window's rows built, got %d", len(w.cache.lines))
	}
	if w.cache.joinedDone {
		t.Errorf("a viewport frame joined the window's %d rows into %d bytes nobody read",
			len(w.cache.lines), len(w.cache.joined))
	}
}

// TestResizeNeverJoinsTheWindow is the same for a full rebuild, which is the
// expensive case: a resize renders EVERY window, including the ones far off
// screen that no viewport will ever clip (BenchmarkWindowBufferResize pays
// that for 50 windows at a time).
func TestResizeNeverJoinsTheWindow(t *testing.T) {
	wb := NewWindowBuffer(80, DefaultStyles())
	for _, id := range []string{"m1", "m2", "m3", "m4", "m5"} {
		wb.AppendOrUpdate(tlv.TagAssistantT, id, strings.Repeat(longBody, 50))
	}
	if len(wb.windows) != 5 {
		t.Fatalf("expected 5 windows, got %d", len(wb.windows))
	}

	wb.WithWidth(100) // invalidates every window and asks for a full rebuild
	wb.ensureLineHeights(false)

	for i := range wb.windows {
		w := wb.WindowAt(i)
		if w.cache.lineCount == 0 {
			t.Errorf("window %d: counting lines produced no count", i)
		}
		if w.cache.joinedDone {
			t.Errorf("window %d: counting lines joined %d bytes of rendered text", i, len(w.cache.joined))
		}
	}
}

// TestRenderReturnsTheRowsJoined pins the other half: making the join lazy
// must not change what Render returns. Non-cursor is the rows joined the way
// they draw; cursor is that same string with row 0 replaced by its
// highlighted form, and row 0 only.
func TestRenderReturnsTheRowsJoined(t *testing.T) {
	wb := NewWindowBuffer(80, DefaultStyles())
	wb.AppendOrUpdate(tlv.TagAssistantT, "msg", "first line\nsecond line\nthird line")
	w := wb.WindowAt(0)
	styles := DefaultStyles()

	plain := w.Render(80, false, styles, false)
	if want := joinVisualLines(w.cache.lines); plain != want {
		t.Errorf("Render =\n%q\nwant the rows joined =\n%q", plain, want)
	}
	i := strings.IndexByte(plain, '\n')
	if i < 0 {
		t.Fatalf("expected the window's own row plus content rows, got one row: %q", plain)
	}

	cursor := w.Render(80, true, styles, false)
	if want := replaceFirstLine(plain, w.cursorLine0()); cursor != want {
		t.Errorf("cursor Render =\n%q\nwant row 0 swapped =\n%q", cursor, want)
	}
	if plain == cursor {
		t.Error("the cursor register left row 0 unchanged")
	}
	if plain[i:] != cursor[strings.IndexByte(cursor, '\n'):] {
		t.Errorf("the cursor register changed more than row 0:\n plain tail %q\n cursor tail %q",
			plain[i:], cursor[strings.IndexByte(cursor, '\n'):])
	}
	// The swap is memoized per register, so asking again is stable.
	if again := w.Render(80, true, styles, false); again != cursor {
		t.Errorf("a second cursor Render differs:\n first  %q\n second %q", cursor, again)
	}
}

// TestJoinIsNotStaleAfterTheRowsChange is the risk laziness introduces: a
// cached projection outliving the rows it was built from. buildLines clears
// the join with every other row-derived memo, so a rebuilt window joins its
// new rows — and the old string is dropped, not held for the window's life.
func TestJoinIsNotStaleAfterTheRowsChange(t *testing.T) {
	wb := NewWindowBuffer(80, DefaultStyles())
	wb.AppendOrUpdate(tlv.TagAssistantT, "msg", "hello")
	w := wb.WindowAt(0)
	styles := DefaultStyles()

	before := w.Render(80, false, styles, false)
	if !w.cache.joinedDone {
		t.Fatal("Render did not build the join it returned")
	}

	w.AppendContent(" world")
	after := w.Render(80, false, styles, false)

	if after == before {
		t.Error("the join survived an invalidation — Render returned the stale string")
	}
	if got := stripANSI(after); !strings.Contains(got, "hello world") {
		t.Errorf("the rebuilt window lost the appended text: %q", got)
	}
	if want := joinVisualLines(w.cache.lines); after != want {
		t.Errorf("Render =\n%q\nwant the NEW rows joined =\n%q", after, want)
	}
}

// TestFoldedRenderReturnsItsRow covers the one-row case: a folded window is a
// single row, and its Render returns that row rather than a copy of it
// (joinVisualLines has a fast path for exactly this).
func TestFoldedRenderReturnsItsRow(t *testing.T) {
	wb := NewWindowBuffer(80, DefaultStyles())
	wb.AppendOrUpdate(tlv.TagAssistantT, "msg", "some content that would be summarized")
	w := wb.WindowAt(0)
	w.Folded = true

	got := w.Render(80, false, DefaultStyles(), false)

	if len(w.cache.lines) != 1 {
		t.Fatalf("a folded window is one row, got %d", len(w.cache.lines))
	}
	if got != w.cache.lines[0].Text {
		t.Errorf("Render = %q, want the row itself = %q", got, w.cache.lines[0].Text)
	}
	if !strings.Contains(stripANSI(got), "some content") {
		t.Errorf("the folded row lost its summary: %q", stripANSI(got))
	}
}

// TestWindowWithoutRendererDrawsNothing covers the guard both entry points
// carry. A Window built outside NewWindow has no renderer, and neither Render
// nor buildLines may dereference it — Render's cursor branch especially, since
// cursorLine0 reads the frame styles that only a render fills.
func TestWindowWithoutRendererDrawsNothing(t *testing.T) {
	styles := DefaultStyles()
	w := &Window{styles: styles}

	if got := w.Render(80, false, styles, false); got != "" {
		t.Errorf("Render with no renderer = %q, want empty", got)
	}
	if got := w.Render(80, true, styles, false); got != "" {
		t.Errorf("cursor Render with no renderer = %q, want empty", got)
	}

	w.buildLines(80, styles, false) // must not panic
	if w.cache.valid {
		t.Error("buildLines with no renderer marked the cache valid")
	}
	if got := w.joined(); got != "" {
		t.Errorf("joined with no rows = %q, want empty", got)
	}
}
