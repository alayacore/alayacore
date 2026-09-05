package terminal

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/app"
	"github.com/alayacore/alayacore/internal/config"
	"github.com/alayacore/alayacore/internal/protocol"
	"github.com/alayacore/alayacore/internal/theme"
	"github.com/alayacore/alayacore/internal/tlv"
)

const mdTableContent = "| name | gender | age |\n|---|---|---|\n| Walllace Gibbon | male | 100 |\n| Harry Potter | male | 10 |"

// TestTextRendererMarkdownModeToggle verifies raw ↔ markdown round-trip
// on an assistant text renderer, including cache invalidation.
func TestTextRendererMarkdownModeToggle(t *testing.T) {
	styles := NewStyles(theme.DefaultTheme())
	r := &textRenderer{tag: tlv.TagAssistantT}
	r.AppendFromTLV(tlv.TagAssistantT, mdTableContent)

	// Raw mode (default): the table is NOT padded.
	raw, _ := r.BuildInner(120, false, styles)
	rawText := joinVisualLines(raw)
	if strings.Contains(rawText, "| name            |") {
		t.Fatalf("raw mode must not pad tables: %q", rawText)
	}

	// Toggle on: cache invalidated, full re-render pads the table.
	r.ToggleMarkdownMode()
	if _, ok := r.TryLineCount(120); ok {
		t.Error("TryLineCount must miss right after toggling markdown mode")
	}
	md, _ := r.BuildInner(120, false, styles)
	mdText := joinVisualLines(md)
	for _, want := range []string{
		"┌─────────────────┬────────┬─────┐",
		"│ name            │ gender │ age │",
		"├─────────────────┼────────┼─────┤",
		"│ Walllace Gibbon │ male   │ 100 │",
		"│ Harry Potter    │ male   │ 10  │",
		"└─────────────────┴────────┴─────┘",
	} {
		if !strings.Contains(mdText, want) {
			t.Errorf("markdown render missing %q in:\n%s", want, mdText)
		}
	}
	if _, ok := r.TryLineCount(120); !ok {
		t.Error("TryLineCount must hit after BuildInner")
	}

	// Toggle off: back to raw.
	r.ToggleMarkdownMode()
	back, _ := r.BuildInner(120, false, styles)
	if joinVisualLines(back) != rawText {
		t.Error("toggling off must restore the raw rendering")
	}
}

// TestTextRendererMarkdownModeStreaming feeds deltas in markdown mode:
// every delta invalidates the cache, and the full re-render must equal a
// transform+wrap of the complete accumulated content. A later long name
// must re-flow the entire first column.
func TestTextRendererMarkdownModeStreaming(t *testing.T) {
	styles := NewStyles(theme.DefaultTheme())
	r := &textRenderer{tag: tlv.TagAssistantT}
	r.ToggleMarkdownMode()
	all := ""

	deltas := []string{
		"| name | gender | age |\n",
		"|---|---|---|\n",
		"| Walllace Gibbon | male | 100 |\n",
		"| Harry Potter | male | 10 |",
	}
	for _, d := range deltas {
		all += d
		r.AppendFromTLV(tlv.TagAssistantT, d)
		if _, ok := r.TryLineCount(120); ok {
			t.Fatal("TryLineCount must miss after a delta in markdown mode")
		}
		got, _ := r.BuildInner(120, false, styles)
		want := wrapVisualLines(renderMarkdownTables(stripANSI(all), 120), 120)
		if !sameVisualLines(got, want) {
			t.Fatalf("markdown streaming mismatch after delta %q", d)
		}
	}

	// A very long name arrives: the whole first column must be re-flowed.
	longName := "A Very Very Long Name Indeed"
	r.AppendFromTLV(tlv.TagAssistantT, "\n| "+longName+" | male | 1 |")
	lines, _ := r.BuildInner(120, false, styles)
	rendered := joinVisualLines(lines)
	if !strings.Contains(rendered, longName) {
		t.Fatalf("long name missing from rendered table:\n%s", rendered)
	}
	// Header cell "name" must now be padded to the long name's width.
	wantHeader := "│ name" + strings.Repeat(" ", len(longName)-len("name")) + " │"
	if !strings.Contains(rendered, wantHeader) {
		t.Errorf("first column not re-flowed for the long name:\n%s", rendered)
	}
}

