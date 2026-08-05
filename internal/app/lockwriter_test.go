package app

import (
	"bytes"
	"sync"
	"testing"
)

func TestLockedWriter(t *testing.T) {
	var buf bytes.Buffer
	lw := NewLockedWriter(&buf)

	if _, err := lw.Write([]byte("hello")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := buf.String(); got != "hello" {
		t.Errorf("expected 'hello', got %q", got)
	}
}

func TestLockedWriterSerializesConcurrentWrites(t *testing.T) {
	var buf bytes.Buffer
	lw := NewLockedWriter(&buf)

	block := bytes.Repeat([]byte("x"), 64)
	const goroutines = 8
	const writesPerGoroutine = 100

	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < writesPerGoroutine; j++ {
				if _, err := lw.Write(block); err != nil {
					t.Errorf("write: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()

	want := goroutines * writesPerGoroutine * len(block)
	if buf.Len() != want {
		t.Fatalf("expected %d bytes, got %d", want, buf.Len())
	}
	// Every byte must be part of an intact block (no interleaving).
	data := buf.Bytes()
	for i := 0; i < len(data); i += len(block) {
		if !bytes.Equal(data[i:i+len(block)], block) {
			t.Fatalf("interleaved write detected at offset %d", i)
		}
	}
}
