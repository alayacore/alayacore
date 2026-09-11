#!/usr/bin/env python3
"""Drive the built binary over a real pty and assert what reaches the screen.

Everything here is what a terminal does and what a user sees: bytes go in one way,
the painted frame comes back the other. Nothing inspects the parser, the model or
any internal state, so a bug that "cannot happen according to the code" has
nowhere to hide — which is the reason this file exists. It found, by accident,
that the code was right and the *test* was wrong; see settled().

Run:  make check-tty          (or)  python3 misc/ttycheck.py ./alayacore
Needs: a POSIX pty. Not runnable under Windows — the binary reads console events
there, and no pseudo-console is created for it; the Windows CI job covers this
ground with unit tests instead.
"""
import fcntl
import os
import pty
import re
import select
import signal
import struct
import subprocess
import sys
import termios
import threading
import time

PASTE_START = b"\x1b[200~"
PASTE_END = b"\x1b[201~"
FOCUS_OUT = b"\x1b[O"
FOCUS_IN = b"\x1b[I"
PROMPT_MARK = b"Enter your prompt"


class Tty:
    """One run of the binary on a master/slave pty pair."""

    def __init__(self, binary, rows=24, cols=80):
        master, slave = pty.openpty()
        fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", rows, cols, 0, 0))
        env = dict(os.environ, TERM="xterm-256color", LINES=str(rows), COLUMNS=str(cols))
        self.proc = subprocess.Popen(
            [binary], stdin=slave, stdout=slave, stderr=slave, env=env, close_fds=True
        )
        os.close(slave)
        self.master = master
        self.buf = bytearray()
        self.lock = threading.Lock()
        threading.Thread(target=self._pump, daemon=True).start()

    def _pump(self):
        while True:
            try:
                data = os.read(self.master, 65536)
            except OSError:
                return
            if not data:
                return
            with self.lock:
                self.buf.extend(data)

    def send(self, data, chunk=None):
        """Write as the terminal side. `chunk` delivers in fixed-size writes, which
        is what puts a read boundary inside a sequence rather than around it."""
        if chunk:
            for i in range(0, len(data), chunk):
                os.write(self.master, data[i : i + chunk])
                time.sleep(0.01)
        else:
            os.write(self.master, data)

    def out(self):
        with self.lock:
            return bytes(self.buf)

    def wait_for(self, needle, timeout=8.0):
        end = time.time() + timeout
        while time.time() < end:
            if needle in self.out():
                return True
            time.sleep(0.05)
        return False

    def settled(self, quiet=0.8, cap=10.0):
        """Wait until the frame stops changing, so the program is idle.

        This is the lesson of a measurement that was wrong before it was right:
        blurring the prompt while the app is still starting up gets silently
        undone, because closing the MCP-init overlay runs restoreFocusAfterConfirm
        → restoreFocus → focusInput. The first sweep of the blur/paste matrix read
        that as "only the first few hundred ms are affected", and it was measuring
        a program that had already re-focused itself. Assert against an idle frame
        or assert nothing.
        """
        last, stable, needed = b"", 0, int(quiet * 10)
        end = time.time() + cap
        while time.time() < end:
            time.sleep(0.1)
            out = self.out()
            stable = stable + 1 if out == last else 0
            last = out
            if stable >= needed:
                return True
        return False

    def close(self):
        try:
            self.proc.send_signal(signal.SIGINT)
            self.proc.wait(timeout=3)
        except Exception:
            self.proc.kill()
            self.proc.wait(timeout=3)


class Checker:
    def __init__(self):
        self.failed = 0

    def check(self, name, ok, note=""):
        print(("  PASS  " if ok else "  FAIL  ") + name + (f"   [{note}]" if note else ""))
        if not ok:
            self.failed += 1


