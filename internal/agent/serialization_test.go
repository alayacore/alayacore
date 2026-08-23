package agent

// Tests for marshalToolInputData's malformed-input fallback.
//
// A provider can stream a truncated/non-JSON tool input. json.RawMessage's
// MarshalJSON validates the raw bytes, so marshaling it directly would
// abort the whole step inside handleToolInputComplete — before the tool
// goroutine starts — leaving the tool window stuck in its pending/spinner
// state with no UF frame to settle it. The fallback string-encodes the
// bytes so the frame still carries the input; the tool then fails to parse
// it and reports a normal error result (UF isError → ✗).

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/protocol"
)

func TestMarshalToolInputDataValidJSON(t *testing.T) {
	input := json.RawMessage(`{"path":"/tmp/x","content":"hello"}`)
	data, err := marshalToolInputData("call_1", "write_file", input)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	var td protocol.ToolInputData
	if err := json.Unmarshal(data, &td); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if td.ID != "call_1" || td.Name != "write_file" {
		t.Errorf("id/name = %q/%q, want call_1/write_file", td.ID, td.Name)
	}
	if string(td.Input) != string(input) {
		t.Errorf("valid input must pass through unchanged: got %q want %q", td.Input, input)
	}
}

func TestMarshalToolInputDataMalformedFallback(t *testing.T) {
	// Truncated JSON — the exact class that used to abort the step.
	input := json.RawMessage(`{"path":"/tmp/x","content":"hel`)
	data, err := marshalToolInputData("call_1", "write_file", input)
	if err != nil {
		t.Fatalf("marshal must not fail on malformed input: %v", err)
	}
	var td protocol.ToolInputData
	if err := json.Unmarshal(data, &td); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	// Fallback: input arrives as a JSON string (quoted) instead of raw bytes.
	var round string
	if err := json.Unmarshal(td.Input, &round); err != nil {
		t.Fatalf("fallback input must be a valid JSON string: %v", err)
	}
	if round != string(input) {
		t.Errorf("round-trip mismatch: %q != %q", round, input)
	}
}

func TestMarshalToolInputDataNilAndEmpty(t *testing.T) {
	// Start frames pass nil input — must stay JSON null.
	data, err := marshalToolInputData("call_1", "write_file", nil)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	var td protocol.ToolInputData
	if err := json.Unmarshal(data, &td); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if td.Input != nil {
		t.Errorf("nil input must stay null, got %q", td.Input)
	}
	// Empty (len 0) input behaves like nil.
	data, err = marshalToolInputData("call_2", "read_file", json.RawMessage{})
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	if err := json.Unmarshal(data, &td); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if td.Input != nil {
		t.Errorf("empty input must stay null, got %q", td.Input)
	}
}

func TestMarshalToolInputDataValidPrimitives(t *testing.T) {
	cases := []json.RawMessage{
		json.RawMessage(`"hello"`), // JSON string
		json.RawMessage(`123`),     // number
		json.RawMessage(`[1,2,3]`), // array
		json.RawMessage(`true`),    // bool
		json.RawMessage(`{"a":1}`), // object
	}
	for _, input := range cases {
		data, err := marshalToolInputData("call_1", "write_file", input)
		if err != nil {
			t.Fatalf("marshal failed for %s: %v", input, err)
		}
		var td protocol.ToolInputData
		if err := json.Unmarshal(data, &td); err != nil {
			t.Fatalf("unmarshal failed for %s: %v", input, err)
		}
		if string(td.Input) != string(input) {
			t.Errorf("valid input %s must pass through unchanged, got %q", input, td.Input)
		}
	}
}

func TestMarshalToolInputDataInvalidUTF8Fallback(t *testing.T) {
	// Malformed bytes including invalid UTF-8. json.Marshal replaces
	// invalid UTF-8 with U+FFFD inside the string (standard Go behavior),
	// so the fallback is always a valid JSON string.
	input := json.RawMessage("{\"path\":\"/tmp/x\",\"bad\":\xff\xfe}")
	data, err := marshalToolInputData("call_1", "write_file", input)
	if err != nil {
		t.Fatalf("marshal must not fail on invalid UTF-8: %v", err)
	}
	var td protocol.ToolInputData
	if err := json.Unmarshal(data, &td); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	var round string
	if err := json.Unmarshal(td.Input, &round); err != nil {
		t.Fatalf("fallback input must be a valid JSON string: %v", err)
	}
	if !json.Valid(td.Input) {
		t.Errorf("fallback must produce valid JSON, got %q", td.Input)
	}
	// Go json replaces invalid UTF-8 with U+FFFD, so the round trip is not
	// byte-identical — just assert the original ASCII prefix survives.
	if !strings.HasPrefix(round, `{"path":"/tmp/x","bad":`) {
		t.Errorf("fallback content mismatch: %q", round)
	}
}
