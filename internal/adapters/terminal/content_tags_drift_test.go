package terminal

import (
	"testing"

	"github.com/alayacore/alayacore/internal/protocol"
	"github.com/alayacore/alayacore/internal/wire"
)

// TestContentDisplayCoversEveryAdmittedKind ties this adapter's rendering to the
// protocol's table. Every kind the protocol admits on a plane the terminal
// ingests must have a display tag here, and the mapping must round-trip through
// displayTagFrame. Adding a kind to the protocol therefore fails this test until
// the adapter is taught to draw it, instead of the frame being silently dropped.
func TestContentDisplayCoversEveryAdmittedKind(t *testing.T) {
	for plane, tag := range map[protocol.Plane]string{
		protocol.PlaneUser:      wire.TagUserX,
		protocol.PlaneAssistant: wire.TagAssistantX,
	} {
		for _, kind := range protocol.PlaneKinds(plane) {
			displayTag, ok := contentDisplayTag(tag, kind)
			if !ok {
				t.Errorf("contentDisplayTag(%q, %q) has no display tag, but the protocol admits %q on plane %q", tag, kind, kind, plane)
				continue
			}
			gotTag, gotKind, ok := displayTagFrame(displayTag)
			if !ok || gotTag != tag || gotKind != kind {
				t.Errorf("displayTagFrame(%q) = (%q, %q, %v), want (%q, %q, true)", displayTag, gotTag, gotKind, ok, tag, kind)
			}
		}
	}
}
