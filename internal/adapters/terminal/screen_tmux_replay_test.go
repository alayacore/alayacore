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
)

func TestScreenMatchesATerminalUnderTmux(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns a tmux server and waits on paint timing")
	}
	tmux, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux is not installed")
	}

	const width, height = 32, 24
	h := newFrameHarness(t, width, height)
	h.step("empty")
	// The clusters whose width a library could get wrong, each on a row of
	// its own so a disagreement shows up as a misplaced row rather than a
	// difference of opinion about one glyph: a keycap (ASCII-led, two cells),
	// an ASCII base with a variation selector, a ZWJ family (one cluster, two
	// cells), a regional-indicator pair, CJK, and the box-drawing and braille
	// glyphs the chrome is drawn with (East-Asian Ambiguous, one cell).
	h.appendWin(tlvAssistantText, "w1",
		"步骤如下：\n1️⃣ 第一步：安装依赖并确认环境变量已经正确设置好了\n2️⃣ 第二步：运行测试\n3️⃣ 第三步：部署上线\n")
	h.step("keycaps")
	h.appendWin(tlvAssistantText, "w2",
		"widths: 1️⃣ a\uFE0F 👨\u200d👩\u200d👧\u200d👦 🇨🇳 中文 ─── ⠿ ✓ ✅\uFE0F\n"+
			"a row that runs past the pane width so the terminal has to soft-wrap it, "+
			"and a second wrapped row to make the run span two terminal rows\n")
	h.step("disputed clusters")
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

	// The chunked replay: 7-byte writes put a boundary inside nearly every
	// sequence a frame contains.
	for _, tc := range []struct {
		name  string
		chunk int
	}{
		{"one write", 0},
		{"7-byte chunks", 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := replayInTmux(t, tmux, width, height, stream, tc.chunk)
			want := h.grid.text()
			if diff := compareLines(want, got); diff != "" {
				t.Errorf("tmux shows a different screen than the grid model predicts:\n%s", diff)
			}
		})
	}
}

const tlvAssistantText = "AT"

// replayInTmux feeds stream to a pane of the given size and returns the pane's
// text. chunk > 0 writes the stream in fixed-size pieces through dd, which is
// what puts a write boundary inside an escape sequence.
func replayInTmux(t *testing.T, tmux string, width, height int, stream []byte, chunk int) []string {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "stream.bin")
	if err := os.WriteFile(src, stream, 0o600); err != nil {
		t.Fatal(err)
	}
	done := filepath.Join(dir, "done")
	socket := filepath.Base(dir)

	cat := "cat " + src
	if chunk > 0 {
		cat = fmt.Sprintf("dd if=%s bs=%d 2>/dev/null", src, chunk)
	}
	pane := fmt.Sprintf("%s; touch %s; exec sleep 30", cat, done)

	// A socket of its own: the reader's tmux, if any, is not this test's to
	// kill.
	run := func(verb ...string) *exec.Cmd {
		return exec.Command(tmux, append([]string{"-L", socket}, verb...)...)
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

	out, err := run("capture-pane", "-p", "-t", "replay").Output()
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