// TestMarkdownModeMatchesFullRewrap is the markdown-mode analog of
// TestIncrementalMatchesFullRewrap: in mdMode there is no incremental
// path at all — every delta produces a full re-render, which must equal
// transform+wrap of the accumulated content.
func TestMarkdownModeMatchesFullRewrap(t *testing.T) {
	styles := NewStyles(theme.DefaultTheme())
	r := &textRenderer{tag: tlv.TagAssistantT}
	r.ToggleMarkdownMode()
	all := ""

	cases := [][]string{
		{"| a | b |", "\n|---|---|", "\n| 1 | 2 |"},
		{"Before\n\n", "| x | y |\n", "|---|---|\n", "| 中文 | 数据 |\n", "| 长一点的单元格内容 | z |", "\n\nafter"},
		{"```\n| not | a |\n|---|---|\n| table |\n```\n", "| real | table |\n", "|---|---|", "| yes | it is |"},
		{"plain\n", "| c |\n", "|---|\n", "| value with \t tab |"},
	}
	for ci, deltas := range cases {
		for _, d := range deltas {
			all += d
			r.AppendFromTLV(tlv.TagAssistantT, d)
			got, _ := r.BuildInner(100, false, styles)
			want := wrapVisualLines(renderMarkdownTables(stripANSI(all), 100), 100)
			if !sameVisualLines(got, want) {
				t.Errorf("case %d: mismatch after delta %q", ci, d)
			}
		}
	}
}

// TestWindowToggleMarkdownModeRestricted verifies only plain-text windows
// (AT/AR) can toggle; user, tool, and system windows cannot.
func TestWindowToggleMarkdownModeRestricted(t *testing.T) {
	styles := NewStyles(theme.DefaultTheme())

	w := NewWindow("1", tlv.TagAssistantT, styles)
	if !w.ToggleMarkdownMode() || !w.MarkdownMode() {
		t.Error("assistant text window must toggle markdown mode")
	}

	wReason := NewWindow("2", tlv.TagAssistantR, styles)
	if !wReason.ToggleMarkdownMode() || !wReason.MarkdownMode() {
		t.Error("reasoning window must toggle markdown mode")
	}

	for _, tc := range []struct {
		tag string
	}{
		{TagWindowSN}, {TagWindowSE}, {tlv.TagUserT}, {tlv.TagAssistantF}, {tlv.TagUserF},
	} {
		other := NewWindow("x", tc.tag, styles)
		if other.ToggleMarkdownMode() {
			t.Errorf("window %s must not toggle markdown mode", tc.tag)
		}
		if other.MarkdownMode() {
			t.Errorf("window %s must never report markdown mode", tc.tag)
		}
	}
}

// TestMarkdownModeIncrementalPlainDelta verifies plain deltas (no table
// rows) keep the incremental wrap path in markdown mode: TryLineCount
// stays valid after each append, exactly like raw mode.
func TestMarkdownModeIncrementalPlainDelta(t *testing.T) {
	styles := NewStyles(theme.DefaultTheme())
	r := &textRenderer{tag: tlv.TagAssistantT, mdMode: true}
	r.AppendFromTLV(tlv.TagAssistantT, "Hello world")
	r.BuildInner(80, false, styles) // populate wrappedLines

	for _, d := range []string{" more text", "\n\nSecond paragraph", " continues", "\n"} {
		r.AppendFromTLV(tlv.TagAssistantT, d)
		if _, ok := r.TryLineCount(80); !ok {
			t.Fatalf("plain delta %q must keep the incremental path (TryLineCount valid)", d)
		}
	}
	// Result equals a full transform+wrap of the accumulated content.
	got, _ := r.BuildInner(80, false, styles)
	want := wrapVisualLines(renderMarkdownTables(stripANSI(r.rawContent()), 80), 80)
	if !sameVisualLines(got, want) {
		t.Error("incremental plain deltas must match full re-render")
	}
}

