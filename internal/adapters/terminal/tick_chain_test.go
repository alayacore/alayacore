package terminal

// The periodic chain has exactly one owner.
//
// Update answers tickMsg with another tickCmd, and that is the whole of the
// 250ms heartbeat: one chain, started by Init. A second producer of tickMsg
// therefore becomes a second, permanent chain — which is what every submit used
// to do. submitCommand and friends arm a one-shot tick 50ms out (scheduleTick),
// so the user sees the result of submitting without waiting for the next beat;
// while that one-shot was a tickMsg, handleTick answered it with another
// tickCmd, so the chain count grew by one per submit and never shrank. Measured
// on the real loop before the split: one extra tickMsg took the rate from 4.0/s
// to 8.0/s, four took it to 20.0/s.
//
// The fix is structural rather than defensive — submitTickMsg runs the same work
// (tickWork) and schedules nothing — so what these tests pin is the shape: only
// tickMsg may come back with a command that arms another beat.

import "testing"

// TestOnlyTickMsgArmsTheChain is the invariant, in two assertions.
func TestOnlyTickMsgArmsTheChain(t *testing.T) {
	m := newTerminalForUpdateStatusTest(NewTerminalOutput(DefaultStyles()))
	m.screenWidth, m.screenHeight = 80, 24
	m.display = m.display.WithHeight(24)

	if _, cmd := m.Update(tickMsg{}); cmd == nil {
		t.Error("tickMsg must arm the next beat: without it the periodic chain stops")
	}

	if _, cmd := m.Update(submitTickMsg{}); cmd != nil {
		t.Error("submitTickMsg must not arm a beat: a submit would add a second, permanent 250ms chain")
	}
}

// TestScheduleTickIsNotATickMsg pins the producer side: scheduleTick is the one
// command whose result used to be a tickMsg.
func TestScheduleTickIsNotATickMsg(t *testing.T) {
	cmd := scheduleTick()
	if cmd == nil {
		t.Fatal("scheduleTick must return a command")
	}
	msg := cmd()
	if _, isTick := msg.(tickMsg); isTick {
		t.Fatal("scheduleTick delivers tickMsg: handleTick would answer it with another beat, so every submit leaves a chain behind")
	}
	if _, ok := msg.(submitTickMsg); !ok {
		t.Fatalf("scheduleTick delivers %T, want submitTickMsg", msg)
	}
}
