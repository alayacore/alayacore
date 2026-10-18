// Package testenv is where a test asks about the environment it is running in.
//
// One question so far: is an external tool this suite treats as a dependency
// present? A test may only answer that for itself — it cannot know whether the
// run it is part of was meant to be complete. "This run must be complete" is a
// property of the command that started it, so `make test` requires the tools (see
// the Makefile and .github/actions/test-deps/action.yml) and a missing one stops
// the run before a test can skip. A test that read CI for that answer would be
// guessing which workflow it is in, and it would guess wrong: it once reported
// test.yml as the workflow that installs a tool, while a release build — which
// declares nothing — was the one running it.
//
// It exists beside internal/wiretest for the same reason: a package whose only
// callers are tests, so the rule is stated once instead of restated wherever a
// tool is needed.
package testenv

import (
	"os/exec"
	"testing"
)

// RequireTool returns the path to tool, skipping the test when it is absent.
//
// role says what the tool is the reference for, so the skip names the dependency
// rather than only the binary, and it points at the two commands that resolve it:
// `make test` refuses to run without the tool, and the action installs it.
func RequireTool(t *testing.T, tool, role string) string {
	t.Helper()
	path, err := exec.LookPath(tool)
	if err != nil {
		t.Skipf("%s is not installed (%s); `make test` requires it and .github/actions/test-deps/action.yml installs it",
			tool, role)
	}
	return path
}
