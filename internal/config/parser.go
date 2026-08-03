package config

import (
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// validKeyPattern defines the allowed format for config keys.
// Keys must start with a letter or underscore, followed by letters,
// digits, underscores, or hyphens. This ensures reliable key-value
// separation and allows values to contain colons without ambiguity.
var validKeyPattern = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_-]*$`)

// ParseError represents an issue encountered during config parsing,
// such as an unknown key or a value that could not be converted to the target field type.
type ParseError struct {
	Key   string // config key
	Value string // raw value string
	Err   string // description of the problem
	Msg   string // optional custom message; when set, replaces the default formatting
}

func (e ParseError) String() string {
	if e.Msg != "" {
		return e.Msg
	}
	return fmt.Sprintf("key %q: cannot parse value %q: %s", e.Key, e.Value, e.Err)
}

// ParseKeyValueBlocks parses multiple config blocks separated by "---"
func ParseKeyValueBlocks(content string) []string {
	// Normalize CRLF to LF first: block splitting relies on the exact
	// "\n---\n" separator, so a CRLF-saved file (e.g. from a Windows
	// editor) would otherwise merge all blocks into one, silently
	// overwriting duplicate keys with the last occurrence.
	content = strings.ReplaceAll(content, "\r\n", "\n")
	// Split by "\n---\n" to get individual blocks
	return strings.Split(content, "\n---\n")
}

// ParseModelList parses key-value block format into a slice of ModelConfig.
// Returns models with a non-empty Name or ModelName, and any parse errors.
// Does NOT validate model fields — callers should validate after this.
func ParseModelList(content string) ([]ModelConfig, []string) {
	blocks := ParseKeyValueBlocks(content)
	models := make([]ModelConfig, 0, len(blocks))
	var errs []string

	for blockIdx, block := range blocks {
		block = strings.TrimSpace(block)
		if block == "" {
			continue
		}

		var m ModelConfig
		for _, e := range ParseKeyValue(block, &m) {
			errs = append(errs, fmt.Sprintf("model block %d: %s", blockIdx+1, e.String()))
		}

		if m.Name != "" || m.ModelName != "" {
			models = append(models, m)
		}
	}

	return models, errs
}

// ParseKeyValue parses key-value config content into a struct using `config` tags.
// The content format is:
//
//	key: value
//	key: "quoted value"
//	key: 'quoted value'
//
// Lines starting with # are treated as comments and ignored.
// Lines starting with --- are block separators and ignored.
// Empty lines are ignored.
// Multiple configs can be separated by "---" on its own line (see ParseKeyValueBlocks).
//
// Keys must match the pattern `^[a-zA-Z_][a-zA-Z0-9_-]*$` — this ensures reliable
// key-value separation and allows values to contain colons without ambiguity.
// Lines that don't match this pattern are treated as comments or malformed input.
//
// Parse errors (e.g. non-numeric value for an int field) and unknown keys
// are collected and returned. Parsing continues after each error so all
// problems are reported at once.
func ParseKeyValue(content string, target any) []ParseError {
	v := reflect.ValueOf(target)
	if v.Kind() != reflect.Ptr || v.Elem().Kind() != reflect.Struct {
		return nil
	}
	v = v.Elem()
	t := v.Type()

	// Build map from config tag names to field indices
	tagToField := make(map[string]int)
	for i := 0; i < t.NumField(); i++ {
		tag := t.Field(i).Tag.Get("config")
		if tag != "" {
			key, _, _ := strings.Cut(tag, ",")
			if key == "-" {
				continue // skip internal fields
			}
			tagToField[key] = i
		}
	}

	var errs []ParseError
	// seenKeys tracks keys already set in this block so duplicates can be
	// reported instead of silently overwriting the first occurrence.
	seenKeys := make(map[string]bool)

	// Parse lines
	for line := range strings.SplitSeq(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line == "---" {
			continue
		}

		// Explicitly skip comment lines
		if strings.HasPrefix(line, "#") {
			continue
		}

		// Find the first colon to separate key and value
		colonIdx := strings.IndexByte(line, ':')
		if colonIdx == -1 {
			errs = append(errs, ParseError{
				Key:   line,
				Value: "",
				Err:   "line without ':' separator (missing colon?)",
			})
			continue
		}

		key := strings.TrimSpace(line[:colonIdx])
		value := line[colonIdx+1:]

		// Validate key format
		if !validKeyPattern.MatchString(key) {
			errs = append(errs, ParseError{
				Key:   key,
				Value: strings.TrimSpace(value),
				Err:   fmt.Sprintf("invalid key format (must match %s)", validKeyPattern.String()),
			})
			continue
		}

		value = strings.TrimSpace(value)
		value = unquoteValue(value)

		// Look up field by tag
		fieldIdx, ok := tagToField[key]
		if !ok {
			errs = append(errs, ParseError{Key: key, Value: value, Err: "unknown config key"})
			continue
		}

		// Duplicate keys in one block are a configuration error: report
		// them and keep the first occurrence (later values are ignored).
		if ok, dupErr := claimKey(seenKeys, key, value); !ok {
			errs = append(errs, *dupErr)
			continue
		}

		if e := setField(fieldIdx, v, value, key); e != nil {
			errs = append(errs, *e)
		}
	}

	return errs
}

// claimKey marks a key as seen in the current block. Empty values are
// "unset" and do not claim the key. Returns false with a ParseError when
// the key was already claimed — duplicate keys are a config error and the
// first occurrence is kept.
func claimKey(seen map[string]bool, key, value string) (bool, *ParseError) {
	if value == "" {
		return true, nil
	}
	if seen[key] {
		return false, &ParseError{
			Key:   key,
			Value: value,
			Msg:   fmt.Sprintf("duplicate key %q: value %q ignored (first occurrence kept)", key, value),
		}
	}
	seen[key] = true
	return true, nil
}

// setField sets a struct field from a string value.
// Returns a ParseError if the value cannot be converted.
// Empty values are silently treated as "unset".
//
//nolint:gocyclo // Type switch over all supported field kinds requires many cases
func setField(fieldIdx int, v reflect.Value, value, key string) *ParseError {
	if value == "" {
		return nil
	}

	field := v.Field(fieldIdx)

	// JSON array or object: try json.Unmarshal for the field type.
	// This enables values like scopes: ["read", "write"] or env: {"KEY": "val"}.
	if len(value) > 0 && (value[0] == '[' || value[0] == '{') {
		if field.CanAddr() && field.Addr().CanInterface() {
			if err := json.Unmarshal([]byte(value), field.Addr().Interface()); err == nil {
				return nil
			}
		}
		// If json.Unmarshal fails, fall through to type-specific parsing
		// so the existing comma-separated slice logic still works.
	}

	// time.Time
	if field.Type() == reflect.TypeOf(time.Time{}) {
		t, err := time.Parse(time.RFC3339, value)
		if err != nil {
			return &ParseError{Key: key, Value: value, Err: "expected RFC3339 timestamp"}
		}
		field.Set(reflect.ValueOf(t))
		return nil
	}

	// time.Duration (int64 kind)
	if field.Type() == reflect.TypeOf(time.Duration(0)) {
		d, err := time.ParseDuration(value)
		if err != nil {
			return &ParseError{Key: key, Value: value, Err: "invalid duration"}
		}
		field.SetInt(int64(d))
		return nil
	}

	switch field.Kind() {
	case reflect.String:
		field.SetString(value)

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		i, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return &ParseError{Key: key, Value: value, Err: "invalid integer"}
		}
		field.SetInt(i)

	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		u, err := strconv.ParseUint(value, 10, 64)
		if err != nil {
			return &ParseError{Key: key, Value: value, Err: "invalid unsigned integer"}
		}
		field.SetUint(u)

	case reflect.Bool:
		field.SetBool(parseBool(value))
		if !isValidBool(value) {
			return &ParseError{Key: key, Value: value, Err: "invalid boolean (expected true/false/yes/no/on/off/1/0)"}
		}

	case reflect.Float32, reflect.Float64:
		f, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return &ParseError{Key: key, Value: value, Err: "invalid float"}
		}
		field.SetFloat(f)

	case reflect.Slice:
		setSliceField(field, value)
	}

	return nil
}

// unquoteValue strips surrounding quotes from a config value.
// Double-quoted strings support escape sequences (\n, \r, \", \\).
// Single-quoted strings are treated as raw literals (no escaping).
func unquoteValue(value string) string {
	if len(value) < 2 {
		return value
	}
	if value[0] == '"' && value[len(value)-1] == '"' {
		return unescapeQuoted(value[1 : len(value)-1])
	}
	if value[0] == '\'' && value[len(value)-1] == '\'' {
		return value[1 : len(value)-1]
	}
	return value
}

// escapeTable maps escape sequence characters to their actual values.
// Zero value means "not a known escape" — the raw characters are kept as-is.
var escapeTable = [256]byte{
	'\\': '\\',
	'"':  '"',
	'n':  '\n',
	'r':  '\r',
}

// unescapeQuoted processes escape sequences in a double-quoted config value.
// Recognized sequences: \\, \", \n, \r.
// Unknown sequences (e.g. \U) are kept as-is for backward compatibility.
func unescapeQuoted(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	i := 0
	for i < len(s) {
		if s[i] == '\\' && i+1 < len(s) {
			if c := escapeTable[s[i+1]]; c != 0 {
				b.WriteByte(c)
			} else {
				// Unknown escape: keep both characters as-is
				b.WriteByte('\\')
				b.WriteByte(s[i+1])
			}
			i += 2
		} else {
			b.WriteByte(s[i])
			i++
		}
	}
	return b.String()
}

func setSliceField(field reflect.Value, value string) {
	// Handle []string with comma-separated values
	if field.Type().Elem().Kind() == reflect.String {
		parts := strings.Split(value, ",")
		slice := reflect.MakeSlice(field.Type(), len(parts), len(parts))
		for i, part := range parts {
			slice.Index(i).SetString(strings.TrimSpace(part))
		}
		field.Set(slice)
	}
}

// parseBool converts a boolean string to its bool value.
// Returns false for unrecognized strings.
func parseBool(s string) bool {
	switch strings.ToLower(s) {
	case "true", "1", "yes", "on":
		return true
	default:
		return false
	}
}

// isValidBool returns true if the string is a recognized boolean value.
func isValidBool(s string) bool {
	switch strings.ToLower(s) {
	case "true", "1", "yes", "on", "false", "0", "no", "off", "":
		return true
	default:
		return false
	}
}
