package terminal

// External process execution and terminal suspension. This is module 5 of
// the self-built TUI stack (see docs/tui-architecture.md): the editor handoff
// (ExecProcess) and Ctrl-Z suspend both release the terminal (exit alt
// screen, restore cooked mode), run something in the foreground, then
// re-acquire the terminal (raw mode + alt screen) and repaint.
//
// The sequence mirrors bubbletea's exec.go + tty.go suspend path. The
// platform-specific parts (parking the input loop so a foreground child
// gets every keystroke, SIGTSTP/SIGCONT) live in exec_unix.go /
// exec_windows.go.

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
// returns, no input is read by the program, so a foreground child gets
// every keystroke.
func (p *Program) releaseTerminal() error {
	p.pauseInput() // park the input loop (no-op where unsupported)
	if err := p.screen.Stop(); err != nil {
		return err
	}
	return p.tty.Restore()
}

// acquireTerminal re-enters raw mode and the alt screen, resumes the input
// loop, forces a full repaint, re-checks the terminal size (it may have
// changed while another process was at the foreground), and propagates the
// new size to the model.
func (p *Program) acquireTerminal() error {
	if err := p.tty.MakeRaw(); err != nil {
		return err
	}
	if err := p.screen.Start(); err != nil {
		return err
	}
	p.resumeInput()
	p.forceRedraw()
	p.width, p.height = p.screen.Size()
	p.msgs <- WindowSizeMsg{Width: p.width, Height: p.height}
	return nil
}

// forceRedraw clears the frame caches so the next render is a full repaint
// even when the view content is unchanged since before the suspend.
func (p *Program) forceRedraw() {
	p.mu.Lock()
	p.lastView = nil
	p.mu.Unlock()
	p.screen.Reset()
}
