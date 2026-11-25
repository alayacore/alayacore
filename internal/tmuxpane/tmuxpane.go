//go:build linux

// Package tmuxpane is the tmux server a test owns: a pane nothing else on the
// machine can see, and a lifetime that ends with the test that started it.
//
// Two checks need a real terminal and get one from tmux — the caret e2e in the
// root package and the frame replay in internal/adapters/terminal. Both need the
// same four things: a server on a socket of its own, a socket file in a directory
// short enough to hold one, one place to run tmux commands against, and a
// teardown that leaves nothing behind. That was written twice, once per check,
// and two copies of a lifetime rule is how a rule comes to disagree with itself;
// it is written here once.
//
// The lifetime rule is the part to know before reading the code: a test's
// cleanups do not run when the test binary is killed, and a tmux server outlives
// the process that started it — so the program still running in one of its panes
// outlives both. New and Close are what keep that from happening; they are the
// orderly pair, and the reason they are shaped the way they are.
//
// Linux-only, like both callers: what keeps the server inside the test's lifetime
// is a Linux facility, and a pane for a check that cannot build here is a pane
// nothing would ask for.
package tmuxpane

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Pane is one tmux server, plus the session a check runs in it.
type Pane struct {
	t    *testing.T
	tmux string
	// dir is the files a case needs — the script its command runs, a pasted
	// buffer, a stream to replay — and the HOME a caller may hand the program, so
	// a reader's own configuration cannot decide what the check starts up
	// against.
	dir string
	// sockDir is TMUX_TMPDIR for every invocation: where the socket file lands.
	// It is apart from dir because a socket path is length-capped and dir is
	// testing.T.TempDir(), whose name alone can reach the cap.
	sockDir string
	// socket is the server's name, fresh per instance: cases run in sequence and
	// each kills its server on cleanup, so reusing a name would let a new-session
	// land on a server that is still exiting.
	socket string
	// session is the session's name, set by Session.
	session string

	closeOnce sync.Once
}

// New returns a pane of this test's own: a socket name nothing else uses, a
// directory for its files, and the cleanups that take both away.
func New(t *testing.T, tmux string) *Pane {
	t.Helper()
	// The socket file goes in a directory of this pane's own, not tmux's default
	// (/tmp/tmux-<uid>): killing a server stops it and leaves the file behind, so
	// the default location collected one per run and nothing owned them.
	sockDir, err := os.MkdirTemp("", "alaya-pane-")
	if err != nil {
		t.Fatalf("making a tmux socket directory: %v", err)
	}
	p := &Pane{
		t:       t,
		tmux:    tmux,
		dir:     t.TempDir(),
		sockDir: sockDir,
		socket:  fmt.Sprintf("alaya-pane-%d-%d", os.Getpid(), time.Now().UnixNano()),
	}
	t.Cleanup(p.Close)
	return p
}

// Dir is the directory this pane's files belong in (see Pane.dir).
func (p *Pane) Dir() string { return p.dir }

// Session starts the pane: a session of the given size, detached, running
// command. The command is passed to tmux as the arguments given, not as a shell
// string: tmux splits a command with rules that are not a shell's, so a caller
// that wants a shell passes "sh", "-c", script, and one that has a path passes
// the path (Quote is for the script case).
func (p *Pane) Session(name string, width, height int, command ...string) {
	p.t.Helper()
	args := []string{"new-session", "-d", "-s", name, "-x", strconv.Itoa(width), "-y", strconv.Itoa(height)}
	p.Run(append(args, command...)...)
	p.session = name
}

// Run invokes tmux on this pane's server and returns its output, failing the test
// if tmux does.
func (p *Pane) Run(args ...string) string {
	p.t.Helper()
	out, err := p.command(args...).CombinedOutput()
	if err != nil {
		p.t.Fatalf("tmux %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// Text is the whole visible pane, as tmux draws it.
func (p *Pane) Text() string {
	p.t.Helper()
	return p.Run("capture-pane", "-p", "-t", p.session)
}

// Rows is the pane's visible rows, right-trimmed. With join, the rows tmux
// considers one soft-wrapped line come back joined (capture-pane -J).
func (p *Pane) Rows(join bool) []string {
	p.t.Helper()
	args := []string{"capture-pane", "-p"}
	if join {
		args = append(args, "-J")
	}
	args = append(args, "-t", p.session)
	lines := strings.Split(strings.TrimSuffix(p.Run(args...), "\n"), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " ")
	}
	return lines
}

// Row is one screen row of the pane, 0-indexed the way #{cursor_y} is.
func (p *Pane) Row(y int) string {
	p.t.Helper()
	rows := p.Rows(false)
	if y < 0 || y >= len(rows) {
		p.t.Fatalf("the pane has %d rows; there is no row %d", len(rows), y)
	}
	return rows[y]
}

// Format asks tmux for one of its format strings about this session, such as
// "#{cursor_x} #{cursor_y} #{cursor_flag}".
func (p *Pane) Format(f string) string {
	p.t.Helper()
	return strings.TrimSpace(p.Run("display-message", "-p", "-t", p.session, f))
}

// SendKeys sends named keys — Left, Home, End — as tmux spells them.
func (p *Pane) SendKeys(names ...string) {
	p.t.Helper()
	p.Run(append([]string{"send-keys", "-t", p.session}, names...)...)
}

// Paste delivers text as a bracketed paste. It goes through a file rather than
// send-keys, so the bytes arrive exactly as given.
func (p *Pane) Paste(text string) {
	p.t.Helper()
	f := filepath.Join(p.dir, "paste")
	if err := os.WriteFile(f, []byte(text), 0o600); err != nil {
		p.t.Fatalf("writing the paste buffer: %v", err)
	}
	p.Run("load-buffer", "-b", "paste", f)
	p.Run("paste-buffer", "-p", "-b", "paste", "-t", p.session)
}

// Close takes this pane away and nothing else: the reader's tmux, if any, is on
// the default socket. A pane is closed more than once (a case's defer and the
// test's cleanup), so the work is guarded by closeOnce — and the server goes
// before the directory holding its socket.
func (p *Pane) Close() {
	p.closeOnce.Do(func() {
		_ = p.command("kill-server").Run() // already dead when a case failed early
		_ = os.RemoveAll(p.sockDir)
	})
}

// command is one tmux invocation against this pane's server. -L puts the server
// on a socket of its own; env carries the rest (see Pane.env).
func (p *Pane) command(args ...string) *exec.Cmd {
	cmd := exec.Command(p.tmux, append([]string{"-L", p.socket}, args...)...)
	cmd.Env = p.env()
	return cmd
}

// env is the environment every tmux invocation of this pane runs with. TMUX and
// TMUX_PANE are cleared: tmux refuses to start a server from inside a session it
// thinks it would have to nest in, and these checks run under whatever shell the
// reader used. TMUX_TMPDIR is where the socket file goes, which is this pane's own
// directory.
func (p *Pane) env() []string {
	return append(os.Environ(), "TMUX=", "TMUX_PANE=", "TMUX_TMPDIR="+p.sockDir)
}

// Quote wraps s in single quotes for a shell command, escaping any single quote
// inside it, so a path with a space, a parenthesis or a quote stays one word.
// tmux splits the command it is given with its own rules, which are not a shell's
// — quoting happens in the script a caller writes, not in the arguments it passes
// (see Session).
func Quote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
