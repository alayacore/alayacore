package terminal

// External process execution and terminal suspension. This is module 5 of
// the self-built TUI stack (see docs/tui-architecture.md): the editor handoff
// (ExecProcess) and Ctrl-Z suspend both release the terminal (exit alt
// screen, restore cooked mode), run something in the foreground, then
// re-acquire the terminal (raw mode + alt screen) and repaint.
//
// The sequence mirrors bubbletea's exec.go + tty.go suspend path. Parking the
// input loop — the half that decides whether the child can read the keyboard
// at all, and whether the program can exit without waiting for a keystroke —
// is shared, in program_input.go; the platform files only bound the read.
// SIGTSTP/SIGCONT is the part that stays in exec_unix.go / exec_windows.go.

import (
	"os"
	"os/exec"
)

// execMsg is used internally to run an exec.Cmd sent with ExecProcess.
type execMsg struct {
	cmd *exec.Cmd
	fn  func(error) Msg
}

// ExecProcess runs the given *exec.Cmd in a blocking fashion, effectively
// suspending the program while the command is running. After the command
// exits the program resumes. It is used for spawning other interactive
// applications such as editors and shells.
//
// The command's stdin/stdout are wired to the terminal when the program
// owns one (the TUI is released first, so the child is in the foreground).
// fn, when non-nil, receives the run error and produces the message that
// resumes the application.
func ExecProcess(c *exec.Cmd, fn func(error) Msg) Cmd {
	return func() Msg {
		return execMsg{cmd: c, fn: fn}
	}
}

// exec runs an execMsg in the foreground of the main loop: release the
// terminal, run the command, re-acquire the terminal, and deliver the
// callback message. It blocks the loop for the whole command duration;
// messages arriving meanwhile (tickers, signals) queue up and are processed
// after it returns.
func (p *Program) exec(msg execMsg, ctxDone <-chan struct{}) {
	if msg.cmd != nil && p.tty != nil {
		// The child runs in the same process group on the same terminal:
		// give it the TTY directly.
		msg.cmd.Stdin = p.tty.In()
		msg.cmd.Stdout = p.tty.Out()
		msg.cmd.Stderr = os.Stderr
	}

	var runErr error
	if msg.cmd != nil {
		runErr = p.Suspend(func() error { return msg.cmd.Run() })
	}

	if msg.fn != nil {
		// The loop is blocked inside p.exec and cannot drain p.msgs, so a
		// blocking send here could deadlock on a full channel (long editor
		// session + queued input). Evaluate the callback inline (it may do
		// I/O — e.g. the editor reads the temp file) and deliver the result
		// from a goroutine, exactly like dispatch does.
		result := msg.fn(runErr)
		go func() {
			select {
			case p.msgs <- result:
			case <-ctxDone:
			}
		}()
	}
}

// Suspend releases the terminal (exit alt screen, restore cooked mode), runs
// run() in the foreground, then re-acquires the terminal (raw mode + alt
// screen) and forces a full repaint. When the program has no terminal (tests),
// run() is called directly without any terminal dance.
func (p *Program) Suspend(run func() error) error {
	if p.tty == nil {
		return run()
	}

	if err := p.releaseTerminal(); err != nil {
		return err
	}

	runErr := run()
	if err := p.acquireTerminal(); err != nil && runErr == nil {
		runErr = err
	}
	return runErr
}

// releaseTerminal parks the input loop and returns the terminal to its
// pre-program state: cooked mode, main screen, visible cursor. After this
// returns, no input is read by the program, so a foreground child gets every
// keystroke — and nothing is left reading that the teardown would later have to
// wait for.
//
// A release that fails partway un-parks the loop before returning the error: a
// parked loop is a program that renders and cannot be typed at, so the caller can
// report the failure but the user cannot answer it.
func (p *Program) releaseTerminal() error {
	p.pauseInput() // waits for the loop to report that it is between reads
	if err := p.screen.Stop(); err != nil {
		p.resumeInput()
		return err
	}
	if err := p.tty.Restore(); err != nil {
		p.resumeInput()
		return err
	}
	return nil
}

// acquireTerminal re-enters raw mode and the alt screen, resumes the input
// loop, forces a full repaint, and re-checks the terminal size (it may have
// changed while another process was at the foreground).
//
// The re-entry is not a formality: a child that owned the console (an editor)
// may have left it in a state we do not want — Windows in particular, where a
// child restores the console mode to whatever it found on entry, which does not
// include the ANSI processing screen.go writes against. TTY.MakeRaw is what
// negotiates it again (console_windows.go).
//
// Unlike the same failure in Run, an error here is returned rather than fatal:
// the session holds work the user has not lost yet, and the one caller
// (editor.go → ExecProcess) surfaces it through EditorFinishedMsg. Rendering
// then continues without raw/alt mode, so frames land on the main screen —
// degraded and reported, which is the lesser harm next to discarding the
// conversation or dying silently.
func (p *Program) acquireTerminal() error {
	// The loop is un-parked whatever happens to the mode negotiation, for the
	// same reason releaseTerminal does: a program that cannot be typed at
	// cannot be told to leave. The degraded path an error below opens onto is
	// documented as the lesser harm, and it is only that if the user can still
	// reach :quit.
	defer p.resumeInput()
	if err := p.tty.MakeRaw(); err != nil {
		return err
	}
	if err := p.screen.Start(); err != nil {
		return err
	}
	p.forceRepaint()
	// The size is re-checked, not announced: refreshSize is the one place that
	// decides when the model hears about a size, and it sends without blocking.
	// Blocking matters here because this runs *on* the event loop, which is the
	// only thing that drains p.msgs — a queue filled by the session's streaming
	// output while the editor was in the foreground would otherwise stop the
	// program on the far side of the handoff, with no loop left to drain it.
	p.refreshSize()
	return nil
}

// forceRepaint clears the frame caches so the next render is a full repaint
// even when the view content is unchanged since before the suspend.
func (p *Program) forceRepaint() {
	p.mu.Lock()
	p.lastView = nil
	p.mu.Unlock()
	p.screen.Reset()
}
