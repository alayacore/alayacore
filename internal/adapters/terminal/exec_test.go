package terminal

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestProgramExecProcessHelper is the helper process spawned by
// TestProgramExecProcess: it writes a marker to stderr and exits 0 (or 1
// when GO_WANT_HELPER_FAIL is set). It is invoked with -test.run limiting
// the run to this function, so the helper body is the only test that runs.
func TestProgramExecProcessHelper(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		t.Skip("helper process")
	}
	_, _ = os.Stderr.WriteString("helper-ran")
	if os.Getenv("GO_WANT_HELPER_FAIL") == "1" {
		os.Exit(1) //nolint:revive // intentional: helper process exit
	}
	os.Exit(0) //nolint:revive // intentional: helper process exit
}

// execTrigger asks the model to run an exec.Cmd via ExecProcess.
type execTrigger struct{ cmd *exec.Cmd }

// execResult is the message produced by the ExecProcess callback.
type execResult struct{ err error }

// execModel runs the command attached to execTrigger and records the result.
type execModel struct {
	result   *execResult
	resultCh chan *execResult
}

func (m *execModel) Init() Cmd { return nil }

func (m *execModel) Update(msg Msg) (Model, Cmd) {
	switch msg := msg.(type) {
	case execTrigger:
		return m, ExecProcess(msg.cmd, func(err error) Msg {
			return execResult{err: err}
		})
	case execResult:
		r := msg
		m.result = &r
		m.resultCh <- &r
		return m, nil
	}
	return m, nil
}

func (m *execModel) View() View { return NewView("v") }

// waitExecResult waits for the exec callback message and returns it.
func waitExecResult(t *testing.T, m *execModel) *execResult {
	t.Helper()
	select {
	case r := <-m.resultCh:
		return r
	case <-time.After(2 * time.Second):
		t.Fatal("exec result not delivered within 2s")
		return nil
	}
}

// TestProgramExecProcess verifies an execMsg runs the command and delivers
// the callback message to Update. The test program has no TTY, so exec runs
// the command directly (Suspend skips the terminal dance).
func TestProgramExecProcess(t *testing.T) {
	msgs := make(chan Msg, 8)
	p, _ := newTestProgram(msgs)
	m := &execModel{resultCh: make(chan *execResult, 1)}

	cmd := exec.Command(os.Args[0], "-test.run=TestProgramExecProcessHelper")
	cmd.Env = append(os.Environ(), "GO_WANT_HELPER_PROCESS=1")
	var errBuf strings.Builder
	cmd.Stderr = &errBuf

	done := make(chan error, 1)
	go func() { _, err := p.run(m); done <- err }()

	msgs <- execTrigger{cmd: cmd}
	r := waitExecResult(t, m)
	msgs <- QuitMsg{}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if r.err != nil {
		t.Fatalf("exec error: %v", r.err)
	}
	if !strings.Contains(errBuf.String(), "helper-ran") {
		t.Errorf("helper stderr = %q, want it to contain %q", errBuf.String(), "helper-ran")
	}
}

// TestProgramExecProcessError verifies a failing command surfaces its error
// through the callback.
func TestProgramExecProcessError(t *testing.T) {
	msgs := make(chan Msg, 8)
	p, _ := newTestProgram(msgs)
	m := &execModel{resultCh: make(chan *execResult, 1)}

	cmd := exec.Command(os.Args[0], "-test.run=TestProgramExecProcessHelper")
	cmd.Env = append(os.Environ(), "GO_WANT_HELPER_PROCESS=1", "GO_WANT_HELPER_FAIL=1")

	done := make(chan error, 1)
	go func() { _, err := p.run(m); done <- err }()

	msgs <- execTrigger{cmd: cmd}
	r := waitExecResult(t, m)
	msgs <- QuitMsg{}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if r.err == nil {
		t.Fatal("expected exec error, got nil")
	}
	if !strings.Contains(r.err.Error(), "exit status") {
		t.Errorf("unexpected error: %v", r.err)
	}
}

// TestProgramSuspendWithoutTTY verifies Suspend runs the function directly
// when the program has no terminal (tests).
func TestProgramSuspendWithoutTTY(t *testing.T) {
	p := &Program{}
	ran := false
	if err := p.Suspend(func() error { ran = true; return nil }); err != nil {
		t.Fatal(err)
	}
	if !ran {
		t.Fatal("run function not called")
	}
}
