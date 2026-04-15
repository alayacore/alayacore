// Package shell provides cross-platform shell detection and command execution.
//
// # Design
//
// A Shell describes how to invoke a specific shell binary (bash, zsh, sh,
// pwsh, powershell).  Each shell carries:
//   - a binary name / path  (e.g. "/bin/bash", "pwsh")
//   - a prompt fragment     (the text injected into the tool description so
//     the LLM knows what syntax is available)
//   - an invocation builder (how to run "<shell> <flags> <command>")
//
// On startup the package probes the OS environment for available shells and
// selects the best candidate via [Detect].  The caller can override the
// choice by setting the ALAYACORE_SHELL environment variable.
package shell

import (
	"os"
	"os/exec"
	"strings"
	"sync"
)

// Shell represents a command shell that can execute commands.
type Shell struct {
	// Name is a human-readable identifier (e.g. "bash", "PowerShell").
	Name string

	// Binary is the shell executable — either an absolute path or a binary
	// name that must be resolvable via PATH.
	Binary string

	// PromptFragment is appended to the execute_command tool description so
	// the LLM knows which syntax features are available.
	PromptFragment string

	// BuildCmd returns an *exec.Cmd that executes the given command string
	// inside this shell.
	BuildCmd func(binary, command string) *exec.Cmd
}

// detection stores the result of the one-time shell detection.
var detection struct {
	once  sync.Once
	shell *Shell
}

// lookPath reports whether binary can be found on PATH.
func lookPath(binary string) bool {
	_, err := exec.LookPath(binary)
	return err == nil
}

// Detect returns the best available shell for the current environment.
// It is safe to call from multiple goroutines; detection runs only once.
func Detect() *Shell {
	detection.once.Do(func() {
		detection.shell = detect()
	})
	return detection.shell
}

// detect performs the actual shell detection.
func detect() *Shell {
	// 1. Honor explicit override.
	if env := os.Getenv("ALAYACORE_SHELL"); env != "" {
		lower := strings.ToLower(env)
		for _, s := range knownShells {
			if strings.EqualFold(s.Name, lower) || s.Binary == env {
				if lookPath(s.Binary) {
					return s
				}
			}
		}
	}

	// 2. Try each known shell in preference order.
	// The OS-specific knownShells list always ends with a guaranteed-present
	// shell (sh on Unix, cmd on Windows), so this loop always succeeds.
	for _, s := range knownShells {
		if lookPath(s.Binary) {
			return s
		}
	}

	// Defensive fallback — should never be reached.
	return knownShells[len(knownShells)-1]
}

// ResolvedBinary returns the absolute path to the shell binary after PATH
// resolution. Falls back to s.Binary if resolution fails.
func (s *Shell) ResolvedBinary() string {
	if path, err := exec.LookPath(s.Binary); err == nil {
		return path
	}
	return s.Binary
}
