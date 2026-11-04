package llm

import (
	"bytes"
	"encoding/json"
	"io"
	"reflect"
	"strings"
)

// ============================================================================
// Simplified JSON Schema Parser (no external dependencies)
// ============================================================================

// schemaTypeObject is the JSON Schema type name for objects.
const schemaTypeObject = "object"

// schemaInfo is a simplified representation of a JSON Schema,
// containing only the fields needed for tool input repair.
// It is a tree structure that mirrors the schema's nesting.
type schemaInfo struct {
	// typeName is the JSON Schema type (e.g., "object", "array", "string",
	// "integer", "number", "boolean"). Empty string means any type.
	typeName string

	// properties maps field names to their schema, for objects.
	properties map[string]*schemaInfo

	// items is the schema for array elements, for arrays.
	items *schemaInfo

	// required is the set of field names that must be present.
	// Used to distinguish null-for-optional from null-for-required.
	required map[string]bool
}

// parseSchema parses a raw JSON Schema into the simplified schemaInfo form.
// Returns nil if the input is empty, not valid JSON, or not an object schema.
func parseSchema(raw json.RawMessage) *schemaInfo {
	if len(raw) == 0 {
		return nil
	}
	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		return nil
	}
	return parseSchemaMap(schema)
}

// parseSchemaMap recursively converts a JSON Schema map to schemaInfo.
func parseSchemaMap(m map[string]any) *schemaInfo {
	info := &schemaInfo{}

	// Extract type. When absent, leave typeName empty (means "any type").
	if t, ok := m["type"].(string); ok {
		info.typeName = t
	}

	// Extract properties (for object type)
	if props, ok := m["properties"].(map[string]any); ok {
		info.properties = make(map[string]*schemaInfo, len(props))
		for key, val := range props {
			if propMap, ok := val.(map[string]any); ok {
				info.properties[key] = parseSchemaMap(propMap)
			}
		}
	}

	// Extract items schema (for array type)
	if items, ok := m["items"].(map[string]any); ok {
		info.items = parseSchemaMap(items)
	}

	// Extract required field names
	if req, ok := m["required"].([]any); ok {
		info.required = make(map[string]bool, len(req))
		for _, r := range req {
			if rs, ok := r.(string); ok {
				info.required[rs] = true
			}
		}
	}

	return info
}

// ============================================================================
// Tool Input Repair
// ============================================================================

// RepairToolInput repairs a tool's input JSON based on its JSON Schema.
// It handles the 4 common LLM output errors:
//
//  1. null for optional fields — removes the field entirely
//  2. JSON-stringified array ("[\"a\",\"b\"]") — parses it to a real array
//  3. Bare object where array expected — wraps in a single-element array;
//     also removes empty object placeholders ({}) in arrays when the item
//     schema has required fields
//  4. Bare string where array expected — wraps in a single-element array
//
// If the input is already valid, it is returned unchanged — as the caller's own
// bytes, not as a re-encoding of them (see the number rule below).
// If the schema cannot be parsed, the input is returned as-is (fail-safe).
//
// Numbers are never re-spelled. A repair is the model's argument being fixed,
// and nothing else about the call may move with it: the value a tool runs with
// is the value the step's history records, so a number this function did not
// choose to change has to come out exactly as it went in. See decodeToolInput
// for what a decoder that loses that guarantee costs.
func RepairToolInput(input json.RawMessage, schema json.RawMessage) json.RawMessage {
	schemaObj := parseSchema(schema)
	if schemaObj == nil || schemaObj.typeName == "" {
		// No type info means "any type" — nothing to validate against
		return input
	}

	// Root must be an object for tool input to make sense
	if schemaObj.typeName != schemaTypeObject {
		return input
	}

	inputMap, ok := decodeToolInput(input)
	if !ok {
		return input
	}

	result := repairObject(inputMap, schemaObj)

	// Nothing to repair: hand back the caller's own bytes. The comparison is on
	// the decoded trees and not on the re-encoded bytes because json.Marshal
	// writes a map's keys in sorted order — an input this function changed
	// nothing about would still come back reordered, and a byte comparison would
	// read that as a repair and replace the model's own argument with it.
	if reflect.DeepEqual(inputMap, result) {
		return input
	}

	fixed, err := json.Marshal(result)
	if err != nil {
		return input
	}
	return fixed
}

