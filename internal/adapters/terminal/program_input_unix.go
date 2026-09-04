//go:build !windows

package terminal

// Unix input source (module 2): the TTY read as the byte stream it already is.
//
// The read is polled rather than waited in: a read on a Unix terminal cannot be
// interrupted, so the only way to bound it is not to start one until something is
// there to read. That is what makes the park protocol (program_input.go) work —
// the loop is between reads once every inputPollTimeout at the latest, and a
// foreground child is handed a terminal with no read of ours pending on it.

import "golang.org/x/sys/unix"

// inputPollTimeout is how long next() waits for the terminal to become readable,
// in the milliseconds unix.Poll counts in. Input costs nothing: Poll returns as
// soon as the terminal has any. What the timeout bounds is everything else — how
// long a park request takes, and how long the loop takes to notice that the
// program is finishing.
const inputPollTimeout = 100

// inputReadSize is the batch boundary: how many bytes one read takes from the
// terminal. It is not a limit on what can be delivered — the parser is a stream
// and keeps an incomplete sequence across calls (key_parser.go).
const inputReadSize = 256

// ttyInput is the Unix implementation of inputSource.
type ttyInput struct {
	tty *TTY
	buf []byte
	fds []unix.PollFd
}

// newInput prepares the program's input source. There is nothing to negotiate:
// MakeRaw has already proved this stream is a terminal, and a terminal is read as
// bytes.
func newInput(t *TTY) (inputSource, error) {
	return &ttyInput{
		tty: t,
		buf: make([]byte, inputReadSize),
		fds: []unix.PollFd{{Fd: int32(t.In().Fd()), Events: unix.POLLIN}},
	}, nil
}

// next implements inputSource.
func (i *ttyInput) next() ([]byte, error) {
	n, err := unix.Poll(i.fds, inputPollTimeout)
	if err != nil {
		if err == unix.EINTR {
			// A signal (SIGWINCH, the common one here) interrupted the wait.
			// Reporting "nothing yet" is the right answer: the loop re-checks
			// the park request, and the resize it interrupted is delivered by
			// the signal watcher, not by this reader.
			return nil, nil
		}
		return nil, err
	}
	if n == 0 {
		return nil, nil // timeout: nothing to read
	}
	cnt, err := i.tty.Read(i.buf)
	if cnt > 0 {
		return i.buf[:cnt], nil
	}
	return nil, err
}
