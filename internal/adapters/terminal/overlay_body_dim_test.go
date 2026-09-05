package terminal

// Body-text dimming under overlays: plain body text (assistant messages,
// reasoning, user message text, tool input/output) carries NO explicit
// foreground color in normal mode — it renders in the terminal's default
// color, exactly like a shell. When an overlay is active (Dimmed styles),
// the body gains the dim color so expanded window content dims together
// with the chrome.

import (
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/tlv"
)

// TestBodyNormalKeepsPlain locks the "terminal default color" contract:
// with normal styles, expanded body text must contain no ANSI at all.
func TestBodyNormalKeepsPlain(t *testing.T) {
	styles := DefaultStyles()

	// textRenderer (assistant text / reasoning / system messages).
	tr := &textRenderer{tag: tlv.TagAssistantT}
	tr.AppendFromTLV(tlv.TagAssistantT, "assistant body text")
	for _, l := range mustLines(t, tr, styles) {
		if strings.Contains(l.Text, "\x1b[") {
			t.Fatalf("normal-mode body must be ANSI-free, got %q", l.Text)
		}
	}

	// userRenderer.
	ur := &userRenderer{textParts: []string{"user body text"}}
	for _, l := range mustLines(t, ur, styles) {
		if strings.Contains(l.Text, "\x1b[") {
			t.Fatalf("normal-mode user body must be ANSI-free, got %q", l.Text)
		}
	}

	// toolRenderer (plain input/output rows). The "───" separator keeps
	// its System color even in normal mode — only the body rows must be
	// ANSI-free.
	tool := &toolRenderer{name: "execute_command", input: "ls -la", output: "tool output"}
	for _, l := range mustLines(t, tool, styles) {
		body := stripANSI(l.Text)
		if body == "ls -la" || body == "tool output" {
			if strings.Contains(l.Text, "\x1b[") {
				t.Fatalf("normal-mode tool body must be ANSI-free, got %q", l.Text)
			}
		}
	}
}

// TestBodyDimmedUnderOverlay locks the dimming contract: with Dimmed
// styles, plain body rows carry the dim foreground; the stripped text is
// unchanged.
func TestBodyDimmedUnderOverlay(t *testing.T) {
	styles := DefaultStyles().Dimmed()
	dimmed := NewStyle().Foreground(styles.ColorDim)

	// textRenderer.
	tr := &textRenderer{tag: tlv.TagAssistantT}
	tr.AppendFromTLV(tlv.TagAssistantT, "assistant body text")
	for _, l := range mustLines(t, tr, styles) {
		if !strings.Contains(l.Text, "\x1b[") {
			t.Fatalf("dimmed body must carry SGR, got %q", l.Text)
		}
		if got := stripANSI(l.Text); got != "assistant body text" {
			t.Fatalf("dimmed stripped body = %q, want %q", got, "assistant body text")
		}
	}

	// userRenderer.
	ur := &userRenderer{textParts: []string{"user body text"}}
	for _, l := range mustLines(t, ur, styles) {
		if got := stripANSI(l.Text); got != "user body text" {
			t.Fatalf("dimmed user stripped = %q, want %q", got, "user body text")
		}
		if l.Text != dimmed.Render("user body text") {
			t.Fatalf("dimmed user body = %q, want %q", l.Text, dimmed.Render("user body text"))
		}
	}

	// toolRenderer: plain input row + output row, separator stays styled.
	tool := &toolRenderer{name: "execute_command", input: "ls -la", output: "tool output"}
	lines := mustLines(t, tool, styles)
	joined := joinVisualLines(lines)
	if got := stripANSI(joined); !strings.Contains(got, "ls -la") || !strings.Contains(got, "tool output") {
		t.Fatalf("dimmed tool body stripped = %q, want both input and output", got)
	}
	for _, l := range lines {
		if stripANSI(l.Text) == "tool output" && l.Text != dimmed.Render("tool output") {
			t.Fatalf("dimmed tool output = %q, want %q", l.Text, dimmed.Render("tool output"))
		}
	}
}

