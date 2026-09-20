package terminal

// Confirm dialog preview contract:
//
//  1. The tool-confirm dialog announces the 'e' key (full input in
//     $EDITOR) on its own centered hint row — "Press e to view the full
//     input." — whenever it carries tool input, mirroring the MCP-init
//     hint style. The title stays clean: a long tool name truncates the
//     title, never the affordance.
//  2. The 2-row input preview wraps like a message window: rows of the
//     same long command line are emitted as ONE continuous soft-wrap run
//     (no hard '\n' inside the command) and, when the input does not
//     fit, the last visible row ends with "…" — a silent drop of the
//     remainder used to make a mid-argument cut look like the real end
//     of the tool input.
//  3. The Screen row diff tracks the run's wrapped span (terminalRows,
//     like a soft-wrapped base line), so repainting the dialog mid-open
//     and closing it both clear every terminal row the run covered — no
//     residue from the wrapped continuation.

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
)

// cupRe matches a CUP cursor-position sequence "\x1b[<row>;<col>H".
var cupRe = regexp.MustCompile(`\x1b\[\d+;\d+H`)

// openToolDialog builds an open tool-confirm dialog sized like a
// 60-column terminal.
func openToolDialog(t *testing.T, toolName, input string) ConfirmDialog {
	t.Helper()
	cd := NewConfirmDialog(DefaultStyles()).WithSize(60, 24)
	return cd.OpenTool("1", toolName, input)
}

// confirmDialogText returns the dialog's rendered content with ANSI
// stripped, split into visual rows.
func confirmDialogRows(t *testing.T, cd ConfirmDialog) []string {
	t.Helper()
	return strings.Split(stripANSI(cd.View().Content), "\n")
}

func TestConfirmDialogAnnouncesEKey(t *testing.T) {
	const boxW = 60

	// With tool input: the dialog must show the hint row telling the
	// user 'e' opens the full input, and the title stays clean.
	withInput := openToolDialog(t, "edit_file", `{"path": "/tmp/a.go", "old_string": "x"}`)
	rows := confirmDialogRows(t, withInput)
	got := strings.Join(rows, "\n")
	if !strings.Contains(got, `Allow "edit_file" to run?`) {
		t.Errorf("tool-confirm title should be the plain question, got:\n%s", got)
	}
	if strings.Contains(got, "e: full input") {
		t.Errorf("title must not carry a cryptic '(e: full input)' tag:\n%s", got)
	}
	if !strings.Contains(got, "Press e to view the full input.") {
		t.Errorf("tool-confirm dialog should announce the 'e' key on a hint row:\n%s", got)
	}

	// Without tool input: 'e' does nothing, so no hint.
	emptyInput := openToolDialog(t, "edit_file", "")
	got = strings.Join(confirmDialogRows(t, emptyInput), "\n")
	if strings.Contains(got, "Press e to view the full input.") {
		t.Errorf("empty-input tool confirm must not advertise 'e', got:\n%s", got)
	}

	// Other dialog kinds never advertise 'e'.
	quit := NewConfirmDialog(DefaultStyles()).WithSize(boxW, 24).OpenQuit()
	got = strings.Join(confirmDialogRows(t, quit), "\n")
	if strings.Contains(got, "Press e to view the full input.") {
		t.Errorf("quit dialog must not advertise 'e', got:\n%s", got)
	}
}

