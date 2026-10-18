package app

import (
	"strings"
	"testing"
)

// TestSystemPromptStatesToolCallOrderWithoutNamingAMode pins the one rule the
// base prompt has to state about tool calls, and the way it must be stated.
//
// The guarantee that holds for every model.conf is the weak one: a step's calls
// may run concurrently and in any order, so a call that needs another call's
// result belongs in a later step. `serial_tool_calls: true` gives a model the
// stronger promise — declared order — but the prompt is one string, built once
// (app.Setup) and handed unchanged to every model the session switches to. So a
// prompt written for serial mode would be telling the model to rely on a
// property that the same session loses the moment the user picks a model that
// does not set the option, and the history carries the consequences of that
// reliance across the switch.
//
// Hence: state what the model may rely on, never an execution mode. A rewrite
// that says "tool calls run in parallel" (or "run in order") is false under the
// other setting, and this test is what stops one from landing quietly.
func TestSystemPromptStatesToolCallOrderWithoutNamingAMode(t *testing.T) {
	prompt := strings.ToLower(systemPromptBase)

	for _, want := range []string{"concurrently", "any order", "later step"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("base prompt no longer states the tool-call order contract (%q missing):\n%s", want, systemPromptBase)
		}
	}

	for _, mode := range []string{"serial", "parallel", "one at a time", "in order"} {
		if strings.Contains(prompt, mode) {
			t.Errorf("base prompt names an execution mode (%q). It is one string for every model.conf, so a mode-specific promise is false for the other mode, and a model that switched mid-session keeps relying on it:\n%s",
				mode, systemPromptBase)
		}
	}
}
