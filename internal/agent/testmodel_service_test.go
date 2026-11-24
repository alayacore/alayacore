package agent

// A task test builds a Session by hand, which means it also has to state the one
// thing production states through the model service: which agent the task runs
// on, and the context window that agent's threshold is measured against. This is
// that marriage, in the same shape SwitchModel publishes it, so a test cannot
// leave the two disagreeing.

import "github.com/alayacore/alayacore/internal/llm"

// testModelService returns a service whose published runtime is agent, carrying
// provider (may be nil) and contextLimit (0 disables the threshold, as --auto-summarize's
// 0 does).
func testModelService(agent *llm.Agent, provider llm.Provider, contextLimit int64) *modelService {
	ms := &modelService{provider: provider}
	ms.contextLimit.Store(contextLimit)
	ms.publishRuntime(agent)
	return ms
}

// setTestContextLimit republishes the current window without touching the agent,
// for the tests that raise or lower it after the session is built. It goes
// through the same publish path a real switch uses, so the snapshot a task reads
// cannot disagree with the number the test set.
func setTestContextLimit(s *Session, limit int64) {
	s.modelService.contextLimit.Store(limit)
	var agent *llm.Agent
	if rt := s.modelService.currentRuntime(); rt != nil {
		agent = rt.agent
	}
	s.modelService.publishRuntime(agent)
}
