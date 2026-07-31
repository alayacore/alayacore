package tlv

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
)

func TestReadTLV_RoundTrip(t *testing.T) {
	msg := EncodeTLV(TagUserT, "hello world")
	tag, value, err := ReadTLV(bytes.NewReader(msg))
	if err != nil {
		t.Fatalf("ReadTLV() error = %v", err)
	}
	if tag != TagUserT {
		t.Errorf("tag = %q, want %q", tag, TagUserT)
	}
	if value != "hello world" {
		t.Errorf("value = %q, want %q", value, "hello world")
	}
}

func TestReadTLV_EmptyValue(t *testing.T) {
	tag, value, err := ReadTLV(bytes.NewReader(EncodeTLV(TagUserEnd, "")))
	if err != nil {
		t.Fatalf("ReadTLV() error = %v", err)
	}
	if tag != TagUserEnd {
		t.Errorf("tag = %q, want %q", tag, TagUserEnd)
	}
	if value != "" {
		t.Errorf("value = %q, want empty", value)
	}
}

// TestReadTLV_RejectsOversizedLength verifies the length guard:
// a peer-controlled length field must be rejected before any allocation.
func TestReadTLV_RejectsOversizedLength(t *testing.T) {
	// Header advertising the maximum uint32 length (~4GB — would OOM
	// without the guard, or panic on 32-bit platforms).
	header := []byte{TagUserT[0], TagUserT[1], 0xff, 0xff, 0xff, 0xff}
	_, _, err := ReadTLV(bytes.NewReader(header))
	if err == nil {
		t.Fatal("ReadTLV() expected error for oversized length, got nil")
	}
	if !strings.Contains(err.Error(), "exceeds maximum") {
		t.Errorf("error = %q, want mention of maximum size", err)
	}

	// Just above the limit (maxMessageSize + 1) is also rejected.
	var over [6]byte
	over[0], over[1] = TagUserT[0], TagUserT[1]
	binary.BigEndian.PutUint32(over[2:], maxMessageSize+1)
	_, _, err = ReadTLV(bytes.NewReader(over[:]))
	if err == nil {
		t.Fatal("ReadTLV() expected error for length > maxMessageSize, got nil")
	}

	// Boundary: length == maxMessageSize passes the size check and falls
	// through to reading the value bytes (which are absent here, so the
	// error must be a read error, NOT a size error).
	var atLimit [6]byte
	atLimit[0], atLimit[1] = TagUserT[0], TagUserT[1]
	binary.BigEndian.PutUint32(atLimit[2:], maxMessageSize)
	_, _, err = ReadTLV(bytes.NewReader(atLimit[:]))
	if err == nil {
		t.Fatal("expected error (value bytes missing), got nil")
	}
	if strings.Contains(err.Error(), "exceeds maximum") {
		t.Errorf("boundary length %d should pass the size check, got: %v", maxMessageSize, err)
	}
}

func TestReadTLV_TruncatedHeader(t *testing.T) {
	_, _, err := ReadTLV(bytes.NewReader([]byte{TagUserT[0]}))
	if err == nil {
		t.Fatal("expected error for truncated header, got nil")
	}
}
