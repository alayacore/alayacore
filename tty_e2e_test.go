//go:build linux

// End-to-end checks against the built binary, driven over a real pty.
//
// Everything else in this tree that tests input talks to internals: a parser fed
// a byte slice, a model fed a message. This file talks to the program the way a
// terminal does — bytes in one end, the painted frame back out the other — and
// asks the only question a user can ask: is the text I pasted on the screen?
//
// That distance is the point. Two of the defects behind this file were invisible
// to reasoning about the code, because the code was correct about the thing it was
// written to do: `InputField` was right that an unfocused box ignores input, and
// `consumeEscape` was right that an unterminated introducer is held. What was
// wrong was an assumption each made about who would set it, and only the running
// program can say what a user ends up seeing.
//
// It also caught a wrong measurement: the first sweep of the blur matrix appeared
// to show that only a paste arriving within a few hundred ms of the blur was lost.
// The program had not finished starting — closing the MCP-init overlay runs
// restoreFocusAfterConfirm → focusInput, which undid the blur under the test. See
// settled(); assert against an idle frame or assert nothing.
//
// Linux-only, and deliberately so: on Windows the program reads console events and
// a test has no console to hand it, and macOS names its pty nodes differently.
// Skipped under -short, because it spawns a process and waits on paint timing.
package main_test

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// The bytes a terminal sends. Named because the assertions are about them and a
// test that spells 0x1b inline is a test that has to be re-decoded every time.
var (
	pasteStart = []byte("\x1b[200~")
	pasteEnd   = []byte("\x1b[201~")
	focusOut   = []byte("\x1b[O") // window loses OS focus (DEC 1004)
	focusIn    = []byte("\x1b[I")
	promptMark = "Enter your prompt" // the placeholder, proof the prompt is on screen
)

// tty is one run of the program on a pty.
type tty struct {
	cmd    *exec.Cmd
	master *os.File

	mu  sync.Mutex
	buf []byte
}

// startProgram opens a pty, launches the binary on its slave side as a session
// leader with that slave as its controlling terminal, and pumps the master into
// memory. The plumbing is spelled out because the alternative is a dependency:
// /dev/ptmx, the slave number, unlocking it, and TIOCSCTTY are what a terminal
// emulator does at startup, and golang.org/x/sys/unix (already required) is enough.
func startProgram(tb *testing.T, binary string) *tty {
	tb.Helper()

	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		tb.Skipf("no pty available (%v); this check needs /dev/ptmx", err)
	}
	num, err := unix.IoctlGetInt(int(master.Fd()), unix.TIOCGPTN)
	if err != nil {
		master.Close()
		tb.Fatalf("TIOCGPTN: %v", err)
	}
	// The unlock takes a pointer to an int, not the int: IoctlSetInt passes its
	// value as the ioctl argument and the kernel reads that address (EFAULT).
	if err := unix.IoctlSetPointerInt(int(master.Fd()), unix.TIOCSPTLCK, 0); err != nil {
		master.Close()
		tb.Fatalf("TIOCSPTLCK: %v", err)
	}
	slavePath := fmt.Sprintf("/dev/pts/%d", num)
	slave, err := os.OpenFile(slavePath, os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		master.Close()
		tb.Fatalf("opening %s: %v", slavePath, err)
	}
	w := unix.Winsize{Row: 24, Col: 80}
	if err := unix.IoctlSetWinsize(int(slave.Fd()), unix.TIOCSWINSZ, &w); err != nil {
		tb.Fatalf("TIOCSWINSZ: %v", err)
	}

	cmd := exec.Command(binary)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	// Setsid + Setctty is what a terminal does for its shell: a new session, and
	// the slave as its controlling terminal. (exec refuses Setctty together with
	// Foreground; the program never needs to be the session's foreground process
	// to read its own stdin.)
	cmd.SysProcAttr = &unix.SysProcAttr{Setsid: true, Setctty: true}
	cmd.Env = []string{"TERM=xterm-256color", "LINES=24", "COLUMNS=80", "HOME=" + tb.TempDir()}
	if err := cmd.Start(); err != nil {
		slave.Close()
		master.Close()
		tb.Fatalf("starting %s: %v", binary, err)
	}
	// The parent's copy goes now that the child has its own: leaving it open would
	// keep the master from ever reporting EOF when the program exits.
	slave.Close()

	t := &tty{cmd: cmd, master: master}
	tb.Cleanup(t.close)
	go t.pump()

	if !t.waitFor([]byte(promptMark), 15*time.Second) {
		t.close()
		tb.Fatalf("the TUI never painted the prompt; first bytes back: %q", first(t.out(), 400))
	}
	// Every case here measures a program at rest, and that is part of the check
	// rather than a convenience. Without the wait, a paste arrives while the
	// prompt is still loading — and against the pre-fbd3dbae binary that alone was
	// enough to lose it, which is a fact about the old gate, not about the menu.
	t.settled(tb, 800*time.Millisecond, 10*time.Second)
	return t
}

