package stream

import "io"

// GenericWriter wraps any io.Writer as a stream.Output.
type GenericWriter struct {
	io.Writer
}

func (w *GenericWriter) WriteString(s string) (int, error) {
	return w.Writer.Write([]byte(s))
}

func (w *GenericWriter) Flush() error {
	if f, ok := w.Writer.(interface{ Flush() error }); ok {
		return f.Flush()
	}
	return nil
}

// GenericReader wraps any io.Reader as a stream.Input.
type GenericReader struct {
	io.Reader
}

func (r *GenericReader) Read(p []byte) (int, error) {
	return r.Reader.Read(p)
}
