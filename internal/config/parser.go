package config

import (
	"reflect"
	"strconv"
	"strings"
	"time"
)

// ParseKeyValue parses key-value config content into a struct using `config` tags.
// The content format is:
//
//	key: value
//	key: "quoted value"
//	key: 'quoted value'
//
// Lines starting with # are comments. Empty lines are ignored.
// Multiple configs can be separated by "---" on its own line.
func ParseKeyValue(content string, target interface{}) {
	parseKeyValue(content, target, false)
}

// ParseKeyValueBlocks parses multiple config blocks separated by "---"
func ParseKeyValueBlocks(content string) []string {
	// Split by "\n---\n" to get individual blocks
	return strings.Split(content, "\n---\n")
}

// ParseKeyValueIntoMap parses key-value content and returns a map
func ParseKeyValueIntoMap(content string) map[string]string {
	result := make(map[string]string)
	lines := strings.Split(content, "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		key, value, found := strings.Cut(line, ":")
		if !found {
			continue
		}

		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)

		// Remove surrounding quotes if present
		if len(value) >= 2 {
			if (value[0] == '"' && value[len(value)-1] == '"') ||
				(value[0] == '\'' && value[len(value)-1] == '\'') {
				value = value[1 : len(value)-1]
			}
		}

		result[key] = value
	}

	return result
}

// parseKeyValue is the internal implementation
func parseKeyValue(content string, target interface{}, skipHyphens bool) {
	v := reflect.ValueOf(target)
	if v.Kind() != reflect.Ptr || v.Elem().Kind() != reflect.Struct {
		return
	}
	v = v.Elem()
	t := v.Type()

	// Build map from config tag names to field indices
	tagToField := make(map[string]int)
	for i := 0; i < t.NumField(); i++ {
		tag := t.Field(i).Tag.Get("config")
		if tag != "" {
			tagToField[tag] = i
		}
	}

	// Parse lines
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Skip "---" separator lines
		if skipHyphens && line == "---" {
			continue
		}

		key, value, found := strings.Cut(line, ":")
		if !found {
			continue
		}

		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)

		// Remove surrounding quotes if present
		if len(value) >= 2 {
			if (value[0] == '"' && value[len(value)-1] == '"') ||
				(value[0] == '\'' && value[len(value)-1] == '\'') {
				value = value[1 : len(value)-1]
			}
		}

		// Look up field by tag
		fieldIdx, ok := tagToField[key]
		if !ok {
			continue
		}

		field := v.Field(fieldIdx)
		setFieldValue(field, value)
	}
}

// setFieldValue sets a struct field value from a string
func setFieldValue(field reflect.Value, value string) {
	// Handle time.Time specially
	if field.Type() == reflect.TypeOf(time.Time{}) {
		if t, err := time.Parse(time.RFC3339, value); err == nil {
			field.Set(reflect.ValueOf(t))
		}
		return
	}

	switch field.Kind() {
	case reflect.String:
		field.SetString(value)

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		// Handle time.Duration specially
		if field.Type() == reflect.TypeOf(time.Duration(0)) {
			if d, err := time.ParseDuration(value); err == nil {
				field.SetInt(int64(d))
			}
			return
		}
		if i, err := strconv.ParseInt(value, 10, 64); err == nil {
			field.SetInt(i)
		}

	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if u, err := strconv.ParseUint(value, 10, 64); err == nil {
			field.SetUint(u)
		}

	case reflect.Bool:
		switch strings.ToLower(value) {
		case "true", "1", "yes", "on":
			field.SetBool(true)
		case "false", "0", "no", "off", "":
			field.SetBool(false)
		}

	case reflect.Float32, reflect.Float64:
		if f, err := strconv.ParseFloat(value, 64); err == nil {
			field.SetFloat(f)
		}

	case reflect.Slice:
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
}
