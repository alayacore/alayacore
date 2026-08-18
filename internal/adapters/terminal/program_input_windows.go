//go:build windows

package terminal

// Windows input loop (module 2): reads bytes from the TTY and feeds them to
// the parser. Console handles cannot be polled with the unix approach, so
// the loop blocks in a plain read; see exec_windows.go for the suspend
// limitation that follows.

import "time"

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
