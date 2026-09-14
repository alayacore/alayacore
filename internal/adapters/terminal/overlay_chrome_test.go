package terminal

// The selector overlays (model, theme, attachment, help) share one chrome:
// a title, the filter box's two rules around the search input, the list
// hanging bare under that box, and the help bar. There is deliberately NO
// row between the search and the list (the old "Current:" / "N items" line),
// and the list draws no rules of its own — the filter box's closing rule is
// the only divider between the two, and the help bar closes the overlay
// below. This test pins that: exactly two full-width rules per overlay, and
// no info line above the list.

import (
	"regexp"
	"strings"
	"testing"
)

func TestSelectorOverlaysDrawOnlyTheFilterBoxRules(t *testing.T) {
	styles := DefaultStyles()
	overlays := []struct {
		name string
		view string
	}{
		{"model", NewModelSelector(styles).Open().WithSize(60, 24).View().Content},
		{"theme", NewThemeSelector(styles).Open([]ThemeEntry{{Name: "dark"}}, "dark").WithSize(60, 24).View().Content},
		{"help", NewHelpWindow(styles).Open().WithSize(72, 24).View().Content},
		{"attachment", NewAttachmentWindow(styles).Open().WithSize(60, 24).View().Content},
	}

	// The one line that used to sit between the search and the list, in both
	// of its forms: "Current: <name>" and "<n> items".
	infoLine := regexp.MustCompile(`^(Current:.*|[0-9]+ items)$`)

	for _, o := range overlays {
		t.Run(o.name, func(t *testing.T) {
			rules := 0
			for _, raw := range strings.Split(stripANSI(o.view), "\n") {
				l := strings.TrimRight(raw, " ")

				if l != "" && strings.Trim(l, "─") == "" {
					rules++
				}
				if infoLine.MatchString(l) {
					t.Errorf("overlay still renders the between-search-and-list info line: %q", l)
				}
			}
			if rules != 2 {
				t.Errorf("overlay draws %d full-width rules, want 2 (the filter box's top and bottom; the list has none)", rules)
			}
		})
	}
}
