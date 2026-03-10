package config

import (
	"testing"
)

func TestParseContextLimit(t *testing.T) {
	tests := []struct {
		input    string
		expected int64
		hasError bool
	}{
		{"", 0, false},
		{"0", 0, false},
		{"1000", 1000, false},
		{"128000", 128000, false},
		{"200K", 200000, false},
		{"200k", 200000, false},
		{"1M", 1000000, false},
		{"1m", 1000000, false},
		{"2M", 2000000, false},
		{"100K", 100000, false},
		{" 200K ", 200000, false}, // whitespace trimming
		{"abc", 0, true},
		{"200X", 0, true},
	}

	for _, tt := range tests {
		result, err := parseContextLimit(tt.input)
		if tt.hasError {
			if err == nil {
				t.Errorf("parseContextLimit(%q) expected error, got nil", tt.input)
			}
		} else {
			if err != nil {
				t.Errorf("parseContextLimit(%q) unexpected error: %v", tt.input, err)
			}
			if result != tt.expected {
				t.Errorf("parseContextLimit(%q) = %d, want %d", tt.input, result, tt.expected)
			}
		}
	}
}
