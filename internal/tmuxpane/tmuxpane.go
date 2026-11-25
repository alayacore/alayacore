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
// The lifetime rule is the part to know before reading the code, and it is stated
// once in docs/development-principles.md → "A test reaps what it starts": a
// cleanup does not run when the test binary is killed, so a process this package
// starts has to end with the test by some other means. Here that is the shape of
// the server itself — not a daemon, but a child of the test carrying Pdeathsig
// (New) — which makes Close the orderly path rather than the only one.
//
// Linux-only, like both callers: what keeps the server inside the test's lifetime
// is a Linux facility, and a pane for a check that cannot build here is a pane
// nothing would ask for.
package tmuxpane

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
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
	// sock is the server's socket file, handed to tmux with -S. It is short on
	// purpose — a socket path is capped near 108 bytes, and dir is
	// testing.T.TempDir(), whose name alone can reach that — and it is in a
	// directory of the pane's own rather than tmux's default (/tmp/tmux-<uid>),
	// which collected a file per server that nothing owned.
	sock string
	// srv is the server as this process's child; New is why it is a child.
	srv *exec.Cmd
	// session is the session's name, set by Session.
	session string

	closeOnce sync.Once
}

// New starts a tmux server of this test's own — a socket nothing else on the
// machine uses, and a directory for the files a case needs — and registers the
// cleanup that takes both away.
//
// The server is started here, in the one mode that does not daemonize (-D) — see
// the package comment for why that is the whole point — and with -f /dev/null, so
// that the reader's own ~/.tmux.conf cannot reach a server that is this check's
// reference terminal. Whatever a laptop set for its own work (default-terminal,
// escape-time, remain-on-exit) would otherwise decide what the check measures,
// which is the same promise the launcher makes for the program.
func New(t *testing.T, tmux string) *Pane {
	t.Helper()
	sockDir, err := os.MkdirTemp("", "alaya-pane-")
	if err != nil {
		t.Fatalf("making a tmux socket directory: %v", err)
	}
	p := &Pane{
		t:    t,
		tmux: tmux,
		dir:  t.TempDir(),
		sock: filepath.Join(sockDir, "sock"),
	}
	// Registered before the server exists, so a server that refuses to start still
	// takes its socket directory with it.
	t.Cleanup(p.Close)
	// -D takes no command, so the session is started separately, by Session.
	p.srv = exec.Command(p.tmux, "-D", "-S", p.sock, "-f", "/dev/null") //nolint:gosec // G204: the program is tmux, and the arguments are a socket path this package made
	p.srv.Env = p.env()
	// SIGKILL, not SIGTERM: the case this covers is a parent that is already gone,
	// so there is nobody left to be polite to. A parent-death signal follows the
	// thread that forked rather than the process, which is a sharp edge for a
	// language that retires threads — the forking thread here is an ordinary
	// runtime one, which is parked rather than retired, and a spurious kill would be
	// loud (the next command fails) rather than a wrong result.
	p.srv.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
	if err := p.srv.Start(); err != nil {
		t.Fatalf("starting a tmux server: %v", err)
	}
	p.waitForServer(10 * time.Second)
	return p
}

