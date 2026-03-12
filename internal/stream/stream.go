package stream

// Package stream defines the minimal IO abstraction and TLV encoding
// used between adaptors (terminal/websocket) and the core session.
// It intentionally stays small: a simple Input/Output pair plus helpers
// for reading/writing framed Tag-Length-Value messages.

import (
	"encoding/binary"
	"io"
)

// Message tags for TLV protocol.
const (
	TagUserText      = 'U' // User text input
	TagAssistantText = 'A' // Assistant text output
	TagReasoning     = 'R' // Reasoning/thinking content
	TagTool          = 'T' // Tool call output
	TagError         = 'E' // Error messages
	TagNotify        = 'N' // Notification messages
	TagSystem        = 'S' // System messages (queue status, model info, etc.)
)

// ChanInput implements Input using a channel of raw TLV-encoded messages.
type ChanInput struct {
	ch  chan []byte
	buf []byte
}

// NewChanInput creates a ChanInput with the given buffer size.
func NewChanInput(bufferSize int) *ChanInput {
	return &ChanInput{ch: make(chan []byte, bufferSize)}
}

// Close closes the input channel, causing Read to return EOF.
func (i *ChanInput) Close() error {
	close(i.ch)
	return nil
}

// Read implements Input. Returns io.EOF when the channel is closed.
func (i *ChanInput) Read(p []byte) (n int, err error) {
	if len(i.buf) > 0 {
		n = copy(p, i.buf)
		i.buf = i.buf[n:]
		return n, nil
	}

	msg, ok := <-i.ch
	if !ok {
		return 0, io.EOF
	}

	i.buf = msg
	n = copy(p, i.buf)
	i.buf = i.buf[n:]
	return n, nil
}

// Emit sends data to the input channel.
func (i *ChanInput) Emit(data []byte) error {
	i.ch <- data
	return nil
}

// EncodeTLV creates a TLV-encoded byte slice.
func EncodeTLV(tag byte, value string) []byte {
	data := []byte(value)
	length := int32(len(data))

	msg := make([]byte, 5+length)
	msg[0] = tag
	binary.BigEndian.PutUint32(msg[1:], uint32(length))
	copy(msg[5:], data)

	return msg
}

// EmitTLV writes a TLV-encoded message to the input.
func (i *ChanInput) EmitTLV(tag byte, value string) error {
	return i.Emit(EncodeTLV(tag, value))
}

// WriteTLV writes a TLV message to the output.
func WriteTLV(output Output, tag byte, value string) error {
	_, err := output.Write(EncodeTLV(tag, value))
	return err
}

// ReadTLV reads a single TLV-framed message from input.
// It blocks until a full frame has been read or an error occurs.
func ReadTLV(input Input) (byte, string, error) {
	header := make([]byte, 5)
	if _, err := io.ReadFull(input, header); err != nil {
		return 0, "", err
	}
	tag := header[0]
	length := binary.BigEndian.Uint32(header[1:])

	if length == 0 {
		return tag, "", nil
	}

	valueBuf := make([]byte, length)
	if _, err := io.ReadFull(input, valueBuf); err != nil {
		return 0, "", err
	}

	return tag, string(valueBuf), nil
}

// Input defines the input interface for the agent processor.
type Input interface {
	Read(p []byte) (n int, err error)
}

// Output defines the output interface for the agent processor.
type Output interface {
	Write(p []byte) (n int, err error)
	WriteString(s string) (n int, err error)
	Flush() error
}

// ReadCloser combines Input with io.Closer.
type ReadCloser struct {
	Input
}

func (rc *ReadCloser) Close() error {
	return nil
}

// WriteCloser combines Output with io.Closer.
type WriteCloser struct {
	Output
}

func (wc *WriteCloser) Close() error {
	return nil
}

// NopInput is an Input that always returns EOF.
type NopInput struct{}

func (n *NopInput) Read(p []byte) (int, error) {
	return 0, io.EOF
}

// NopOutput is an Output that discards all output.
type NopOutput struct{}

func (n *NopOutput) Write(p []byte) (int, error) {
	return len(p), nil
}

func (n *NopOutput) WriteString(s string) (int, error) {
	return len(s), nil
}

func (n *NopOutput) Flush() error {
	return nil
}
