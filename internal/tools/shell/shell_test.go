package shell

import (
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestDetectReturnsNonNil(t *testing.T) {
	s := Detect()
	if s == nil {
		t.Fatal("Detect() returned nil")
	}
	t.Run("fields_set", func(t *testing.T) {
		if s.Name == "" {
			t.Error("shell Name is empty")
		}
		if s.Binary == "" {
			t.Error("shell Binary is empty")
		}
		if s.PromptFragment == "" {
			t.Error("shell PromptFragment is empty")
		}
		if s.BuildCmd == nil {
			t.Error("shell BuildCmd is nil")
		}
	})
}

func TestDetectIsIdempotent(t *testing.T) {
	a := Detect()
	b := Detect()
	if a != b {
		t.Error("Detect() returned different shells on subsequent calls")
	}
}

func TestDetectEnvOverride(t *testing.T) {
	// Save and restore the env var.
	orig := os.Getenv("ALAYACORE_SHELL")
	defer os.Setenv("ALAYACORE_SHELL", orig)

	// Reset the detection state so Detect() runs again.
	detection.once = sync.Once{}
	detection.shell = nil

	// Find a shell that definitely exists on this system.
	candidates := []string{"sh", "bash", "zsh"}
	var found string
	for _, c := range candidates {
		if p, _ := exec.LookPath(c); p != "" {
			found = c
			break
		}
	}
	if found == "" {
		t.Skip("no known shell found on PATH")
	}

	os.Setenv("ALAYACORE_SHELL", found)
	s := detectForTest(t)
	if s.Binary != found {
		t.Errorf("expected binary %q, got %q", found, s.Binary)
	}
}

// detectForTest is a test helper that calls Detect() and guarantees a non-nil
// result, satisfying staticcheck's SA5011 analysis.
func detectForTest(t *testing.T) *Shell {
	t.Helper()
	s := Detect()
	if s == nil {
		t.Fatal("Detect() returned nil")
	}
	return s
}

func TestResolvedBinary(t *testing.T) {
	s := Detect()
	resolved := s.ResolvedBinary()
	if resolved == "" {
		t.Error("ResolvedBinary() returned empty string")
	}
	// On Unix, LookPath should resolve to an absolute path.
	if resolved[0] != '/' {
		t.Errorf("ResolvedBinary() = %q, expected absolute path", resolved)
	}
}

func TestKnownShellsHaveBuildCmd(t *testing.T) {
	for _, s := range knownShells {
		if s.BuildCmd == nil {
			t.Errorf("shell %q (%s) has nil BuildCmd", s.Name, s.Binary)
		}
	}
}

func TestBuildCmdProducesValidCmd(t *testing.T) {
	s := Detect()
	cmd := s.BuildCmd(s.ResolvedBinary(), "echo hello")
	if cmd == nil {
		t.Fatal("BuildCmd returned nil")
	}
}

// TestFormatDuration pins the spelling used where a limit is reported. It is
// deliberately not Go's "5m0s": this line is paid for on every timeout, and the
// number is the one that fired, so nothing about it is rounded.
func TestFormatDuration(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{300 * time.Millisecond, "300ms"},
		{1 * time.Second, "1s"},
		{30 * time.Second, "30s"},
		{90 * time.Second, "1m30s"},
		{5 * time.Minute, "5m"},
		{1 * time.Hour, "1h"},
		{90 * time.Minute, "1h30m"},
	}
	for _, tt := range tests {
		if got := FormatDuration(tt.d); got != tt.want {
			t.Errorf("FormatDuration(%v) = %q, want %q", tt.d, got, tt.want)
		}
	}
}

// The timeout must not reach the tool description: the description is prompt
// prefix, so a value a startup flag sets would invalidate the provider's cache
// on every change. This is the shape the bug had, pinned so it cannot come back
// — the fragment is the whole description, whatever the limit is.
func TestPromptFragmentIgnoresTheTimeout(t *testing.T) {
	orig := DefaultCommandTimeout
	defer func() { DefaultCommandTimeout = orig }()

	s := detectForTest(t)

	DefaultCommandTimeout = 0
	unlimited := s.PromptFragment
	DefaultCommandTimeout = 5 * time.Second
	if s.PromptFragment != unlimited {
		t.Errorf("the shell's description changed with the timeout: %q", s.PromptFragment)
	}
	for _, leak := range []string{"5s", "killed after", "without a timeout"} {
		if strings.Contains(s.PromptFragment, leak) {
			t.Errorf("the shell's description still carries the timeout (%q): %q", leak, s.PromptFragment)
		}
	}
}
