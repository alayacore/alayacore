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
// --command-timeout, a wrapper that sets it for one project — costs a full
// re-write of that prefix. The limit is stated where it is actionable instead:
// in the result of the command it stopped, which is the one moment a model
// needs the number (see FormatDuration).
//
// On startup the package probes the OS environment for available shells and
// selects the best candidate via [Detect]. A miss is not silently swallowed:
// an ALAYACORE_SHELL value that names an unknown shell, or one whose binary is
// not on PATH, is reported on stderr and detection carries on without it.
package shell

import (
	"fmt"
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
// This is a package-level var so that app.Setup can override it from the
// --command-timeout CLI flag. Tools created before that assignment would carry
// the old value into the deadline they install, which is why app.Setup applies
// it first.
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
//
// An ALAYACORE_SHELL value that cannot be honored — an unknown name, or a known
// one whose binary is not on PATH — is reported on stderr rather than dropped.
// The variable overrides a preference order over whatever the machine happens to
// have, and a silent miss is the one failure its user cannot see: they asked for
// zsh, the model was told bash, and every command that follows is written in the
// wrong syntax with nothing to point at. The warning goes to the same stderr the
// other startup warnings use, before the TUI takes the screen.
func detect() *Shell {
	// 1. Honor explicit override.
	if env := os.Getenv("ALAYACORE_SHELL"); env != "" {
		switch s := shellByName(env); {
		case s == nil:
			fmt.Fprintf(os.Stderr,
				"warning: ALAYACORE_SHELL=%q is not a known shell (known: %s); using shell detection\n",
				env, knownShellNames())
		case !lookPath(s.Binary):
			fmt.Fprintf(os.Stderr,
				"warning: ALAYACORE_SHELL=%q names %q, which is not on PATH; using shell detection\n",
				env, s.Binary)
		default:
			return s
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

// shellByName returns the known shell the user named — by display name,
// case-insensitively, or by binary name.
func shellByName(name string) *Shell {
	for _, s := range knownShells {
		if strings.EqualFold(s.Name, name) || s.Binary == name {
			return s
		}
	}
	return nil
}

// knownShellNames renders what ALAYACORE_SHELL accepts, in preference order, for
// the warning a miss produces. A shell whose display name and binary differ shows
// both, since either is accepted: "pwsh (PowerShell Core)".
func knownShellNames() string {
	names := make([]string, 0, len(knownShells))
	for _, s := range knownShells {
		if strings.EqualFold(s.Name, s.Binary) {
			names = append(names, s.Binary)
			continue
		}
		names = append(names, fmt.Sprintf("%s (%s)", s.Binary, s.Name))
	}
	return strings.Join(names, ", ")
}

// ResolvedBinary returns the absolute path to the shell binary after PATH
// resolution. Falls back to s.Binary if resolution fails.
func (s *Shell) ResolvedBinary() string {
	if path, err := exec.LookPath(s.Binary); err == nil {
		return path
	}
	return s.Binary
}
