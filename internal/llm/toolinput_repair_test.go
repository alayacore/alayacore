package llm

import (
	"encoding/json"
	"testing"
)

// ============================================================================
// RepairToolInput Unit Tests
// ============================================================================

func TestRepairToolInput_NullOptionalField(t *testing.T) {
	// Pattern 1: null for optional field → remove it
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {"type": "string"},
			"start_line": {"type": "integer"}
		},
		"required": ["path"]
	}`)

	tests := []struct {
		name  string
		input json.RawMessage
		want  json.RawMessage
	}{
		{
			name:  "null optional field removed",
			input: json.RawMessage(`{"path":"/foo","start_line":null}`),
			want:  json.RawMessage(`{"path":"/foo"}`),
		},
		{
			name:  "null required field kept",
			input: json.RawMessage(`{"path":null}`),
			want:  json.RawMessage(`{"path":null}`),
		},
		{
			name:  "multiple null optionals removed",
			input: json.RawMessage(`{"path":"/foo","start_line":null,"num_lines":null}`),
			want:  json.RawMessage(`{"path":"/foo"}`),
		},
		{
			name:  "no null fields unchanged",
			input: json.RawMessage(`{"path":"/foo","start_line":5}`),
			want:  json.RawMessage(`{"path":"/foo","start_line":5}`),
		},
		{
			name:  "empty input with required field null",
			input: json.RawMessage(`{"path":null,"start_line":null}`),
			want:  json.RawMessage(`{"path":null}`),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RepairToolInput(tt.input, schema)
			if !jsonEqual(got, tt.want) {
				t.Errorf("got %s, want %s", got, tt.want)
			}
		})
	}
}

func TestRepairToolInput_StringifiedArray(t *testing.T) {
	// Pattern 2: JSON string that looks like an array → parse as real array
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"files": {"type": "array", "items": {"type": "string"}}
		},
		"required": ["files"]
	}`)

	tests := []struct {
		name  string
		input json.RawMessage
		want  json.RawMessage
	}{
		{
			name:  "stringified array of strings",
			input: json.RawMessage(`{"files":"[\"a\",\"b\",\"c\"]"}`),
			want:  json.RawMessage(`{"files":["a","b","c"]}`),
		},
		{
			name:  "stringified empty array",
			input: json.RawMessage(`{"files":"[]"}`),
			want:  json.RawMessage(`{"files":[]}`),
		},
		{
			name:  "already valid array unchanged",
			input: json.RawMessage(`{"files":["a","b"]}`),
			want:  json.RawMessage(`{"files":["a","b"]}`),
		},
		{
			name:  "non-array string kept as string (not an array in schema? actually it is)",
			input: json.RawMessage(`{"files":"hello"}`),
			want:  json.RawMessage(`{"files":["hello"]}`),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RepairToolInput(tt.input, schema)
			if !jsonEqual(got, tt.want) {
				t.Errorf("got %s, want %s", got, tt.want)
			}
		})
	}
}

