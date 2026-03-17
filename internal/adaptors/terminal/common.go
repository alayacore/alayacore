package terminal

import (
	"github.com/alayacore/alayacore/internal/llm"
)

// AgentFactory creates a new agent for each client session
type AgentFactory func() *llm.Agent
