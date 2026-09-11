package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Settings
		wantErr string // substring; "" means must validate cleanly
	}{
		{
			name: "defaults are valid",
			cfg:  Settings{},
		},
		{
			name: "max-steps zero means unlimited",
			cfg:  Settings{MaxSteps: 0},
		},
		{
			name:    "negative max-steps is rejected",
			cfg:     Settings{MaxSteps: -1},
			wantErr: "--max-steps must be >= 0 (got -1)",
		},
		{
			name:    "auto-summarize above 100 can never trigger",
			cfg:     Settings{AutoSummarize: 150},
			wantErr: "--auto-summarize must be 0 (disabled) or 1-100",
		},
		{
			name:    "negative auto-summarize is rejected",
			cfg:     Settings{AutoSummarize: -5},
			wantErr: "--auto-summarize",
		},
		{
			name: "auto-summarize at the boundaries is valid",
			cfg:  Settings{AutoSummarize: 100},
		},
		{
			name:    "terseio cannot answer tool confirmations",
			cfg:     Settings{TerseIO: true, ToolConfirm: []string{"execute_command"}},
			wantErr: "--terseio and --tool-confirm are mutually exclusive",
		},
		{
			name: "tool-confirm alone is fine (plainio answers it)",
			cfg:  Settings{PlainIO: true, ToolConfirm: []string{"execute_command"}},
		},
		{
			name:    "explicit out-of-range reasoning level is rejected",
			cfg:     Settings{ReasoningLevelSet: true, ReasoningLevel: 3},
			wantErr: "--reasoning-level must be 0, 1, or 2",
		},
		{
			name: "reasoning level left unset is not validated",
			cfg:  Settings{ReasoningLevel: DefaultReasoningLevel},
		},
		{
			name: "an empty debug dir means disabled",
			cfg:  Settings{DebugLogDir: ""},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Validate(&tt.cfg)

			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() = %v, want no error", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Validate() = nil, want error containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("Validate() = %q, want it to contain %q", err, tt.wantErr)
			}
			// Every failure must be classifiable as a usage error by callers
			// (main maps it onto exit code 2).
			if !errors.Is(err, ErrUsage) {
				t.Errorf("Validate() error %q is not wrapped in ErrUsage", err)
			}
		})
	}
}

// The rendered message must read as one sentence on stderr:
// "Error: invalid configuration: --max-steps must be >= 0 (got -1); ..."
func TestValidateMessageReadsWell(t *testing.T) {
	cfg := Settings{MaxSteps: -1}
	err := Validate(&cfg)
	if err == nil {
		t.Fatal("expected an error")
	}
	line := "Error: " + err.Error()
	if strings.Contains(line, ": :") || strings.HasSuffix(line, ":") {
		t.Errorf("message has a doubled or dangling separator: %q", line)
	}
	t.Logf("rendered: %s", line)
}

// Validate is pure: a creatable --debug-log directory passes without anything
// being created. Creation is Prepare's job (the tests below), which is what
// keeps validation callable from a test — or twice — with no effect on the
// machine.
func TestValidateIsPureForDebugDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "logs")
	cfg := Settings{DebugLogDir: dir}

	if err := Validate(&cfg); err != nil {
		t.Fatalf("Validate() = %v, want nil for a creatable directory", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("Validate() touched the filesystem: stat(%q) err = %v, want not-exist", dir, err)
	}
}

// Prepare creates the directory Validate accepted.
func TestPrepareCreatesDebugDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "logs")
	cfg := Settings{DebugLogDir: dir}

	if err := Prepare(&cfg); err != nil {
		t.Fatalf("Prepare() = %v, want nil for a creatable directory", err)
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		t.Fatalf("expected %q to have been created (stat err = %v)", dir, err)
	}
}

// An unusable path is a usage error, so main reports it and exits exactly as it
// does for a Validate failure — one message, exit 2.
func TestPrepareRejectsUnusableDebugDir(t *testing.T) {
	cfg := Settings{DebugLogDir: "/proc/self/not-a-real-parent/deeper"}
	err := Prepare(&cfg)
	if err == nil {
		t.Fatal("Prepare() = nil, want an error for an unusable path")
	}
	if !strings.Contains(err.Error(), "--debug-log directory") {
		t.Errorf("Prepare() = %q, want it to name the --debug-log directory", err)
	}
	if !errors.Is(err, ErrUsage) {
		t.Errorf("Prepare() error %q is not wrapped in ErrUsage", err)
	}
}

// An unset --debug-log is disabled: Prepare must not create anything.
func TestPrepareNoDebugDirIsNoOp(t *testing.T) {
	if err := Prepare(&Settings{}); err != nil {
		t.Fatalf("Prepare() = %v, want nil when --debug-log is unset", err)
	}
}
