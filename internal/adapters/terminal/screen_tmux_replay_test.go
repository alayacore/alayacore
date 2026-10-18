//go:build linux

package terminal

// The grid model in screen_grid_model_test.go stands in for a terminal in every
// frame-geometry test, so its fidelity is worth checking against the real thing
// rather than only asserting it in a comment. This file replays a recorded
// frame stream into tmux and compares the pane with the model's screen.
//
// It also settles a question that comes up whenever a display corrupts under a
// terminal multiplexer: whether the escape sequences a frame is split across
// several writes, and the multiplexer acts on a partial one. They are, and it
// does not. A frame is one Write call, but a pty holds a few kilobytes, so the
// kernel hands the multiplexer a frame in pieces and can put a boundary
// anywhere — inside a CUP, inside an SGR, inside an OSC. tmux's parser is a
// state machine that keeps its state across reads, and the 7-byte-chunk replay
// here lands on the same screen as the unchunked one. (A separate sweep split a
// sequence at every byte offset with a 700ms gap between the halves and saw the
// same thing; it is not checked in, because it is one tmux run per offset.) So
// a corrupted frame is the program's arithmetic, not the transport — which is
// what the geometry tests then go and find.
//
// What this cannot settle is the frame's synchronized update (DECSET 2026,
// docs/tui.md → Line Wrapping): the capture reads the pane's grid, which is
// updated whether or not the host defers painting, so a host that implements the
// mode and one that drops it compare equal here. The replay pins the bytes — both
// kinds of host end on the same screen — and the flicker's absence is left to a
// host that implements the mode.
//
// The comparison against the model is a different kind of claim and is treated
// as one: it puts this package's width table next to a terminal's, and for
// cluster-composed text those are two implementations of a moving standard.
// assertMatchesModel says which rows are asserted and which are only reported.
//
// Linux-only and skipped under -short, like tty_e2e_test.go: it spawns a tmux
// server and waits on paint timing. It runs on its own socket (-L), so a tmux
// the reader is working in is left alone.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alayacore/alayacore/internal/testenv"
)

func TestScreenMatchesATerminalUnderTmux(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns a tmux server and waits on paint timing")
	}
	// A box without a terminal multiplexer skips this test; `make test` is what
	// refuses to start without tmux, so a run that was meant to be complete
	// cannot lose the suite's only comparison against a real terminal (see
	// internal/testenv).
	tmux := testenv.RequireTool(t, "tmux", "this test's reference terminal")
	version := tmuxVersion(t, tmux)

	const width, height = 32, 24
	h := newFrameHarness(t, width, height)
	h.step("empty")
	// The clusters whose width a library could get wrong, each on a row of
	// its own so a disagreement shows up as a misplaced row rather than a
	// difference of opinion about one glyph: a keycap (ASCII-led, two cells),
	// an ASCII base with a variation selector, a ZWJ family (one cluster, two
	// cells), a regional-indicator pair, CJK, and the box-drawing and braille
	// glyphs the chrome is drawn with (East-Asian Ambiguous, one cell).
	h.appendWin(tagAssistantText, "w1",
		"步骤如下：\n1️⃣ 第一步：安装依赖并确认环境变量已经正确设置好了\n2️⃣ 第二步：运行测试\n3️⃣ 第三步：部署上线\n")
	h.step("keycaps")
	h.appendWin(tagAssistantText, "w2",
		"widths: 1️⃣ a\uFE0F 👨\u200d👩\u200d👧\u200d👦 🇨🇳 中文 ─── ⠿ ✓ ✅\uFE0F\n"+
			"a row that runs past the pane width so the terminal has to soft-wrap it, "+
			"and a second wrapped row to make the run span two terminal rows\n")
	h.step("disputed clusters")
	// Ill-formed bytes, one shape per row so a disagreement is a misplaced row
	// rather than an argument about one glyph: a lone continuation byte, a 3-byte
	// encoding cut short, a lone C1 control that an escape parser would mistake for
	// an introducer, a byte that cannot appear in UTF-8, and an overlong encoding.
	// No producer sends these — every frame's text reaches the adapter out of a
	// json.Unmarshal or a json.Marshal, and both replace ill-formed bytes — so what
	// this row checks is the invariant at the far end of them: the bytes the adapter
	// emits are well-formed, and the pane tmux paints from them is the pane the grid
	// model predicted. Without sanitizeUTF8 the raw bytes would go out, tmux would
	// substitute for them on its own rules, and the two would differ.
	h.appendWin(tagAssistantText, "w3",
		"ill-formed: a\x80b\n"+
			"truncated: a\xe4\xb8z\n"+
			"lone C1: a\x9b[m b\n"+
			"not utf8: a\xf5b a\xfe\xffb\n"+
			"overlong: a\xc0\x80b\n")
	h.step("ill-formed bytes")
	for i, tc := range []struct{ name, input string }{
		{"ls", "ls -la"},
		{"execute_command", "cd /home/wallace/playground/alayacore && go test ./internal/adapters/terminal/ -run Test -v"},
		{"search_content", "pattern"},
	} {
		h.m.out.Write(toolCallFrames(fmt.Sprintf("t%d", i), tc.name, tc.input, "ok"))
		h.m.out.FlushPendingDeltas()
		h.step("tool " + tc.name)
	}
	h.key('l', ModCtrl) // the model selector on a pane narrower than its box
	h.step("overlay")
	h.key(KeyEsc, 0)
	h.step("overlay closed")
	for i := 0; i < 5; i++ {
		h.key('k', 0)
	}

	var stream []byte
	for _, b := range h.written {
		stream = append(stream, b...)
	}
	// The alt screen, which is where this program draws.
	stream = append([]byte("\x1b[?1049h"), stream...)

	// Two deliveries of the same stream: whole, and in 7-byte writes, which
	// put a boundary inside nearly every sequence a frame contains.
	whole := replayInTmux(t, tmux, width, height, stream, 0)
	chunked := replayInTmux(t, tmux, width, height, stream, 7)

	// Claim 1, the transport one: splitting a frame across writes cannot
	// change the screen. Both sides are the same tmux on the same runner, so
	// whatever its Unicode tables and its capture format do cancels out — this
	// assertion holds on any tmux, which is why it is the one that is hard.
	if d := compareLines(whole, chunked); d != "" {
		t.Errorf("%s: splitting the frame across 7-byte writes changed the screen:\n%s", version, d)
	}

	// Claim 2, the oracle one: the grid model predicts what a terminal shows.
	assertMatchesModel(t, version, h.grid.text(), whole)
}

