package stream

import (
	"bytes"
	"io"
	"testing"
)

func TestEncodeTLV(t *testing.T) {
	tests := []struct {
		name    string
		tag     string
		value   string
		wantLen int
		wantTag string
		wantVal string
	}{
		{
			name:    "simple message",
			tag:     TagTextUser,
			value:   "Hello, World!",
			wantLen: 6 + 13, // 6 header + 13 value
			wantTag: TagTextUser,
			wantVal: "Hello, World!",
		},
		{
			name:    "empty value",
			tag:     TagTextAssistant,
			value:   "",
			wantLen: 6, // just header
			wantTag: TagTextAssistant,
			wantVal: "",
		},
		{
			name:    "unicode value",
			tag:     TagTextUser,
			value:   "你好世界 🌍",
			wantLen: 6 + len("你好世界 🌍"), // 6 header + actual byte length
			wantTag: TagTextUser,
			wantVal: "你好世界 🌍",
		},
		{
			name:    "long message",
			tag:     TagTextReasoning,
			value:   string(make([]byte, 1000)),
			wantLen: 6 + 1000,
			wantTag: TagTextReasoning,
			wantVal: string(make([]byte, 1000)),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encoded := EncodeTLV(tt.tag, tt.value)
			if len(encoded) != tt.wantLen {
				t.Errorf("EncodeTLV() length = %d, want %d", len(encoded), tt.wantLen)
			}

			// Decode and verify
			tag, value, err := ReadTLV(&byteReader{data: encoded})
			if err != nil {
				t.Fatalf("ReadTLV() error = %v", err)
			}
			if tag != tt.wantTag {
				t.Errorf("ReadTLV() tag = %q, want %q", tag, tt.wantTag)
			}
			if value != tt.wantVal {
				t.Errorf("ReadTLV() value = %q, want %q", value, tt.wantVal)
			}
		})
	}
}

func TestChanInput(t *testing.T) {
	t.Run("emit and read", func(t *testing.T) {
		input := NewChanInput(10)

		// Emit a message
		err := input.Emit([]byte("test message"))
		if err != nil {
			t.Fatalf("Emit() error = %v", err)
		}

		// Read the message
		buf := make([]byte, 100)
		n, err := input.Read(buf)
		if err != nil {
			t.Fatalf("Read() error = %v", err)
		}
		if string(buf[:n]) != "test message" {
			t.Errorf("Read() = %q, want %q", string(buf[:n]), "test message")
		}
	})

	t.Run("emit TLV", func(t *testing.T) {
		input := NewChanInput(10)

		// Emit TLV message
		err := input.EmitTLV(TagTextUser, "Hello")
		if err != nil {
			t.Fatalf("EmitTLV() error = %v", err)
		}

		// Read and decode
		tag, value, err := ReadTLV(input)
		if err != nil {
			t.Fatalf("ReadTLV() error = %v", err)
		}
		if tag != TagTextUser {
			t.Errorf("ReadTLV() tag = %q, want %q", tag, TagTextUser)
		}
		if value != "Hello" {
			t.Errorf("ReadTLV() value = %q, want %q", value, "Hello")
		}
	})

	t.Run("close returns EOF", func(t *testing.T) {
		input := NewChanInput(10)

		// Close the input
		err := input.Close()
		if err != nil {
			t.Fatalf("Close() error = %v", err)
		}

		// Read should return EOF
		buf := make([]byte, 100)
		_, err = input.Read(buf)
		if err != io.EOF {
			t.Errorf("Read() error = %v, want io.EOF", err)
		}
	})

	t.Run("multiple messages", func(t *testing.T) {
		input := NewChanInput(10)

		messages := []struct {
			tag   string
			value string
		}{
			{TagTextUser, "first"},
			{TagTextAssistant, "second"},
			{TagTextReasoning, "third"},
		}

		// Emit all messages
		for _, msg := range messages {
			err := input.EmitTLV(msg.tag, msg.value)
			if err != nil {
				t.Fatalf("EmitTLV() error = %v", err)
			}
		}

		// Read and verify all messages
		for _, want := range messages {
			tag, value, err := ReadTLV(input)
			if err != nil {
				t.Fatalf("ReadTLV() error = %v", err)
			}
			if tag != want.tag || value != want.value {
				t.Errorf("ReadTLV() = (%q, %q), want (%q, %q)",
					tag, value, want.tag, want.value)
			}
		}
	})
}

func TestWriteTLV(t *testing.T) {
	t.Run("write to buffer", func(t *testing.T) {
		buf := &bytes.Buffer{}
		output := &bufferOutput{buf}

		err := WriteTLV(output, TagTextUser, "test message")
		if err != nil {
			t.Fatalf("WriteTLV() error = %v", err)
		}

		// Verify the written data
		tag, value, err := ReadTLV(&byteReader{data: buf.Bytes()})
		if err != nil {
			t.Fatalf("ReadTLV() error = %v", err)
		}
		if tag != TagTextUser {
			t.Errorf("tag = %q, want %q", tag, TagTextUser)
		}
		if value != "test message" {
			t.Errorf("value = %q, want %q", value, "test message")
		}
	})
}

// byteReader wraps a byte slice to implement Input
type byteReader struct {
	data []byte
	pos  int
}

func (r *byteReader) Read(p []byte) (n int, err error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	n = copy(p, r.data[r.pos:])
	r.pos += n
	return n, nil
}

// bufferOutput wraps a bytes.Buffer to implement Output
type bufferOutput struct {
	*bytes.Buffer
}

func (b *bufferOutput) Flush() error {
	return nil
}
