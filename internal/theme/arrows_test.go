package theme

import (
	"strings"
	"testing"

	ansi "github.com/charmbracelet/x/ansi"
)

// The default glyphs are pinned by codepoint on purpose: the pair is
// chosen for what the terminal can be relied on to draw in one cell, not
// just for how it looks.
func TestDefaultArrowGlyphs(t *testing.T) {
	if r := []rune(DefaultFoldArrow); len(r) != 1 || r[0] != '\u25B8' { // ▸
		t.Errorf("DefaultFoldArrow = %q, want ▸ (U+25B8)", DefaultFoldArrow)
	}
	if r := []rune(DefaultUnfoldArrow); len(r) != 1 || r[0] != '\u25BE' { // ▾
		t.Errorf("DefaultUnfoldArrow = %q, want ▾ (U+25BE)", DefaultUnfoldArrow)
	}
	for _, g := range []string{DefaultFoldArrow, DefaultUnfoldArrow} {
		if w := ansi.StringWidth(g); w != 1 {
			t.Errorf("default arrow %q is %d cells wide, want 1", g, w)
		}
		if r := []rune(g); len(r) != 1 {
			t.Errorf("default arrow %q is %d runes, want a bare single rune", g, len(r))
		}
	}
}

// The heavier triangle pair most projects reach for is exactly the pair
// the defaults avoid: these codepoints are East Asian Width "A" and/or
// Emoji_Presentation=Yes, so some terminals draw them two cells wide
// while our width model says one — silently misaligned headers. The
// defaults must never drift back onto one of them.
func TestDefaultArrowsAvoidUnreliableCodepoints(t *testing.T) {
	unreliable := map[rune]string{
		'\u25B6': "▶ ambiguous width, emoji presentation",
		'\u25BC': "▼ ambiguous width, emoji presentation",
		'\u25B2': "▲ ambiguous width, emoji presentation",
		'\u25C0': "◀ ambiguous width, emoji presentation",
		'\u23F5': "⏵ emoji presentation (media play key)",
		'\u23F7': "⏷ emoji presentation (media key)",
		'\u25CF': "● ambiguous width",
		'\u25CB': "○ ambiguous width",
		'\u2192': "→ ambiguous width",
		'\u2193': "↓ ambiguous width",
		'\u2630': "☰ counted as two cells by the layout's width model",
	}
	for _, g := range []string{DefaultFoldArrow, DefaultUnfoldArrow} {
		for _, r := range g {
			if why, bad := unreliable[r]; bad {
				t.Errorf("default arrow uses %q: %s", string(r), why)
			}
		}
	}
}

// The embedded themes must agree with the Go defaults — they are the two
// sources users see, and a divergence reads as a bug in whichever one
// they happened to open.
func TestBundledThemesAgreeOnArrows(t *testing.T) {
	def := DefaultTheme()
	light, errs := parseTheme(lightThemeContent)
	if len(errs) > 0 {
		t.Fatalf("embedded light theme reported errors: %v", errs)
	}
	for name, th := range map[string]*Theme{"dark": def, "light": light} {
		if th.FoldArrow != DefaultFoldArrow {
			t.Errorf("%s theme fold_arrow = %q, want %q", name, th.FoldArrow, DefaultFoldArrow)
		}
		if th.UnfoldArrow != DefaultUnfoldArrow {
			t.Errorf("%s theme unfold_arrow = %q, want %q", name, th.UnfoldArrow, DefaultUnfoldArrow)
		}
	}
}

func TestArrowGlyphs(t *testing.T) {
	cases := []struct {
		name       string
		fold       string
		unfold     string
		wantFold   string
		wantUnfold string
		wantMsgs   int
	}{
		{
			name: "valid theme glyphs kept", fold: ">", unfold: "v",
			wantFold: ">", wantUnfold: "v",
		},
		{
			// An unset key is not a bad value: the caller's default
			// fills in, and nothing is reported.
			name: "empty falls back silently", unfold: "v",
			wantFold: DefaultFoldArrow, wantUnfold: "v",
		},
		{
			name: "emoji-presentation sequence replaced",
			fold: "▶️", unfold: "▼", // ▶ + VS16 → two cells
			wantFold: DefaultFoldArrow, wantUnfold: "▼", wantMsgs: 1,
		},
		{
			// VS15 forces text presentation: still one cell, so a user
			// who wants the heavier triangle keeps it.
			name: "text-presentation variation kept",
			fold: "▶︎", unfold: "▼︎", // ▶/▼ + VS15
			wantFold: "▶︎", wantUnfold: "▼︎",
		},
		{
			name: "multi-cluster value replaced",
			fold: "[+]", unfold: "-",
			wantFold: DefaultFoldArrow, wantUnfold: "-", wantMsgs: 1,
		},
		{
			name: "both arrows replaced",
			fold: "▸▾", unfold: "☰",
			wantFold: DefaultFoldArrow, wantUnfold: DefaultUnfoldArrow, wantMsgs: 2,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fold, unfold, msgs := ArrowGlyphs(tc.fold, tc.unfold)
			if fold != tc.wantFold || unfold != tc.wantUnfold {
				t.Errorf("ArrowGlyphs(%q, %q) = %q, %q; want %q, %q",
					tc.fold, tc.unfold, fold, unfold, tc.wantFold, tc.wantUnfold)
			}
			if len(msgs) != tc.wantMsgs {
				t.Fatalf("got %d messages, want %d: %v", len(msgs), tc.wantMsgs, msgs)
			}
			for _, m := range msgs {
				if !strings.Contains(m, "arrow") {
					t.Errorf("message %q does not name the offending key", m)
				}
			}
			for _, g := range []string{fold, unfold} {
				if w := ansi.StringWidth(g); w != 1 {
					t.Errorf("result %q is %d cells wide, want 1", g, w)
				}
			}
		})
	}
}

// A theme file with an unusable arrow is reported (so the user learns
// which key is wrong) and still yields a renderable theme.
func TestParseThemeReportsUnusableArrow(t *testing.T) {
	th, errs := parseTheme("fold_arrow: \"[+]\"\n")
	if th.FoldArrow != DefaultFoldArrow {
		t.Errorf("fold_arrow = %q, want the default %q", th.FoldArrow, DefaultFoldArrow)
	}
	if len(errs) != 1 {
		t.Fatalf("got %d errors, want 1: %v", len(errs), errs)
	}
	if !strings.Contains(errs[0], "fold_arrow") {
		t.Errorf("error %q does not name fold_arrow", errs[0])
	}
}
