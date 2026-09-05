package terminal

// Every overlay closes with a one-line help bar drawn by renderHelpBar, which
// truncates to the box width. A truncated hint is not a shortened hint — the key
// simply never reaches the person using the overlay — and the bar truncates
// rather than wrapping because a second row would change the overlay's height,
// which the overlays deliberately share. So fitting the text is the only
// available answer, and this test is what keeps it that way.
//
// The expected bars are spelled out here rather than read back from the
// components, on the same reasoning as the example census in
// markdown_docs_test.go: an assertion derived from the code under test lets
// whoever edits the code also delete the hint and stay green.

import (
	"strings"
	"testing"
)

func TestOverlayHelpBarsFit(t *testing.T) {
	s := DefaultStyles()
	for _, st := range []struct {
		name     string
		boxWidth int
		render   func() string
		want     string
	}{
		{
			name: "attachment, path input", boxWidth: 60,
			render: func() string {
				aw := NewAttachmentWindow(s).Open()
				aw.FilterInputFocused = true
				return aw.View().Content
			},
			want: "  tab: list | enter: pick | ctrl+w: up a level | ctrl+a: url",
		},
		{
			name: "attachment, file list", boxWidth: 60,
			render: func() string {
				aw := NewAttachmentWindow(s).Open()
				aw.FilterInputFocused = false
				return aw.View().Content
			},
			want: "  tab: search | j/k: navigate | enter: pick | esc: close",
		},
		{
			name:     "attachment, URL mode",
			boxWidth: 60,
			render:   func() string { return NewAttachmentWindow(s).Open().switchToURL().View().Content },
			want:     "  enter: add URL | ctrl+a: switch to local | esc: close",
		},
		{
			name: "model, filter", boxWidth: 60,
			render: func() string {
				ms := NewModelSelector(s).Open()
				ms.FilterInputFocused = true
				return ms.View().Content
			},
			want: "  tab: list | ctrl+r: reload | enter: select | esc: close",
		},
		{
			name: "model, list", boxWidth: 60,
			render: func() string {
				ms := NewModelSelector(s).Open()
				ms.FilterInputFocused = false
				return ms.View().Content
			},
			want: "  tab: search | j/k: navigate | enter: select | q/esc: close",
		},
		{
			name: "theme, filter", boxWidth: 60,
			render: func() string {
				ts := NewThemeSelector(s).Open([]ThemeEntry{{Name: "dark"}}, "dark")
				ts.FilterInputFocused = true
				return ts.View().Content
			},
			want: "  tab: list | enter: select | esc: close",
		},
		{
			name: "theme, list", boxWidth: 60,
			render: func() string {
				ts := NewThemeSelector(s).Open([]ThemeEntry{{Name: "dark"}}, "dark")
				ts.FilterInputFocused = false
				return ts.View().Content
			},
			want: "  tab: search | j/k: navigate | enter: select | q/esc: close",
		},
		{
			name:     "help, filter",
			boxWidth: 72,
			render: func() string {
				hw := NewHelpWindow(s).Open()
				hw.FilterInputFocused = true
				return hw.View().Content
			},
			want: "  tab: list | esc: close",
		},
		{
			name:     "help, list",
			boxWidth: 72,
			render: func() string {
				hw := NewHelpWindow(s).Open()
				hw.FilterInputFocused = false
				return hw.View().Content
			},
			want: "  tab: filter | j/k: navigate | enter: copy to input | q/esc: close",
		},
	} {
		t.Run(st.name, func(t *testing.T) {
			if w := cellWidth(st.want); w > st.boxWidth {
				t.Errorf("help text is %d cells, which does not fit the %d-cell box; renderHelpBar would cut it",
					w, st.boxWidth)
			}

			lines := strings.Split(stripANSI(st.render()), "\n")
			row := strings.TrimRight(lines[len(lines)-1], " ")
			if row != st.want {
				t.Errorf("the bar that renders is %q, want %q", row, st.want)
			}
		})
	}
}
