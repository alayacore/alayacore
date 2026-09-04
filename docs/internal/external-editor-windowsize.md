# External Editor and WindowSizeMsg

When the user opens an external editor (e.g. via `Ctrl+O`) and then exits
back, the TUI runtime re-checks the terminal size and tells the model about it.
This file documents how, and when it does not.

## How It Works

### 1. Editor starts — the input loop parks, then the terminal is released

`ExecProcess` (`internal/adapters/terminal/exec.go`) returns a `Cmd` that
the runtime dispatches via `Program.exec`. `exec` calls `p.releaseTerminal()`,
which waits for the input loop to report that it is between reads, leaves the
alt screen, and restores the terminal to its pre-program state. The external
editor then takes over the terminal (blocking) with the keyboard to itself —
which is what the parking step is for, and the reason it waits rather than
asking and moving on. See [windows-console.md](windows-console.md).

### 2. Editor runs — SIGWINCH may be missed

On Unix, the resize-listener goroutine (`watchSignals`) is still running and
listening for `SIGWINCH` while the editor owns the terminal. However, as the
`RestoreTerminal()` comment in the upstream code explains:

```go
// If the output is a terminal, it may have been resized while another
// process was at the foreground, in which case we may not have received
// SIGWINCH. Detect any size change now and propagate the new size as
// needed.
```

On Windows there is no `SIGWINCH` to miss — `signals_windows.go` registers only
`SIGINT`/`SIGTERM`, and `resizeSignal()` returns `nil` — so the query below is the
*only* resize information the program has while a child is in the foreground, and
it is load-bearing rather than belt-and-braces. (The console does queue a
window-buffer-size event, and the input loop reads it; `refreshSize` remains the
one that decides, for the reason in windows-console.md.) With no child running,
size changes come from `Program.refreshSize` on the model tick.

### 3. Editor exits — terminal is restored — the size is re-checked

After the editor process finishes, `exec` calls `p.acquireTerminal()`
(`internal/adapters/terminal/exec.go`), which re-enters raw mode and the
alt screen, resumes the input loop, forces a full repaint, and re-checks the
size through the same function the model tick uses:

```go
func (p *Program) acquireTerminal() error {
	if err := p.tty.MakeRaw(); err != nil {
		return err
	}
	if err := p.screen.Start(); err != nil {
		return err
	}
	p.resumeInput()
	p.forceRepaint()
	p.refreshSize()
	return nil
}
```

So the message is sent **when the size changed**, and not when it did not —
`refreshSize` compares against the size the loop last committed to and leaves the
tracked pair alone if its send cannot be queued. The full repaint is
unconditional: whatever the child drew on the way out is gone from the screen
either way.

## Implications for AlayaCore

`Terminal.handleWindowSize()` re-renders the display on every `WindowSizeMsg`, so a
resize that happened while the editor was in the foreground is picked up on the
far side of the handoff. When nothing changed, no message is sent and the
repaint from `forceRepaint` is the only work done — which is the same frame
content, so nothing about the display depends on the distinction.
