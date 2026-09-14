package terminal

// Regression test for the scroll complaint: scrolling a viewport whose
// fragments soft-wrap (rows wider than the terminal) must not truncate
// content or shift the rows below ("The simple test passes … add
// diagnostic ou" instead of "…diagnostic output:", rows below misaligned).
//
// The row diff between consecutive frames positions changed rows at their
// soft-wrapped TERMINAL rows (the accumulated wrap count). With the
// newline index, a wrapped logical row would push every changed row below
// it onto the wrong screen row: the row diff overwrites the middle of the
// wrapped content with the next window's rule and leaves stale rows below.

import (
	"bytes"
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/tlv"
)

// lineAt returns the textual content of grid row i (trimmed of trailing
// whitespace). Empty if i is out of range.
func lineAt(grid [][]rune, i int) string {
	if i < 0 || i >= len(grid) {
		return ""
	}
	return strings.TrimRight(string(grid[i]), " ")
}

// TestScrollDiffSoftWrapAlignment scrolls a display whose window content
// wraps to several terminal rows and asserts the frame diff renders the
// exact intended rows: no truncated content, no residue, no shift.
func TestScrollDiffSoftWrapAlignment(t *testing.T) {
	const W, H = 40, 5
	content := "The simple test passes. The user's bug might require a more specific scenario. Let me make the test more aggressive and add diagnostic output:" // 142 chars, wraps to 4 rows at W=40

	wb := NewWindowBuffer(W, DefaultStyles())
	wb.AppendOrUpdate(tlv.TagAssistantT, "w1", content)
	wb.AppendOrUpdate(tlv.TagAssistantR, "ar-1", "short reasoning")
	// w1 = labeled opening rule + 4 content rows = 5 visual lines;
	// ar = 1 folded line. Total 6 lines, one more than the viewport.

	dm := NewDisplayModel(wb, DefaultStyles()).WithHeight(H).updateContent()
	// Auto-follow: viewport [1,6) — the four content rows + AR.
	if got := dm.YOffset(); got != 1 {
		t.Fatalf("YOffset = %d, want 1 (6 lines - 5 viewport)", got)
	}

	var buf bytes.Buffer
	s := &Screen{out: &buf}
	s.Resize(W, H) // row diff needs the terminal width (production sets it)

	var grid [][]rune
	render := func() {
		v := dm.View()
		v.FullScreen = true
		if err := s.Render(v.Content, nil, v.FullScreen); err != nil {
			t.Fatalf("Render: %v", err)
		}
		grid = applyFrame(grid, buf.String(), W)
		buf.Reset()
	}

	render()
	// Frame 1 (viewport [1,6)): the window's own line is cut off, so it is
	// PINNED at row 0 and the body row that would have been there (content
	// row 0) is displaced. Rows 1-3 are therefore content rows 1-3, each
	// wrapping across the width, and row 4 is the folded reasoning window.
	if !strings.HasPrefix(lineAt(grid, 0), "- ASSISTANT") {
		t.Fatalf("frame1 row 0 = %q, want the pinned window line", lineAt(grid, 0))
	}
	for i := 1; i < 4; i++ {
		want := strings.TrimRight(content[i*40:min((i+1)*40, len(content))], " ")
		if got := lineAt(grid, i); got != want {
			t.Fatalf("frame1 row %d = %q, want %q (content row %d)", i, got, want, i)
		}
	}
	if !strings.Contains(lineAt(grid, 4), "REASONING") {
		t.Fatalf("frame1 row 4 = %q, want the folded reasoning window", lineAt(grid, 4))
	}

	// Scroll up one line: viewport [0,5) — the window's own line + the four
	// content rows (the AR line scrolls off).
	dm = dm.MarkUserScrolled().ScrollUp(1).updateContent()
	if got := dm.YOffset(); got != 0 {
		t.Fatalf("YOffset after scroll = %d, want 0", got)
	}
	render()

	// The scrolled frame must render exactly:
	//   row 0: the window's own line (marker + label + timestamp)
	//   rows 1-4: the four content rows
	if !strings.HasPrefix(lineAt(grid, 0), "- ASSISTANT") {
		t.Errorf("row 0 = %q, want the window's own line (marker + label)", lineAt(grid, 0))
	}
	// Join the raw rows (keeping the wrap-boundary spaces) and trim the
	// padding — the full original content must survive intact.
	joined := strings.TrimRight(string(grid[1])+string(grid[2])+string(grid[3])+string(grid[4]), " ")
	if joined != content {
		t.Errorf("scrolled content truncated/misaligned:\n  got:  %q\n  want: %q", joined, content)
	}
	// The AR line scrolled off and must not survive anywhere.
	for i := 0; i < H; i++ {
		if strings.Contains(lineAt(grid, i), "REASONING") {
			t.Errorf("row %d trails scrolled-off content: %q", i, lineAt(grid, i))
		}
	}
}
