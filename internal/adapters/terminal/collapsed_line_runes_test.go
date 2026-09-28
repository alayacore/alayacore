package terminal

// A collapsed window line is assembled by slicing a plain line at byte offsets
// derived from the pieces it was built from; a truncation between building and
// slicing moves every piece after the cut (runeBoundary is the fix, and its doc
// is the account). This file sweeps the widths and names where that happens — a
// folded tool window on a 32-column pane, which is an ordinary tmux split,
// holding "execute_command", used to emit E2 80, an SGR sequence, then A6.
//
// The invariant is stated about bytes rather than appearance because appearance
// is what hid it: the styled line looks plausible in a diff, and only
// utf8.ValidString says it cannot be drawn.

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/alayacore/alayacore/internal/tlv"
)

// TestCollapsedToolLineKeepsRunesWhole sweeps the widths where the row's
// budget runs out inside the tool name, inside the ellipsis, and inside the
// input, for names of every length around the label column. Every line the
// renderer returns must be whole UTF-8 and must fit the budget it was given.
func TestCollapsedToolLineKeepsRunesWhole(t *testing.T) {
	names := []string{
		"ls", "edit_file", "read_file", "execute_command",
		"search_content", "a_really_quite_absurdly_long_tool_name",
	}
	inputs := []string{
		"",
		"ls -la",
		"cd /home/wallace/playground/alayacore && go test ./internal/adapters/terminal/",
		"中文参数 与 English mixed 内容",
		strings.Repeat("x", 200),
	}
	styles := DefaultStyles()

	for _, name := range names {
		for _, input := range inputs {
			for width := 4; width <= 80; width++ {
				tr := &toolRenderer{name: name, input: input, status: ToolStatusSuccess}
				line, count := tr.BuildCollapsed(width, styles)
				if count != 1 {
					t.Fatalf("name=%q width=%d: lineCount = %d, want 1", name, width, count)
				}
				plain := stripANSI(line)
				if !utf8.ValidString(line) {
					t.Errorf("name=%q input=%.12q width=%d: the styled line is not valid UTF-8: %q",
						name, input, width, line)
				}
				if !utf8.ValidString(plain) {
					t.Errorf("name=%q input=%.12q width=%d: the line is not valid UTF-8: %q",
						name, input, width, plain)
				}
				// The collapsed line is one row of a window, which the
				// viewport prefixes with the fold marker and a space.
				if budget := max(0, width-collapsedPrefixWidth); cellWidth(line) > budget {
					t.Errorf("name=%q input=%.12q width=%d: the row is %d cells for a %d-cell budget: %q",
						name, input, width, cellWidth(line), budget, line)
				}
			}
		}
	}
}

// TestCollapsedTextLineKeepsRunesWhole is the same sweep for the text
// windows, whose summary carries a head, an ellipsis and a tail, and whose
// ellipsis offset is a byte offset into a line a later truncation can
// shorten.
func TestCollapsedTextLineKeepsRunesWhole(t *testing.T) {
	contents := []string{
		"short",
		strings.Repeat("中文测试内容", 20),
		"ascii head " + strings.Repeat("tail words ", 30),
		"mixed 中文 head and a very long tail " + strings.Repeat("…", 40),
	}
	styles := DefaultStyles()
	for _, content := range contents {
		for width := 4; width <= 80; width++ {
			tr := &textRenderer{tag: tlv.TagAssistantT, content: content}
			line, _ := tr.BuildCollapsed(width, styles)
			if !utf8.ValidString(line) {
				t.Errorf("width=%d content=%.16q: the line is not valid UTF-8: %q", width, content, line)
			}
			if budget := max(0, width-collapsedPrefixWidth); cellWidth(line) > budget {
				t.Errorf("width=%d content=%.16q: the row is %d cells for a %d-cell budget: %q",
					width, content, cellWidth(line), budget, line)
			}
		}
	}
}

// TestNarrowPaneFrameIsWholeUTF8 is the report end to end: a session's worth
// of folded tool windows on a pane narrow enough to truncate their names must
// produce a frame that is whole UTF-8 and fills exactly the screen.
func TestNarrowPaneFrameIsWholeUTF8(t *testing.T) {
	for _, width := range []int{20, 24, 28, 32, 36, 40} {
		t.Run(fmt.Sprintf("%d columns", width), func(t *testing.T) {
			h := newFrameHarness(t, width, 24)
			h.step("empty")
			for i, name := range []string{"ls", "execute_command", "search_content", "edit_file"} {
				h.m.out.Write(toolCallFrames(fmt.Sprintf("t%d", i), name,
					"cd /home/wallace/playground/alayacore && go test ./internal/adapters/terminal/ -run Test -v",
					"ok  \tgithub.com/alayacore/alayacore/internal/adapters/terminal\t0.02s"))
				h.m.out.FlushPendingDeltas()
				h.step("tool " + name)
				v := h.m.View()
				if !utf8.ValidString(v.Content) {
					t.Fatalf("the frame is not valid UTF-8:\n%q", v.Content)
				}
			}
			for i := 0; i < 5; i++ {
				h.key('k', 0)
			}
			for i := 0; i < 5; i++ {
				h.key('j', 0)
			}
		})
	}
}
