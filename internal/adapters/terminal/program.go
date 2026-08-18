package terminal

// Program: the event loop that drives the TUI — Update/Cmd dispatch, ticks,
// rendering, and terminal lifecycle. This is module 3 of the self-built TUI
// stack (see REFACTOR.md §8.3).
//
// The loop mirrors bubbletea's (third_party/bubbletea/tea.go) minus the
// cell-buffer renderer: messages arrive from the input reader and from
// commands, special messages (QuitMsg/SuspendMsg/BatchMsg/sequenceMsg/
// ClearScreenMsg/WindowSizeMsg) are handled internally, everything else goes
// to model.Update. After every Update the view is rendered through
// Screen.Render (raw passthrough; skipped when content and cursor are
// unchanged). Terminal cleanup (alt screen exit + raw mode restore) happens
// in Run's defer, so panics and errors always restore the terminal.

import (
	"fmt"
	"image/color"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

// Msg contains data from the result of an I/O operation. Messages trigger
// the update function.
type Msg any

// Cmd is an I/O operation that returns a message when it's complete. If
// it's nil it's considered a no-op.
type Cmd func() Msg

// Model contains the program's state as well as its core functions.
type Model interface {
	// Init is the first function that will be called. It returns an
	// optional initial command.
	Init() Cmd

	// Update is called when a message is received.
	Update(Msg) (Model, Cmd)

	// View renders the program's UI after every Update.
	View() View
}

// View represents the terminal view rendered after each Update.
type View struct {
	// Content is the screen content of the view, written verbatim to the
	// terminal (raw passthrough mode).
	Content string

	// Cursor, when not nil, shows the real terminal cursor at the given
	// position (X, Y are cell coordinates relative to the frame's top-left
	// corner). A visible real cursor is required for IME on-the-spot
	// preedit rendering and candidate-window anchoring.
	Cursor *Cursor

	// AltScreen puts the program in the alternate screen buffer.
	AltScreen bool

	// Raw is the passthrough mode used by this application: content is
	// written verbatim so the terminal soft-wraps it natively.
	Raw bool

	// ReportFocus enables focus reporting (FocusMsg/BlurMsg).
	ReportFocus bool
}

// NewView is a helper function to create a View with the given content.
func NewView(s string) View {
	return View{Content: s}
}

// CursorShape represents a terminal cursor shape.
type CursorShape int

// Cursor shapes (DECSCUSR parameter values).
const (
	CursorBlock CursorShape = iota
	CursorUnderline
	CursorBar
)

// Cursor represents a cursor on the terminal screen.
type Cursor struct {
	X, Y  int
	Color color.Color
	Shape CursorShape
	Blink bool
}

// NewCursor returns a new cursor with the default settings and the given
// position (block, blinking).
func NewCursor(x, y int) *Cursor {
	return &Cursor{X: x, Y: y, Shape: CursorBlock, Blink: true}
}

// Quit is a special command that tells the program to exit.
func Quit() Msg { return QuitMsg{} }

// QuitMsg signals that the program should quit.
type QuitMsg struct{}

// Suspend is a special command that tells the program to suspend.
// NOTE: suspension (editor handoff, Ctrl-Z) is module 5 (S2); the message
// is currently ignored by the loop.
func Suspend() Msg { return SuspendMsg{} }

// SuspendMsg signals the program should suspend.
type SuspendMsg struct{}

// ClearScreen is a special command that forces a full clear+redraw. In raw
// passthrough mode every render already clears the whole screen, so this is
// effectively a no-op (kept for API compatibility).
func ClearScreen() Msg { return clearScreenMsg{} }

type clearScreenMsg struct{}

// BatchMsg is a message used to perform a bunch of commands concurrently.
type BatchMsg []Cmd

// Batch performs a bunch of commands concurrently with no ordering
// guarantees about the results.
func Batch(cmds ...Cmd) Cmd {
	return compactCmds[BatchMsg](cmds)
}

// sequenceMsg runs commands one at a time, in order.
type sequenceMsg []Cmd

// Sequence runs the given commands one at a time, in order.
func Sequence(cmds ...Cmd) Cmd {
	return compactCmds[sequenceMsg](cmds)
}

// compactCmds ignores nil commands and returns the most direct command
// possible (nil for none, the single command itself, or a batch/sequence).
func compactCmds[T ~[]Cmd](cmds []Cmd) Cmd {
	var valid []Cmd
	for _, c := range cmds {
		if c != nil {
			valid = append(valid, c)
		}
	}
	switch len(valid) {
	case 0:
		return nil
	case 1:
		return valid[0]
	default:
		return func() Msg { return T(valid) }
	}
}

// Tick produces a command that fires once after the given duration.
func Tick(d time.Duration, fn func(time.Time) Msg) Cmd {
	t := time.NewTimer(d)
	return func() Msg {
		ts := <-t.C
		t.Stop()
		for len(t.C) > 0 {
			<-t.C
		}
		return fn(ts)
	}
}

// escSequenceTimeout is how long the input loop waits for more bytes after
// an incomplete escape sequence (e.g. a lone ESC) before resolving it.
const escSequenceTimeout = 50 * time.Millisecond

// Program runs a Model.
type Program struct {
	tty    *TTY
	parser *InputParser
	screen *Screen

	msgs chan Msg
	cmds chan Cmd

	width, height int

	mu       sync.Mutex
	lastView *View // content/cursor of the last rendered frame
}

// Run starts the TUI program: opens the TTY, enters raw mode and the alt
// screen, drives the model, and restores the terminal before returning.
func Run(model Model) (Model, error) {
	tty, err := OpenTTY()
	if err != nil {
		return model, err
	}
	if err := tty.MakeRaw(); err != nil {
		return model, fmt.Errorf("terminal: enter raw mode: %w", err)
	}

	p := &Program{
		tty:    tty,
		parser: &InputParser{},
		screen: NewScreen(tty.Out()),
		msgs:   make(chan Msg, 64),
		cmds:   make(chan Cmd),
	}
	p.width, p.height = p.screen.Size()

	if err := p.screen.Start(); err != nil {
		_ = tty.Restore()
		return model, err
	}

	// Restore the terminal on every exit path (including panics).
	defer func() {
		_ = p.screen.Stop()
		_ = tty.Restore()
	}()

	return p.run(model)
}

// run is the main loop, factored out so tests can drive the program with an
// injected message channel (Program{msgs: ch}).
func (p *Program) run(model Model) (Model, error) {
	ctxDone := make(chan struct{})
	defer close(ctxDone)

	if p.tty != nil {
		// Initial window size, input reader, and signal watcher.
		p.msgs <- WindowSizeMsg{Width: p.width, Height: p.height}
		go p.readInput(ctxDone)
		go p.watchSignals(ctxDone)
	}

	// Initial command and first render.
	cmd := model.Init()
	if cmd != nil {
		go p.dispatch(cmd, ctxDone)
	}
	p.render(model)

	for msg := range p.msgs {
		switch msg := msg.(type) {
		case QuitMsg:
			return model, nil
		case SuspendMsg:
			// Module 5 (S2): suspend + editor handoff.
			continue
		case clearScreenMsg:
			continue
		case BatchMsg:
			go p.execBatch(msg, ctxDone)
			continue
		case sequenceMsg:
			go p.execSequence(msg, ctxDone)
			continue
		case WindowSizeMsg:
			p.width, p.height = msg.Width, msg.Height
			p.screen.Resize(msg.Width, msg.Height)
		}

		var cmd Cmd
		var err error
		func() {
			defer func() {
				if r := recover(); r != nil {
					err = fmt.Errorf("terminal: panic in Update: %v", r)
				}
			}()
			model, cmd = model.Update(msg)
		}()
		if err != nil {
			return model, err
		}
		if cmd != nil {
			go p.dispatch(cmd, ctxDone)
		}
		p.render(model)
	}
	return model, nil // unreachable: msgs never closes
}

// dispatch runs a command in a goroutine and delivers its result message.
func (p *Program) dispatch(cmd Cmd, ctxDone <-chan struct{}) {
	msg := cmd()
	if msg == nil {
		return
	}
	select {
	case p.msgs <- msg:
	case <-ctxDone:
	}
}

// execBatch runs a batch of commands concurrently.
func (p *Program) execBatch(cmds BatchMsg, ctxDone <-chan struct{}) {
	var wg sync.WaitGroup
	for _, cmd := range cmds {
		if cmd == nil {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			p.execOne(cmd, ctxDone)
		}()
	}
	wg.Wait()
}

// execSequence runs commands one at a time, in order.
func (p *Program) execSequence(cmds sequenceMsg, ctxDone <-chan struct{}) {
	for _, cmd := range cmds {
		if cmd == nil {
			continue
		}
		msg := cmd()
		switch msg := msg.(type) {
		case BatchMsg:
			p.execBatch(msg, ctxDone)
		case sequenceMsg:
			p.execSequence(msg, ctxDone)
		default:
			if msg != nil {
				select {
				case p.msgs <- msg:
				case <-ctxDone:
					return
				}
			}
		}
	}
}

// execOne runs a single command and delivers its result.
func (p *Program) execOne(cmd Cmd, ctxDone <-chan struct{}) {
	msg := cmd()
	if msg == nil {
		return
	}
	select {
	case p.msgs <- msg:
	case <-ctxDone:
	}
}

// readInput reads bytes from the TTY and feeds them to the parser. When a
// read ends with an incomplete escape sequence, it waits briefly for more
// bytes before resolving it (so a lone ESC becomes the Escape key).
func (p *Program) readInput(ctxDone <-chan struct{}) {
	buf := make([]byte, 256)
	for {
		n, err := p.tty.Read(buf)
		if n > 0 {
			if msgs := p.parser.Parse(buf[:n]); len(msgs) > 0 {
				for _, msg := range msgs {
					select {
					case p.msgs <- msg:
					case <-ctxDone:
						return
					}
				}
			}
			if p.parser.HasPending() {
				// Wait briefly for the rest of a split sequence.
				select {
				case <-time.After(escSequenceTimeout):
					if msgs := p.parser.Flush(); len(msgs) > 0 {
						for _, msg := range msgs {
							select {
							case p.msgs <- msg:
							case <-ctxDone:
								return
							}
						}
					}
				case <-ctxDone:
					return
				}
			}
		}
		if err != nil {
			return
		}
	}
}

// watchSignals handles SIGINT/SIGTERM (quit) and SIGWINCH (resize).
func (p *Program) watchSignals(ctxDone <-chan struct{}) {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM, syscall.SIGWINCH)
	defer signal.Stop(sig)
	for {
		select {
		case <-ctxDone:
			return
		case s := <-sig:
			switch s {
			case syscall.SIGWINCH:
				p.width, p.height = p.screen.Size()
				select {
				case p.msgs <- WindowSizeMsg{Width: p.width, Height: p.height}:
				case <-ctxDone:
					return
				}
			default:
				select {
				case p.msgs <- QuitMsg{}:
				case <-ctxDone:
					return
				}
			}
		}
	}
}

// render renders the given model's view. It is a no-op when the content and
// cursor are unchanged since the last frame.
func (p *Program) render(model Model) {
	v := model.View()
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.lastView != nil &&
		p.lastView.Content == v.Content &&
		cursorsEqual(p.lastView.Cursor, v.Cursor) &&
		p.lastView.AltScreen == v.AltScreen &&
		p.lastView.Raw == v.Raw {
		return
	}
	_ = p.screen.Render(v.Content, v.Cursor)
	last := v
	p.lastView = &last
}
