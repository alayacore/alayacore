package terminal

import (
	"bytes"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeModel is a minimal Model for driving the event loop in tests.
type fakeModel struct {
	initCmd Cmd
	updates []Msg
	views   []string
	panicOn any
}

func (m *fakeModel) Init() Cmd { return m.initCmd }

func (m *fakeModel) Update(msg Msg) (Model, Cmd) {
	m.updates = append(m.updates, msg)
	if m.panicOn != nil {
		if _, isQuit := msg.(QuitMsg); !isQuit && msgType(msg) == msgType(m.panicOn) {
			panic("boom")
		}
	}
	return m, nil
}

func (m *fakeModel) View() View {
	m.views = append(m.views, "frame")
	return NewView(strings.Join(m.views, "\n"))
}

// msgType returns a comparable type identifier for a message.
func msgType(v any) string { return fmt.Sprintf("%T", v) }

// newTestProgram builds a Program with an injected message channel and a
// buffer-backed screen.
func newTestProgram(msgs chan Msg) (*Program, *bytes.Buffer) {
	var buf bytes.Buffer
	return &Program{
		msgs:   msgs,
		screen: &Screen{out: &buf},
	}, &buf
}

// TestProgramUpdateAndQuit verifies messages reach Update and QuitMsg ends
// the loop.
func TestProgramUpdateAndQuit(t *testing.T) {
	msgs := make(chan Msg, 4)
	p, _ := newTestProgram(msgs)
	m := &fakeModel{}

	go func() {
		msgs <- KeyPressMsg(Key{Code: 'j'})
		msgs <- WindowSizeMsg{Width: 100, Height: 40}
		msgs <- QuitMsg{}
	}()

	if _, err := p.run(m); err != nil {
		t.Fatal(err)
	}

	if len(m.updates) != 2 {
		t.Fatalf("expected 2 updates, got %d: %#v", len(m.updates), m.updates)
	}
	if _, ok := m.updates[0].(KeyPressMsg); !ok {
		t.Errorf("update[0] = %T, want KeyPressMsg", m.updates[0])
	}
	ws, ok := m.updates[1].(WindowSizeMsg)
	if !ok || ws.Width != 100 || ws.Height != 40 {
		t.Errorf("update[1] = %#v, want WindowSizeMsg{100,40}", m.updates[1])
	}
}

// TestProgramCmdResult verifies a command returned by Update is dispatched
// and its result message is fed back into Update.
func TestProgramCmdResult(t *testing.T) {
	msgs := make(chan Msg, 4)
	p, _ := newTestProgram(msgs)
	m := &cmdModel{resultCh: make(chan int, 1)}

	done := make(chan error, 1)
	go func() { _, err := p.run(m); done <- err }()

	msgs <- cmdTrigger{}
	if err := waitFor(t, func() bool { return m.result != 0 }); err != nil {
		t.Fatal(err)
	}
	msgs <- QuitMsg{}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if m.result != 42 {
		t.Errorf("cmd result = %d, want 42", m.result)
	}
}

type cmdTrigger struct{}

type cmdResultMsg int

// cmdModel returns a command from Update that delivers a cmdResultMsg.
type cmdModel struct {
	result   int
	resultCh chan int
}

func (m *cmdModel) Init() Cmd { return nil }

func (m *cmdModel) Update(msg Msg) (Model, Cmd) {
	switch msg := msg.(type) {
	case cmdTrigger:
		return m, func() Msg { return cmdResultMsg(42) }
	case cmdResultMsg:
		m.result = int(msg)
		m.resultCh <- m.result
		return m, nil
	}
	return m, nil
}

func (m *cmdModel) View() View { return NewView("v") }

// TestProgramBatchSequence verifies Batch and Sequence command execution.
func TestProgramBatchSequence(t *testing.T) {
	msgs := make(chan Msg, 8)
	p, _ := newTestProgram(msgs)

	var mu sync.Mutex
	var order []string
	ran := make(chan string, 8)

	run := func(name string) Msg {
		mu.Lock()
		order = append(order, name)
		mu.Unlock()
		ran <- name
		return batchResult{name}
	}

	batchCmd := Batch(
		func() Msg { return run("b1") },
		func() Msg { return run("b2") },
	)
	seqCmd := Sequence(
		func() Msg { return run("s1") },
		func() Msg { return run("s2") },
	)

	m := &cmdMapModel{cmds: map[string]Cmd{"batch": batchCmd, "seq": seqCmd}}

	done := make(chan error, 1)
	go func() { _, err := p.run(m); done <- err }()

	msgs <- cmdTrigger{} // batch
	for i := 0; i < 2; i++ {
		<-ran
	}
	msgs <- seqTrigger{} // sequence
	for i := 0; i < 2; i++ {
		<-ran
	}
	msgs <- QuitMsg{}
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	if len(order) != 4 {
		t.Fatalf("expected 4 command executions, got %d: %v", len(order), order)
	}
	got := map[string]bool{}
	for _, o := range order {
		got[o] = true
	}
	for _, want := range []string{"b1", "b2", "s1", "s2"} {
		if !got[want] {
			t.Errorf("command %q never ran: %v", want, order)
		}
	}
	// Sequence must run in order (s1 before s2).
	si, sj := -1, -1
	for i, o := range order {
		if o == "s1" {
			si = i
		}
		if o == "s2" {
			sj = i
		}
	}
	if si == -1 || sj == -1 || si > sj {
		t.Errorf("sequence not in order: %v", order)
	}
}

type batchResult struct{ name string }
type seqResult struct{}
type seqTrigger struct{}

// cmdMapModel dispatches commands by trigger type.
type cmdMapModel struct {
	cmds map[string]Cmd
}

func (m *cmdMapModel) Init() Cmd { return nil }

func (m *cmdMapModel) Update(msg Msg) (Model, Cmd) {
	switch msg.(type) {
	case cmdTrigger:
		return m, m.cmds["batch"]
	case seqTrigger:
		return m, m.cmds["seq"]
	case batchResult, seqResult:
		return m, nil
	}
	return m, nil
}

func (m *cmdMapModel) View() View { return NewView("v") }

// TestProgramTick verifies Tick delivers a message after the duration.
func TestProgramTick(t *testing.T) {
	msgs := make(chan Msg, 4)
	p, _ := newTestProgram(msgs)

	m := &tickModel{}
	m.mu.Lock()
	m.ticked = 0
	m.mu.Unlock()

	done := make(chan error, 1)
	go func() { _, err := p.run(m); done <- err }()

	start := time.Now()
	msgs <- tickTrigger{}
	if err := waitFor(t, func() bool { return m.tickedCount() > 0 }); err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(start)
	msgs <- QuitMsg{}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if elapsed < 20*time.Millisecond {
		t.Errorf("tick fired too early: %v", elapsed)
	}
}

type tickTrigger struct{}
type tickDone struct{}

type tickModel struct {
	mu     sync.Mutex
	ticked int
}

func (m *tickModel) tickedCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.ticked
}