// TestMarkdownModeIncrementalPipeDelta verifies deltas that can form or
// extend a table invalidate the cache (full re-render path), so the
// re-flowed table is visible immediately.
func TestMarkdownModeIncrementalPipeDelta(t *testing.T) {
	styles := NewStyles(theme.DefaultTheme())
	r := &textRenderer{tag: tlv.TagAssistantT, mdMode: true}
	r.AppendFromTLV(tlv.TagAssistantT, "intro\n")
	r.BuildInner(80, false, styles) // populate wrappedLines

	// Table header arrives: pipe line → full path.
	r.AppendFromTLV(tlv.TagAssistantT, "| name | age |\n")
	if _, ok := r.TryLineCount(80); ok {
		t.Fatal("pipe delta must invalidate the incremental cache")
	}
	lines, _ := r.BuildInner(80, false, styles)
	if !strings.Contains(joinVisualLines(lines), "intro\n| name | age |") {
		t.Fatalf("unexpected render: %q", joinVisualLines(lines))
	}

	// Delimiter arrives: full path, table now padded.
	r.AppendFromTLV(tlv.TagAssistantT, "|---|---|")
	lines, _ = r.BuildInner(80, false, styles)
	if !strings.Contains(joinVisualLines(lines), "┌──────┬─────┐\n│ name │ age │") {
		t.Fatalf("table not rendered as a grid after the header: %q", joinVisualLines(lines))
	}
}

// TestMarkdownModeTailTransition verifies the open-table tail state:
// a table row without a trailing newline keeps the tail "inside" the
// table (next plain delta re-renders), while a trailing newline closes
// it (next plain delta goes incremental).
func TestMarkdownModeTailTransition(t *testing.T) {
	styles := NewStyles(theme.DefaultTheme())

	// Without trailing newline: the next delta merges onto the table row.
	r := &textRenderer{tag: tlv.TagAssistantT, mdMode: true}
	r.AppendFromTLV(tlv.TagAssistantT, "| a | b |\n|---|---|\n| 1 | 2 |")
	r.BuildInner(80, false, styles)
	r.AppendFromTLV(tlv.TagAssistantT, " tail") // merges onto "| 1 | 2 |"
	if _, ok := r.TryLineCount(80); ok {
		t.Fatal("delta after an open table tail must re-render")
	}
	lines, _ := r.BuildInner(80, false, styles)
	// " tail" merged onto the last row adds a third cell: the row is still
	// a table row, so it re-renders as a 3-column grid (empty header/delimiter).
	if !strings.Contains(joinVisualLines(lines), "│ 1 │ 2 │ tail │") {
		t.Errorf("merged tail row missing: %q", joinVisualLines(lines))
	}

	// With trailing newline: the table is closed; plain deltas are safe
	// to append incrementally.
	r2 := &textRenderer{tag: tlv.TagAssistantT, mdMode: true}
	r2.AppendFromTLV(tlv.TagAssistantT, "| a | b |\n|---|---|\n| 1 | 2 |\n")
	r2.BuildInner(80, false, styles)
	r2.AppendFromTLV(tlv.TagAssistantT, "after the table")
	if _, ok := r2.TryLineCount(80); !ok {
		t.Fatal("plain delta after a closed table must go incremental")
	}
	got, _ := r2.BuildInner(80, false, styles)
	want := wrapVisualLines(renderMarkdownTables(stripANSI(r2.rawContent()), 80), 80)
	if !sameVisualLines(got, want) {
		t.Error("closed-table incremental append must match full re-render")
	}
}

// TestNoMarkdownFlagDefaultOff verifies --no-markdown makes new assistant
// text windows start in raw mode while per-window toggling still works.
func TestNoMarkdownFlagDefaultOff(t *testing.T) {
	appCfg := &app.Config{Cfg: &config.Settings{NoMarkdown: true}}
	terminal := NewTerminalWithTheme(NewTerminalOutput(DefaultStyles()), nopWriteCloser{}, appCfg, 120, 24, theme.DefaultTheme(), nil, "theme-dark")
	terminal = terminal.focusDisplay()
	terminal.out.WindowBuffer().AppendOrUpdate(tlv.TagAssistantT, "test1", mdTableContent)
	terminal.display = terminal.display.WithWindowCursor(0)

	w := terminal.out.WindowBuffer().WindowAt(0)
	if w == nil || w.MarkdownMode() {
		t.Fatal("--no-markdown windows should start in raw mode")
	}
	// Per-window toggle still works.
	terminal.Update(KeyPressMsg(Key{Code: 'r'}))
	if !w.MarkdownMode() {
		t.Error("r must still enable markdown mode per window")
	}
}