func TestRepairToolInput_BareStringAsArray(t *testing.T) {
	// Pattern 4: bare string where array expected → wrap in array
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"files": {"type": "array", "items": {"type": "string"}}
		},
		"required": ["files"]
	}`)

	tests := []struct {
		name  string
		input json.RawMessage
		want  json.RawMessage
	}{
		{
			name:  "bare string wrapped in array",
			input: json.RawMessage(`{"files":"README.md"}`),
			want:  json.RawMessage(`{"files":["README.md"]}`),
		},
		{
			name:  "bare integer wrapped in array",
			input: json.RawMessage(`{"files":42}`),
			want:  json.RawMessage(`{"files":[42]}`),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RepairToolInput(tt.input, schema)
			if !jsonEqual(got, tt.want) {
				t.Errorf("got %s, want %s", got, tt.want)
			}
		})
	}
}

func TestRepairToolInput_BareObjectAsArray(t *testing.T) {
	// Pattern 3: bare object where array of objects expected → wrap in array
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"tools": {
				"type": "array",
				"items": {
					"type": "object",
					"properties": {
						"name": {"type": "string"},
						"arguments": {"type": "object"}
					},
					"required": ["name"]
				}
			}
		},
		"required": ["tools"]
	}`)

	tests := []struct {
		name  string
		input json.RawMessage
		want  json.RawMessage
	}{
		{
			name:  "bare object wrapped in array",
			input: json.RawMessage(`{"tools":{"name":"read_file","arguments":{"path":"/foo"}}}`),
			want:  json.RawMessage(`{"tools":[{"name":"read_file","arguments":{"path":"/foo"}}]}`),
		},
		{
			name:  "already valid array unchanged",
			input: json.RawMessage(`{"tools":[{"name":"read_file","arguments":{"path":"/foo"}}]}`),
			want:  json.RawMessage(`{"tools":[{"name":"read_file","arguments":{"path":"/foo"}}]}`),
		},
		{
			name:  "empty object placeholder removed from array",
			input: json.RawMessage(`{"tools":[{"name":"read_file","arguments":{"path":"/foo"}},{}]}`),
			want:  json.RawMessage(`{"tools":[{"name":"read_file","arguments":{"path":"/foo"}}]}`),
		},
		{
			name:  "null element removed from array",
			input: json.RawMessage(`{"tools":[{"name":"read_file","arguments":{"path":"/foo"}},null]}`),
			want:  json.RawMessage(`{"tools":[{"name":"read_file","arguments":{"path":"/foo"}}]}`),
		},
		{
			name:  "all empty placeholders become empty array",
			input: json.RawMessage(`{"tools":[{},{}]}`),
			want:  json.RawMessage(`{"tools":[]}`),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RepairToolInput(tt.input, schema)
			if !jsonEqual(got, tt.want) {
				t.Errorf("got %s, want %s", got, tt.want)
			}
		})
	}
}

func TestRepairToolInput_NestedObjects(t *testing.T) {
	// Nested object repair (recursive null removal in nested objects)
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"config": {
				"type": "object",
				"properties": {
					"name": {"type": "string"},
					"timeout": {"type": "integer"}
				},
				"required": ["name"]
			}
		},
		"required": ["config"]
	}`)

	tests := []struct {
		name  string
		input json.RawMessage
		want  json.RawMessage
	}{
		{
			name:  "null optional field in nested object",
			input: json.RawMessage(`{"config":{"name":"test","timeout":null}}`),
			want:  json.RawMessage(`{"config":{"name":"test"}}`),
		},
		{
			name:  "null required field kept in nested object",
			input: json.RawMessage(`{"config":{"name":null}}`),
			want:  json.RawMessage(`{"config":{"name":null}}`),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RepairToolInput(tt.input, schema)
			if !jsonEqual(got, tt.want) {
				t.Errorf("got %s, want %s", got, tt.want)
			}
		})
	}
}

func TestRepairToolInput_EdgeCases(t *testing.T) {
	// Edge cases: empty schema, no schema, invalid input, etc.
	tests := []struct {
		name   string
		input  json.RawMessage
		schema json.RawMessage
		want   json.RawMessage
	}{
		{
			name:   "empty schema returns input unchanged",
			input:  json.RawMessage(`{"path":"/foo"}`),
			schema: json.RawMessage(``),
			want:   json.RawMessage(`{"path":"/foo"}`),
		},
		{
			name:   "nil schema returns input unchanged",
			input:  json.RawMessage(`{"path":"/foo"}`),
			schema: nil,
			want:   json.RawMessage(`{"path":"/foo"}`),
		},
		{
			name:   "invalid input JSON unchanged (string compare)",
			input:  json.RawMessage(`not json at all`),
			schema: json.RawMessage(`{"type":"object"}`),
			want:   json.RawMessage(`not json at all`),
		},
		{
			name:   "array at root (not object) unchanged",
			input:  json.RawMessage(`["a","b"]`),
			schema: json.RawMessage(`{"type":"array","items":{"type":"string"}}`),
			want:   json.RawMessage(`["a","b"]`),
		},
		{
			name:   "no matching schema properties",
			input:  json.RawMessage(`{"unknown_field":"value"}`),
			schema: json.RawMessage(`{"type":"object","properties":{"known":{"type":"string"}}}`),
			want:   json.RawMessage(`{"unknown_field":"value"}`),
		},
		{
			name:   "null unknown field also removed (not in required, not in properties)",
			input:  json.RawMessage(`{"path":"/foo","extra":null}`),
			schema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`),
			want:   json.RawMessage(`{"path":"/foo"}`),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RepairToolInput(tt.input, tt.schema)
			// Use string comparison for invalid JSON (can't jsonEqual non-JSON)
			if string(got) == string(tt.input) && string(tt.input) == string(tt.want) {
				return // all match as strings
			}
			if !jsonEqual(got, tt.want) {
				t.Errorf("got %s, want %s", got, tt.want)
			}
		})
	}
}

