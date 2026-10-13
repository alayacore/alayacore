package agent

import (
	"testing"
	"time"

	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/tlv"
)

// TestStepClockIsSharedByEchoAndRecord pins the reason Session.stepStartedAt
// exists: the timestamp a frame is echoed with and the one persisted on the
// part come from the same clock, so a reloaded message shows the time it was
// shown live — not a second time.Now() taken after the step's tools ran.
func TestStepClockIsSharedByEchoAndRecord(t *testing.T) {
	s := &Session{sessionConfig: sessionConfig{modelService: &modelService{}}}
	stamp := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	s.stepStartedAt = stamp

	if got, want := s.wireMetaNow(7).Time, stamp.Format(time.RFC3339); got != want {
		t.Errorf("echo time = %q, want the step clock %q", got, want)
	}

	part := &llm.TextPart{Text: "hi"}
	s.stampParts([]llm.ContentPart{part})
	if !part.GetTime().Equal(stamp) {
		t.Errorf("recorded time = %v, want the step clock %v", part.GetTime(), stamp)
	}
}

// TestStepClockFallsBackWhenUnset: a task that never runs OnBeforeSend (a
// summarize) has no step clock, so both sides fall back to the clock at the
// moment of use rather than producing a zero time.
func TestStepClockFallsBackWhenUnset(t *testing.T) {
	s := &Session{sessionConfig: sessionConfig{modelService: &modelService{}}}

	if s.wireMetaNow(1).Time == "" {
		t.Error("echo time is empty with no step clock; want a fallback timestamp")
	}
	part := &llm.TextPart{Text: "hi"}
	s.stampParts([]llm.ContentPart{part})
	if part.GetTime().IsZero() {
		t.Error("recorded time is zero with no step clock; want a fallback timestamp")
	}
}

// TestContentFrameRejectsAKindOnTheWrongSide: the tag names the side and the
// item names the kind, so a kind on the wrong side is rejected rather than
// stored — otherwise a reasoning item sent under UX would be a user-role part
// in memory but assistant content in the file (contentPartJSON writes a
// ReasoningPart as AX whatever its role). The same rule covers a tool result's
// nested items, which are model input too.
func TestContentFrameRejectsAKindOnTheWrongSide(t *testing.T) {
	cases := []struct {
		name    string
		tag     string
		body    string
		wantErr bool
	}{
		{"reasoning under UX", tlv.TagUserX, `{"type":"reasoning","text":"x"}`, true},
		{"image under AX", tlv.TagAssistantX, `{"type":"image","uri":"x"}`, true},
		{"video under AX", tlv.TagAssistantX, `{"type":"video","uri":"x"}`, true},
		{"text under UX", tlv.TagUserX, `{"type":"text","text":"x"}`, false},
		{"image under UX", tlv.TagUserX, `{"type":"image","uri":"x"}`, false},
		{"reasoning under AX", tlv.TagAssistantX, `{"type":"reasoning","text":"x"}`, false},
		{"text under AX", tlv.TagAssistantX, `{"type":"text","text":"x"}`, false},
		{"reasoning in a tool result", tlv.TagUserF,
			`{"call_id":"t1","output":[{"type":"reasoning","text":"x"}]}`, true},
		{"image in a tool result", tlv.TagUserF,
			`{"call_id":"t1","output":[{"type":"image","uri":"x"}]}`, false},
		{"text in a tool result", tlv.TagUserF,
			`{"call_id":"t1","output":[{"type":"text","text":"x"}]}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := contentPartFromJSON(tc.tag, []byte(tc.body))
			if (err != nil) != tc.wantErr {
				t.Errorf("contentPartFromJSON(%s, %s) err = %v, wantErr = %v", tc.tag, tc.body, err, tc.wantErr)
			}
		})
	}
}
