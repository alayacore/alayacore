# External Editor and WindowSizeMsg

When the user opens an external editor (e.g. via `Ctrl+O`) and then exits
back, the TUI runtime **always emits a `WindowSizeMsg`**, even if the
terminal was never resized. This file documents why.

## How It Works

### 1. Editor starts — terminal is released

`ExecProcess` (`internal/adapters/terminal/exec.go`) returns a `Cmd` that
the runtime dispatches via `Program.exec`. `exec` calls
`p.releaseTerminal()` (no arguments), which parks the input loop, leaves
the alt screen, and restores the terminal to its pre-program state. The
external editor then takes over the terminal (blocking).

### 2. Editor runs — SIGWINCH may be missed

While the editor has control of the terminal, the TUI runtime's
resize-listener goroutine is still running and listening for `SIGWINCH`.
However, as the `RestoreTerminal()` comment in the upstream code explains:

```go
// If the output is a terminal, it may have been resized while another
// process was at the foreground, in which case we may not have received
// SIGWINCH. Detect any size change now and propagate the new size as
// needed.
```

### 3. Editor exits — terminal is restored — `WindowSizeMsg` is sent

After the editor process finishes, `exec` calls `p.acquireTerminal()`
(`internal/adapters/terminal/exec.go`), which re-enters raw mode and the
alt screen, resumes the input loop, forces a full repaint, queries the
current terminal size, and **always sends a `WindowSizeMsg`**:

```go
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
```

There is **no comparison against the previous size** — the message is sent
unconditionally every time `acquireTerminal()` runs after an external
editor (or any other foreground child) returns.

## Implications for AlayaCore

Since `Terminal.handleWindowSize()` re-renders the display on every
`WindowSizeMsg`, the display will be re-rendered after every external
editor session, even when no resize occurred. This is a harmless no-op
(same width → same output) but worth being aware of when debugging or
tracing message flow.