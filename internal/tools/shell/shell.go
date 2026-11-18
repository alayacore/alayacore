// Package shell provides cross-platform shell detection and command execution.
//
// # Design
//
// A Shell describes how to invoke a specific shell binary (bash, zsh, sh,
// pwsh, powershell).  Each shell carries:
//   - a binary name / path  (e.g. "/bin/bash", "pwsh")
//   - a prompt fragment     (the syntax info injected into the tool
//     description so the LLM knows what features are available)
//   - an invocation builder (how to run "<shell> <flags> <command>")
//
// [DefaultCommandTimeout] defines a global limit on tool execution time
// (0 = no limit); it is referenced by the execute_command and search_content
// implementations, and by [FormatDuration], which renders it for the result
// text a limit-ending command produces.
//
// The value is deliberately absent from the tool description. A description is
// part of the prompt prefix the provider caches, and a startup flag spelled
// into it means that any change to the flag — a script passing
// --command-timeout, an ALAYACORE_COMMAND_TIMEOUT edited in a profile — costs
// a full re-write of that prefix. The limit is stated where it is actionable
// instead: in the result of the command it stopped, which is the one moment a
// model needs the number (see FormatDuration).
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
	"time"
)

// DefaultCommandTimeout is the maximum duration a shell command may run
// before being killed. A value of 0 (the default) means no limit — commands
// run until they finish or are canceled. execute_command, search_content and
// [FormatDuration] all reference this value.
//
// This is a package-level var so that main() / app.Setup can override it
// from the --command-timeout CLI flag or the ALAYACORE_COMMAND_TIMEOUT
// environment variable. Tools created before that assignment would carry the
// old value into the deadline they install, which is why app.Setup applies it
// first.
var DefaultCommandTimeout time.Duration // 0 = no limit

type Shell struct {
	// Name is a human-readable identifier (e.g. "bash", "PowerShell").
	Name string

	// Binary is the shell executable — either an absolute path or a binary
	// name that must be resolvable via PATH.
	Binary string

	// PromptFragment describes the shell syntax available. This is the
	// LLM-facing tool description, verbatim.
	PromptFragment string

	// BuildCmd returns an *exec.Cmd that executes the given command string
	// inside this shell.
	BuildCmd func(binary, command string) *exec.Cmd
}

// FormatDuration renders a command timeout for the result text that a limit
// reports: "300ms", "30s", "5m". It is the only rendering of
// DefaultCommandTimeout's value, and it is written where the limit was felt —
// in the header of the command it killed — never into the tool description,
// which is prompt prefix (see the package comment).
//
// The spelling is Go's own duration syntax, shortened: it is compact because
// this line is paid for on every timeout, and exact because the model weighs it
// against how long its command had been running.
func FormatDuration(d time.Duration) string {
	s := d.String() // "300ms", "30s", "5m0s", "1h30m0s", "1h0m0s"
	// "5m0s" is how Go spells a whole number of minutes; the zero seconds are
	// noise, and trimming them is all the shortening this needs.
	switch {
	case strings.HasSuffix(s, "h0m0s"):
		return strings.TrimSuffix(s, "0m0s")
	case strings.HasSuffix(s, "m0s"):
		return strings.TrimSuffix(s, "0s")
	}
	return s
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
