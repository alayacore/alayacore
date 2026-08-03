package config

import (
	"strings"
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
# name: commented-out
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

func TestParseKeyValueCommentWithColon(t *testing.T) {
	// Lines starting with # are explicitly skipped as comments,
	// even if they contain a colon.
	content := `# name: "should-be-ignored"
name: real-value
`
	var cfg TestConfig
	errors := ParseKeyValue(content, &cfg)

	if cfg.Name != "real-value" {
		t.Errorf("Expected Name 'real-value', got %q", cfg.Name)
	}
	if len(errors) != 0 {
		t.Errorf("Expected 0 errors, got %d: %v", len(errors), errors)
	}
}

func TestParseKeyValueMissingColon(t *testing.T) {
	content := `name "test"
port: 80
`
	var cfg TestConfig
	errors := ParseKeyValue(content, &cfg)

	if cfg.Port != 80 {
		t.Errorf("Expected Port 80, got %d", cfg.Port)
	}
	// name line has no colon → should produce an error
	found := false
	for _, e := range errors {
		if e.Key == `name "test"` && e.Err == "line without ':' separator (missing colon?)" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Expected error for missing colon, got %v", errors)
	}
}

func TestParseKeyValueInvalidKeyFormat(t *testing.T) {
	content := `name: valid
invalid key: value
port: 80
`
	var cfg TestConfig
	errors := ParseKeyValue(content, &cfg)

	if cfg.Name != "valid" {
		t.Errorf("Expected Name 'valid', got %q", cfg.Name)
	}
	if cfg.Port != 80 {
		t.Errorf("Expected Port 80, got %d", cfg.Port)
	}
	// "invalid key" contains a space → should produce an error
	found := false
	for _, e := range errors {
		if e.Key == "invalid key" && strings.Contains(e.Err, "invalid key format") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Expected error for invalid key format, got %v", errors)
	}
}

func TestParseKeyValueValueWithColons(t *testing.T) {
	content := `url: http://example.com:8080/path?q=1
name: test
`
	var cfg struct {
		URL  string `config:"url"`
		Name string `config:"name"`
	}
	ParseKeyValue(content, &cfg)

	if cfg.URL != "http://example.com:8080/path?q=1" {
		t.Errorf("Expected URL 'http://example.com:8080/path?q=1', got %q", cfg.URL)
	}
	if cfg.Name != "test" {
		t.Errorf("Expected Name 'test', got %q", cfg.Name)
	}
}

func TestParseKeyValueDashSeparatorSkipped(t *testing.T) {
	// "---" lines are block separators and should be silently skipped
	// in parseConfig (they are handled by ParseKeyValueBlocks). The two
	// name keys therefore live in the same block: the duplicate is
	// reported and the first occurrence is kept.
	content := `name: first
---
name: second
`
	var cfg TestConfig
	errors := ParseKeyValue(content, &cfg)

	if cfg.Name != "first" {
		t.Errorf("Expected Name 'first' (first occurrence kept), got %q", cfg.Name)
	}
	if len(errors) != 1 {
		t.Fatalf("Expected 1 duplicate-key error, got %d: %v", len(errors), errors)
	}
	if !strings.Contains(errors[0].String(), "duplicate key") {
		t.Errorf("expected duplicate-key error, got: %s", errors[0].String())
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

func TestParseKeyValueBlocks_CRLF(t *testing.T) {
	// CRLF line endings (e.g. Windows editors) must split identically
	// to LF. Before normalization, "\r\n---\r\n" never matched the
	// "\n---\n" separator and the whole file became a single block.
	content := "name: first\r\n---\r\nname: second\r\n---\r\nname: third\r\n"
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

func TestParseKeyValue_DuplicateKey(t *testing.T) {
	// Duplicate keys in one block are reported and the first occurrence
	// is kept (later values are ignored).
	content := `name: first
name: second
name: third
`
	var cfg TestConfig
	errs := ParseKeyValue(content, &cfg)

	if len(errs) != 2 {
		t.Fatalf("expected 2 duplicate-key errors, got %d: %v", len(errs), errs)
	}
	for i, e := range errs {
		if !strings.Contains(e.String(), "duplicate key") {
			t.Errorf("error %d should mention duplicate key, got: %s", i, e.String())
		}
	}
	if cfg.Name != "first" {
		t.Errorf("expected first occurrence kept, got %q", cfg.Name)
	}
}

func TestParseKeyValue_EmptyValueDoesNotClaimKey(t *testing.T) {
	// An empty value is "unset" and must not count as a duplicate for a
	// later real value.
	content := `name:
name: real
`
	var cfg TestConfig
	errs := ParseKeyValue(content, &cfg)
	if len(errs) != 0 {
		t.Fatalf("expected no errors, got %v", errs)
	}
	if cfg.Name != "real" {
		t.Errorf("expected name 'real', got %q", cfg.Name)
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

type JSONConfig struct {
	Scopes []string          `config:"scopes"`
	Env    map[string]string `config:"env"`
	Tags   []string          `config:"tags"`
}

func TestParseKeyValueJSON(t *testing.T) {
	content := `scopes: ["read", "write"]
env: {"TOKEN": "abc123", "HOST": "localhost"}
tags: prod,us-east
`
	var cfg JSONConfig
	ParseKeyValue(content, &cfg)

	if len(cfg.Scopes) != 2 || cfg.Scopes[0] != "read" || cfg.Scopes[1] != "write" {
		t.Errorf("Expected scopes [read write], got %v", cfg.Scopes)
	}
	if len(cfg.Env) != 2 || cfg.Env["TOKEN"] != "abc123" || cfg.Env["HOST"] != "localhost" {
		t.Errorf("Expected env {TOKEN: abc123, HOST: localhost}, got %v", cfg.Env)
	}
	if len(cfg.Tags) != 2 || cfg.Tags[0] != "prod" || cfg.Tags[1] != "us-east" {
		t.Errorf("Expected tags [prod us-east], got %v", cfg.Tags)
	}
}

func TestParseKeyValueJSONPlainString(t *testing.T) {
	// JSON values like ["single"] should not break plain string parsing.
	content := `name: ["hello"]`
	var cfg struct {
		Name string `config:"name"`
	}
	ParseKeyValue(content, &cfg)
	if cfg.Name != "[\"hello\"]" {
		t.Errorf("Expected plain string '[\"hello\"]', got %q", cfg.Name)
	}
}

func TestParseKeyValueSkipsDashTag(t *testing.T) {
	// Fields tagged with config:"-" must not be settable via key-value parsing.
	type Cfg struct {
		ID   int    `config:"-"`
		Name string `config:"name"`
	}
	content := `name: "hello"
-: 42
`
	var cfg Cfg
	ParseKeyValue(content, &cfg)
	if cfg.Name != "hello" {
		t.Errorf("Name = %q, want %q", cfg.Name, "hello")
	}
	if cfg.ID != 0 {
		t.Errorf("ID should remain 0 (config:\"-\" should be unsettable), got %d", cfg.ID)
	}
}
