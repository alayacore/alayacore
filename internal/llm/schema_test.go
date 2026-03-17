package llm

import (
	"encoding/json"
	"testing"
)

func TestGenerateSchema(t *testing.T) {
	type TestInput struct {
		Name        string `json:"name" jsonschema:"required,description=The name of the item"`
		Description string `json:"description" jsonschema:"description=Optional description"`
		Type        string `json:"type" jsonschema:"required,description=The type,enum=foo|bar|baz"`
	}

	schema := GenerateSchema(TestInput{})

	// Verify it's valid JSON
	var result map[string]interface{}
	if err := json.Unmarshal(schema, &result); err != nil {
		t.Fatalf("Generated invalid JSON: %v", err)
	}

	// Check type
	if result["type"] != "object" {
		t.Errorf("Expected type 'object', got %v", result["type"])
	}

	// Check required fields
	required, ok := result["required"].([]interface{})
	if !ok {
		t.Fatal("required field is not an array")
	}
	if len(required) != 2 {
		t.Errorf("Expected 2 required fields, got %d", len(required))
	}

	// Check properties
	props, ok := result["properties"].(map[string]interface{})
	if !ok {
		t.Fatal("properties is not an object")
	}

	// Check name field
	nameField, ok := props["name"].(map[string]interface{})
	if !ok {
		t.Fatal("name property is not an object")
	}
	if nameField["type"] != "string" {
		t.Errorf("Expected name type 'string', got %v", nameField["type"])
	}
	if nameField["description"] != "The name of the item" {
		t.Errorf("Unexpected name description: %v", nameField["description"])
	}

	// Check enum field
	typeField, ok := props["type"].(map[string]interface{})
	if !ok {
		t.Fatal("type property is not an object")
	}
	enum, ok := typeField["enum"].([]interface{})
	if !ok {
		t.Fatal("enum is not an array")
	}
	if len(enum) != 3 {
		t.Errorf("Expected 3 enum values, got %d", len(enum))
	}

	t.Logf("Generated schema: %s", string(schema))
}