func TestRKeyTogglesMarkdownMode(t *testing.T) {
	terminal := NewTerminalWithTheme(NewTerminalOutput(DefaultStyles()), nopWriteCloser{}, nil, 120, 24, theme.DefaultTheme(), nil, "theme-dark")
	terminal = terminal.focusDisplay()
	terminal.out.WindowBuffer().AppendOrUpdate(tlv.TagAssistantT, "test1", mdTableContent)
	terminal.display = terminal.display.WithWindowCursor(0)

	w := terminal.out.WindowBuffer().WindowAt(0)
	if w == nil || !w.MarkdownMode() {
		t.Fatal("assistant text windows should start in markdown mode by default")
	}
	rendered := w.Render(120, false, terminal.display.styles, NewStyle().Foreground(terminal.display.styles.ColorDim), false)
	if !strings.Contains(rendered, "│ name            │ gender │ age │") {
		t.Errorf("default render should show the grid:\n%s", rendered)
	}

	// r → markdown off (raw)
	if _, cmd := terminal.Update(KeyPressMsg(Key{Code: 'r'})); cmd != nil {
		t.Error("r should not emit a command")
	}
	if w.MarkdownMode() {
		t.Error("r should disable markdown mode")
	}
	rendered = w.Render(120, false, terminal.display.styles, NewStyle().Foreground(terminal.display.styles.ColorDim), false)
	if strings.Contains(rendered, "| name            | gender | age |") {
		t.Errorf("raw render must not pad the table:\n%s", rendered)
	}

	// r again → markdown on
	terminal.Update(KeyPressMsg(Key{Code: 'r'}))
	if !w.MarkdownMode() {
		t.Error("r should re-enable markdown mode")
	}
}

// TestRKeyNoopWhenFolded verifies r does nothing on a folded window
// (markdown state is unchanged).
func TestRKeyNoopWhenFolded(t *testing.T) {
	terminal := NewTerminalWithTheme(NewTerminalOutput(DefaultStyles()), nopWriteCloser{}, nil, 120, 24, theme.DefaultTheme(), nil, "theme-dark")
	terminal = terminal.focusDisplay()
	terminal.out.WindowBuffer().AppendOrUpdate(tlv.TagAssistantT, "test1", mdTableContent)
	terminal.display = terminal.display.WithWindowCursor(0)
	terminal.out.WindowBuffer().ToggleFold(0) // fold it

	w := terminal.out.WindowBuffer().WindowAt(0)
	if !w.Folded {
		t.Fatal("window should be folded")
	}
	before := w.MarkdownMode()
	terminal.Update(KeyPressMsg(Key{Code: 'r'}))
	if w.MarkdownMode() != before {
		t.Error("r must be a no-op on folded windows")
	}
}

// TestRKeyNoopOnNonTextWindow verifies r does nothing on tool windows.
func TestRKeyNoopOnNonTextWindow(t *testing.T) {
	terminal := NewTerminalWithTheme(NewTerminalOutput(DefaultStyles()), nopWriteCloser{}, nil, 120, 24, theme.DefaultTheme(), nil, "theme-dark")
	terminal = terminal.focusDisplay()
	terminal.out.WindowBuffer().HandleToolInputEvent(protocol.ToolInputData{ID: "tool1", Name: "read_file", Input: json.RawMessage(`{"path":"x"}`)}, 1)
	terminal.display = terminal.display.WithWindowCursor(0)

	w := terminal.out.WindowBuffer().WindowAt(0)
	if w == nil {
		t.Fatal("tool window missing")
	}
	terminal.Update(KeyPressMsg(Key{Code: 'r'}))
	if w.MarkdownMode() {
		t.Error("r must be a no-op on tool windows")
	}
}

