package terminal

// Regression test for the Tab-focus flicker: the overlay box must keep
// its width — and therefore its horizontal position — when Tab toggles
// between the filter input and the list. The two focus states render
// DIFFERENT help bars, and the old `%-*s` formatting padded but never
// truncated: the longer list help overflowed the box, widening the
// measured box (renderOverlay derives the box width from the widest
// row) and shifting the whole overlay left on every Tab press.

import (
	"strings"
	"testing"
)

func TestOverlayBoxStableAcrossTabFocus(t *testing.T) {
	tab := KeyPressMsg(Key{Code: KeyTab})

	tests := []struct {
		name string
		open func(m Terminal) Terminal
		view func(m Terminal) string
	}{
		{
			name: "model selector",
			open: func(m Terminal) Terminal {
				m.modelSelector = m.modelSelector.Open().WithSize(80, 24)
				m.input = m.input.Blur()
				return m
			},
			view: func(m Terminal) string { return m.modelSelector.View().Content },
		},
		{
			name: "theme selector",
			open: func(m Terminal) Terminal {
				m.themeSelector = m.themeSelector.Open(nil, "").WithSize(80, 24)
				m.input = m.input.Blur()
				return m
			},
			view: func(m Terminal) string { return m.themeSelector.View().Content },
		},
		{
			name: "attachment picker",
			open: func(m Terminal) Terminal {
				m.attachmentWindow = m.attachmentWindow.Open().WithSize(80, 24)
				m.input = m.input.Blur()
				return m
			},
			view: func(m Terminal) string { return m.attachmentWindow.View().Content },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := tt.open(newTestTerminal())

			// Three frames: input focus → list focus → input focus again.
			widths := []int{Width(tt.view(m))}
			for i := 0; i < 2; i++ {
				mm, _ := m.Update(tab)
				m = mm.(Terminal)
				widths = append(widths, Width(tt.view(m)))
			}

			if widths[0] != widths[1] || widths[1] != widths[2] {
				t.Errorf("overlay box width changed on Tab: %d -> %d -> %d (box would jump)",
					widths[0], widths[1], widths[2])
			}

			// No row may overflow the terminal width — the help bar is the
			// usual culprit (it differs between focus states and must be
			// truncated to the box width).
			for i := 0; i < 3; i++ {
				for _, row := range strings.Split(tt.view(m), "\n") {
					if w := cellWidth(row); w > widths[0] {
						t.Errorf("row overflows the box (w=%d > %d): %q", w, widths[0], row)
					}
				}
				mm, _ := m.Update(tab)
				m = mm.(Terminal)
			}
		})
	}
}
