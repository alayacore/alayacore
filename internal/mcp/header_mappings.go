package mcp

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

// ============================================================================
// x-mcp-header Support
// ============================================================================
//
// Functions in this file handle the x-mcp-header extension (2026-07-28+),
// which allows servers to designate tool parameters to be mirrored as HTTP
// headers (Mcp-Param-{Name}) so intermediaries can inspect requests without
// parsing the body.

// x-mcp-header schema type constants.
const (
	schemaTypeObject  = "object"
	schemaTypeString  = "string"
	schemaTypeInteger = "integer"
	schemaTypeBoolean = "boolean"
	headerTrue        = "true"
	headerFalse       = "false"
)

// parseHeaderMappings extracts x-mcp-header annotations from a tool's
// inputSchema. It walks the properties at the root level and returns
// a HeaderMapping for each parameter that has the x-mcp-header extension.
//
// Per the 2026-07-28 spec, x-mcp-header annotations are only valid on
// statically-reachable properties (direct children of the root object,
// reachable via a chain of `properties` keys). Nesting beyond root is
// permitted as long as every step is a `properties` key.
func parseHeaderMappings(schema json.RawMessage) []HeaderMapping {
	var root struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(schema, &root); err != nil || len(root.Properties) == 0 {
		return nil
	}

	var mappings []HeaderMapping
	for propName, propRaw := range root.Properties {
		var prop struct {
			Type       string                     `json:"type"`
			XMcpHeader string                     `json:"x-mcp-header"`
			Properties map[string]json.RawMessage `json:"properties"`
		}
		if err := json.Unmarshal(propRaw, &prop); err != nil {
			continue
		}

		// Direct annotation on this property.
		if prop.XMcpHeader != "" {
			// Only string, integer, boolean are valid.
			if prop.Type == schemaTypeString || prop.Type == schemaTypeInteger || prop.Type == schemaTypeBoolean {
				mappings = append(mappings, HeaderMapping{
					ParamPath:  []string{propName},
					HeaderName: prop.XMcpHeader,
					ParamType:  prop.Type,
				})
			}
			// Skip properties with x-mcp-header but unsupported type.
			continue
		}

		// Recurse into nested object properties.
		if prop.Type == schemaTypeObject && len(prop.Properties) > 0 {
			nested := parseNestedHeaderMappings(prop.Properties, []string{propName})
			mappings = append(mappings, nested...)
		}
	}

	return mappings
}

// parseNestedHeaderMappings recursively walks nested object properties
// looking for x-mcp-header annotations. parentPath is the chain of
// property keys leading to this level.
func parseNestedHeaderMappings(props map[string]json.RawMessage, parentPath []string) []HeaderMapping {
	var mappings []HeaderMapping
	for propName, propRaw := range props {
		var prop struct {
			Type       string                     `json:"type"`
			XMcpHeader string                     `json:"x-mcp-header"`
			Properties map[string]json.RawMessage `json:"properties"`
		}
		if err := json.Unmarshal(propRaw, &prop); err != nil {
			continue
		}

		path := append(append([]string{}, parentPath...), propName)

		if prop.XMcpHeader != "" {
			if prop.Type == schemaTypeString || prop.Type == schemaTypeInteger || prop.Type == schemaTypeBoolean {
				mappings = append(mappings, HeaderMapping{
					ParamPath:  path,
					HeaderName: prop.XMcpHeader,
					ParamType:  prop.Type,
				})
			}
			continue
		}

		if prop.Type == schemaTypeObject && len(prop.Properties) > 0 {
			nested := parseNestedHeaderMappings(prop.Properties, path)
			mappings = append(mappings, nested...)
		}
	}
	return mappings
}

