//go:build linux

package main_test

// Where the caret is, judged by a terminal rather than by this package.
//
// The prompt scrolls horizontally: a line wider than the field shows a window of
// itself, and two pieces of arithmetic have to agree about which window — the one
// the caret is drawn in, and the one the text is drawn from. InputField decides
// both from a prefix sum over the line's clusters (ensureCursorVisible,
// CursorCell, buildVisibleText), and a disagreement is not a wrong number so much
// as a caret that sits on a character other than the one it edits.
//
// That agreement is checked from the inside too: cursor_probe_test.go sweeps
// 83,712 states and requires the single pass to land where the walks it replaced
// landed. What an internal comparison cannot say is whether any of it survives the
// rest of the way out — the field's View, the window layout, the Screen's row
// diff, and a terminal's own width table. So this file drives the built binary
// inside tmux and asks the terminal where the caret is (#{cursor_x}) and what it
// drew there (capture-pane), then requires the two to describe the same window.
//
// What it asserts is deliberately relational. The field's text origin is learned
// from a Home keystroke and its width from an ASCII calibration line, rather than
// assumed, because a hard-coded column would make this a test of the current
// chrome. What is then required of every
// caret position is: the caret is shown; it is inside the field's cells; its
// column lands on a character boundary; the text drawn from the field's origin is
// exactly the window that column implies; and walking the caret left never moves
// the drawn caret right. Those hold for any scroll policy, so they pin the two
// arithmetics to each other without pinning either to an implementation.
//
// The alphabet is ASCII and CJK ideographs, and that is a limit rather than an
// oversight. screen_tmux_replay_test.go documents that this program's width table
// and a terminal's are two implementations of a moving standard, and that they
// disagree about keycaps and cluster-composed emoji. A disagreement there would
// fail this test for a reason that has nothing to do with the caret, so the
// characters here are the ones UAX #11 calls Wide and every tmux this could run
// under draws as two cells.
//
// Text arrives as a bracketed paste rather than as keystrokes. A paste accumulates
// bytes and decodes once at the end, so no read boundary can split a character in
// it — which keeps a failure here about the caret. The keystroke path, where a
// boundary inside a multi-byte encoding used to put a U+FFFD per byte in the
// field, is case 5 of TestEndToEndOverPty.
//
// What this cannot see is a window drawn *wider* than the field. The field's text
// starts at the pane's first column and runs to its last, so cells past the field's
// width wrap to the next terminal row and the row this test reads looks exactly as
// it should. Under-fill, a wrong window start, and a caret that disagrees with the
// text around it are all visible here; over-fill is not, and is left to the checks
// that read the field's own output rather than a terminal's — TestInputFieldGraphemeWidth
// and TestInputFieldFuzzInvariants, both of which fail on a window one cell too wide.
//
// Linux-only and skipped under -short, like the other two checks that need a real
// terminal. It runs tmux on its own socket (-L), so a tmux the reader is working
// in is left alone; only that socket's server is killed, on cleanup.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// tmuxProgram is one run of the program inside a tmux pane, on a socket nothing
// else uses.
type tmuxProgram struct {
	tb      *testing.T
	tmux    string
	socket  string
	session string
	dir     string
}

