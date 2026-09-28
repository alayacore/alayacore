package terminal

// Benchmarks for the folded-window redesign.
//
// These simulate a realistic long agent session like the one in the
// user's screenshot: the majority of windows are folded tool calls
// (AF) and reasoning (AR) collapsed to a single header line, with a
// handful of unfolded user (UT) / assistant (AT) messages.

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/protocol"
	"github.com/alayacore/alayacore/internal/theme"
	"github.com/alayacore/alayacore/internal/tlv"
)

// makeFoldedSession builds a WindowBuffer resembling a long agent session:
//   - 80 folded tool windows (AF) with realistic input + output
//   - 20 folded reasoning windows (AR)
//   - 10 unfolded assistant windows (AT)
//   - 10 unfolded user windows (UT)
//
// Total 120 windows: 100 folded and 20 unfolded, which is what the fold
// defaults in WindowBuffer.AppendOrUpdate produce for these tags (AT and UT
// open expanded, everything else collapsed). Width 120 like a wide terminal.
func makeFoldedSession() *WindowBuffer {
	styles := NewStyles(theme.DefaultTheme())
	wb := NewWindowBuffer(120, styles)

	// Folded tool windows: input line + long-ish output (command results).
	for i := 0; i < 80; i++ {
		id := fmt.Sprintf("call-%03d", i)
		input := fmt.Sprintf("execute_command: grep -rn \"fontWeight\\|font-weight\" src-elm/src src-elm/*.js 2>/dev/null | grep -v style.css | head -20 (step %d)", i)
		wb.HandleToolInputEvent(protocol.ToolInputData{
			ID:    id,
			Name:  "execute_command",
			Input: json.RawMessage(input),
		}, uint64(i))
		// Command output — a few lines.
		output := strings.Repeat("  some output line from command execution\n", 4)
		wb.HandleToolOutput(id, output, false, uint64(i))
	}

	// Folded reasoning windows.
	for i := 0; i < 20; i++ {
		id := fmt.Sprintf("reason-%03d", i)
		content := strings.Repeat("reasoning about the task and planning the next steps\n", 3)
		wb.AppendOrUpdate(tlv.TagAssistantR, id, content)
	}

	// Unfolded assistant messages.
	for i := 0; i < 10; i++ {
		id := fmt.Sprintf("assist-%03d", i)
		content := strings.Repeat("All edits are in place. Let me do a final sanity check — brace balance, light-mode conflicts.\n", 5)
		wb.AppendOrUpdate(tlv.TagAssistantT, id, content)
	}

	// Unfolded user messages.
	for i := 0; i < 10; i++ {
		id := fmt.Sprintf("user-%03d", i)
		wb.AppendOrUpdate(tlv.TagUserT, id, fmt.Sprintf("Please fix issue #%d in the style sheet", i))
	}

	return wb
}

// BenchmarkFoldedSessionGetAll renders the whole visible viewport of a
// folded-heavy session. This is what runs on every display refresh.
func BenchmarkFoldedSessionGetAll(b *testing.B) {
	wb := makeFoldedSession()
	wb.SetViewportPosition(0, 40)
	_ = wb.GetTotalLines()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = wb.GetAll(-1, false)
	}
}

// BenchmarkFoldedSessionCursorMovement moves the window cursor through the
// session (20 moves per iteration), like pressing j/k repeatedly. Includes
// the EnsureCursorVisible + updateContent path.
func BenchmarkFoldedSessionCursorMovement(b *testing.B) {
	wb := makeFoldedSession()
	wb.SetViewportPosition(0, 40)
	_ = wb.GetTotalLines()

	dm := NewDisplayModel(wb, NewStyles(theme.DefaultTheme()))
	dm = dm.WithHeight(40)
	dm = dm.WithDisplayFocused(true)
	dm = dm.updateContent()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for j := 0; j < 20; j++ {
			dm = dm.WithWindowCursor(j)
			dm = dm.EnsureCursorVisible()
			dm = dm.updateContent()
		}
	}
}

