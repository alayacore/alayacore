//go:build windows

package terminal

// Windows input source (module 2): the console input buffer read as events, and
// translated into the byte stream the parser reads (console_events.go).
//
// The console is read as events rather than as bytes because a byte read cannot be
// bounded. Go turns os.File.Read on a console into ReadConsole, which in cooked
// mode waits for a whole line and which no API reliably cancels
// (CancelIoEx/CancelSynchronousIo are best-effort on console input — the reason
// Bubble Tea's reader warns against counting on cancellation, and the reason
// cancelreader's Cancel can return false). An unbounded read is not a small
// inefficiency here: it is what made the editor handoff unusable. The program
// releases the terminal before starting the child, and the child shares the
// console's input buffer, so a read still pending when the handoff happens takes
// the child's keystrokes — and a read still pending when the program exits is a
// read os.File.Close waits for, which is a process that will not exit, which is a
// shell prompt that does not appear until the next keystroke satisfies it.
//
// Events have no such pending state. GetNumberOfConsoleInputEvents counts what is
// already in the buffer, and ReadConsoleInput consumes from that same buffer, so
// asking for no more events than were counted cannot block: the readiness test and
// the read are taken in the same currency. (A byte read cannot do that: it filters
// the events down to the ones that have a character form, so a count of pending
// events does not promise a byte read will return. That is why the count-gated
// byte read is not the fix, and why the translation lives here instead.)
//
// What is given up is ENABLE_VIRTUAL_TERMINAL_INPUT, the mode that used to do that
// translation for us in the byte stream — a mode this program does not set itself
// (x/term turns it on inside MakeRaw) and whose output no test had ever observed.
// In exchange the mapping is now a table in this tree, tested against the parser
// on every platform.

import (
	"fmt"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	// consoleInputPollInterval is how long next() waits before reporting that
	// the console has nothing. It bounds two things a user can feel: the delay
	// before a keystroke is read, and the delay before a park request takes
	// effect and the editor starts. The wait cannot be a real block on the
	// console handle — a console input handle is only waitable when it is
	// opened with FILE_FLAG_OVERLAPPED, and the one that answers it then
	// sometimes signals with nothing to read, which turns a wait into a spin —
	// so this is a poll, and it is short so that the poll is invisible.
	consoleInputPollInterval = 10 * time.Millisecond

	// consoleEventsPerRead caps how many events one call consumes. The cap
	// exists to bound the batch, not to protect the buffer: the encoded bytes
	// go into a slice this source owns, which grows for a large paste.
	consoleEventsPerRead = 128
)

var (
	modkernel32           = windows.NewLazySystemDLL("kernel32.dll")
	procReadConsoleInputW = modkernel32.NewProc("ReadConsoleInputW")
)

// consoleInput is the Windows implementation of inputSource.
type consoleInput struct {
	handle  windows.Handle
	records []inputRecord
	encoded []byte
	enc     keyEncoder
}

// newInput prepares the program's input source. The console is the only input
// this platform reads (openTTY falls back to CONIN$ precisely when stdin is not a
// console), so the one thing to establish is that the handle really is an input
// buffer: a console screen buffer answers the same queries for its own mode and
// would fail the first read instead. Failing here is the honest answer — the
// interface cannot read keys from this handle — and it is the same judgment
// enterVT makes about the output handle.
func newInput(t *TTY) (inputSource, error) {
	h := windows.Handle(t.In().Fd())
	if _, err := pendingConsoleEvents(h); err != nil {
		return nil, fmt.Errorf("terminal: this stream is not a console keyboard the interface can read (%w)", err)
	}
	return &consoleInput{
		handle:  h,
		records: make([]inputRecord, consoleEventsPerRead),
	}, nil
}

// next implements inputSource.
func (c *consoleInput) next() ([]byte, error) {
	pending, err := pendingConsoleEvents(c.handle)
	if err != nil {
		return nil, err
	}
	if pending == 0 {
		time.Sleep(consoleInputPollInterval)
		return nil, nil
	}

	n := min(int(pending), len(c.records))
	read, err := readConsoleEvents(c.handle, c.records[:n])
	if err != nil {
		return nil, err
	}
	if read == 0 {
		// The events counted are gone. Something else consumed them — the
		// console input buffer belongs to the screen, not to this process, and
		// a child that has just been handed the terminal is the likeliest
		// culprit — so wait rather than ask again immediately: a loop that
		// spun here would be spinning in the middle of an editor session.
		time.Sleep(consoleInputPollInterval)
		return nil, nil
	}

	c.encoded = c.encoded[:0]
	for _, rec := range c.records[:read] {
		c.encoded = c.enc.append(c.encoded, rec)
	}
	return c.encoded, nil
}

// pendingConsoleEvents counts the events queued on the console input buffer.
func pendingConsoleEvents(h windows.Handle) (uint32, error) {
	var n uint32
	if err := windows.GetNumberOfConsoleInputEvents(h, &n); err != nil {
		return 0, fmt.Errorf("GetNumberOfConsoleInputEvents: %w", err)
	}
	return n, nil
}

// readConsoleEvents consumes up to len(records) queued events and returns how
// many it took. ReadConsoleInputW has no binding in x/sys/windows, so the call is
// made through the lazy-proc route that package itself uses; the signature is
// (input handle, *INPUT_RECORD, DWORD count, *DWORD read) → BOOL.
//
// It is called only with a count the buffer was just reported to hold, which is
// what keeps it from blocking (see the file comment).
func readConsoleEvents(h windows.Handle, records []inputRecord) (int, error) {
	if len(records) == 0 {
		return 0, nil
	}
	var read uint32
	ok, _, err := procReadConsoleInputW.Call(
		uintptr(h),
		uintptr(unsafe.Pointer(&records[0])), //nolint:gosec // G103: the ABI the API's own signature asks for
		uintptr(len(records)),                //nolint:gosec // G115: a slice length, and the API takes a DWORD
		uintptr(unsafe.Pointer(&read)),       //nolint:gosec // G103: out-parameter, as above
	)
	if ok == 0 {
		if err != nil {
			return 0, fmt.Errorf("ReadConsoleInput: %w", err)
		}
		return 0, fmt.Errorf("ReadConsoleInput: %w", syscall.EINVAL)
	}
	return int(read), nil
}
