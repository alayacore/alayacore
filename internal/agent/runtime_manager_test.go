package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRuntimeManager(t *testing.T) {
	// Create a temporary directory for testing
	tmpDir := t.TempDir()

	runtimePath := filepath.Join(tmpDir, "runtime.conf")

	// Test creating a new runtimeManager with empty path
	rm := newRuntimeManager(runtimePath)
	if rm.getActiveModel() != "" {
		t.Errorf("Expected empty active model, got: %s", rm.getActiveModel())
	}

	// Test setting active model
	err := rm.setActiveModel("Test Model")
	if err != nil {
		t.Errorf("Failed to set active model: %v", err)
	}
	if rm.getActiveModel() != "Test Model" {
		t.Errorf("Expected 'Test Model', got: %s", rm.getActiveModel())
	}

	// Test that file was created
	if _, err := os.Stat(runtimePath); os.IsNotExist(err) {
		t.Error("Runtime config file was not created")
	}

	// Test loading from existing file
	rm2 := newRuntimeManager(runtimePath)
	if rm2.getActiveModel() != "Test Model" {
		t.Errorf("Expected 'Test Model' after reload, got: %s", rm2.getActiveModel())
	}
}

func TestParseRuntimeConfig(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		expected string
	}{
		{
			name:     "with double quotes",
			content:  `active_model: "My Model"`,
			expected: "My Model",
		},
		{
			name:     "with single quotes",
			content:  `active_model: 'My Model'`,
			expected: "My Model",
		},
		{
			name:     "without quotes",
			content:  `active_model: My Model`,
			expected: "My Model",
		},
		{
			name:     "with comments",
			content:  "# Comment\nactive_model: \"My Model\"\n# Another comment",
			expected: "My Model",
		},
		{
			name:     "empty content",
			content:  "",
			expected: "",
		},
		{
			name:     "no active_model field",
			content:  "other_field: value",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config, _ := parseRuntimeConfig(tt.content, "runtime.conf")
			if config.ActiveModel != tt.expected {
				t.Errorf("Expected %q, got %q", tt.expected, config.ActiveModel)
			}
		})
	}
}

func TestFormatRuntimeConfig(t *testing.T) {
	config := runtimeConfig{ActiveModel: "Test Model"}
	result := formatRuntimeConfig(config)

	// Check that it contains the expected content
	if !contains(result, `active_model: "Test Model"`) {
		t.Errorf("Expected output to contain active_model, got: %s", result)
	}
}

func TestRuntimeManagerCreatesFileOnLoad(t *testing.T) {
	// Create a temporary directory for testing
	tmpDir := t.TempDir()

	runtimePath := filepath.Join(tmpDir, "runtime.conf")

	// Verify file doesn't exist initially
	if _, err := os.Stat(runtimePath); !os.IsNotExist(err) {
		t.Fatal("Runtime file should not exist initially")
	}

	// Create runtimeManager - this should create the file
	rm := newRuntimeManager(runtimePath)

	// Verify file was created
	if _, err := os.Stat(runtimePath); os.IsNotExist(err) {
		t.Error("Runtime config file was not created on Load")
	}

	// Verify the manager has correct default state
	if rm.getActiveModel() != "" {
		t.Errorf("Expected empty active model, got: %s", rm.getActiveModel())
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
