//go:build windows

package shell

import "os/exec"

// ----- Windows shell definitions -----

var (
	shellPwsh = &Shell{
		Name:           "PowerShell Core",
		Binary:         "pwsh",
		PromptFragment: "Execute a command using PowerShell (pwsh). Commands run without TTY; interactive programs will timeout.",
		BuildCmd: func(binary, command string) *exec.Cmd {
			return exec.Command(binary, "-NoLogo", "-NonInteractive", "-Command", command)
		},
	}

	shellPowerShell = &Shell{
		Name:           "Windows PowerShell",
		Binary:         "powershell",
		PromptFragment: "Execute a command using Windows PowerShell. Commands run without TTY; interactive programs will timeout.",
		BuildCmd: func(binary, command string) *exec.Cmd {
			return exec.Command(binary, "-NoLogo", "-NonInteractive", "-Command", command)
		},
	}

	shellCmd = &Shell{
		Name:           "cmd",
		Binary:         "cmd",
		PromptFragment: "Execute a command using cmd.exe. No PowerShell cmdlets. Commands run without TTY; interactive programs will timeout.",
		BuildCmd: func(binary, command string) *exec.Cmd {
			return exec.Command(binary, "/C", command)
		},
	}
)

// knownShells lists shells in preference order for Windows.
// cmd.exe is always available on Windows, so the list is guaranteed to
// produce a match.
var knownShells = []*Shell{
	shellPwsh,
	shellPowerShell,
	shellCmd,
}
