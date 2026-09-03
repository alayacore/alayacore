package agent

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/llm"
)

// invertedArrivalProvider streams the answer BEFORE the thinking and never closes
// a block — the shape of a stream cut mid-turn by a server that put `content`
// ahead of `reasoning`. It declares the record layout (Position) as it goes, which
// is the only reason the persisted order can still be right.
type invertedArrivalProvider struct{}

func (invertedArrivalProvider) StreamMessages(_ context.Context, _ []llm.ContentPart, _ []llm.ToolDefinition, _, _ string) (iter.Seq2[llm.StreamEvent, error], error) {
	return func(yield func(llm.StreamEvent, error) bool) {
		yield(llm.TextDeltaEvent{Delta: "ANSWER", Key: "text", Position: 2}, nil)
		yield(llm.ReasoningDeltaEvent{Delta: "THINK", Key: "reasoning", Position: 1}, nil)
		yield(nil, errors.New("stream ended before its terminating signal"))
	}, nil
}

func (invertedArrivalProvider) SetReasoningLevel(_ int)                       {}
func (invertedArrivalProvider) SetReasoningConfigs(_ map[int]json.RawMessage) {}
func (invertedArrivalProvider) SetVideoConfig(_ int, _ int)                   {}

// The requirement is about the session file, not the screen: whatever order the
// bytes arrived in, the record written to disk has to be the turn as the protocol
// defines it, because that file is replayed to the model and re-laid on reopen.
//
// This walks the whole way: a cut step, through llm.Agent, through the task's
// publish path, into the file, and back out through the loader.
func TestSessionFileKeepsProtocolOrderForCutStep(t *testing.T) {
	agent := llm.NewAgent(llm.AgentConfig{Provider: invertedArrivalProvider{}, MaxSteps: 2})
	session := &Session{
		sessionConfig: sessionConfig{
			modelService:  &modelService{agent: agent},
			SessionConfig: SessionConfig{NoDelta: true},
		},
		sharedState: sharedState{histCounter: 10},
		runState:    runState{taskEventCh: make(chan taskEvent, 20)},
	}
	session.taskResultCh = make(chan []llm.ContentPart, 1)
	session.Contents = []llm.ContentPart{
		&llm.TextPart{Text: "hi", ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleUser}},
	}

	session.runTaskNormal(context.Background(), []llm.ContentPart{
		&llm.TextPart{Text: "do it", ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleUser}},
	})

	// handleTaskDone does this on the real path: adopt the worker's history, then
	// write it. Doing it here keeps the assertion about the file, not the memory.
	session.Contents = <-session.taskResultCh
	sessionPath := filepath.Join(t.TempDir(), "order.alaya")
	if err := session.saveContentToFile(sessionPath, session.Contents); err != nil {
		t.Fatalf("save: %v", err)
	}

	reloaded, err := loadSession(sessionPath)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	// Compare the assistant's turn only: the prompts are the user's own parts.
	var got []string
	for _, p := range reloaded.Contents {
		if p.GetRole() != llm.RoleAssistant {
			continue
		}
		switch v := p.(type) {
		case *llm.ReasoningPart:
			got = append(got, "reasoning:"+v.Text)
		case *llm.TextPart:
			got = append(got, "text:"+v.Text)
		}
	}
	joined := strings.Join(got, ",")
	want := "reasoning:THINK,text:ANSWER"
	if joined != want {
		t.Errorf("session file order = [%s], want [%s] — an answer recorded before the thinking that "+
			"produced it is replayed as a turn the model never sent", joined, want)
	}
}