// tmuxVersion returns the banner of the tmux the test is about to trust, so a
// disagreement names the build it disagreed with.
func tmuxVersion(t *testing.T, tmux string) string {
	t.Helper()
	out, err := exec.Command(tmux, "-V").Output()
	if err != nil {
		return "tmux (version unknown)"
	}
	return strings.TrimSpace(string(out))
}

// assertMatchesModel compares a captured pane with the grid model's screen.
//
// Rows carrying a cluster whose width a HOST is measured to bill differently are
// not asserted, only reported (hostMayBillDifferently; host_width_test.go ties
// its list to the probe). Their width depends on whether the host applies UAX #29
// clustering and the emoji-presentation rule, or falls back to wcwidth per code
// point — two answers that are both defensible and that different terminal
// builds give. This test was written against tmux 3.7c, where a keycap is one
// 2-cell cluster; on the tmux 3.4 that ubuntu-latest ships, the same bytes come
// back as a 2-cell "1"+VS16 followed by a separate combining keycap, so five
// rows differed and the build went red over a fact about somebody else's Unicode
// tables. Everything else — ASCII, CJK, box drawing, braille — is asserted, and
// those are the glyphs the app draws. The flags and the ZWJ family are not: the
// waiver covers any row carrying a regional indicator or a joiner.
//
// The consequence is not just a cell: a row the layout counts as one terminal
// row is one cell wider to such a host, so the HOST wraps it onto the row below,
// and the frame's rows below it move. That is the frame's problem and it is
// contained rather than fixed (docs/tui.md → Line Wrapping, and the closure in
// screen.go → diffFrameRows); what is not fixable by any width table is the
// cluster's own cell count, and no table can be the host's.
func assertMatchesModel(t *testing.T, version string, want, got []string) {
	t.Helper()
	var unexplained, hostDependent []string
	for i := 0; i < len(want) || i < len(got); i++ {
		var w, g string
		if i < len(want) {
			w = want[i]
		}
		if i < len(got) {
			g = got[i]
		}
		if w == g {
			continue
		}
		row := fmt.Sprintf("  row %d:\n    model: %q\n    tmux:  %q", i, w, g)
		if hostMayBillDifferently(w) || hostMayBillDifferently(g) {
			hostDependent = append(hostDependent, row)
			continue
		}
		unexplained = append(unexplained, row)
	}
	if len(unexplained) > 0 {
		t.Errorf("%s shows a different screen than the grid model predicts:\n%s",
			version, strings.Join(unexplained, "\n"))
	}
	if len(hostDependent) > 0 {
		t.Logf("%s bills %d row(s) of cluster-composed text differently than this package's table does, so the app's rows would land a cell off there; the model is not judged on them:\n%s",
			version, len(hostDependent), strings.Join(hostDependent, "\n"))
	}
}

