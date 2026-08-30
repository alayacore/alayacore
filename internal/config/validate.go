package config

import (
	"errors"
	"fmt"
	"os"
)

// ErrUsage marks a settings inconsistency caused by how the flags were
// combined or valued. main reports these on stderr and exits with code 2,
// distinguishing "you invoked alayacore incorrectly" from a runtime failure.
var ErrUsage = errors.New("invalid configuration")

// Validate checks cross-flag consistency and value ranges that Parse cannot see
// while parsing a single flag.
//
// The rule is fail-fast: an explicit setting that cannot possibly do what the
// user meant must be reported, never accepted and ignored. Each case below was
// previously silent — a mistyped flag produced an unexplained hang, an agent
// that did nothing while blaming the model, or a feature that never triggered.
//
// Returns the first problem found, wrapped in ErrUsage.
func Validate(s *Settings) error {
	// --terseio consumes all of stdin as the prompt or command, so tool
	// confirmations (answered via subsequent stdin lines) can never be
	// resolved. Fail fast instead of silently declining tools mid-task.
	if s.TerseIO && len(s.ToolConfirm) > 0 {
		return fmt.Errorf("%w: --terseio and --tool-confirm are mutually exclusive: terseio consumes stdin, so tool confirmations cannot be answered. Use --plainio for interactive confirmation", ErrUsage)
	}

	// --reasoning-level must be in [0, 2]. Fail fast instead of silently
	// ignoring an explicit but out-of-range value.
	if s.ReasoningLevelSet && (s.ReasoningLevel < ReasoningLevelOff || s.ReasoningLevel > ReasoningLevelMax) {
		return fmt.Errorf("%w: --reasoning-level must be 0, 1, or 2 (0=off, 1=normal, 2=max)", ErrUsage)
	}

	// --max-steps bounds the agent loop. A negative bound makes the loop body
	// unreachable, so the agent did nothing and still reported
	// "agent loop exceeded maximum steps" — the model got blamed for a
	// mistyped flag.
	if s.MaxSteps < 0 {
		return fmt.Errorf("%w: --max-steps must be >= 0 (got %d); 0 means no limit", ErrUsage, s.MaxSteps)
	}

	// --auto-summarize is a percentage of the context window. Above 100 the
	// threshold is unreachable and the feature the user just enabled silently
	// never fires; below 0 it means "disabled", which 0 already spells.
	if s.AutoSummarize < 0 || s.AutoSummarize > 100 {
		return fmt.Errorf("%w: --auto-summarize must be 0 (disabled) or 1-100 (threshold percentage), got %d", ErrUsage, s.AutoSummarize)
	}

	// --debug-log must be usable. Every consumer (the API logger and one MCP
	// transport each) would otherwise fail independently and report the same
	// problem N times — a wall of per-server errors for what is one bad path.
	// Creating the directory here is not a new side effect: the logger itself
	// does exactly this before opening its files.
	if s.DebugLogDir != "" {
		if err := os.MkdirAll(s.DebugLogDir, 0o755); err != nil {
			return fmt.Errorf("%w: --debug-log directory %q cannot be used: %v", ErrUsage, s.DebugLogDir, err)
		}
	}

	return nil
}
