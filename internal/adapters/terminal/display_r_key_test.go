package terminal

import (
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/theme"
	"github.com/alayacore/alayacore/internal/tlv"
)

// TestPressRTogglesMarkdownInViewport reproduces the bug where pressing
// 'r' to toggle markdown rendering on an unfolded assistant-text window
// did NOT update the visible viewport: the user had to fall back to
// Ctrl+R (handleRedraw) to see the change.
//
// Root cause: the display.go case keyR handler called
// EnsureCursorVisible().updateContent() — EnsureCursorVisible calls
// GetWindowLineRange → ensureLineHeights, which sets wb.dirty=false at
// the end as a side effect. By the time updateContent ran, IsDirty()
// returned false and updateContent early-returned without rebuilding
// scroll content. Fix: reorder to updateContent().EnsureCursorVisible().
func TestPressRTogglesMarkdownInViewport(t *testing.T) {
	styles := NewStyles(theme.DefaultTheme())
	wb := NewWindowBuffer(80, styles)
	display := NewDisplayModel(wb, styles)

	table := "| name | gender | age |\n|---|---|---|\n| Walllace | male | 100 |\n| Harry | male | 10 |\n"
	wb.AppendOrUpdate(tlv.TagAssistantT, "win-1", table)

	// By default new windows are unfolded (Folded=false). Put the cursor
	// on it and focus the display so 'r' is routed to the display model
	// and reaches the toggle branch (Folded==false → toggle is allowed).
	display = display.WithDisplayFocused(true)
	display = display.WithWindowCursor(0)

	// Prime the scroll content (mimics first paint).
	display = display.WithHeight(20)
	display, _ = display.Update(KeyPressMsg(Key{Text: "G"})) // goto end + autoscroll
	display = display.updateContent()

	before := display.scrollView.content
	if !strings.Contains(before, "│ name     │ gender │ age │") {
		t.Fatalf("priming failed: expected the rendered grid header in 'before', got:\n%s", stripANSI(before))
	}

	// Press 'r' — should toggle markdown OFF (default is ON for AT) and
	// rebuild the viewport scroll content with raw (unpadded) rows.
	display, _ = display.Update(KeyPressMsg(Key{Text: "r"}))
	after := display.scrollView.content

	if before == after {
		t.Fatal("pressing 'r' did NOT change scroll content — bug reproduced")
	}
	if w := display.windowBuffer.WindowAt(0); w != nil {
		if tr, ok := w.renderer.(*textRenderer); !ok || tr.mdMode {
			t.Fatalf("expected markdown mode to be OFF after first 'r' press, got mdMode=%v ok=%v", tr.mdMode, ok)
		}
	}
	if !strings.Contains(after, "| name | gender | age |") {
		t.Errorf("after first 'r' expected raw unpadded header, got:\n%s", stripANSI(after))
	}
	if strings.Contains(after, "| name     |") {
		t.Errorf("after first 'r' should be raw (no padding) but found padded header:\n%s", stripANSI(after))
	}

	// Press 'r' again — should toggle markdown back ON (padded).
	display, _ = display.Update(KeyPressMsg(Key{Text: "r"}))
	back := display.scrollView.content

	if back == after {
		t.Fatal("second 'r' did NOT change scroll content")
	}
	if !strings.Contains(back, "│ name     │ gender │ age │") {
		t.Errorf("second 'r' should restore the rendered grid, got:\n%s", stripANSI(back))
	}
}