func TestConfirmToolPreviewWrapAndEllipsis(t *testing.T) {
	const boxW = 60

	// Single-line input longer than two preview rows. Pure ASCII so the
	// wrap boundaries are exact character positions.
	input := `{"file_path": "/home/user/project/main.go", "old_string": "` +
		strings.Repeat("x", 300) + `"}`
	cd := openToolDialog(t, "edit_file", input)
	rows := confirmDialogRows(t, cd)

	// No row may exceed the box width — the preview rows run exactly to
	// the wrap boundary like message-window rows.
	for i, row := range rows {
		if w := cellWidth(row); w > boxW {
			t.Errorf("row %d is %d cells wide, exceeds box width %d: %q", i, w, boxW, row)
		}
	}

	// The cut must be marked: exactly one row ends with "…" and it is
	// the second preview row, ending exactly at the box width (the
	// marker's cell replaces the cut content's last cell, never
	// overflowing).
	var markerRows []int
	for i, row := range rows {
		if strings.HasSuffix(row, "…") {
			markerRows = append(markerRows, i)
		}
	}
	if len(markerRows) != 1 {
		t.Fatalf("expected exactly one preview row ending with '…', found %d (rows: %q)",
			len(markerRows), rows)
	}
	markerRow := rows[markerRows[0]]
	if w := cellWidth(markerRow); w != boxW {
		t.Errorf("marker row width = %d, want %d (full row with '…' in the last cell): %q",
			w, boxW, markerRow)
	}

	// The visible preview is the head of the input, cut to the marker:
	// row 0 is the first full 60 cells and the marker row is the next
	// 59 cells plus "…" (the marker's cell replaces the 60th).
	row0 := input[:60]
	markerWant := input[60:119] + "…"
	if rows[markerRows[0]] != markerWant {
		t.Errorf("marker row should be the input cut to 59 cells + '…':\n  got:  %q\n  want: %q",
			rows[markerRows[0]], markerWant)
	}
	if rows[markerRows[0]-1] != row0 {
		t.Errorf("first preview row should be the first full 60 cells:\n  got:  %q\n  want: %q",
			rows[markerRows[0]-1], row0)
	}
}

func TestConfirmToolPreviewNoEllipsisWhenFits(t *testing.T) {
	cd := openToolDialog(t, "read_file", `{"path": "/tmp/a.go", "start_line": 1}`)
	got := strings.Join(confirmDialogRows(t, cd), "\n")
	if strings.Contains(got, "…") {
		t.Errorf("short tool input fully visible in the preview must not show '…':\n%s", got)
	}
}

func TestConfirmToolPreviewEllipsisForMultilineInput(t *testing.T) {
	// Three short lines do not fit in a 2-row preview: the cut must be
	// marked even though no single line overflows the width.
	input := "first argument line\nsecond argument line\nthird argument line"
	cd := openToolDialog(t, "edit_file", input)
	got := strings.Join(confirmDialogRows(t, cd), "\n")
	if !strings.Contains(got, "second argument line…") {
		t.Errorf("multiline preview cut should end with '…' on its last visible row:\n%s", got)
	}
}

func TestConfirmDialogNarrowWidthMarkers(t *testing.T) {
	// On a narrow terminal a long tool name plus the title hint cannot
	// share one row: the title row must end with "…" (never silently
	// drop the overflow), and the preview keeps its own marker. No row
	// may exceed the box width.
	const boxW = 40
	cd := NewConfirmDialog(DefaultStyles()).WithSize(boxW, 24).OpenTool(
		"1", "some_very_long_tool_name_here",
		`{"file_path":"/home/user/project/main.go","old_string":"`+strings.Repeat("x", 400)+`"}`,
	)
	rows := confirmDialogRows(t, cd)
	for i, row := range rows {
		if w := cellWidth(row); w > boxW {
			t.Errorf("row %d is %d cells wide, exceeds box width %d: %q", i, w, boxW, row)
		}
	}
	titleRow := rows[2] // blank, title, blank, preview…
	if !strings.HasSuffix(titleRow, "…") {
		t.Errorf("wrapped title should end with '…' at width %d, got: %q", boxW, titleRow)
	}
	if !strings.HasPrefix(titleRow, `Allow "some_very_long_tool_name_here"`) {
		t.Errorf("title row should keep the question head, got: %q", titleRow)
	}
}

// descPreviewRegion returns the raw overlay bytes from the start of the
// description preview to the end of its "…" marker, plus the same region
// with ANSI stripped.
func descPreviewRegion(t *testing.T, out string) (raw, plain string) {
	t.Helper()
	start := strings.Index(out, `{"file_path":`)
	if start < 0 {
		t.Fatal("description preview not found in overlay output")
	}
	rest := out[start:]
	end := strings.Index(rest, "…")
	if end < 0 {
		t.Fatalf("preview truncation marker not found after %q", rest[:min(40, len(rest))])
	}
	raw = rest[:end+len("…")]
	return raw, stripANSI(raw)
}

