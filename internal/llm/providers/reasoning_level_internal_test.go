package providers

// The reasoning level moves while a request is being built.
//
// :reasoning_level_set is an immediate command, so the session's run() goroutine
// stores the new level while the task goroutine is inside StreamMessages building
// the next body. The two sides share one llm.ReasoningLevel — the session holds
// it, the provider reads through the pointer it was constructed with — so the
// shape under test is literally that: a writer that only knows the level object,
// a reader that only knows the provider.
//
// Run with -race (make test does) for this to mean anything.

import (
	"encoding/json"
	"sync"
	"testing"

	"github.com/alayacore/alayacore/internal/llm"
)

func TestReasoningLevelIsReadableWhileItIsSet(t *testing.T) {
	level := llm.NewReasoningLevel(0)
	p, err := NewOpenAI(BaseConfig{APIKey: "test-key", ReasoningLevel: level})
	if err != nil {
		t.Fatalf("NewOpenAI: %v", err)
	}
	p.SetReasoningConfigs(map[int]json.RawMessage{
		// A distinct top-level key per level, so a body says which level it was
		// built from without having to trust the level itself.
		0: json.RawMessage(`{"lvl0":true}`),
		1: json.RawMessage(`{"lvl1":true}`),
		2: json.RawMessage(`{"lvl2":true}`),
	})

	const iterations = 2000
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)

	go func() { // the writer: run(), applying :r — touches only the level object
		defer wg.Done()
		defer close(stop)
		for i := 0; i < iterations; i++ {
			level.Set(i % 3)
		}
	}()

	go func() { // the reader: the task goroutine, once per request
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			body := p.mergeReasoningConfig(map[string]any{})
			// Exactly one level's JSON, the one the read returned — never zero
			// (an unreadable level would key a missing map entry) and never two.
			found := 0
			for _, key := range []string{"lvl0", "lvl1", "lvl2"} {
				if _, ok := body[key]; ok {
					found++
				}
			}
			if found != 1 {
				t.Errorf("one merge saw %d levels of %v: %v", found, []string{"lvl0", "lvl1", "lvl2"}, body)
				return
			}
		}
	}()

	wg.Wait()
}

// TestSetReasoningLevelTakesEffectOnTheNextBody: the promise the command makes,
// in the smallest form that can express it — a level stored between two merges is
// what the second one sees. Nothing tells the provider about it.
func TestSetReasoningLevelTakesEffectOnTheNextBody(t *testing.T) {
	level := llm.NewReasoningLevel(1)
	p, err := NewAnthropic(BaseConfig{APIKey: "test-key", ReasoningLevel: level})
	if err != nil {
		t.Fatalf("NewAnthropic: %v", err)
	}
	p.SetReasoningConfigs(map[int]json.RawMessage{
		1: json.RawMessage(`{"thinking":{"type":"enabled"}}`),
		2: json.RawMessage(`{"thinking":{"type":"max"}}`),
	})

	first := p.mergeReasoningConfig(map[string]any{})
	if got := first["thinking"].(map[string]any)["type"]; got != "enabled" {
		t.Fatalf("level 1 merged thinking.type = %v, want enabled", got)
	}

	level.Set(2)
	second := p.mergeReasoningConfig(map[string]any{})
	if got := second["thinking"].(map[string]any)["type"]; got != "max" {
		t.Fatalf("level 2 merged thinking.type = %v, want max — the shared level did not reach the body", got)
	}
}

// TestProviderWithoutASharedLevelOwnsOne: a provider built outside a session (a
// test, a one-off) must behave exactly as a plain int field would — level 0, no
// fields merged — instead of panicking on a nil the caller never knew about.
func TestProviderWithoutASharedLevelOwnsOne(t *testing.T) {
	p, err := NewAnthropic(BaseConfig{APIKey: "test-key"})
	if err != nil {
		t.Fatalf("NewAnthropic: %v", err)
	}
	p.SetReasoningConfigs(map[int]json.RawMessage{
		0: json.RawMessage(`{"thinking":{"type":"disabled"}}`),
	})
	if got := p.reasoningLevel.Get(); got != 0 {
		t.Fatalf("a provider with no shared level reads %d, want 0 (off)", got)
	}
	body := p.mergeReasoningConfig(map[string]any{})
	if _, ok := body["thinking"]; !ok {
		t.Error("level 0's reasoning_0 config was not merged")
	}
}
