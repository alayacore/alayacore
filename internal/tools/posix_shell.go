package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/alayacore/alayacore/internal/llm"
	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

// PosixShellInput represents the input for the posix_shell tool
type PosixShellInput struct {
	Command string `json:"command" jsonschema:"required,description=The shell command to execute"`
}

// NewPosixShellTool creates a new posix_shell tool for executing shell commands
func NewPosixShellTool() llm.Tool {
	return llm.NewTool(
		"posix_shell",
		`Execute a shell command.

Rules:
- Use POSIX-compliant shell syntax only (no bash/zsh-specific features)
- Prefer simple, standard commands over complex pipelines
- Quote filenames with spaces or special characters
- Check command output for errors before proceeding
- Clean up temporary files when done`,
	).
		WithSchema(llm.GenerateSchema(PosixShellInput{})).
		WithExecute(llm.TypedExecute(executePosixShell)).
		Build()
}

func executePosixShell(ctx context.Context, args PosixShellInput) (llm.ToolResultOutput, error) {
	var stdout, stderr bytes.Buffer

	parser := syntax.NewParser()
	prog, err := parser.Parse(strings.NewReader(args.Command), "")
	if err != nil {
		return llm.NewTextErrorResponse("parse error: " + err.Error()), nil
	}

	cwd, _ := os.Getwd()
	runner, err := interp.New(
		interp.Dir(cwd),
		interp.Env(expand.ListEnviron(os.Environ()...)),
		interp.StdIO(os.Stdin, &stdout, &stderr),
		interp.ExecHandlers(),
	)
	if err != nil {
		return llm.NewTextErrorResponse("failed to create runner: " + err.Error()), nil
	}

	err = runner.Run(ctx, prog)
	output := stdout.String()
	if stderr.Len() > 0 {
		if output != "" {
			output += "\n"
		}
		output += stderr.String()
	}

	if err != nil {
		var exitStatus interp.ExitStatus
		if errors.As(err, &exitStatus) {
			return llm.NewTextErrorResponse(fmt.Sprintf("[%d] %s", exitStatus, output)), nil
		}
		if output != "" {
			return llm.NewTextErrorResponse(fmt.Sprintf("%s\n%s", err.Error(), output)), nil
		}
		return llm.NewTextErrorResponse(err.Error()), nil
	}

	return llm.NewTextResponse(output), nil
}
