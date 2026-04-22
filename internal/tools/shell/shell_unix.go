//go:build !windows

package shell

import "os/exec"

// ----- Unix shell definitions -----

var (
	shellBash = &Shell{
		Name:           "bash",
		Binary:         "bash",
		PromptFragment: "Execute a shell command using bash. Arrays are 0-indexed (${array[0]}). Commands run detached without TTY; interactive programs (sudo, ssh, vim) will timeout.",
		BuildCmd: func(binary, command string) *exec.Cmd {
			return exec.Command(binary, "-c", command)
		},
	}

	shellZsh = &Shell{
		Name:           "zsh",
		Binary:         "zsh",
		PromptFragment: "Execute a shell command using zsh. Arrays are 1-indexed (${array[1]}). Commands run detached without TTY; interactive programs (sudo, ssh, vim) will timeout.",
		BuildCmd: func(binary, command string) *exec.Cmd {
			return exec.Command(binary, "-c", command)
		},
	}

	shellSh = &Shell{
		Name:           "sh",
		Binary:         "sh",
		PromptFragment: "Execute a shell command using POSIX sh. No arrays, no [[ ]], no brace expansion. Commands run detached without TTY; interactive programs (sudo, ssh, vim) will timeout.",
		BuildCmd: func(binary, command string) *exec.Cmd {
			return exec.Command(binary, "-c", command)
		},
	}
)

// knownShells lists shells in preference order for Unix-like systems.
// sh is always available on POSIX systems, so the list is guaranteed to
// produce a match.
var knownShells = []*Shell{
	shellBash,
	shellZsh,
	shellSh,
}