// startProgramInTmux launches the binary in a detached tmux pane of the given size
// and waits for the prompt, mirroring what startProgram waits for over a pty.
func startProgramInTmux(tb *testing.T, tmux, binary string, width, height int) *tmuxProgram {
	tb.Helper()
	p := &tmuxProgram{
		tb:   tb,
		tmux: tmux,
		// A socket of its own, and a fresh one per instance: the cases run in
		// sequence and each kills its server on cleanup, so reusing a name would
		// let a new-session land on a server that is still exiting.
		socket:  fmt.Sprintf("alaya-caret-%d-%d", os.Getpid(), time.Now().UnixNano()),
		session: "caret",
		dir:     tb.TempDir(),
	}
	tb.Cleanup(p.close)

	// tmux takes the pane's command as one argument and then splits it again with
	// its own rules, which are not a shell's — quoted paths come through with their
	// quotes still on them. So the environment is set by a launcher script that /bin/sh
	// does parse, and tmux is handed the script's path, which has nothing to split.
	// HOME is empty and private: startProgram does the same over a pty, so a
	// reader's own configuration cannot decide what this test starts up against.
	launch := filepath.Join(p.dir, "launch.sh")
	script := "#!/bin/sh\nexec env HOME=" + shellQuote(p.dir) + " TERM=xterm-256color " + shellQuote(binary) + "\n"
	if err := os.WriteFile(launch, []byte(script), 0o700); err != nil {
		tb.Fatalf("writing the pane's launcher: %v", err)
	}
	p.run("new-session", "-d", "-x", strconv.Itoa(width), "-y", strconv.Itoa(height),
		"-s", p.session, launch)

	if !p.waitPrompt(15 * time.Second) {
		p.close()
		tb.Fatalf("the TUI never painted the prompt in tmux; the pane reads:\n%s", p.pane())
	}
	p.settle(400*time.Millisecond, 10*time.Second)
	return p
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// run invokes tmux on this program's socket and returns its output. TMUX and
// TMUX_PANE are cleared: tmux refuses to start a server from inside a session it
// thinks it would have to nest in, and these checks run under whatever shell the
// reader used. -L already puts the server on a socket of its own.
func (p *tmuxProgram) run(args ...string) string {
	p.tb.Helper()
	cmd := exec.Command(p.tmux, append([]string{"-L", p.socket}, args...)...)
	cmd.Env = append(os.Environ(), "TMUX=", "TMUX_PANE=")
	out, err := cmd.CombinedOutput()
	if err != nil {
		p.tb.Fatalf("tmux %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// close kills this socket's server and nothing else. The reader's tmux, if any, is
// on the default socket.
func (p *tmuxProgram) close() {
	cmd := exec.Command(p.tmux, "-L", p.socket, "kill-server")
	cmd.Env = append(os.Environ(), "TMUX=", "TMUX_PANE=")
	_ = cmd.Run() // already dead when a test failed early; not an error worth having
}

func (p *tmuxProgram) waitPrompt(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if strings.Contains(p.pane(), promptMark) {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

// pane is the whole visible screen, as tmux draws it.
func (p *tmuxProgram) pane() string {
	return p.run("capture-pane", "-p", "-t", p.session)
}

// rowAt is one screen row, 0-indexed the way #{cursor_y} is.
func (p *tmuxProgram) rowAt(y int) string {
	p.tb.Helper()
	rows := strings.Split(strings.TrimSuffix(p.pane(), "\n"), "\n")
	if y < 0 || y >= len(rows) {
		p.tb.Fatalf("the pane has %d rows; there is no row %d", len(rows), y)
	}
	return rows[y]
}

// caret is where the terminal thinks the cursor is. A frame that does not move it
// leaves the previous reading in place, which is the point: the terminal's idea of
// the caret is the current one, not this frame's.
func (p *tmuxProgram) caret() (x, y int, shown bool) {
	p.tb.Helper()
	out := strings.TrimSpace(p.run("display-message", "-p", "-t", p.session,
		"#{cursor_x} #{cursor_y} #{cursor_flag}"))
	f := strings.Fields(out)
	if len(f) != 3 {
		p.tb.Fatalf("tmux reported the cursor as %q", out)
	}
	x, err := strconv.Atoi(f[0])
	if err != nil {
		p.tb.Fatalf("cursor_x %q: %v", f[0], err)
	}
	y, err = strconv.Atoi(f[1])
	if err != nil {
		p.tb.Fatalf("cursor_y %q: %v", f[1], err)
	}
	return x, y, f[2] == "1"
}

// reading is the caret and the row it sits on, taken so that they describe one
// frame. The two come from separate tmux invocations and the program repaints on a
// tick of its own, so the caret is read again afterwards and the pair is retaken if
// it moved in between — a row and a caret from different frames would report a
// disagreement between them that neither has.
func (p *tmuxProgram) reading() (x, y int, shown bool, row string) {
	p.tb.Helper()
	for attempt := 0; attempt < 20; attempt++ {
		x, y, shown = p.caret()
		row = p.rowAt(y)
		x2, y2, shown2 := p.caret()
		if x == x2 && y == y2 && shown == shown2 {
			return x, y, shown, row
		}
	}
	p.tb.Fatal("the caret did not hold still long enough to read the row it sits on")
	return 0, 0, false, ""
}

// settle waits for the pane and the caret to stop changing, so a reading is taken
// from a frame the program finished painting.
func (p *tmuxProgram) settle(quiet, limit time.Duration) {
	p.tb.Helper()
	deadline := time.Now().Add(limit)
	var last string
	still := time.Duration(0)
	for time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
		x, y, shown := p.caret()
		now := fmt.Sprintf("%s|%d,%d,%v", p.pane(), x, y, shown)
		if now == last {
			still += 50 * time.Millisecond
			if still >= quiet {
				return
			}
			continue
		}
		still, last = 0, now
	}
	p.tb.Logf("the pane never went quiet within %v; readings may be mid-frame", limit)
}

// paste delivers text as a bracketed paste, through a file so the bytes are exact.
func (p *tmuxProgram) paste(text string) {
	p.tb.Helper()
	f := filepath.Join(p.dir, "paste")
	if err := os.WriteFile(f, []byte(text), 0o600); err != nil {
		p.tb.Fatalf("writing the paste buffer: %v", err)
	}
	p.run("load-buffer", "-b", "caret", f)
	p.run("paste-buffer", "-p", "-b", "caret", "-t", p.session)
}

// key sends named keys — Left, Home, End — as tmux spells them.
func (p *tmuxProgram) key(names ...string) {
	p.tb.Helper()
	p.run(append([]string{"send-keys", "-t", p.session}, names...)...)
}

// TestCaretAndTextAgreeAboutTheWindowUnderTmux walks the caret across lines wider
// than the field and requires the caret's column and the text drawn around it to
// describe one window.
func TestCaretAndTextAgreeAboutTheWindowUnderTmux(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns a tmux server and waits on paint timing")
	}
	tmux, err := exec.LookPath("tmux")
	if err != nil {
		// The disposition screen_tmux_replay_test.go makes: a skip on a developer's
		// box, a failure in CI, where the workflow installs tmux as the reference
		// terminal and `go test ./...` runs without -v, so a skip would be invisible.
		if os.Getenv("CI") != "" {
			t.Fatalf("tmux is not installed, but the workflow installs it as this test's reference terminal (see .github/workflows/test.yml): %v", err)
		}
		t.Skip("tmux is not installed")
	}
	binary := buildProgram(t)

	const width, height = 80, 24
	// The field's width in cells, learned once. It is a property of the pane and
	// its chrome rather than of any line, and it cannot be read off a case's own
	// Home row: a window is filled with whole characters, so the cells a Home row
	// shows reach the field's width only when no character can straddle the right
	// edge. ASCII always reaches it; a line mixing one- and two-cell characters
	// draws 79 cells in an 80-cell field, and taking that for the width makes every
	// window in the case one character too short.
	w := learnFieldWidth(t, tmux, binary, width, height)

	for _, tc := range []struct{ name, line string }{
		// 300 cells, so the field scrolls and every cell is one byte.
		{"ascii", strings.Repeat("0123456789", 30)},
		// 204 cells in 102 runes: a cell offset and a rune index are different
		// numbers here, which is the confusion this case exists to catch.
		{"cjk", strings.Repeat("你好世界中文", 17)},
		// 180 cells in 120 runes, alternating one- and two-cell characters, so the
		// window's right edge falls mid-pattern and on both parities.
		{"mixed", strings.Repeat("a你b好c世", 20)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := startProgramInTmux(t, tmux, binary, width, height)
			defer p.close()

			p.paste(tc.line)
			p.settle(400*time.Millisecond, 10*time.Second)
			line := []rune(tc.line)
			total := cellsBefore(t, line, len(line))

			// Home is the origin: there the visible start is 0 and the caret is in
			// the field's first cell, so the column the caret is drawn at is the
			// column the field's text starts at. It is read rather than assumed
			// because the chrome to its left is not this test's business.
			p.key("Home")
			p.settle(300*time.Millisecond, 5*time.Second)
			hx, hy, shown, homeRow := p.reading()
			if !shown {
				t.Fatal("the caret is not shown with the field focused and text in it")
			}
			origin, fieldRow := hx, hy
			// Home must draw the window that fills the field as far as whole
			// characters allow. This is the same rule check applies at every other
			// position, and it is what confirms the learned width against this case's
			// own chrome rather than trusting the calibration blindly.
			if got, want := strings.TrimRight(sliceFromCell(t, homeRow, origin), " "), windowAt(t, line, 0, w); got != want {
				t.Fatalf("at Home the field drew %q; a %d-cell window from rune 0 is %q", got, w, want)
			}
			if total <= w {
				t.Fatalf("the line is %d cells and the field is %d: this case cannot scroll, so it cannot test what it is for", total, w)
			}

			// check requires the caret and the row it sits on to describe the window
			// of line the caret is at, and returns the caret's cell so a walk can
			// require monotonicity of it.
			check := func(label string, pos int) int {
				t.Helper()
				x, y, shown, row := p.reading()
				if !shown {
					t.Fatalf("%s: the caret is not shown", label)
				}
				if y != fieldRow {
					t.Errorf("%s: the caret is on row %d, but the field's row is %d", label, y, fieldRow)
				}
				cell := x - origin
				if cell < 0 || cell > w {
					t.Errorf("%s: the caret is at cell %d of a %d-cell field", label, cell, w)
					return cell
				}
				// The window the caret's column implies: the cells drawn before the
				// caret are the cells between the window's start and pos, so the
				// window starts that many cells back from pos.
				startCells := cellsBefore(t, line, pos) - cell
				start := runeIndexAtCell(t, line, startCells)
				if start < 0 {
					t.Errorf("%s: the caret's cell %d does not land on a character boundary; the window would have to start %d cells in",
						label, cell, startCells)
					return cell
				}
				if start > pos {
					t.Errorf("%s: the window starts at rune %d, past the caret at %d", label, start, pos)
					return cell
				}
				want := windowAt(t, line, start, w)
				got := strings.TrimRight(sliceFromCell(t, row, origin), " ")
				if got != want {
					t.Errorf("%s: the caret is at cell %d, so the field must show the window from rune %d:\n"+
						"\tdrawn:   %q\n"+
						"\timplies: %q", label, cell, start, got, want)
				}
				return cell
			}

			// Leftward from the end. Every position is a fresh scroll decision, and
			// the drawn caret may hold still or move left as the window follows it —
			// but it may never move right, which is the one direction a caret cannot
			// go when the key pressed was Left.
			p.key("End")
			p.settle(300*time.Millisecond, 5*time.Second)
			end := check("at End", len(line))
			// At End the window must run to the line's end. A caret after the last
			// character with characters still hidden to its right is a caret whose
			// typing the user cannot see.
			startCells := total - end
			if start := runeIndexAtCell(t, line, startCells); start < 0 {
				t.Errorf("at End the caret's cell %d implies a window starting %d cells in, which is not a character boundary", end, startCells)
			} else {
				drawn, rest := windowAt(t, line, start, w), string(line[start:])
				if drawn != rest {
					t.Errorf("at End the window stops before the line does: %d cells drawn of a %d-cell tail",
						cellWidthOf(t, drawn), cellWidthOf(t, rest))
				}
				if cellWidthOf(t, rest) != end {
					t.Errorf("at End the caret is at cell %d but the tail it follows is %d cells wide", end, cellWidthOf(t, rest))
				}
			}

			prev, pos := end, len(line)
			for _, target := range samplePositions(t, line, w) {
				for i := 0; i < pos-target; i++ {
					p.key("Left")
				}
				if pos != target {
					p.settle(250*time.Millisecond, 5*time.Second)
				}
				pos = target
				cell := check(fmt.Sprintf("after Left to position %d", pos), pos)
				if cell > prev {
					t.Errorf("position %d: the caret moved right (cell %d → %d) in response to Left", pos, prev, cell)
				}
				prev = cell
			}
			if pos != 0 {
				t.Fatalf("the walk ended at position %d, not 0", pos)
			}
			if prev != 0 {
				t.Errorf("at position 0 the caret is at cell %d; the window's first cell is 0", prev)
			}

			// Rightward, a few positions: the mirror direction, where the caret may
			// hold still or move right but never left.
			prev = 0
			for _, target := range []int{1, 2, w / 2, len(line) / 2, len(line) - 1, len(line)} {
				if target <= pos || target > len(line) {
					continue
				}
				for i := 0; i < target-pos; i++ {
					p.key("Right")
				}
				p.settle(250*time.Millisecond, 5*time.Second)
				pos = target
				cell := check(fmt.Sprintf("after Right to position %d", pos), pos)
				if cell < prev {
					t.Errorf("position %d: the caret moved left (cell %d → %d) in response to Right", pos, prev, cell)
				}
				prev = cell
			}
		})
	}
}

// learnFieldWidth returns how many cells the prompt's field gives its text, by
// putting a line of ASCII in it and reading what Home draws. ASCII because a
// window is filled with whole characters: with one-cell characters the drawn window
// reaches the field's width exactly, and with wider ones it stops short by up to a
// character and says nothing about the width it stopped inside.
func learnFieldWidth(t *testing.T, tmux, binary string, width, height int) int {
	t.Helper()
	p := startProgramInTmux(t, tmux, binary, width, height)
	defer p.close()

	// Longer than the pane, so Home's window is limited by the field and not by the
	// line it is a window of.
	p.paste(strings.Repeat("x", width*4))
	p.settle(400*time.Millisecond, 10*time.Second)
	p.key("Home")
	p.settle(300*time.Millisecond, 5*time.Second)

	x, _, shown, row := p.reading()
	if !shown {
		t.Fatal("the caret is not shown while measuring the field's width")
	}
	// At Home the caret sits in the field's first cell, so its column is the field's
	// text origin and the row from that origin is the window.
	w := cellWidthOf(t, strings.TrimRight(sliceFromCell(t, row, x), " "))
	if w <= 0 {
		t.Fatalf("measuring the field's width drew nothing from column %d: %q", x, row)
	}
	if w != width {
		t.Logf("the field gives its text %d cells in a %d-column pane; the difference is chrome, and the cases use this number rather than the pane's", w, width)
	}
	return w
}

// samplePositions is the set of caret positions a leftward walk visits: the ends,
// the neighborhood of the point where a window first has to scroll — measured
// both from the line's start and from its end — and a spread across the rest.
//
// Positions are rune indices and the neighborhoods are counted in cells, because
// for a line of wide characters the two are not the same numbering: 80 cells is 80
// runes of ASCII and 40 of CJK. A cell offset that falls inside a character has no
// rune index and is skipped, which for an all-wide line is every other offset.
// Duplicates collapse and the result is descending, the order the walk needs.
func samplePositions(tb *testing.T, line []rune, w int) []int {
	tb.Helper()
	n := len(line)
	total := cellsBefore(tb, line, n)
	seen := make(map[int]bool, n)
	var out []int
	add := func(p int) {
		if p < 0 {
			return
		}
		p = max(0, min(n, p))
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	for _, p := range []int{n, n - 1, n - 2, n - 3, 3, 2, 1, 0} {
		add(p)
	}
	for _, c := range []int{w - 2, w - 1, w, w + 1, w + 2} {
		add(runeIndexAtCell(tb, line, c))
		add(runeIndexAtCell(tb, line, total-c))
	}
	for i := 1; i <= 12; i++ {
		add(n * i / 13)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] > out[j] })
	return out
}

// cellWidthOf is this test's own width model, and it is only ever asked about the
// two alphabets the cases above are built from: ASCII, one cell, and the CJK
// ideographs of the U+4E00 block, two cells in UAX #11 and two cells in every tmux
// this could run under. Anything else is a failure of the test rather than of the
// program, and says so.
func cellWidthOf(tb *testing.T, s string) int {
	tb.Helper()
	n := 0
	for _, r := range s {
		n += runeWidth(tb, r)
	}
	return n
}

// cellsBefore is the cells line[:pos] draws.
func cellsBefore(tb *testing.T, line []rune, pos int) int {
	tb.Helper()
	return cellWidthOf(tb, string(line[:pos]))
}

func runeWidth(tb *testing.T, r rune) int {
	tb.Helper()
	switch {
	case r < 0x80:
		return 1
	case r >= 0x4E00 && r <= 0x9FFF:
		return 2
	}
	tb.Fatalf("this test's width model does not cover %q (U+%04X); it is only meaningful over alphabets a terminal and this program cannot disagree about", r, r)
	return 0
}

// sliceFromCell returns s from a cell offset onward. A byte or rune index would be
// wrong for the CJK cases, where the offset counts cells and the text does not.
func sliceFromCell(tb *testing.T, s string, cell int) string {
	tb.Helper()
	n := 0
	for i, r := range s {
		if n >= cell {
			return s[i:]
		}
		n += runeWidth(tb, r)
	}
	if n == cell {
		return ""
	}
	tb.Fatalf("column %d is not a character boundary in %q", cell, s)
	return ""
}

// runeIndexAtCell returns the rune index whose prefix is exactly cells wide, or -1
// when no rune starts there. A caret drawn at a column that is not a boundary is a
// failure the caller reports, not something to round away.
func runeIndexAtCell(tb *testing.T, line []rune, cells int) int {
	tb.Helper()
	n := 0
	for i, r := range line {
		if n == cells {
			return i
		}
		n += runeWidth(tb, r)
	}
	if n == cells {
		return len(line)
	}
	return -1
}

// windowAt is the text a field of w cells shows when its window starts at rune
// index start: whole characters, up to the last one that fits. It is the rule
// buildVisibleText states, restated here independently — a test that called the
// program's own function would be comparing it with itself.
func windowAt(tb *testing.T, line []rune, start, w int) string {
	tb.Helper()
	if start < 0 || start > len(line) {
		tb.Fatalf("window start %d is outside a line of %d runes", start, len(line))
	}
	var b strings.Builder
	cells := 0
	for i := start; i < len(line); i++ {
		cw := runeWidth(tb, line[i])
		if cells+cw > w {
			break
		}
		b.WriteRune(line[i])
		cells += cw
	}
	return b.String()
}
