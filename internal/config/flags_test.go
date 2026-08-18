package config

import (
	"os"
	"testing"
)

func TestParseReasoningLevelFlag(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		want    int
		wantSet bool
	}{
		{"explicit off", []string{"--reasoning-level", "0"}, ReasoningLevelOff, true},
		{"explicit normal", []string{"--reasoning-level=1"}, ReasoningLevelNormal, true},
		{"explicit max", []string{"--reasoning-level", "2"}, ReasoningLevelMax, true},
		{"absent keeps default", nil, DefaultReasoningLevel, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			os.Args = append([]string{"alayacore"}, tt.args...)
			s := Parse()
			if s.ReasoningLevel != tt.want {
				t.Errorf("ReasoningLevel = %d, want %d", s.ReasoningLevel, tt.want)
			}
			if s.ReasoningLevelSet != tt.wantSet {
				t.Errorf("ReasoningLevelSet = %v, want %v", s.ReasoningLevelSet, tt.wantSet)
			}
		})
	}
}

func TestParseNoMarkdownFlag(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want bool
	}{
		{"absent means markdown on by default", nil, false},
		{"explicit disable", []string{"--no-markdown"}, true},
		{"explicit disable with equals", []string{"--no-markdown=true"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			os.Args = append([]string{"alayacore"}, tt.args...)
			s := Parse()
			if s.NoMarkdown != tt.want {
				t.Errorf("NoMarkdown = %v, want %v", s.NoMarkdown, tt.want)
			}
		})
	}
}