func TestRepairToolInput_ComplexMCPLikeSchema(t *testing.T) {
	// MCP-like schema with nested arrays of objects
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"files": {
				"type": "array",
				"items": {
					"type": "object",
					"properties": {
						"path": {"type": "string"},
						"content": {"type": "string"}
					},
					"required": ["path"]
				}
			}
		},
		"required": ["files"]
	}`)

	tests := []struct {
		name  string
		input json.RawMessage
		want  json.RawMessage
	}{
		{
			name:  "stringified array of objects",
			input: json.RawMessage(`{"files":"[{\"path\":\"/a\",\"content\":\"hi\"}]"}`),
			want:  json.RawMessage(`{"files":[{"path":"/a","content":"hi"}]}`),
		},
		{
			name:  "bare object wrapped",
			input: json.RawMessage(`{"files":{"path":"/a","content":"hi"}}`),
			want:  json.RawMessage(`{"files":[{"path":"/a","content":"hi"}]}`),
		},
		{
			name:  "array with mixed valid and empty placeholder",
			input: json.RawMessage(`{"files":[{"path":"/a","content":"hi"},{},{"path":"/b"}]}`),
			want:  json.RawMessage(`{"files":[{"path":"/a","content":"hi"},{"path":"/b"}]}`),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RepairToolInput(tt.input, schema)
			if !jsonEqual(got, tt.want) {
				t.Errorf("got %s, want %s", got, tt.want)
			}
		})
	}
}

func TestRepairToolInput_CombinedPatterns(t *testing.T) {
	// Multiple patterns in a single input
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {"type": "string"},
			"start_line": {"type": "integer"},
			"files": {"type": "array", "items": {"type": "string"}}
		},
		"required": ["path"]
	}`)

	tests := []struct {
		name  string
		input json.RawMessage
		want  json.RawMessage
	}{
		{
			name:  "null optional + stringified array + bare string all at once",
			input: json.RawMessage(`{"path":"/foo","start_line":null,"files":"[\"a\",\"b\"]"}`),
			want:  json.RawMessage(`{"path":"/foo","files":["a","b"]}`),
		},
		{
			name:  "null optional + bare string as array",
			input: json.RawMessage(`{"path":"/foo","start_line":null,"files":"README.md"}`),
			want:  json.RawMessage(`{"path":"/foo","files":["README.md"]}`),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RepairToolInput(tt.input, schema)
			if !jsonEqual(got, tt.want) {
				t.Errorf("got %s, want %s", got, tt.want)
			}
		})
	}
}

func TestRepairToolInput_NoSchemaType(t *testing.T) {
	// Schema with no type field (any type) — should not change anything
	schema := json.RawMessage(`{
		"properties": {
			"data": {}
		}
	}`)

	input := json.RawMessage(`{"data":null}`)
	got := RepairToolInput(input, schema)
	// Schema has no type: "" → guard returns input unchanged
	if !jsonEqual(got, input) {
		t.Errorf("expected no change for schema without type, got %s", got)
	}
}

