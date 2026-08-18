package shell

import (
	"os"
	"os/exec"
	"sync"
	"testing"
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
