package tlv

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"strings"
	"testing"
)

func TestReadTLV_RoundTrip(t *testing.T) {
	msg, err := EncodeTLV(TagUserT, "hello world")
	if err != nil {
		t.Fatalf("EncodeTLV() error = %v", err)
	}
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
	msg, err := EncodeTLV(TagUserEnd, "")
	if err != nil {
		t.Fatalf("EncodeTLV() error = %v", err)
	}
	tag, value, err := ReadTLV(bytes.NewReader(msg))
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

// TestCheckEncodeLength verifies the encode-side size guard without
// allocating a multi-GB string (maxMessageSize ≈ 2GB).
func TestCheckEncodeLength(t *testing.T) {
	if err := checkEncodeLength(0); err != nil {
		t.Errorf("zero length should pass, got: %v", err)
	}
	if err := checkEncodeLength(maxMessageSize); err != nil {
		t.Errorf("boundary length %d should pass, got: %v", maxMessageSize, err)
	}
	if err := checkEncodeLength(maxMessageSize + 1); err == nil {
		t.Error("expected error for length maxMessageSize+1, got nil")
	}
	if err := checkEncodeLength(1<<32 - 1); err == nil {
		t.Error("expected error for max uint32 length, got nil")
	}
}

// TestEncodeTLV_NormalValue verifies the two-value return of the new
// signature (success path).
func TestEncodeTLV_NormalValue(t *testing.T) {
	msg, err := EncodeTLV(TagUserT, "hello")
	if err != nil {
		t.Fatalf("EncodeTLV() error = %v", err)
	}
	if len(msg) != 6+len("hello") {
		t.Errorf("encoded length = %d, want %d", len(msg), 6+len("hello"))
	}
}

// TestEncodeTLV_InvalidTag verifies EncodeTLV rejects tags that are not
// exactly 2 characters with an error instead of panicking on the tag byte
// indexing — a user-supplied or malformed tag (e.g. from a misc tool or an
// external adapter) must fail cleanly.
func TestEncodeTLV_InvalidTag(t *testing.T) {
	for _, tag := range []string{"", "A", "ABC"} {
		t.Run(fmt.Sprintf("tag=%q", tag), func(t *testing.T) {
			if _, err := EncodeTLV(tag, "x"); err == nil {
				t.Fatalf("EncodeTLV(%q) succeeded, want an error", tag)
			}
		})
	}
}
