package llm

import (
	"encoding/json"
	"reflect"
	"strings"
)

// SchemaField tags for struct fields
// Use like: `json:"name" jsonschema:"required,description=The name of the file"`
//
// Supported tags:
//   - required: marks the field as required
//   - description=...: sets the field description
//   - type=...: overrides the type (defaults to string)
//   - enum=...: comma-separated list of allowed values
type SchemaField struct {
	Type        string   `json:"type,omitempty"`
	Description string   `json:"description,omitempty"`
	Enum        []string `json:"enum,omitempty"`
}

// GenerateSchema generates a JSON schema from a struct using reflection
func GenerateSchema(v interface{}) json.RawMessage {
	t := reflect.TypeOf(v)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		panic("GenerateSchema: expected struct")
	}

	schema := map[string]interface{}{
		"type":       "object",
		"properties": make(map[string]SchemaField),
	}
	properties := make(map[string]SchemaField)
	schema["properties"] = properties
	var required []string

	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if field.Anonymous {
			continue
		}

		jsonTag := field.Tag.Get("json")
		if jsonTag == "-" {
			continue
		}

		fieldName := strings.Split(jsonTag, ",")[0]
		if fieldName == "" {
			fieldName = field.Name
		}

		schemaField := SchemaField{
			Type: "string", // default
		}

		// Parse jsonschema tag
		if tag := field.Tag.Get("jsonschema"); tag != "" {
			parts := strings.Split(tag, ",")
			for _, part := range parts {
				part = strings.TrimSpace(part)
				switch {
				case part == "required":
					required = append(required, fieldName)
				case strings.HasPrefix(part, "description="):
					schemaField.Description = strings.TrimPrefix(part, "description=")
				case strings.HasPrefix(part, "type="):
					schemaField.Type = strings.TrimPrefix(part, "type=")
				case strings.HasPrefix(part, "enum="):
					enumStr := strings.TrimPrefix(part, "enum=")
					schemaField.Enum = strings.Split(enumStr, "|")
				}
			}
		}

		properties[fieldName] = schemaField
	}

	if len(required) > 0 {
		schema["required"] = required
	}

	result, err := json.Marshal(schema)
	if err != nil {
		panic("GenerateSchema: " + err.Error())
	}
	return json.RawMessage(result)
}