// A repair fixes one thing the model got wrong. Every number in the call is
// something it got right, and a tool runs with what this function returns — so
// the spellings here are the assertion, not an approximation of it. jsonEqual
// cannot check that: it compares through float64 and would confirm the very loss
// these cases exist to catch.
func TestRepairToolInput_NumbersKeepTheirLiterals(t *testing.T) {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"id": {"type": "integer"},
			"ratio": {"type": "number"},
			"tags": {"type": "array", "items": {"type": "string"}},
			"ids": {"type": "array", "items": {"type": "integer"}},
			"note": {"type": "string"}
		},
		"required": ["id"]
	}`)

	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "identifier past float64's exact range survives a repair",
			in:   `{"id":1234567890123456789,"tags":null}`,
			want: `{"id":1234567890123456789}`,
		},
		{
			name: "2^53+1 is not rounded down to 2^53",
			in:   `{"id":9007199254740993}`,
			want: `{"id":9007199254740993}`,
		},
		{
			name: "negative identifier",
			in:   `{"id":-9223372036854775808}`,
			want: `{"id":-9223372036854775808}`,
		},
		{
			name: "trailing-zero float keeps the spelling the model chose",
			in:   `{"id":1,"ratio":1.0}`,
			want: `{"id":1,"ratio":1.0}`,
		},
		{
			name: "exponent keeps its spelling",
			in:   `{"id":1,"ratio":1e10}`,
			want: `{"id":1,"ratio":1e10}`,
		},
		{
			name: "stringified array of identifiers",
			in:   `{"id":1,"ids":"[1234567890123456789,9007199254740993]"}`,
			want: `{"id":1,"ids":[1234567890123456789,9007199254740993]}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RepairToolInput(json.RawMessage(tt.in), schema)
			if string(got) != tt.want {
				t.Errorf("got %s, want %s", got, tt.want)
			}
		})
	}
}

// An input that needs no repair is returned as the caller's own bytes. That is
// what keeps an argument byte-identical to what the model sent — key order and
// all — and it is the cheapest guarantee to state: no repair, no re-encoding.
func TestRepairToolInput_UnchangedInputKeepsItsBytes(t *testing.T) {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {"type": "string"},
			"start_line": {"type": "integer"}
		},
		"required": ["path"]
	}`)

	// Deliberately not in sorted key order, and not spelled the way
	// json.Marshal would: an unchanged input may not be re-encoded at all.
	in := `{"start_line":5,"path":"/foo"}`

	if got := RepairToolInput(json.RawMessage(in), schema); string(got) != in {
		t.Errorf("unchanged input was re-encoded:\n got: %s\nwant: %s", got, in)
	}
}

// ============================================================================
// Integration-style test: RepairToolInput is idempotent
// ============================================================================
func TestRepairToolInput_Idempotent(t *testing.T) {
	// Applying repair twice should give the same result as applying it once.
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {"type": "string"},
			"start_line": {"type": "integer"},
			"files": {"type": "array", "items": {"type": "string"}}
		},
		"required": ["path"]
	}`)

	inputs := []json.RawMessage{
		json.RawMessage(`{"path":"/foo","start_line":null}`),
		json.RawMessage(`{"path":"/foo","files":"[\"a\",\"b\"]"}`),
		json.RawMessage(`{"path":"/foo","files":"README.md"}`),
		json.RawMessage(`{"path":"/foo","start_line":null,"files":"[\"a\"]"}`),
	}

	for _, input := range inputs {
		first := RepairToolInput(input, schema)
		second := RepairToolInput(first, schema)
		if !jsonEqual(first, second) {
			t.Errorf("repair not idempotent:\n  input:  %s\n  first:  %s\n  second: %s", input, first, second)
		}
	}
}

// ============================================================================
// Helpers
// ============================================================================

// jsonEqual compares two JSON values by unmarshaling and re-marshaling.
// This handles key ordering differences and whitespace.
func jsonEqual(a, b json.RawMessage) bool {
	var va, vb any
	if err := json.Unmarshal(a, &va); err != nil {
		return false
	}
	if err := json.Unmarshal(b, &vb); err != nil {
		return false
	}
	ma, _ := json.Marshal(va)
	mb, _ := json.Marshal(vb)
	return string(ma) == string(mb)
}