// encodeHeaderValue converts a parameter value to its HTTP header string
// representation per the MCP 2026-07-28 spec.
//
// Returns the encoded value and whether Base64 encoding was applied.
func encodeHeaderValue(value any, paramType string) (string, bool) {
	var raw string
	switch paramType {
	case schemaTypeString:
		s, ok := value.(string)
		if !ok {
			return "", false
		}
		raw = s
	case schemaTypeInteger:
		// JSON numbers decode as float64 by default. Values outside the
		// IEEE-754 safe integer range would lose precision when formatted;
		// per spec they are not permitted, so omit the header.
		switch v := value.(type) {
		case float64:
			raw = formatSafeInteger(v)
		case int:
			raw = formatSafeInteger(float64(v))
		case int64:
			raw = formatSafeInteger(float64(v))
		default:
			return "", false
		}
	case schemaTypeBoolean:
		b, ok := value.(bool)
		if !ok {
			return "", false
		}
		if b {
			raw = headerTrue
		} else {
			raw = headerFalse
		}
	default:
		return "", false
	}

	if needsBase64Encoding(raw) {
		return "=?base64?" + base64.StdEncoding.EncodeToString([]byte(raw)) + "?=", true
	}
	return raw, false
}

// formatSafeInteger formats an integer value as its decimal string
// representation. Returns "" (header omitted) for values outside the
// IEEE-754 safe integer range, which would lose precision.
func formatSafeInteger(v float64) string {
	if v < float64(minSafeInteger) || v > float64(maxSafeInteger) {
		return ""
	}
	return fmt.Sprintf("%.0f", v)
}

// needsBase64Encoding returns true if the value must be Base64-encoded
// for safe use in an HTTP header per RFC 9110.
func needsBase64Encoding(s string) bool {
	if s == "" {
		return false
	}
	// Sentinel pattern: if value looks like =?base64?...?=, encode it.
	if strings.HasPrefix(s, "=?base64?") && strings.HasSuffix(s, "?=") {
		return true
	}
	// Check for leading/trailing whitespace.
	if s[0] == ' ' || s[0] == '\t' || s[len(s)-1] == ' ' || s[len(s)-1] == '\t' {
		return true
	}
	// Check for non-ASCII or control characters.
	for _, r := range s {
		if r < 0x20 || r > 0x7E {
			return true
		}
	}
	return false
}

// resolveNestedValue looks up a value in a nested map by path.
// For path ["location", "region"], it returns argsMap["location"]["region"].
func resolveNestedValue(m map[string]any, path []string) (any, bool) {
	if len(path) == 0 {
		return nil, false
	}
	current := m
	for i, key := range path {
		val, ok := current[key]
		if !ok {
			return nil, false
		}
		if i == len(path)-1 {
			return val, true
		}
		next, ok := val.(map[string]any)
		if !ok {
			return nil, false
		}
		current = next
	}
	return nil, false
}

// ============================================================================
// x-mcp-header Constraint Validation
// ============================================================================
//
// Per the 2026-07-28 spec, clients using the Streamable HTTP transport
// MUST reject tool definitions where any x-mcp-header value violates the
// constraints: header names must be RFC 9110 field-name tokens, unique
// case-insensitively, applied only to primitive types (string, integer,
// boolean — not number), and only to properties statically reachable via
// `properties` chains. Integer parameters must stay within the IEEE-754
// safe range (−2^53+1 .. 2^53−1).

// minSafeInteger / maxSafeInteger bound the IEEE-754 double-precision
// safe integer range required for x-mcp-header integer parameters.
// Typed int64 (not untyped): the constants exceed the 32-bit int range,
// and release-all builds 32-bit targets (linux/386, linux/arm), where an
// untyped constant passed to fmt would overflow.
const (
	minSafeInteger int64 = -(1<<53 - 1) // -2^53+1
	maxSafeInteger int64 = 1<<53 - 1    // 2^53-1
)

// xMcpHeaderAnnotation is a raw x-mcp-header annotation found anywhere in
// an inputSchema, used for validity checking.
type xMcpHeaderAnnotation struct {
	headerName string
	propType   string // JSON Schema type of the annotated property
	minimum    *float64
	maximum    *float64
}