// decodeToolInput decodes a tool call's arguments, keeping every number as the
// literal the model wrote.
//
// json.Unmarshal is the wrong decoder here, and using it was a silent
// corruption: it turns every number into a float64, which holds integers exactly
// only up to 2^53. A tool whose argument is a 64-bit identifier — a snowflake, a
// database key, a nanosecond timestamp; anything MCP-shaped — was handed an
// identifier the model never sent (…789 arrived as …800), and because the
// repaired value is what the tool runs with AND what history records, nothing
// anywhere disagreed out loud: the transcript showed the model's call, the tool
// received a different one. json.Number is the literal text, so a number this
// package does not deliberately change comes out byte for byte as it went in —
// including 1.0 and 1e10, whose spelling float64 also normalizes.
//
// Decoded with a Decoder rather than json.Unmarshal because UseNumber is a
// Decoder option. The trailing check keeps Unmarshal's strictness: an argument
// blob with a second value after the object is not an object.
func decodeToolInput(input json.RawMessage) (map[string]any, bool) {
	var inputMap map[string]any
	if !decodeLiterals(bytes.NewReader(input), &inputMap) {
		return nil, false
	}
	return inputMap, true
}

// decodeStringifiedArray decodes a JSON array that arrived as a string, under
// the same literal-preserving rule as decodeToolInput: a stringified array of
// identifiers is one more spelling of the same loss.
func decodeStringifiedArray(s string) ([]any, bool) {
	var arr []any
	if !decodeLiterals(strings.NewReader(s), &arr) {
		return nil, false
	}
	return arr, true
}

// decodeLiterals decodes exactly one JSON value from r into dst, numbers kept as
// their literals. It reports false on a malformed value or on anything after it.
func decodeLiterals(r io.Reader, dst any) bool {
	dec := json.NewDecoder(r)
	dec.UseNumber()
	if err := dec.Decode(dst); err != nil {
		return false
	}
	_, err := dec.Token()
	return err == io.EOF
}

// ============================================================================
// Recursive Repair Helpers
// ============================================================================

// repairObject repairs a JSON object according to its schema.
// Handles Pattern 1 (null removal) and recurses into nested values.
func repairObject(obj map[string]any, schema *schemaInfo) map[string]any {
	result := make(map[string]any, len(obj))

	for k, v := range obj {
		propSchema := schema.properties[k]

		// Pattern 1: null for optional fields → remove the field
		if v == nil {
			if !schema.required[k] {
				continue // drop null optional field
			}
			// Keep null for required fields (models sometimes send null
			// for required fields too; better to keep than to break the call)
			result[k] = v
			continue
		}

		if propSchema == nil {
			// No schema info for this field — keep as-is
			result[k] = v
			continue
		}

		result[k] = repairValue(v, propSchema)
	}

	return result
}

// repairValue repairs a single JSON value according to its schema type.
func repairValue(value any, schema *schemaInfo) any {
	if value == nil || schema == nil {
		return value
	}

	switch schema.typeName {
	case schemaTypeObject:
		if obj, ok := value.(map[string]any); ok {
			return repairObject(obj, schema)
		}
		return value // can't fix non-object into object

	case "array":
		return repairArrayValue(value, schema)

	default:
		// Primitive types or unknown type — nothing to repair
		return value
	}
}

// repairArrayValue handles a value that should be an array per schema.
// It handles Patterns 2, 3, and 4.
func repairArrayValue(value any, schema *schemaInfo) any {
	// Already a valid array — repair each element
	if arr, ok := value.([]any); ok {
		return repairArrayElements(arr, schema)
	}

	// Pattern 2: JSON string that looks like a serialized array
	if str, ok := value.(string); ok {
		if arr, ok := decodeStringifiedArray(str); ok {
			return repairArrayElements(arr, schema)
		}
		// Pattern 4: bare string — wrap in single-element array
		return repairSingleElementArray(str, schema)
	}

	// Pattern 3: bare object/number/bool where array expected — wrap
	return repairSingleElementArray(value, schema)
}

// repairSingleElementArray wraps a single value in an array and repairs it.
func repairSingleElementArray(value any, schema *schemaInfo) []any {
	if schema.items != nil {
		return []any{repairValue(value, schema.items)}
	}
	return []any{value}
}

// repairArrayElements repairs each element in an array.
// Also handles the "empty placeholder" sub-case of Pattern 3.
func repairArrayElements(arr []any, schema *schemaInfo) []any {
	result := make([]any, 0, len(arr))
	for _, elem := range arr {
		if elem == nil {
			continue // skip null elements
		}

		// Skip empty object placeholders (Pattern 3 sub-case):
		// Models sometimes emit {} as a placeholder when they intend
		// to fill in the element later or as a no-op. We detect this
		// by checking if the item schema has required fields and the
		// object is empty — a legitimate empty object with no required
		// fields is kept.
		if obj, ok := elem.(map[string]any); ok && len(obj) == 0 {
			if schema.items != nil && len(schema.items.required) > 0 {
				continue
			}
		}

		if schema.items != nil {
			result = append(result, repairValue(elem, schema.items))
		} else {
			result = append(result, elem)
		}
	}
	return result
}
