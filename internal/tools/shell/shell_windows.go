//go:build windows

package shell

import "os/exec"

// ----- Windows shell definitions -----

var (
	shellPwsh = &Shell{
		Name:   "PowerShell Core",
		Binary: "pwsh",
		PromptFragment: `Execute a command using PowerShell (pwsh).

- PowerShell Core syntax is available (cmdlets, pipelines, objects)
- Prefer simple, standard commands over complex pipelines
- Quote filenames with spaces or special characters
- Check command output for errors before proceeding
- Clean up temporary files when done
- Commands run in a detached session with no controlling terminal and stdin closed. Interactive programs that require a TTY or terminal input will fail immediately.`,
		BuildCmd: func(binary, command string) *exec.Cmd {
			return exec.Command(binary, "-NoLogo", "-NonInteractive", "-Command", command)
		},
	}

	shellPowerShell = &Shell{
		Name:   "Windows PowerShell",
		Binary: "powershell",
		PromptFragment: `Execute a command using Windows PowerShell.

- Windows PowerShell syntax is available (cmdlets, pipelines, objects)
- Prefer simple, standard commands over complex pipelines
- Quote filenames with spaces or special characters
- Check command output for errors before proceeding
- Clean up temporary files when done
- Commands run in a detached session with no controlling terminal and stdin closed. Interactive programs that require a TTY or terminal input will fail immediately.`,
		BuildCmd: func(binary, command string) *exec.Cmd {
			return exec.Command(binary, "-NoLogo", "-NonInteractive", "-Command", command)
		},
	}

	shellCmd = &Shell{
		Name:   "cmd",
		Binary: "cmd",
		PromptFragment: `Execute a command using cmd.exe.

- Only cmd.exe syntax is available (batch scripting, %VAR% expansion, etc.) — no PowerShell cmdlets
- Prefer simple, standard commands over complex pipelines
- Quote filenames with spaces or special characters
- Check command output for errors before proceeding
- Clean up temporary files when done
- Commands run in a detached session with no controlling terminal and stdin closed. Interactive programs that require a TTY or terminal input will fail immediately.`,
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