// validateXMcpHeaderAnnotations checks a tool's inputSchema against the
// 2026-07-28 x-mcp-header constraints. Per the spec, a tool definition
// with any violating annotation MUST be rejected (excluded from
// tools/list).
func validateXMcpHeaderAnnotations(schema json.RawMessage) error {
	all := collectAllXMcpHeaderAnnotations(schema)
	if len(all) == 0 {
		return nil
	}

	// Per-annotation checks: header name syntax, case-insensitive
	// uniqueness, supported property type, and integer safe range.
	seen := make(map[string]struct{}, len(all))
	for _, a := range all {
		if !isValidHeaderName(a.headerName) {
			return fmt.Errorf("x-mcp-header %q is not a valid HTTP field-name token", a.headerName)
		}
		key := strings.ToLower(a.headerName)
		if _, dup := seen[key]; dup {
			return fmt.Errorf("x-mcp-header %q is not case-insensitively unique", a.headerName)
		}
		seen[key] = struct{}{}

		switch a.propType {
		case schemaTypeString, schemaTypeBoolean:
			// Always valid.
		case schemaTypeInteger:
			if a.minimum != nil && *a.minimum < float64(minSafeInteger) {
				return fmt.Errorf("x-mcp-header integer parameter %q: minimum %v below safe range %d",
					a.headerName, *a.minimum, minSafeInteger)
			}
			if a.maximum != nil && *a.maximum > float64(maxSafeInteger) {
				return fmt.Errorf("x-mcp-header integer parameter %q: maximum %v above safe range %d",
					a.headerName, *a.maximum, maxSafeInteger)
			}
		default:
			return fmt.Errorf("x-mcp-header %q applied to unsupported type %q (only string, integer, boolean)",
				a.headerName, a.propType)
		}
	}

	// Reachability check: every annotation must sit on a property
	// statically reachable via `properties` chains only. parseHeaderMappings
	// collects exactly those; any difference means an annotation lives
	// under items, composition keywords, conditionals, or $ref.
	if len(all) != len(parseHeaderMappings(schema)) {
		return fmt.Errorf("x-mcp-header annotation on a property that is not statically reachable " +
			"via `properties` chains (through items, composition keywords, conditionals, or $ref)")
	}

	return nil
}

// collectAllXMcpHeaderAnnotations walks the entire inputSchema tree —
// including arrays (items), composition keywords (oneOf/anyOf/allOf/not),
// conditionals (if/then/else), and $defs — and returns every x-mcp-header
// annotation found, regardless of reachability.
func collectAllXMcpHeaderAnnotations(schema json.RawMessage) []xMcpHeaderAnnotation {
	var root map[string]any
	if err := json.Unmarshal(schema, &root); err != nil {
		return nil
	}

	var out []xMcpHeaderAnnotation
	var walk func(node map[string]any)
	walk = func(node map[string]any) {
		for k, v := range node {
			if k == "properties" {
				props, ok := v.(map[string]any)
				if !ok {
					continue
				}
				for _, propRaw := range props {
					propSchema, ok := propRaw.(map[string]any)
					if !ok {
						continue
					}
					if h, ok := propSchema["x-mcp-header"].(string); ok {
						out = append(out, xMcpHeaderAnnotation{
							headerName: h,
							propType:   schemaTypeOf(propSchema),
							minimum:    schemaNumber(propSchema["minimum"]),
							maximum:    schemaNumber(propSchema["maximum"]),
						})
					}
					walk(propSchema)
				}
				continue
			}
			// Recurse into any other schema containers (items,
			// composition keywords, conditionals, $defs, etc.).
			switch t := v.(type) {
			case map[string]any:
				walk(t)
			case []any:
				for _, item := range t {
					if itemMap, ok := item.(map[string]any); ok {
						walk(itemMap)
					}
				}
			}
		}
	}
	walk(root)
	return out
}

// schemaTypeOf returns the "type" of a JSON Schema node, or "" if absent.
func schemaTypeOf(schema map[string]any) string {
	if t, ok := schema["type"].(string); ok {
		return t
	}
	return ""
}

// schemaNumber extracts a JSON number from a schema keyword, or nil.
func schemaNumber(v any) *float64 {
	if f, ok := v.(float64); ok {
		return &f
	}
	return nil
}

// isValidHeaderName reports whether s is a valid HTTP field-name token
// (RFC 9110 Section 5.6.2: 1*tchar). Token syntax also excludes control
// characters such as CR/LF.
func isValidHeaderName(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !isTokenChar(s[i]) {
			return false
		}
	}
	return true
}

// isTokenChar reports whether b is a valid RFC 9110 tchar.
func isTokenChar(b byte) bool {
	switch {
	case b >= 'a' && b <= 'z', b >= 'A' && b <= 'Z', b >= '0' && b <= '9':
		return true
	}
	switch b {
	case '!', '#', '$', '%', '&', '\'', '*', '+', '-', '.', '^', '_', '`', '|', '~':
		return true
	}
	return false
}
