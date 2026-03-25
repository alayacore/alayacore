package config

import (
	"testing"
	"time"
)

type TestConfig struct {
	Name    string `config:"name"`
	Port    int    `config:"port"`
	Enabled bool   `config:"enabled"`
}

func TestParseKeyValue(t *testing.T) {
	content := `# Test config
name: "test-app"
port: 8080
enabled: true
`
	var cfg TestConfig
	ParseKeyValue(content, &cfg)

	if cfg.Name != "test-app" {
		t.Errorf("Expected Name 'test-app', got %q", cfg.Name)
	}
	if cfg.Port != 8080 {
		t.Errorf("Expected Port 8080, got %d", cfg.Port)
	}
	if !cfg.Enabled {
		t.Errorf("Expected Enabled true, got %v", cfg.Enabled)
	}
}

func TestParseKeyValueUnquoted(t *testing.T) {
	content := `name: myapp
port: 3000
enabled: yes
`
	var cfg TestConfig
	ParseKeyValue(content, &cfg)

	if cfg.Name != "myapp" {
		t.Errorf("Expected Name 'myapp', got %q", cfg.Name)
	}
	if cfg.Port != 3000 {
		t.Errorf("Expected Port 3000, got %d", cfg.Port)
	}
	if !cfg.Enabled {
		t.Errorf("Expected Enabled true, got %v", cfg.Enabled)
	}
}

func TestParseKeyValueSingleQuotes(t *testing.T) {
	content := `name: 'my-app'`
	var cfg TestConfig
	ParseKeyValue(content, &cfg)

	if cfg.Name != "my-app" {
		t.Errorf("Expected Name 'my-app', got %q", cfg.Name)
	}
}

func TestParseKeyValueComments(t *testing.T) {
	content := `# This is a comment
name: test
# Another comment
port: 80
`
	var cfg TestConfig
	ParseKeyValue(content, &cfg)

	if cfg.Name != "test" {
		t.Errorf("Expected Name 'test', got %q", cfg.Name)
	}
	if cfg.Port != 80 {
		t.Errorf("Expected Port 80, got %d", cfg.Port)
	}
}

func TestParseKeyValueUnknownField(t *testing.T) {
	content := `name: test
unknown_field: ignored
port: 80
`
	var cfg TestConfig
	ParseKeyValue(content, &cfg)

	if cfg.Name != "test" {
		t.Errorf("Expected Name 'test', got %q", cfg.Name)
	}
	if cfg.Port != 80 {
		t.Errorf("Expected Port 80, got %d", cfg.Port)
	}
}

func TestParseKeyValueBlocks(t *testing.T) {
	content := `name: first
---
name: second
---
name: third`
	blocks := ParseKeyValueBlocks(content)

	if len(blocks) != 3 {
		t.Fatalf("Expected 3 blocks, got %d", len(blocks))
	}

	var cfg1, cfg2, cfg3 TestConfig
	ParseKeyValue(blocks[0], &cfg1)
	ParseKeyValue(blocks[1], &cfg2)
	ParseKeyValue(blocks[2], &cfg3)

	if cfg1.Name != "first" {
		t.Errorf("Expected cfg1.Name 'first', got %q", cfg1.Name)
	}
	if cfg2.Name != "second" {
		t.Errorf("Expected cfg2.Name 'second', got %q", cfg2.Name)
	}
	if cfg3.Name != "third" {
		t.Errorf("Expected cfg3.Name 'third', got %q", cfg3.Name)
	}
}

func TestParseKeyValueBoolVariants(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"enabled: true", true},
		{"enabled: false", false},
		{"enabled: yes", true},
		{"enabled: no", false},
		{"enabled: on", true},
		{"enabled: off", false},
		{"enabled: 1", true},
		{"enabled: 0", false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			var cfg TestConfig
			ParseKeyValue(tt.input, &cfg)
			if cfg.Enabled != tt.expected {
				t.Errorf("Expected %v, got %v", tt.expected, cfg.Enabled)
			}
		})
	}
}

type TimeConfig struct {
	CreatedAt time.Time `config:"created_at"`
}

func TestParseKeyValueTime(t *testing.T) {
	content := `created_at: 2024-01-15T10:30:00Z`
	var cfg TimeConfig
	ParseKeyValue(content, &cfg)

	expected := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
	if !cfg.CreatedAt.Equal(expected) {
		t.Errorf("Expected %v, got %v", expected, cfg.CreatedAt)
	}
}
