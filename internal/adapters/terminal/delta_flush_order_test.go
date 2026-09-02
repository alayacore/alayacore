package terminal

import (
	"testing"

	"github.com/alayacore/alayacore/internal/theme"
	"github.com/alayacore/alayacore/internal/tlv"
)

// Regression test for a REASONING window appearing below the ASSISTANT window
// it preceded, while the session file recorded them in the correct order.
//
// A window's position is fixed at creation (WindowBuffer only appends), and
// streaming deltas are coalesced into pendingTextDeltas keyed by historyID, so
// the flush that first materializes both windows decides their order. That
// flush iterated a Go map — randomized per run — making the outcome a coin
// flip whenever a turn's reasoning and text are still pending together, which
// is the normal case for a short reasoning. flushPendingDeltas now sorts by
// historyID.
//
// Both frame sequences are covered: the one providers/openai.go emits today
// (reasoning complete first) and the one it emitted before its complete-event
// reorder. The flush must place windows by historyID either way, so a provider
// that finishes its blocks out of order cannot scramble the display.
//
// The randomized map order is per-iteration, so a single pass would not detect
// a regression; iterate enough times that it fails with overwhelming
// probability.
func TestFlushPendingDeltasCreatesWindowsInHistoryIDOrder(t *testing.T) {
	const iterations = 200

	arDelta := encodeTestTLV(tlv.TagAssistantRDelta, tlv.WrapID("1", "The user said hello?"))
	atDelta := encodeTestTLV(tlv.TagAssistantTDelta, tlv.WrapID("2", "Hello! How can I help?"))
	arFrame := encodeTestTLV(tlv.TagAssistantR, tlv.WrapID("1", ""))
	atFrame := encodeTestTLV(tlv.TagAssistantT, tlv.WrapID("2", ""))

	tests := []struct {
		name   string
		frames [][]byte
	}{
		{"reasoning complete frame first", [][]byte{arDelta, atDelta, arFrame, atFrame}},
		{"text complete frame first", [][]byte{arDelta, atDelta, atFrame, arFrame}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for i := range iterations {
				out := NewTerminalOutput(NewStyles(theme.DefaultTheme()))
				out.SetWindowWidth(80)

				var buf []byte
				for _, f := range tt.frames {
					buf = append(buf, f...)
				}
				if _, err := out.Write(buf); err != nil {
					t.Fatalf("iteration %d: Write failed: %v", i, err)
				}

				wb := out.WindowBuffer()
				if wb.WindowCount() != 2 {
					t.Fatalf("iteration %d: expected 2 windows, got %d", i, wb.WindowCount())
				}
				if got := wb.WindowAt(0).Tag(); got != tlv.TagAssistantR {
					t.Fatalf("iteration %d: window 0 must be reasoning (AR), got %s — "+
						"flushPendingDeltas is not ordering by historyID", i, got)
				}
				if got := wb.WindowAt(1).Tag(); got != tlv.TagAssistantT {
					t.Fatalf("iteration %d: window 1 must be assistant text (AT), got %s", i, got)
				}
			}
		})
	}
}