func TestConfirmDialogPreviewEmittedAsSoftRun(t *testing.T) {
	// Full-width dialog: the two preview rows of a long single-line
	// command must be emitted as ONE continuous soft-wrap run — no hard
	// '\n', no CUP between them — so a terminal selection copies the
	// command head without fake newlines, exactly like a message
	// window's soft-wrap fragment.
	const screenW = 60
	input := `{"file_path":"/home/user/project/main.go","old_string":"` + strings.Repeat("x", 300) + `"}`
	cd := NewConfirmDialog(DefaultStyles()).WithSize(screenW, 24).OpenTool(
		"1", "edit_file", "edit_file: "+input+"\n")
	base := strings.Repeat("BASE\n", 23) + "BASE"
	out := cd.RenderOverlay(base, screenW, 24)

	raw, plain := descPreviewRegion(t, out)
	if strings.Contains(raw, "\n") {
		t.Errorf("hard newline inside the command preview run: %q", raw)
	}
	if cupRe.MatchString(raw) {
		t.Errorf("preview rows must join without CUP (one soft run), got: %q", raw)
	}
	// The visible head is row 0 (60 cells) plus row 1 cut to 59 cells +
	// "…" — one logical text, no padding spaces inside.
	if want := input[:119] + "…"; plain != want {
		t.Errorf("soft-run content mismatch:\n  got:  %q\n  want: %q", plain, want)
	}
}

func TestConfirmDialogPreviewHardRowsWhenNarrow(t *testing.T) {
	// A dialog narrower than the terminal cannot cross rows with a soft
	// run (the terminal would wrap at the screen width, not the box
	// width): it must fall back to per-row CUP emission, which keeps the
	// geometry correct at the cost of hard row separation.
	const screenW = 80
	input := `{"file_path":"/home/user/project/main.go","old_string":"` + strings.Repeat("x", 300) + `"}`
	cd := NewConfirmDialog(DefaultStyles()).WithSize(40, 24).OpenTool(
		"1", "edit_file", "edit_file: "+input+"\n")
	base := strings.Repeat("BASE\n", 23) + "BASE"
	out := cd.RenderOverlay(base, screenW, 24)

	raw, _ := descPreviewRegion(t, out)
	if !cupRe.MatchString(raw) {
		t.Errorf("narrow-dialog preview must emit each row at its own CUP position, got: %q", raw)
	}
}

func TestConfirmDialogPreviewKeepsOriginalLineBreaks(t *testing.T) {
	// Description whose second shown row starts a NEW original line
	// ("abc" then a long second line): message-window semantics keep
	// original lines hard-separated, so the rows must NOT merge into one
	// soft run even on a full-width dialog.
	const screenW = 60
	desc := "abc\n" + `{"file_path":"/home/user/project/main.go","old_string":"` +
		strings.Repeat("x", 300) + `"}`
	cd := NewConfirmDialog(DefaultStyles()).WithSize(screenW, 24).OpenTool(
		"1", "edit_file", "edit_file: "+desc+"\n")
	base := strings.Repeat("BASE\n", 23) + "BASE"
	out := cd.RenderOverlay(base, screenW, 24)

	// The first shown row is "abc" (a new original line), so the two
	// preview rows are separated by their own CUP positions.
	start := strings.Index(out, "abc")
	if start < 0 {
		t.Fatal("first description row not found in overlay output")
	}
	rest := out[start:]
	end := strings.Index(rest, "…")
	if end < 0 {
		t.Fatalf("preview truncation marker not found after %q", rest[:min(40, len(rest))])
	}
	raw := rest[:end+len("…")]
	if !cupRe.MatchString(raw) {
		t.Errorf("rows starting new original lines must be hard-separated (CUP between them), got: %q", raw)
	}
}