func (m *tickModel) Init() Cmd { return nil }

func (m *tickModel) Update(msg Msg) (Model, Cmd) {
	switch msg.(type) {
	case tickTrigger:
		return m, Tick(30*time.Millisecond, func(_ time.Time) Msg { return tickDone{} })
	case tickDone:
		m.mu.Lock()
		m.ticked++
		m.mu.Unlock()
		return m, nil
	}
	return m, nil
}

func (m *tickModel) View() View { return NewView("v") }

// waitFor polls cond until it is true or times out.
func waitFor(t *testing.T, cond func() bool) error {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return nil
		}
		time.Sleep(5 * time.Millisecond)
	}
	return fmt.Errorf("condition not met within 2s")
}

// TestProgramRender verifies the loop renders the model's view.
func TestProgramRender(t *testing.T) {
	msgs := make(chan Msg, 4)
	p, buf := newTestProgram(msgs)
	m := &fakeModel{}

	go func() {
		msgs <- KeyPressMsg(Key{Code: 'j'})
		msgs <- QuitMsg{}
	}()

	if _, err := p.run(m); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(buf.String(), "frame") {
		t.Errorf("expected rendered frame in output, got %q", buf.String())
	}
}

// TestProgramPanicRecovery verifies a panic in Update returns an error
// instead of crashing the process.
func TestProgramPanicRecovery(t *testing.T) {
	msgs := make(chan Msg, 4)
	p, _ := newTestProgram(msgs)

	type panicMsg struct{}
	m := &fakeModel{panicOn: panicMsg{}}

	go func() {
		msgs <- panicMsg{}
		msgs <- QuitMsg{}
	}()

	_, err := p.run(m)
	if err == nil {
		t.Fatal("expected error from panic, got nil")
	}
	if !strings.Contains(err.Error(), "panic in Update") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestProgramSuspendIgnored verifies SuspendMsg does not end the loop
// (module 5 will implement it).
func TestProgramSuspendIgnored(t *testing.T) {
	msgs := make(chan Msg, 4)
	p, _ := newTestProgram(msgs)
	m := &fakeModel{}

	go func() {
		msgs <- SuspendMsg{}
		msgs <- KeyPressMsg(Key{Code: 'j'})
		msgs <- QuitMsg{}
	}()

	if _, err := p.run(m); err != nil {
		t.Fatal(err)
	}
	// SuspendMsg is handled internally and not delivered to Update.
	if len(m.updates) != 1 {
		t.Errorf("expected 1 update (key only), got %d: %#v", len(m.updates), m.updates)
	}
}