// waitForServer waits until the server answers, so that the first real command
// does not race it: -D means the socket appears a moment after Start returns, and a
// client that arrives early fails to connect.
//
// Which question to ask is the subtle part, and each of the two obvious ones is
// wrong for its own reason. new-session starts a server when it cannot find one,
// and a daemon is exactly what this pane must not end up with. list-sessions asks
// about *sessions*: a server that has none answers with an error on some builds, so
// "did it succeed" would be a build's decision rather than this harness's. Global
// options are answered by any tmux that is up, with a session or without, and asking
// for them cannot start a server — both measured, on the runner's 3.4 and a
// development box's 3.7c.
func (p *Pane) waitForServer(limit time.Duration) {
	p.t.Helper()
	deadline := time.Now().Add(limit)
	var last string
	for {
		cmd := p.command("show-options", "-g")
		var complaint bytes.Buffer
		cmd.Stderr = &complaint
		err := cmd.Run()
		last = strings.TrimSpace(complaint.String())
		if err == nil {
			return
		}
		if time.Now().After(deadline) {
			p.t.Fatalf("the tmux server never answered on %s (%s): %s", p.sock, p.version(), last)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// version is the banner of the tmux this check is running, so a failure names the
// build it failed with — the equality of two tmux builds is not something this
// harness may assume.
func (p *Pane) version() string {
	out, err := exec.Command(p.tmux, "-V").Output() //nolint:gosec // G204: the program is tmux, and it takes one fixed flag
	if err != nil {
		return "tmux, version unknown"
	}
	return strings.TrimSpace(string(out))
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
	p.run(append(args, command...)...)
	p.session = name
}

// run invokes tmux on this pane's server and returns its output, failing the test
// if tmux does. Only stdout is the answer: stderr is tmux's complaint, and a
// capture that merged the two would put a warning in the middle of the screen it
// was asked for. It is unexported on purpose — a check reaches tmux through a
// method here, so a new verb is added in one place rather than spelled in a test
// file.
func (p *Pane) run(args ...string) string {
	p.t.Helper()
	var out, complaint bytes.Buffer
	cmd := p.command(args...)
	cmd.Stdout, cmd.Stderr = &out, &complaint
	if err := cmd.Run(); err != nil {
		p.t.Fatalf("tmux %s: %v\n%s%s", strings.Join(args, " "), err, out.String(), complaint.String())
	}
	return out.String()
}

// Text is the whole visible pane as tmux draws it, trailing spaces and all: the
// caret check compares snapshots of it for sameness, and a row the program blanked
// by writing spaces over it is a change. Rows is the reading side.
func (p *Pane) Text() string {
	p.t.Helper()
	return p.run("capture-pane", "-p", "-t", p.session)
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
	lines := strings.Split(strings.TrimSuffix(p.run(args...), "\n"), "\n")
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
	return strings.TrimSpace(p.run("display-message", "-p", "-t", p.session, f))
}

// SendKeys sends named keys — Left, Home, End — as tmux spells them.
func (p *Pane) SendKeys(names ...string) {
	p.t.Helper()
	p.run(append([]string{"send-keys", "-t", p.session}, names...)...)
}

// Paste delivers text as a bracketed paste. It goes through a file rather than
// send-keys, so the bytes arrive exactly as given.
func (p *Pane) Paste(text string) {
	p.t.Helper()
	f := filepath.Join(p.dir, "paste")
	if err := os.WriteFile(f, []byte(text), 0o600); err != nil {
		p.t.Fatalf("writing the paste buffer: %v", err)
	}
	p.run("load-buffer", "-b", "paste", f)
	p.run("paste-buffer", "-p", "-b", "paste", "-t", p.session)
}

// Close takes this pane away and nothing else: the reader's tmux, if any, is on
// the default socket. A pane is closed more than once (a case's defer and the
// test's cleanup), so the work is guarded by closeOnce — and the server goes
// before the directory holding its socket.
func (p *Pane) Close() {
	p.closeOnce.Do(func() {
		_ = p.command("kill-server").Run() // already dead when a case failed early
		if p.srv != nil && p.srv.Process != nil {
			// Reap it: it is this process's child, so its exit is ours to collect.
			// A server that does not answer the kill in time is killed outright —
			// a teardown that hangs is worse than one that is not tidy, and the
			// test it runs for is finished either way.
			done := make(chan struct{})
			go func() { _ = p.srv.Wait(); close(done) }()
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				_ = p.srv.Process.Kill()
			}
		}
		_ = os.RemoveAll(filepath.Dir(p.sock))
	})
}

// command is one tmux invocation against this pane's server. -S is its socket
// file, which is what makes this server nobody else's; env carries the rest (see
// Pane.env).
func (p *Pane) command(args ...string) *exec.Cmd {
	cmd := exec.Command(p.tmux, append([]string{"-S", p.sock}, args...)...) //nolint:gosec // G204: the program is tmux, as in New, and the arguments are this pane's own socket path and what a method here passed
	cmd.Env = p.env()
	return cmd
}

// env is the environment every tmux invocation of this pane runs with. TMUX and
// TMUX_PANE are cleared: tmux refuses to start a server from inside a session it
// thinks it would have to nest in, and these checks run under whatever shell the
// reader used.
func (p *Pane) env() []string {
	return append(os.Environ(), "TMUX=", "TMUX_PANE=")
}

// Quote wraps s in single quotes for a shell command, escaping any single quote
// inside it, so a path with a space, a parenthesis or a quote stays one word.
// tmux splits the command it is given with its own rules, which are not a shell's
// — quoting happens in the script a caller writes, not in the arguments it passes
// (see Session).
func Quote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
