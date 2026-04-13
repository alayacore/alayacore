# External Editor and WindowSizeMsg

When the user opens an external editor (e.g. via `Ctrl+O`) and then exits back, Bubble Tea **always emits a `WindowSizeMsg`**, even if the terminal was never resized.

## How It Works

### 1. Editor starts — terminal is released

`tea.ExecProcess` triggers `p.exec()`, which calls `p.releaseTerminal(false)`. This stops the renderer, cancels the input reader, and restores the original terminal state. The external editor then takes over the terminal (blocking).

### 2. Editor runs — SIGWINCH may be missed

While the editor has control of the terminal, the Bubble Tea program's `listenForResize` goroutine is still running and listening for `SIGWINCH`. However, as the `RestoreTerminal()` comment explains:

```go
// If the output is a terminal, it may have been resized while another
// process was at the foreground, in which case we may not have received
// SIGWINCH. Detect any size change now and propagate the new size as
// needed.
```

### 3. Editor exits — terminal is restored — `checkResize()` is called

After the editor process finishes, `p.exec()` calls `p.RestoreTerminal()`, which reinitializes the terminal and then calls:

```go
go p.checkResize()
```

This queries the current terminal size and **always sends a `WindowSizeMsg`**:

```go
func (p *Program) checkResize() {
	if p.ttyOutput == nil {
		return
	}

	w, h, err := term.GetSize(p.ttyOutput.Fd())
	if err != nil {
		return
	}

	p.width, p.height = w, h
	p.Send(WindowSizeMsg{Width: w, Height: h})
}
```

Note that there is **no comparison against the previous size** — the message is sent unconditionally every time `checkResize()` runs.

## Implications for AlayaCore

Since `Terminal.handleWindowSize()` re-renders the display on every `WindowSizeMsg`, the display will be re-rendered after every external editor session, even when no resize occurred. This is a harmless no-op (same width → same output) but worth being aware of when debugging or tracing message flow.
