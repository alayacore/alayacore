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

func TestParseCommandTimeoutFlag(t *testing.T) {
	// Isolate from any ALAYACORE_COMMAND_TIMEOUT in the environment. Empty is
	// the unset value here: config reads it with os.Getenv. t.Setenv restores
	// the real environment (including "was not set") when the test ends.
	t.Setenv("ALAYACORE_COMMAND_TIMEOUT", "")

	// Parse reads os.Args, which is process-global: restore it, or every test
	// that runs after this one in the same binary sees these flags.
	origArgs := os.Args
	t.Cleanup(func() { os.Args = origArgs })

	tests := []struct {
		name string
		args []string
		want int
	}{
		{"absent means no limit", nil, 0},
		{"explicit no limit", []string{"--command-timeout=0"}, 0},
		{"explicit 30 seconds", []string{"--command-timeout", "30"}, 30},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			os.Args = append([]string{"alayacore"}, tt.args...)
			s := Parse()
			if s.CommandTimeout != tt.want {
				t.Errorf("CommandTimeout = %d, want %d", s.CommandTimeout, tt.want)
			}
		})
	}
}

func TestParseCommandTimeoutEnv(t *testing.T) {
	origArgs := os.Args
	t.Cleanup(func() { os.Args = origArgs })

	// Env var provides the default when the flag is absent.
	t.Setenv("ALAYACORE_COMMAND_TIMEOUT", "45")
	os.Args = []string{"alayacore"}
	s := Parse()
	if s.CommandTimeout != 45 {
		t.Errorf("CommandTimeout = %d, want 45 (from env)", s.CommandTimeout)
	}

	// Env var 0 also means no limit.
	t.Setenv("ALAYACORE_COMMAND_TIMEOUT", "0")
	os.Args = []string{"alayacore"}
	s = Parse()
	if s.CommandTimeout != 0 {
		t.Errorf("CommandTimeout = %d, want 0 (env no limit)", s.CommandTimeout)
	}

	// An explicit flag overrides the env var.
	t.Setenv("ALAYACORE_COMMAND_TIMEOUT", "45")
	os.Args = []string{"alayacore", "--command-timeout", "30"}
	s = Parse()
	if s.CommandTimeout != 30 {
		t.Errorf("CommandTimeout = %d, want 30 (flag beats env)", s.CommandTimeout)
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
