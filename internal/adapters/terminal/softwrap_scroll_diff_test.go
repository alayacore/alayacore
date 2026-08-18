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
	const W, H = 40, 6
	content := "The simple test passes. The user's bug might require a more specific scenario. Let me make the test more aggressive and add diagnostic output:" // 142 chars, wraps to 4 rows at W=40

	wb := NewWindowBuffer(W, DefaultStyles())
	wb.AppendOrUpdate(tlv.TagAssistantT, "w1", content)
	wb.AppendOrUpdate(tlv.TagAssistantR, "ar-1", "short reasoning")
	// w1 = header + top rule + 4 content rows + bottom rule = 7 visual
	// lines; ar = 1 folded line. Total 8 lines.

	dm := NewDisplayModel(wb, DefaultStyles()).WithHeight(H).updateContent()
	// Auto-follow: viewport [2,8) — the content tail + bottom rule + AR.
	if got := dm.YOffset(); got != 2 {
		t.Fatalf("YOffset = %d, want 2 (8 lines - 6 viewport)", got)
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
	// Frame 1 (viewport [2,8)): rows 0-3 = content, row 4 = bottom rule,
	// row 5 = folded reasoning.
	for i := 0; i < 4; i++ {
		want := strings.TrimRight(content[i*40:min((i+1)*40, len(content))], " ")
		if got := lineAt(grid, i); got != want {
			t.Fatalf("frame1 row %d = %q, want %q", i, got, want)
		}
	}
	if !strings.Contains(lineAt(grid, 4), "─") || !strings.Contains(lineAt(grid, 5), "REASONING") {
		t.Fatalf("frame1 rows 4-5 wrong: %q / %q", lineAt(grid, 4), lineAt(grid, 5))
	}

	// Scroll up one line: viewport [1,7) — top rule + 4 content rows +
	// bottom rule (the AR line scrolls off).
	dm = dm.MarkUserScrolled().ScrollUp(1).updateContent()
	if got := dm.YOffset(); got != 1 {
		t.Fatalf("YOffset after scroll = %d, want 1", got)
	}
	render()

	// The scrolled frame must render exactly:
	//   row 0: top rule
	//   rows 1-4: the four content rows
	//   row 5: bottom rule
	if !strings.Contains(lineAt(grid, 0), "─") {
		t.Errorf("row 0 = %q, want the top rule", lineAt(grid, 0))
	}
	// Join the raw rows (keeping the wrap-boundary spaces) and trim the
	// padding — the full original content must survive intact.
	joined := strings.TrimRight(string(grid[1])+string(grid[2])+string(grid[3])+string(grid[4]), " ")
	if joined != content {
		t.Errorf("scrolled content truncated/misaligned:\n  got:  %q\n  want: %q", joined, content)
	}
	if !strings.Contains(lineAt(grid, 5), "─") {
		t.Errorf("row 5 = %q, want the bottom rule", lineAt(grid, 5))
	}
	// The AR line scrolled off and must not survive anywhere.
	for i := 0; i < H; i++ {
		if strings.Contains(lineAt(grid, i), "REASONING") {
			t.Errorf("row %d trails scrolled-off content: %q", i, lineAt(grid, i))
		}
	}
}
