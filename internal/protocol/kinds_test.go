package protocol

import "testing"

func TestKindAllowed(t *testing.T) {
	modelKinds := []string{TypeText, TypeImage, TypeVideo, TypeAudio, TypeDocument}
	for _, kind := range modelKinds {
		if !KindAllowed(PlaneUser, kind) {
			t.Errorf("user content must admit %q", kind)
		}
		if !KindAllowed(PlaneToolResult, kind) {
			t.Errorf("tool-result content must admit %q", kind)
		}
	}
	if KindAllowed(PlaneUser, TypeReasoning) {
		t.Error("user content must not admit reasoning")
	}
	if KindAllowed(PlaneToolResult, TypeReasoning) {
		t.Error("tool-result content must not admit reasoning")
	}
	if KindAllowed(PlaneAssistant, TypeImage) {
		t.Error("assistant content must not admit image")
	}
	if !KindAllowed(PlaneAssistant, TypeReasoning) || !KindAllowed(PlaneAssistant, TypeText) {
		t.Error("assistant content must admit text and reasoning")
	}
	if KindAllowed(Plane("bogus"), TypeText) {
		t.Error("an unknown plane must admit nothing")
	}
}

// TestUserAndToolResultKindsAgree pins the one fact the partition relies on: the
// two into-the-model planes carry the same kinds. If they ever diverge, a part
// that is valid as user content would be rejected as tool-result content (or the
// reverse) for no reason a reader could see.
func TestUserAndToolResultKindsAgree(t *testing.T) {
	user := PlaneKinds(PlaneUser)
	tool := PlaneKinds(PlaneToolResult)
	if len(user) != len(tool) {
		t.Fatalf("user kinds %v and tool-result kinds %v differ", user, tool)
	}
	for i := range user {
		if user[i] != tool[i] {
			t.Fatalf("user kinds %v and tool-result kinds %v differ", user, tool)
		}
	}
}

// TestPlaneKindsIsACopy guards the table: a caller ranging over a plane's kinds
// must not be able to change what the protocol admits.
func TestPlaneKindsIsACopy(t *testing.T) {
	got := PlaneKinds(PlaneUser)
	got[0] = "mutated"
	if again := PlaneKinds(PlaneUser); again[0] != TypeText {
		t.Fatalf("PlaneKinds exposed the table: got %q after mutation", again[0])
	}
}
