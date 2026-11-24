package agent

// A model switch may now land while a task is running, so the published runtime
// is the one piece of state two goroutines share: run() replaces it, the task
// reads the one it started with. These pin the two properties that make that
// safe — the reader takes a snapshot and never sees it change, and the pair
// inside a snapshot always belongs to the same model.

import (
	"sync"
	"testing"

	"github.com/alayacore/alayacore/internal/llm"
)

// TestPublishedSnapshotIsReplacedNotMutated: a task that captured its runtime
// keeps it. This is the whole reason a switch can be accepted mid-task — the
// running step cannot have the model changed under it, because the switch
// publishes a new snapshot rather than writing into the one it is reading.
func TestPublishedSnapshotIsReplacedNotMutated(t *testing.T) {
	first := &llm.Agent{}
	second := &llm.Agent{}

	s := &Session{sessionConfig: sessionConfig{
		modelService: testModelService(first, nil, 1000),
	}}

	// What a task does at its start.
	rt := s.currentRuntime()
	if rt == nil || rt.agent != first {
		t.Fatalf("currentRuntime() = %+v, want the published first agent", rt)
	}

	// What :model_set does while that task runs.
	s.modelService.contextLimit.Store(2000)
	s.modelService.publishRuntime(second)

	if rt.agent != first || rt.contextLimit != 1000 {
		t.Errorf("the task's snapshot changed under it: agent=%p limit=%d, want %p/1000",
			rt.agent, rt.contextLimit, first)
	}
	// …and the next task gets the new one, window included.
	next := s.currentRuntime()
	if next.agent != second || next.contextLimit != 2000 {
		t.Errorf("next snapshot = %+v, want agent %p with limit 2000", next, second)
	}
}

// TestModelRuntimeSnapshotIsRaceFree exercises the read and the write together
// under -race: the task's read (one Load per task, then per-step threshold
// checks) against a stream of switches. Without the snapshot these are two plain
// fields, one written by run() while the task reads them — which is a data race,
// not a stale value.
func TestModelRuntimeSnapshotIsRaceFree(t *testing.T) {
	agents := []*llm.Agent{{}, {}}
	ms := testModelService(agents[0], nil, 100)
	s := &Session{sessionConfig: sessionConfig{modelService: ms}}

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			ms.contextLimit.Store(int64(i%2 + 1))
			ms.publishRuntime(agents[i%2])
		}
	}()

	for i := 0; i < 2000; i++ {
		rt := s.currentRuntime()
		if rt == nil {
			t.Fatal("the runtime disappeared mid-switch")
		}
		// The pair travels together: a snapshot always carries a window, so no
		// reader can see the new agent with the old model's limit of zero.
		if rt.contextLimit == 0 {
			t.Fatal("a published snapshot carried no context window")
		}
		if rt.agent == nil {
			t.Fatal("a published snapshot carried no agent")
		}
		if s.Agent() == nil || s.contextLimit() == 0 {
			t.Fatal("an accessor disagreed with the snapshot")
		}
	}
	close(stop)
	wg.Wait()
}
