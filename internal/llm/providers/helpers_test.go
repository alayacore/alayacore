package providers_test

import "github.com/alayacore/alayacore/internal/llm"

// testMsg builds the flat ContentParts a provider call takes from a role and
// the parts of one message. Shared by the unit tests and the integration
// tests, so it lives outside integration_test.go's build tag.
func testMsg(role llm.MessageRole, parts ...llm.ContentPart) []llm.ContentPart {
	for _, p := range parts {
		p.SetRole(role)
	}
	return parts
}