// TestMarkdownModeTableCellSplitAcrossDeltas reproduces the df-output bug:
// the tokenizer splits "/run/user/1001 |" into three deltas —
// "/run/user/", "1", "001 |". The middle delta ("1") does not start with
// '|' and merges into the open table row; without merge-aware tail
// tracking the open-table state was reset to false, so the final
// "001 |" went through the incremental path and got concatenated onto
// the rendered table row ("| … | /run/user/1 |001 |") instead of
// re-rendering the whole cell as "/run/user/1001 |".
func TestMarkdownModeTableCellSplitAcrossDeltas(t *testing.T) {
	styles := NewStyles(theme.DefaultTheme())
	r := &textRenderer{tag: tlv.TagAssistantT, mdMode: true}

	// A small df-style table, last row streamed with a split cell.
	deltas := []string{
		"| Filesystem | Size | Used | Avail | Use% | Mounted on |\n",
		"|---|---|---|---|---|---|\n",
		"| tmpfs | 9.4G | 3.0M | 9.4G | 1% | /run |\n",
		"| /dev/nvme0n1p5 | 916G | 423G | 447G | 49% | / |\n",
		"| tmpfs | 5.0M | 12K | 5.0M | 1% | /run/lock |\n",
		"| tmpfs | 9.4G | 5.8M | 9.4G | 1% | /run/user/",
		"1",
		"001 |\n",
	}
	for _, d := range deltas {
		r.AppendFromTLV(tlv.TagAssistantT, d)
		if _, ok := r.TryLineCount(80); !ok {
			r.BuildInner(80, false, styles)
		}
	}

	lines, _ := r.BuildInner(80, false, styles)
	rendered := joinVisualLines(lines)

	if !strings.Contains(rendered, "/run/user/1001") {
		t.Fatalf("last cell must render the full path /run/user/1001:\n%s", rendered)
	}
	if strings.Contains(rendered, "|001") {
		t.Fatalf("incremental concatenation leak: '|001' found in render:\n%s", rendered)
	}
	// The last row must be a proper 6-column record ending with the closing rule.
	if !strings.Contains(rendered, "│ /run/user/1001 │") {
		t.Fatalf("last row must end with the closing rule:\n%s", rendered)
	}
}

// TestMarkdownTableWideChars covers unicode handling in markdown table
// cells: CJK (width 2), ZWJ emoji clusters (👨‍👩‍👧‍👦), and combining
// marks (é). The rendering pipeline uses cellWidth for column
// sizing and ansi.Hardwrap (cluster-aware) for wrapping, so we expect:
//   - column widths sized by display columns (not byte length)
//   - overflow hard-wrapped, never truncated (no "…" at all)
//   - wrapping never splits mid-rune or mid-cluster (no U+FFFD artifacts)
//   - padding spaces compensate for wide chars
func TestMarkdownTableWideChars(t *testing.T) {
	styles := NewStyles(theme.DefaultTheme())

	// familyEmoji is the ZWJ family 👨‍👩‍👧‍👦 — 7 codepoints, 1 grapheme
	// cluster, 2 display cols. Used to verify cluster integrity in cells.
	familyEmoji := "\U0001F468\u200D\U0001F469\u200D\U0001F467\u200D\U0001F466"

	cases := []struct {
		name        string
		content     string
		width       int
		mustContain []string // substrings (post-stripANSI) the output must contain
		mustNotHave []string // substrings the output must NOT contain (e.g. U+FFFD)
	}{
		{
			name:    "CJK fits without truncation",
			content: "| col1 | col2 |\n|---|---|\n| 中文 | 内容 |\n| 日本語 | データ |",
			width:   40,
			mustContain: []string{
				"中文", "内容", "日本語", "データ",
			},
			mustNotHave: []string{"…", "\uFFFD"},
		},
		{
			// Was "CJK cell truncated cleanly": truncation is gone. The
			// cell now wraps across rows of the record, cluster-intact.
			name:    "CJK cell wraps cleanly instead of truncating",
			content: "| a | b |\n|---|---|\n| 中文内容测试更多内容 | x |",
			width:   20,
			// At width 20 the label column (1 cell) still leaves room, so the
			// record renders inline and the cell wraps across rows; it survives
			// as fragments. Full-content integrity is checked by
			// TestMarkdownTablesNeverTruncate.
			mustContain: []string{
				"中文内容测试", // label + value are one wrapped run
				"更多内容",
				"x",
			},
			mustNotHave: []string{
				"…",      // never truncated
				"\uFFFD", // no cluster-split artifacts
			},
		},
		{
			name:    "ZWJ family cluster survives intact in cell",
			content: "| a | b |\n|---|---|\n| " + familyEmoji + " | x |",
			width:   40,
			mustContain: []string{
				familyEmoji, // cluster preserved as a whole
			},
			mustNotHave: []string{"\uFFFD"}, // no broken cluster (U+FFFD only appears when a cluster is split)
		},
		{
			name:    "ZWJ family truncated (dropped whole, not split)",
			content: "| a | b |\n|---|---|\n| " + familyEmoji + "rest | x |",
			width:   10, // very narrow — forces truncation
			mustNotHave: []string{
				"\uFFFD", // never a broken cluster
				// The full family emoji would survive intact if it fits
				// — we only verify it's not broken when it gets cut.
			},
		},
		{
			name:    "combining mark stays attached",
			content: "| a | b |\n|---|---|\n| café résumé | x |",
			width:   20,
			mustContain: []string{
				"café", // combining acute preserved
			},
			mustNotHave: []string{"\uFFFD"},
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			r := &textRenderer{tag: tlv.TagAssistantT}
			r.AppendFromTLV(tlv.TagAssistantT, tt.content)
			r.ToggleMarkdownMode() // enable table formatting
			lines, _ := r.BuildInner(tt.width, false, styles)
			rendered := joinVisualLines(lines)
			plain := stripANSI(rendered)

			for _, want := range tt.mustContain {
				if !strings.Contains(plain, want) {
					t.Errorf("rendered output must contain %q:\n%s", want, rendered)
				}
			}
			for _, bad := range tt.mustNotHave {
				if strings.Contains(plain, bad) {
					t.Errorf("rendered output must NOT contain %q:\n%s", bad, rendered)
				}
			}
			// Every rendered line must respect the width budget.
			for _, line := range lines {
				if w := cellWidth(line.Text); w > tt.width {
					t.Errorf("line width %d exceeds budget %d: %q", w, tt.width, line.Text)
				}
			}
		})
	}
}