// TestConfirmDialogCloseClearsBothPreviewRows is a Screen-level
// regression for the residue complaint: after the dialog closes, BOTH
// preview rows (including the second, wrapped row that ends with "…")
// must be repainted with the base content. The anchor CUP row keeps
// containsCUP true on both frames, so the close goes through the steady
// row-diff path — the path that used to leave the wrapped continuation
// behind.
func TestConfirmDialogCloseClearsBothPreviewRows(t *testing.T) {
	// Screen-level regression for the residue complaint: after the
	// dialog closes, BOTH preview rows (including the second, wrapped
	// row that ends with "…") must be repainted with the base content.
	const W, H = 60, 24
	baseRows := make([]string, H)
	for i := range baseRows {
		baseRows[i] = strings.Repeat("B", W)
	}
	base := strings.Join(baseRows, "\n")
	anchor := "\x1b[24;1HANCHOR"

	input := `{"file_path":"/home/user/project/main.go","old_string":"` + strings.Repeat("x", 300) + `"}`
	cd := NewConfirmDialog(DefaultStyles()).WithSize(W, H).OpenTool(
		"1", "edit_file", "edit_file: "+input+"\n")

	s := &Screen{out: &bytes.Buffer{}}
	s.Resize(W, H)
	if err := s.Render(cd.RenderOverlay(base, W, H)+anchor, nil, true); err != nil {
		t.Fatal(err)
	}
	grid := applyFrame(nil, s.out.(*bytes.Buffer).String(), W)
	joined := ""
	for r := 3; r <= 12; r++ {
		joined += lineAt(grid, r) + "\n"
	}
	if !strings.Contains(joined, `Allow "edit_file" to run?`) ||
		!strings.Contains(joined, "Press e to view the full input.") ||
		!strings.Contains(joined, "…") || !strings.Contains(joined, "y / n") {
		t.Fatalf("dialog not fully drawn in frame 1:\n%s", joined)
	}

	s.out.(*bytes.Buffer).Reset()
	if err := s.Render(base+anchor, nil, true); err != nil {
		t.Fatal(err)
	}
	grid = applyFrame(grid, s.out.(*bytes.Buffer).String(), W)

	for r := 3; r <= 12; r++ {
		if got := lineAt(grid, r); got != strings.Repeat("B", W) {
			t.Errorf("row %d after dialog close = %q, want base content (residue left behind)", r, got)
		}
	}
}

func TestConfirmDialogRunUpdateThenCloseLeavesNoResidue(t *testing.T) {
	// The MCP-init style life cycle: the dialog stays open while its
	// description (a long single server list line → a wrapped run) is
	// updated several times, then closes. The row diff must repaint the
	// whole wrapped run on every update (no stale continuation from the
	// previous run) and clear both rows on close.
	const W, H = 60, 24
	baseRows := make([]string, H)
	for i := range baseRows {
		baseRows[i] = strings.Repeat("B", W)
	}
	base := strings.Join(baseRows, "\n")
	anchor := "\x1b[24;1HANCHOR"

	mk := func(desc string) ConfirmDialog {
		cd := NewConfirmDialog(DefaultStyles()).WithSize(W, H).OpenMCPInit()
		return cd.UpdateMCPInitProgress([]string{desc})
	}
	descA := "server-alpha-" + strings.Repeat("a", 200)
	descB := "server-beta-" + strings.Repeat("b", 250)

	s := &Screen{out: &bytes.Buffer{}}
	s.Resize(W, H)
	render := func(content string, grid [][]rune) [][]rune {
		s.out.(*bytes.Buffer).Reset()
		if err := s.Render(content, nil, true); err != nil {
			t.Fatal(err)
		}
		return applyFrame(grid, s.out.(*bytes.Buffer).String(), W)
	}

	// Open with run A.
	grid := render(mk(descA).RenderOverlay(base, W, H)+anchor, nil)
	// Update to run B — wrapped run text changed mid-open.
	grid = render(mk(descB).RenderOverlay(base, W, H)+anchor, grid)
	joined := ""
	for r := 3; r <= 12; r++ {
		joined += lineAt(grid, r) + "\n"
	}
	if !strings.Contains(joined, "server-beta-") || strings.Contains(joined, "server-alpha-") {
		t.Errorf("dialog content not updated to run B:\n%s", joined)
	}
	// Close — both rows of the run must be restored to base.
	grid = render(base+anchor, grid)
	for r := 3; r <= 12; r++ {
		if got := lineAt(grid, r); got != strings.Repeat("B", W) {
			t.Errorf("row %d after close = %q, want base content (residue left behind)", r, got)
		}
	}
}

