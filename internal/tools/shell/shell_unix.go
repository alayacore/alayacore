//go:build !windows

package shell

import "os/exec"

// ----- Unix shell definitions -----

var (
	shellBash = &Shell{
		Name:   "bash",
		Binary: "bash",
		PromptFragment: `Execute a shell command using bash.

- Bash syntax is available (brace expansion, [[ ]], arrays, etc.)
- Arrays are 0-indexed (first element is ${array[0]}), unlike zsh which is 1-indexed
- Prefer simple, standard commands over complex pipelines
- Quote filenames with spaces or special characters
- Check command output for errors before proceeding
- Clean up temporary files when done
- Commands run in a detached session with no controlling terminal and stdin closed. Interactive programs (sudo, ssh, vim, etc.) that require a TTY or terminal input will hang and be killed after a timeout.`,
		BuildCmd: func(binary, command string) *exec.Cmd {
			return exec.Command(binary, "-c", command)
		},
	}

	shellZsh = &Shell{
		Name:   "zsh",
		Binary: "zsh",
		PromptFragment: `Execute a shell command using zsh.

- Zsh syntax is available (glob qualifiers, [[ ]], arrays, etc.)
- Arrays are 1-indexed (first element is ${array[1]}), unlike bash which is 0-indexed
- Prefer simple, standard commands over complex pipelines
- Quote filenames with spaces or special characters
- Check command output for errors before proceeding
- Clean up temporary files when done
- Commands run in a detached session with no controlling terminal and stdin closed. Interactive programs (sudo, ssh, vim, etc.) that require a TTY or terminal input will hang and be killed after a timeout.`,
		BuildCmd: func(binary, command string) *exec.Cmd {
			return exec.Command(binary, "-c", command)
		},
	}

	shellSh = &Shell{
		Name:   "sh",
		Binary: "sh",
		PromptFragment: `Execute a shell command using POSIX sh.

- Only POSIX sh syntax is available — no arrays, no [[ ]], no brace expansion
- Prefer simple, standard commands over complex pipelines
- Quote filenames with spaces or special characters
- Check command output for errors before proceeding
- Clean up temporary files when done
- Commands run in a detached session with no controlling terminal and stdin closed. Interactive programs (sudo, ssh, vim, etc.) that require a TTY or terminal input will hang and be killed after a timeout.`,
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