// TestMarkdownTableRowAlignment verifies that all rows in a rendered
// markdown table have the same display width — a basic correctness
// invariant the table renderer must preserve (even when individual
// cells contain wide chars / unicode clusters that affect display
// width differently from byte length). Tested at multiple widths
// because the shrink-to-fit path can introduce alignment bugs that
// don't show up when the table fits naturally.
func TestMarkdownTableRowAlignment(t *testing.T) {
	styles := NewStyles(theme.DefaultTheme())

	// Content with mixed CJK, ZWJ, combining, and VS-16 cells in the
	// same column to stress column-width calculations.
	content := "| input | display | issue |\n" +
		"|---|---|---|\n" +
		"| `é` (e + combining acute) | 1 cell | []rune gives 2 runes; iterating backward could include just the accent and drop th |\n" +
		"| family ZWJ emoji | 2 cells | []rune gives 7 runes; iterating backward could cut mid-cluster |\n" +
		"| ☺ VS-16 | 1 or 2 cells | VS-16 changes width; per-rune check may be wrong |"

	// Also test with a ZWJ family emoji and explicit VS-16 — the actual
	// unicode constructs from the docs comment about wide-char handling.
	content2 := "| input | display | issue |\n" +
		"|---|---|---|\n" +
		"| `é` (e + combining acute) | 1 cell | content A |\n" +
		"| 👨‍👩‍👧‍👦 (family ZWJ) | 2 cells | content B |\n" +
		"| ☺\uFE0F (text vs emoji style) | 1 or 2 cells | content C |"

	for i, content := range []string{content, content2} {
		r := &textRenderer{tag: tlv.TagAssistantT}
		r.AppendFromTLV(tlv.TagAssistantT, content)
		r.ToggleMarkdownMode()

		for _, width := range []int{200, 100, 80, 60, 40} {
			t.Run(fmt.Sprintf("case=%d/width=%d", i, width), func(t *testing.T) {
				lines, _ := r.BuildInner(width, false, styles)
				if len(lines) < 3 {
					t.Fatalf("expected at least 3 rows (header, delim, body), got %d", len(lines))
				}
				var expectedWidth int
				for i, line := range lines {
					w := cellWidth(line.Text)
					if i == 0 {
						expectedWidth = w
						continue
					}
					if w != expectedWidth {
						t.Errorf("row %d width %d != row 0 width %d\nrow 0: %q\nrow %d: %q",
							i, w, expectedWidth, lines[0].Text, i, line.Text)
					}
				}
			})
		}
	}
}