func (t *tty) pump() {
	chunk := make([]byte, 4096)
	for {
		n, err := t.master.Read(chunk)
		if n > 0 {
			t.mu.Lock()
			t.buf = append(t.buf, chunk[:n]...)
			t.mu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

func (t *tty) close() {
	if t.cmd != nil && t.cmd.Process != nil {
		_ = t.cmd.Process.Signal(os.Interrupt)
		done := make(chan struct{})
		go func() { _, _ = t.cmd.Process.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			_ = t.cmd.Process.Kill()
		}
		t.cmd = nil
	}
	if t.master != nil {
		t.master.Close()
		t.master = nil
	}
}

// send writes bytes as the terminal side. chunk > 0 delivers in fixed-size
// writes, which is what puts a read boundary *inside* a sequence rather than
// around it — the difference the cut-marker check depends on.
func (t *tty) send(tb *testing.T, data []byte, chunk int) {
	tb.Helper()
	if chunk <= 0 {
		if _, err := t.master.Write(data); err != nil {
			tb.Fatalf("writing to the pty: %v", err)
		}
		return
	}
	for i := 0; i < len(data); i += chunk {
		end := min(i+chunk, len(data))
		if _, err := t.master.Write(data[i:end]); err != nil {
			tb.Fatalf("writing chunk %d of the pty stream: %v", i/chunk, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (t *tty) out() []byte {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]byte(nil), t.buf...)
}

func (t *tty) waitFor(needle []byte, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if bytes.Contains(t.out(), needle) {
			return true
		}
		time.Sleep(25 * time.Millisecond)
	}
	return false
}

// settled waits until the painted frame stops changing, so the program is idle.
//
// Without this, a blur test measures startup. The program's own loading path
// closes the MCP-init overlay with restoreFocusAfterConfirm, which calls
// restoreFocus and re-focuses the prompt — silently undoing the `ESC [ O` a test
// just sent. The first version of the matrix here missed that and concluded that
// the blur only affected a few hundred ms; against a settled frame the effect was
// permanent, and the earlier reading was simply not a measurement of what it
// claimed to be.
func (t *tty) settled(tb *testing.T, quiet time.Duration, cap time.Duration) bool {
	tb.Helper()
	var last []byte
	stable := time.Duration(0)
	deadline := time.Now().Add(cap)
	for time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
		out := t.out()
		if bytes.Equal(out, last) {
			stable += 100 * time.Millisecond
		} else {
			stable = 0
		}
		last = out
		if stable >= quiet {
			return true
		}
	}
	tb.Logf("frame never went quiet within %v; blur-dependent assertions may be weakened", cap)
	return false
}

func (t *tty) paste(tb *testing.T, content []byte) {
	tb.Helper()
	t.send(tb, append(append(append([]byte(nil), pasteStart...), content...), pasteEnd...), 0)
}

// buildProgram compiles the binary under test, once per run. ALAYACORE_BIN points
// the check at an existing build instead — which is how these assertions were
// shown to fail against the commits before the fixes rather than merely to pass
// against this one.
func buildProgram(tb *testing.T) string {
	tb.Helper()
	if pre := os.Getenv("ALAYACORE_BIN"); pre != "" {
		abs, err := filepath.Abs(pre)
		if err != nil {
			tb.Fatalf("ALAYACORE_BIN=%s: %v", pre, err)
		}
		if _, err := os.Stat(abs); err != nil {
			tb.Fatalf("ALAYACORE_BIN: %v", err)
		}
		return abs
	}
	dir := tb.TempDir()
	bin := filepath.Join(dir, "alayacore")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	if out, err := cmd.CombinedOutput(); err != nil {
		tb.Fatalf("building the program under test: %v\n%s", err, out)
	}
	return bin
}

func first(b []byte, n int) []byte {
	if len(b) <= n {
		return b
	}
	return b[:n]
}

// TestEndToEndOverPty is the set of things a user of this program can do to a
// paste, each judged by whether the pasted text ends up on the screen.
func TestEndToEndOverPty(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns the built binary and waits on paint timing")
	}
	binary := buildProgram(t)

	// 1. The report's shape. A terminal's context menu blurs the window, and the
	//    paste that menu hands back arrives inside the blur — at menu-dwell
	//    lengths, because reading a menu and finding "Paste" takes seconds. The
	//    blur gate, when it existed, was permanent, so every dwell lost it.
	//
	//    "exactly once" is asked behaviourally: one more character must land at
	//    the end of a single copy. Counting the marker in the byte stream cannot
	//    say that, because every repaint writes the line again.
	for _, dwell := range []time.Duration{0, 500 * time.Millisecond, 2 * time.Second} {
		t.Run(fmt.Sprintf("paste after %v of menu blur", dwell), func(t *testing.T) {
			mark := []byte(fmt.Sprintf("BLUR%v", dwell))
			th := startProgram(t, binary)
			th.send(t, focusOut, 0)
			time.Sleep(dwell)
			th.paste(t, mark)
			landed := th.waitFor(mark, 3*time.Second)
			th.send(t, focusIn, 0)
			time.Sleep(200 * time.Millisecond)
			th.send(t, []byte("Q"), 0)
			single := th.waitFor(append(append([]byte(nil), mark...), 'Q'), 3*time.Second)
			doubled := th.waitFor(append(append([]byte(nil), mark...), mark...), 600*time.Millisecond)
			if !landed || !single || doubled {
				t.Errorf("pasted text on screen: landed=%v single-copy=%v doubled=%v", landed, single, doubled)
			}
			th.close()
		})
	}

	// 2. A read boundary inside the closing marker. The program reads at most
	//    inputReadSize bytes at a time (program_input_unix.go); 6 + 245 = 251, so
	//    the 256-byte read stops five bytes into `ESC [ 201 ~`. That head is
	//    indistinguishable from paste content, and writing it into the content is
	//    a paste that never closes — which then takes the keystrokes behind it
	//    down the same hole. Hence the second assertion, which is the one that
	//    matters: the keyboard has to come back.
	t.Run("paste whose end marker is cut by the read boundary", func(t *testing.T) {
		th := startProgram(t, binary)
		content := []byte(strings.Repeat("y", 239) + "CUT-OK")
		stream := append(append(append([]byte(nil), pasteStart...), content...), pasteEnd...)
		th.send(t, stream, 256)
		landed := th.waitFor([]byte("CUT-OK"), 3*time.Second)
		th.send(t, []byte("Z"), 0)
		keys := th.waitFor([]byte("CUT-OKZ"), 3*time.Second)
		if !landed {
			t.Error("the cut paste never reached the screen")
		}
		if !keys {
			t.Error("the keystroke after the cut paste never reached the screen: the parser is still holding a paste")
		}
		th.close()
	})

	// 3. A terminal reply the program never asked for, split one byte per read.
	//    Its body is exactly the text that must not reach the prompt, and an
	//    introducer resolved early makes the rest of the reply arrive as typing.
	t.Run("reply split one byte per read leaves nothing behind", func(t *testing.T) {
		th := startProgram(t, binary)
		reply := []byte("\x1b]11;rgb:1e1e/1e1e/1e1e\x07")
		th.send(t, reply, 1)
		time.Sleep(500 * time.Millisecond)
		out := string(th.out())
		for _, body := range []string{"rgb:1e1e", "11;rgb"} {
			if strings.Contains(out, body) {
				t.Errorf("the reply's body reached the screen as %q", body)
			}
		}
		th.paste(t, []byte("AFTER-REPLY"))
		if !th.waitFor([]byte("AFTER-REPLY"), 3*time.Second) {
			t.Error("the paste behind the split reply never reached the screen")
		}
		th.close()
	})

	// 4. The path that worked all along: a plain bracketed paste of a short
	//    block, no menu, no blur. Here so that a future change cannot satisfy 1–3
	//    by treating every paste as an error case that happens to print.
	t.Run("an ordinary paste still works", func(t *testing.T) {
		th := startProgram(t, binary)
		th.paste(t, []byte("MIDDLE-CLICK-OK"))
		if !th.waitFor([]byte("MIDDLE-CLICK-OK"), 3*time.Second) {
			t.Error("the paste never reached the screen")
		}
		th.close()
	})
}
