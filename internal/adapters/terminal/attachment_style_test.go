package terminal

// The attachment badge's register, and the alert channel it must not spend.
//
// A badge names what accompanies a message — it is header material, so it
// takes the window chrome's own register (muted + bold, the same pair a
// window's line is drawn in) and never the theme's warning color, which is
// the palette's one alert channel: spent on what the reader is asked to
// decide about (a confirmation, a draft waiting to be sent). And the badge
// is the same object folded and unfolded, so both rows must paint it the
// same way (see toolNameStyle, window.go).

import (
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/tlv"
)

// warningColorParams returns the parameters the theme's warning color paints
// with ("38;2;R;G;B"), taken from a style built for the purpose so the
// assertions below neither hard-code a palette value nor care whether the
// surface also sets bold.
func warningColorParams(t *testing.T, st *Styles) string {
	t.Helper()
	rendered := NewStyle().Foreground(st.ColorWarning).Render("Z")
	i := strings.IndexRune(rendered, 'Z')
	if i <= 0 {
		t.Fatalf("no color sequence in %q — the check would be vacuous", rendered)
	}
	params := strings.TrimSuffix(strings.TrimPrefix(rendered[:i], "\x1b["), "m")
	if params == "" {
		t.Fatal("the warning color painted nothing — the check would be vacuous")
	}
	return params
}

func TestAttachmentBadgesNeverDrawWithTheAlertColor(t *testing.T) {
	st := DefaultStyles()
	warn := warningColorParams(t, st)
	// The alert channel itself is still alive: Confirm owns it, and so does
	// the multi-line prompt box (input_capacity_test.go pins that one).
	if !strings.Contains(st.Confirm.Render("Z"), warn) {
		t.Fatalf("Styles.Confirm no longer carries the warning color; this test proves nothing")
	}

	media := []string{tlv.MediaLabel(tlv.TagUserI), tlv.MediaLabel(tlv.TagUserA)}
	ur := &userRenderer{textParts: []string{"what are these?"}, mediaParts: media}

	lines, _ := ur.BuildInner(80, false, st)
	surfaces := map[string]string{
		"expanded body":     joinVisualLines(lines),
		"collapsed line":    collapseOf(t, ur, st, 80),
		"prompt box":        NewPromptInput(st).WithWidth(60).WithAttachments(media).WithValue("hi").View().Content,
		"expanded (dimmed)": dimmedBody(t, ur, st),
	}
	for name, got := range surfaces {
		if strings.Contains(got, warn) {
			t.Errorf("%s still paints with the warning color:\n  %q", name, got)
		}
	}
}

func TestAttachmentBadgesKeepTheirRegisterAcrossFold(t *testing.T) {
	st := DefaultStyles()
	media := []string{tlv.MediaLabel(tlv.TagUserI), tlv.MediaLabel(tlv.TagUserA)}
	ur := &userRenderer{textParts: []string{"what are these?"}, mediaParts: media}

	lines, _ := ur.BuildInner(80, false, st)
	want := st.Attachment.Render("📷 Image  🎵 Audio")
	if lines[0].Text != want {
		t.Errorf("expanded badge row:\n  got  %q\n  want %q", lines[0].Text, want)
	}
	if wantBold := st.Label.Bold(true).Render("📷 Image  🎵 Audio"); want != wantBold {
		t.Errorf("Styles.Attachment is no longer the window chrome's register (muted + bold)")
	}

	collapsed := collapseOf(t, ur, st, 80)
	if badgeRun := st.Attachment.Render("📷1 🎵1"); !strings.Contains(collapsed, badgeRun) {
		t.Errorf("collapsed badge run is not in the attachment style:\n  got  %q\n  want it to contain %q", collapsed, badgeRun)
	}
}

// TestCollapsedBadgesFallBackWhenTheCutLandsInsideThem pins the degenerate
// half of the rule above: the run is styled only when it survived truncation
// whole. A half-badge is not worth two colors, so the summary then takes the
// content's plain muted — and nothing about the line's width or text changes.
func TestCollapsedBadgesFallBackWhenTheCutLandsInsideThem(t *testing.T) {
	st := DefaultStyles()
	ur := &userRenderer{
		textParts: []string{strings.Repeat("analyze ", 8) + "this"},
		mediaParts: []string{
			tlv.MediaLabel(tlv.TagUserI), tlv.MediaLabel(tlv.TagUserV),
			tlv.MediaLabel(tlv.TagUserA), tlv.MediaLabel(tlv.TagUserD),
		},
	}
	line := collapseOf(t, ur, st, 24)
	if w := cellWidth(stripANSI(line)); w > 24 {
		t.Errorf("collapsed line overflows its width: %d cells, %q", w, stripANSI(line))
	}
	// One bold run only — the label column. A styled badge run would add a
	// second "\x1b[1;" register opening, which is what the fallback avoids.
	if n := strings.Count(line, "\x1b[1;"); n != 1 {
		t.Errorf("a badge run cut mid-way must not be half-styled: %d bold runs in %q, want 1 (the label)", n, line)
	}
	if plain := stripANSI(line); !strings.HasPrefix(plain, "USER PROMPT") {
		t.Errorf("the label column is unaffected by the fallback: %q", plain)
	}
}

func collapseOf(t *testing.T, ur *userRenderer, st *Styles, width int) string {
	t.Helper()
	line, _ := ur.BuildCollapsed(width, st)
	return line
}

func dimmedBody(t *testing.T, ur *userRenderer, st *Styles) string {
	t.Helper()
	lines, _ := ur.BuildInner(80, false, st.Dimmed())
	return joinVisualLines(lines)
}