// TestBodyDimmedIncrementalAppend verifies the textRenderer colored cache
// stays correct across incremental streaming appends while dimmed.
func TestBodyDimmedIncrementalAppend(t *testing.T) {
	styles := DefaultStyles().Dimmed()

	tr := &textRenderer{tag: tlv.TagAssistantT}
	tr.AppendFromTLV(tlv.TagAssistantT, "hello")
	lines := mustLines(t, tr, styles)
	if got := stripANSI(lines[0].Text); got != "hello" {
		t.Fatalf("first build = %q, want %q", got, "hello")
	}

	// Incremental append (the fast path) must recolor the new content.
	tr.AppendFromTLV(tlv.TagAssistantT, " world")
	lines = mustLines(t, tr, styles)
	if got := stripANSI(lines[0].Text); got != "hello world" {
		t.Fatalf("after append = %q, want %q", got, "hello world")
	}
	if !strings.Contains(lines[0].Text, "\x1b[") {
		t.Fatalf("after append, dimmed body lost its SGR: %q", lines[0].Text)
	}
}

// TestBodyDimmedStyledRowsNotDoubleWrapped verifies that rows that
// already carry SGR (media badges, separators, diff rows) are NOT
// wrapped in a second Body style — under Dimmed they already resolve to
// ColorDim.
func TestBodyDimmedStyledRowsNotDoubleWrapped(t *testing.T) {
	styles := DefaultStyles().Dimmed()

	// edit_file diff: +/- rows are styled (DiffAdd/DiffRemove → dim);
	// context rows are plain and must get the Body dim.
	tr := &toolRenderer{name: "edit_file", input: "edit_file: /tmp/x\n- old\n+ new\n  ctx"}
	lines := mustLines(t, tr, styles)
	for _, l := range lines {
		if l.Text == "" {
			continue
		}
		// A styled row has exactly one SGR + one reset (2 escapes).
		if n := strings.Count(l.Text, "\x1b["); n > 2 {
			t.Fatalf("row double-wrapped: %d SGR escapes in %q", n, l.Text)
		}
	}

	// userRenderer: media badge row is styled once.
	ur := &userRenderer{mediaParts: []string{"image.png"}, textParts: []string{"see above"}}
	for _, l := range mustLines(t, ur, styles) {
		if n := strings.Count(l.Text, "\x1b["); n > 2 {
			t.Fatalf("media row double-wrapped: %d SGR escapes in %q", n, l.Text)
		}
	}
}

// TestBodyCacheInvalidatedOnBlockedSwitch verifies the colored cache is
// dropped when the renderer is invalidated (blocked state flip).
func TestBodyCacheInvalidatedOnBlockedSwitch(t *testing.T) {
	normal := DefaultStyles()
	dimmed := normal.Dimmed()

	tr := &textRenderer{tag: tlv.TagAssistantT}
	tr.AppendFromTLV(tlv.TagAssistantT, "body")

	// Normal first: plain.
	if got := mustLines(t, tr, normal)[0].Text; strings.Contains(got, "\x1b[") {
		t.Fatalf("normal body must be plain, got %q", got)
	}
	// Dimmed (overlay opens): Window.Render invalidates the renderer.
	tr.Invalidate()
	if got := mustLines(t, tr, dimmed)[0].Text; !strings.Contains(got, "\x1b[") {
		t.Fatalf("dimmed body must carry SGR, got %q", got)
	}
	// Back to normal (overlay closes): plain again.
	tr.Invalidate()
	if got := mustLines(t, tr, normal)[0].Text; strings.Contains(got, "\x1b[") {
		t.Fatalf("normal body after close must be plain, got %q", got)
	}
}

func mustLines(t *testing.T, r WindowRendering, styles *Styles) []visualLine {
	t.Helper()
	lines, _ := r.BuildInner(80, false, styles)
	if len(lines) == 0 {
		t.Fatal("BuildInner returned no lines")
	}
	return lines
}