// The wait window shown after :quit was confirmed while a task runs: it reports
// the step from the status snapshot (the same one the status bar reads), names
// the one key that does anything, and is not a choice — Esc and n leave it
// standing, because the quit has already been sent.
func TestConfirmQuitWaitingDialog(t *testing.T) {
	cd := NewConfirmDialog(DefaultStyles()).WithSize(60, 24).OpenQuitWaiting()
	if !cd.IsOpen() || cd.Kind() != ConfirmQuitWaiting {
		t.Fatalf("OpenQuitWaiting: open=%v kind=%v", cd.IsOpen(), cd.Kind())
	}

	cd = cd.UpdateQuitWaiting(StatusSnapshot{InProgress: true, CurrentStep: 3, MaxSteps: 10})
	got := strings.Join(confirmDialogRows(t, cd), "\n")
	for _, want := range []string{"Waiting for the task to finish", "Step 3/10", "Press c to cancel the task and exit now."} {
		if !strings.Contains(got, want) {
			t.Errorf("wait window is missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "y / n") {
		t.Errorf("the wait window must not offer a choice:\n%s", got)
	}

	// Esc and n are the cancel keys of every other dialog here; in this one
	// they must change nothing.
	for _, key := range []KeyPressMsg{{Code: KeyEscape}, {Code: 'n'}, {Code: 'y'}} {
		after, results := cd.Update(key)
		if len(results) != 0 || !after.IsOpen() {
			t.Errorf("key %v closed or answered the wait window: results=%v open=%v", key, results, after.IsOpen())
		}
	}

	// 'c' is the way out: the task is canceled, which is what lets the session
	// end now.
	after, results := cd.Update(KeyPressMsg{Code: 'c'})
	if after.IsOpen() {
		t.Error("'c' should close the wait window")
	}
	if len(results) != 1 {
		t.Fatalf("'c' produced %d results, want 1", len(results))
	}
	msg, ok := results[0].(ConfirmResultMsg)
	if !ok {
		t.Fatalf("result = %T, want ConfirmResultMsg", results[0])
	}
	if !msg.Result.Canceled || msg.Result.Kind != ConfirmQuitWaiting {
		t.Errorf("result = %+v, want a cancel of ConfirmQuitWaiting", msg.Result)
	}
}

// Ctrl+G does the same as 'c' here: it is the global "cancel" chord, and while
// this window is up the task is the only thing left to cancel.
func TestConfirmQuitWaitingCtrlG(t *testing.T) {
	cd := NewConfirmDialog(DefaultStyles()).WithSize(60, 24).OpenQuitWaiting()
	after, results := cd.Update(KeyPressMsg{Code: 'g', Mod: ModCtrl})
	if after.IsOpen() {
		t.Error("Ctrl+G should close the wait window")
	}
	if len(results) != 1 {
		t.Fatalf("Ctrl+G produced %d results, want 1", len(results))
	}
	msg, ok := results[0].(ConfirmResultMsg)
	if !ok || !msg.Result.Canceled {
		t.Errorf("Ctrl+G result = %#v, want a cancel", results[0])
	}
}

// A snapshot without a step still says something true: the task is in progress.
func TestConfirmQuitWaitingWithoutStep(t *testing.T) {
	cd := NewConfirmDialog(DefaultStyles()).WithSize(60, 24).OpenQuitWaiting()
	cd = cd.UpdateQuitWaiting(StatusSnapshot{InProgress: true})
	if got := strings.Join(confirmDialogRows(t, cd), "\n"); !strings.Contains(got, "Task in progress") {
		t.Errorf("wait window without a step:\n%s", got)
	}
}