// BenchmarkFoldedToolStreamingDelta simulates the streaming hot path:
// a tool call is running, its window is folded, and Uf preview snapshots
// keep arriving every tick. Each arrival marks the window dirty and
// triggers line-height recomputation.
func BenchmarkFoldedToolStreamingDelta(b *testing.B) {
	wb := makeFoldedSession()
	wb.SetViewportPosition(0, 40)
	_ = wb.GetTotalLines()

	// The currently-streaming tool window.
	wb.HandleToolInputEvent(protocol.ToolInputData{
		ID:   "call-live",
		Name: "write_file",
	}, 0)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		wb.HandleToolOutputDelta("call-live", fmt.Sprintf(" 42%% [████████░░░░░░░░] chunk %d", i), 0)
		_ = wb.GetTotalLines()
	}
}

// BenchmarkFoldedTextStreamingDelta is the text-window counterpart of
// BenchmarkFoldedToolStreamingDelta, and it deliberately measures more: the
// whole frame (delta + line tracking + viewport render), because for a folded
// TEXT window the frame is where the cost is.
//
// The two fold summaries are built differently. A folded tool window shows the
// first line of its input, so an output delta does not re-read the output at
// all — that is the 100ns the benchmark above measures. A folded text window
// shows head + "…" + tail of its whole content (textRenderer.BuildCollapsed →
// collapsedSummary → headAndTailParts), so deriving that row means reading the
// message: one walk to find the head, one to find the tail.
//
// It used to mean a great deal more than reading it. Both cutters answered from
// a helper that materialized every grapheme cluster of the content — a struct
// and a substring per cluster, twice per frame — so this frame cost 99μs and
// 503 KB at 2 KB of content and 13.5ms and 56.8 MB at 128 KB: 12x to 81x more
// than the same content EXPANDED, which is the inversion this benchmark exists
// to make visible. Two materializations came out — width.go's cluster list, and
// the whole-message escape copy the summary used to cut 75 cells out of — and
// the numbers are 15.1μs / 5.1 KB and 770μs / 134 KB, lighter than the expanded
// side at every size. What is left is honest: the line COUNT of a folded window
// is O(1) (UpdateLineCountFast returns 1 without touching the renderer) and the
// ROW is one pass over the content, O(content) in time and O(cut) in memory.
//
// Reasoning windows fold by default and stream for as long as the model thinks,
// which puts this on the per-tick path of a long "thinking" phase — so the
// slope across the three sizes is the point, not any one number. See
// docs/internal/virtual-rendering-performance.md → "The fold summary
// materialized every cluster (found and fixed)".
//
// Each size runs twice, folded and expanded, on identical content: the
// expanded side is the control. Folding a window is supposed to be the cheap
// state, and for a tool window it is; for a text window the two sides are the
// wrong way round, and the pair is what shows that without anyone having to
// trust a prose figure.
//
// The buffer is rebuilt per iteration under StopTimer, so one iteration is one
// frame against a content size that does not drift as the run goes on: unlike
// the benchmarks above, these memory and allocation columns mean the same thing
// at -benchtime 100x and at 1s.
func BenchmarkFoldedTextStreamingDelta(b *testing.B) {
	styles := NewStyles(theme.DefaultTheme())
	line := "reasoning about the task and planning the next steps\n"
	sizes := []struct {
		name string
		kb   int
	}{{"2KB", 2}, {"32KB", 32}, {"128KB", 128}}
	states := []struct {
		name   string
		folded bool
	}{{"folded", true}, {"expanded", false}}

	for _, size := range sizes {
		content := strings.Repeat(line, size.kb*1000/len(line))
		for _, state := range states {
			b.Run(size.name+"/"+state.name, func(b *testing.B) {
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					b.StopTimer()
					wb := NewWindowBuffer(120, styles)
					wb.AppendOrUpdate(tlv.TagAssistantR, "reason-live", content)
					wb.WindowAt(0).Folded = state.folded
					wb.SetViewportPosition(0, 40)
					_ = wb.GetTotalLines()
					_ = wb.GetAll(-1, false)
					b.StartTimer()

					wb.AppendOrUpdate(tlv.TagAssistantR, "reason-live", " more")
					_ = wb.GetTotalLines()
					_ = wb.GetAll(-1, false)
				}
			})
		}
	}
}