def started(binary, settle=True):
    """A Tty with the prompt on screen, and (by default) nothing else happening."""
    t = Tty(binary)
    if not t.wait_for(PROMPT_MARK):
        raise RuntimeError("the TUI never painted the prompt")
    if settle and not t.settled():
        print("  NOTE  frame never went quiet; blur-dependent assertions may be weak")
    return t


def paste(t, content):
    t.send(PASTE_START + content + PASTE_END)


def run(binary):
    print(f"\n===== {binary} =====")
    c = Checker()
    probe = Tty(binary)
    up = probe.wait_for(PROMPT_MARK)
    probe.close()
    c.check("TUI starts and paints the prompt", up)
    if not up:
        return c.failed

    # 1. The report's shape: a context menu blurs the window, and the paste that
    #    menu hands back arrives inside that blur. Measured at menu-dwell lengths,
    #    because a user opening a menu and finding "Paste" takes seconds, not
    #    milliseconds — and the gate, when it existed, was permanent.
    for dwell in (0.0, 0.5, 2.0):
        mark = f"BLUR{int(dwell * 1000)}".encode()
        t = started(binary)
        t.send(FOCUS_OUT)
        time.sleep(dwell)
        paste(t, mark)
        lands = t.wait_for(mark, 3.0)
        # The value must hold the block once. Counting the marker in the byte
        # stream cannot say that (every repaint writes the line again), so ask
        # behaviour: one more character lands at the end of a single copy.
        t.send(FOCUS_IN)
        time.sleep(0.2)
        t.send(b"Q")
        once = t.wait_for(mark + b"Q", 3.0)
        t.close()
        c.check(f"paste after {int(dwell * 1000)} ms of menu-blur reaches the prompt, once",
                lands and once, f"landed={lands} single-copy={once}")

    # 2. A read boundary inside the closing marker. 6 (start) + 245 (content)
    #    = 251, so the 256-byte read stops five bytes into `ESC [ 201 ~`: the head
    #    is paste content as far as a parser reading raw bytes can tell, and
    #    writing it into the content is a paste that never ends — with every later
    #    keystroke swallowed behind it.
    content = b"y" * 239 + b"CUT-OK"
    t = started(binary)
    stream = PASTE_START + content + PASTE_END
    t.send(stream, chunk=256)
    landed = t.wait_for(b"CUT-OK", 3.0)
    t.send(b"Z")
    keys = t.wait_for(b"CUT-OKZ", 3.0)
    t.close()
    c.check("a paste whose end marker is cut by the read boundary lands", landed)
    c.check("and the keystrokes after it are still keystrokes", keys)

    # 3. A reply split byte by byte must not type its own body. The program never
    #    asks for a colour reply, so one arriving means it was left for us by the
    #    terminal or by a program that ran earlier in the same window.
    t = started(binary)
    reply = b"\x1b]11;rgb:1e1e/1e1e/1e1e\x07"
    t.send(reply, chunk=1)
    time.sleep(0.5)
    leaked = re.search(rb"rgb:1e1e|11;rgb", t.out()) is not None
    paste(t, b"AFTER-REPLY")
    works = t.wait_for(b"AFTER-REPLY", 3.0)
    t.close()
    c.check("a reply fed one byte at a time leaves nothing in the prompt", not leaked)
    c.check("and the paste that follows it works", works)

    # 4. Middle-click, the path that never lost anything in the report: no menu,
    #    no blur, plain bracketed paste of a short block.
    t = started(binary)
    paste(t, b"MIDDLE-CLICK-OK")
    c.check("an ordinary paste still works", t.wait_for(b"MIDDLE-CLICK-OK", 3.0))
    t.close()

    return c.failed


if __name__ == "__main__":
    binary = sys.argv[1] if len(sys.argv) > 1 else "./alayacore"
    if not os.path.exists(binary):
        sys.exit(f"no such binary: {binary} (run `make build` first)")
    failed = run(binary)
    print("\nOVERALL:", "PASS" if failed == 0 else f"FAIL ({failed})")
    sys.exit(0 if failed == 0 else 1)
