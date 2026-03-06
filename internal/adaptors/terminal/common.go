package terminal

import (
	"charm.land/fantasy"
)

// AgentFactory creates a new agent for each client session
type AgentFactory func() fantasy.Agent

// Adaptor is the interface for terminal adaptors
type Adaptor interface {
	Start()
}