// replayInTmux feeds stream to a pane of the given size and returns the pane's
// text. chunk > 0 writes the stream in fixed-size pieces through dd, which is
// what puts a write boundary inside an escape sequence.
func replayInTmux(t *testing.T, tmux string, width, height int, stream []byte, chunk int) []string {
	t.Helper()
	return replayInTmuxCapture(t, tmux, width, height, stream, chunk, false)
}

// replayInTmuxJoined is replayInTmux with `capture-pane -J`, which joins the
// rows the pane considers ONE logical (soft-wrapped) line. Two captures of the
// same stream therefore answer a second question: whether a write form leaves a
// wrapped line joined (screen_tmux_join_test.go) or splits it into hard rows.
func replayInTmuxJoined(t *testing.T, tmux string, width, height int, stream []byte) []string {
	t.Helper()
	return replayInTmuxCapture(t, tmux, width, height, stream, 0, true)
}

func replayInTmuxCapture(t *testing.T, tmux string, width, height int, stream []byte, chunk int, join bool) []string {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "stream.bin")
	if err := os.WriteFile(src, stream, 0o600); err != nil {
		t.Fatal(err)
	}
	done := filepath.Join(dir, "done")
	// The socket file goes in this test's own directory, not tmux's default
	// (/tmp/tmux-<uid>): kill-server stops the server but leaves the file behind.
	// Short on purpose — a socket path is capped near 108 bytes.
	sockDir, err := os.MkdirTemp("", "alaya-replay-")
	if err != nil {
		t.Fatal(err)
	}
	// Registered before the kill-server cleanup below, so the server goes first.
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })
	// A name of its own, fresh per run. It was filepath.Base(dir), which is the
	// subtest's "001" — the same name every run, over a socket file that was
	// never removed. That is the reuse the caret e2e avoids by naming its socket
	// with the pid and the clock.
	socket := fmt.Sprintf("alaya-replay-%d-%d", os.Getpid(), time.Now().UnixNano())
	// TMUX and TMUX_PANE are cleared for the same reason the caret e2e clears
	// them: these checks run under whatever shell the reader used.
	env := append(os.Environ(), "TMUX=", "TMUX_PANE=", "TMUX_TMPDIR="+sockDir)

	// Every path in the pane's command is quoted. The command is a shell string
	// and the paths carry the test's name (t.TempDir() does), so a subtest name
	// with a space or a parenthesis would otherwise end the command early: the
	// pane's shell exits, the session ends, the server exits, and this harness
	// waits out its deadline instead of failing where the mistake is.
	cat := "cat " + shellQuote(src)
	if chunk > 0 {
		cat = fmt.Sprintf("dd if=%s bs=%d 2>/dev/null", shellQuote(src), chunk)
	}
	pane := fmt.Sprintf("%s; touch %s; exec sleep 30", cat, shellQuote(done))

	// A socket of its own: the reader's tmux, if any, is not this test's to
	// kill.
	run := func(verb ...string) *exec.Cmd {
		cmd := exec.Command(tmux, append([]string{"-L", socket}, verb...)...)
		cmd.Env = env
		return cmd
	}
	if out, err := run("new-session", "-d", "-s", "replay",
		"-x", fmt.Sprint(width), "-y", fmt.Sprint(height), "sh", "-c", pane).CombinedOutput(); err != nil {
		t.Fatalf("tmux new-session: %v\n%s", err, out)
	}
	t.Cleanup(func() {
		_ = run("kill-server").Run()
	})

	deadline := time.Now().Add(30 * time.Second)
	for {
		if _, err := os.Stat(done); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the pane never finished writing the stream")
		}
		time.Sleep(20 * time.Millisecond)
	}
	// The pane has the bytes; give the server its redraw.
	time.Sleep(300 * time.Millisecond)

	args := []string{"capture-pane", "-p"}
	if join {
		args = append(args, "-J")
	}
	args = append(args, "-t", "replay")
	out, err := run(args...).Output()
	if err != nil {
		t.Fatalf("tmux capture-pane: %v", err)
	}
	lines := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " ")
	}
	return lines
}

// compareLines renders a line-by-line difference between the modeled screen
// and the captured one.
func compareLines(want, got []string) string {
	var b strings.Builder
	for i := 0; i < len(want) || i < len(got); i++ {
		var w, g string
		if i < len(want) {
			w = want[i]
		}
		if i < len(got) {
			g = got[i]
		}
		if w != g {
			fmt.Fprintf(&b, "  row %d:\n    model: %q\n    tmux:  %q\n", i, w, g)
		}
	}
	return b.String()
}

// shellQuote wraps s in single quotes for a shell command, escaping any single
// quote inside it, so a path with a space, parenthesis or quote is one word.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
