package mcpauth

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alayacore/alayacore/internal/platform"
)

// sendCapture collects the CI commands the flow submits. A channel is used
// instead of a slice because the flow writes from a goroutine while the
// test reads from the main goroutine.
type sendCapture struct {
	ch chan string
}

func newSendCapture() *sendCapture {
	return &sendCapture{ch: make(chan string, 4)}
}

func (c *sendCapture) send(cmd string) error {
	c.ch <- cmd
	return nil
}

// syncBuffer is a mutex-guarded buffer for the flow's printer. Two servers'
// flows run concurrently and both print, so the test's printer must
// serialize — exactly as the real adapters' print callbacks do.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) printf(format string, args ...any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fmt.Fprintf(&s.b, format, args...)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// fakeCallbackServer is a stub for Flow.startServer that delivers a canned
// result immediately and records its cleanup on cleanupCh.
type fakeCallbackServer struct {
	cleanupCh chan struct{}
}

func (f *fakeCallbackServer) start(result platform.CallbackResult) func(string, string, string) (<-chan platform.CallbackResult, string, func()) {
	return func(_, _, _ string) (<-chan platform.CallbackResult, string, func()) {
		ch := make(chan platform.CallbackResult, 1)
		ch <- result
		return ch, "http://127.0.0.1:4242/callback", func() {
			select {
			case f.cleanupCh <- struct{}{}:
			default:
			}
		}
	}
}

type testFlowEnv struct {
	flow *Flow
	out  *syncBuffer
	ci   *sendCapture
	fake *fakeCallbackServer
}

// newTestFlow builds a flow with a capturing printer, a command capture,
// and a fake callback server. The default unresolved policy is left nil
// (decline); tests that need the manual path install their own.
func newTestFlow() *testFlowEnv {
	out := &syncBuffer{}
	ci := newSendCapture()
	flow := New(ci.send, out.printf)
	flow.openURL = func(string) error { return nil }
	return &testFlowEnv{flow: flow, out: out, ci: ci, fake: &fakeCallbackServer{cleanupCh: make(chan struct{}, 1)}}
}

func TestFlow_SendsConfirmOnCallback(t *testing.T) {
	env := newTestFlow()
	env.flow.startServer = env.fake.start(platform.CallbackResult{Code: "auth-code-123"})

	env.flow.Start("github", "https://example.com/authorize?redirect_uri={{redirect_uri}}&state={{state}}")

	got := <-env.ci.ch
	want := "mcp_confirm github auth-code-123 http://127.0.0.1:4242/callback"
	if got != want {
		t.Errorf("command = %q, want %q", got, want)
	}
	<-env.fake.cleanupCh

	text := env.out.String()
	if !strings.Contains(text, "https://example.com/authorize?redirect_uri=http%3A%2F%2F127.0.0.1%3A4242%2Fcallback") {
		t.Errorf("output = %q, want substituted URL", text)
	}
}

func TestFlow_CallbackErrorDeclinesServer(t *testing.T) {
	env := newTestFlow()
	env.flow.startServer = env.fake.start(platform.CallbackResult{Err: errors.New("state mismatch")})

	env.flow.Start("github", "https://example.com/authorize")

	got := <-env.ci.ch
	if got != "mcp_decline github" {
		t.Errorf("command = %q, want mcp_decline github", got)
	}
	<-env.fake.cleanupCh

	if !strings.Contains(env.out.String(), "state mismatch") {
		t.Errorf("output = %q, want callback error text", env.out.String())
	}
}

// A nil unresolved policy must decline: an adapter that cannot receive a
// typed code has no other honest outcome, and leaving the server pending
// would hang MCP initialization forever.
func TestFlow_UnresolvedDefaultsToDeclineOnTimeout(t *testing.T) {
	env := newTestFlow()
	// Callback server that never delivers a result.
	env.flow.startServer = func(_, _, _ string) (<-chan platform.CallbackResult, string, func()) {
		return make(chan platform.CallbackResult), "http://127.0.0.1:4242/callback", func() {
			select {
			case env.fake.cleanupCh <- struct{}{}:
			default:
			}
		}
	}
	env.flow.timeout = 30 * time.Millisecond

	env.flow.Start("github", "https://example.com/authorize")

	select {
	case got := <-env.ci.ch:
		if got != "mcp_decline github" {
			t.Errorf("command = %q, want mcp_decline github", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("flow did not decline after timeout")
	}
	select {
	case <-env.fake.cleanupCh:
	case <-time.After(2 * time.Second):
		t.Fatal("flow did not clean up after timeout")
	}
	if !strings.Contains(env.out.String(), "timed out") {
		t.Errorf("output = %q, want the timeout reason", env.out.String())
	}
}

// The manual policy (plainio on a terminal) hands the server to the
// adapter and keeps the flow alive without declining — the session
// completes the server when the typed :mcp_confirm arrives.
func TestFlow_UnresolvedWaitManualKeepsWaiting(t *testing.T) {
	env := newTestFlow()
	env.flow.startServer = func(_, _, _ string) (<-chan platform.CallbackResult, string, func()) {
		return make(chan platform.CallbackResult), "http://127.0.0.1:4242/callback", func() {
			select {
			case env.fake.cleanupCh <- struct{}{}:
			default:
			}
		}
	}
	env.flow.timeout = 30 * time.Millisecond
	var gotServer, gotRedirect, gotReason string
	env.flow.SetUnresolved(func(server, redirectURI, reason string) Action {
		gotServer, gotRedirect, gotReason = server, redirectURI, reason
		return WaitManual
	})

	env.flow.Start("github", "https://example.com/authorize")

	select {
	case <-env.fake.cleanupCh:
	case <-time.After(2 * time.Second):
		t.Fatal("flow did not stop waiting after timeout")
	}
	if gotServer != "github" || gotRedirect != "http://127.0.0.1:4242/callback" {
		t.Errorf("unresolved args = %q, %q; want server+redirect URI", gotServer, gotRedirect)
	}
	if !strings.Contains(gotReason, "timed out") {
		t.Errorf("reason = %q, want timeout text", gotReason)
	}
	if len(env.ci.ch) != 0 {
		t.Error("manual path must not decline the server")
	}
}

// The browser could not be opened: the automatic path cannot receive a
// callback, so with a nil policy the server is declined immediately rather
// than left hanging.
func TestFlow_BrowserOpenFailureDeclines(t *testing.T) {
	env := newTestFlow()
	env.flow.startServer = func(_, _, _ string) (<-chan platform.CallbackResult, string, func()) {
		return make(chan platform.CallbackResult), "http://127.0.0.1:4242/callback", func() {
			select {
			case env.fake.cleanupCh <- struct{}{}:
			default:
			}
		}
	}
	env.flow.openURL = func(string) error { return errors.New("no DISPLAY") }
	env.flow.timeout = 30 * time.Second

	env.flow.Start("github", "https://example.com/authorize")

	select {
	case got := <-env.ci.ch:
		if got != "mcp_decline github" {
			t.Errorf("command = %q, want mcp_decline github", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("flow did not decline after browser open failure")
	}
	<-env.fake.cleanupCh

	if !strings.Contains(env.out.String(), "failed to open browser: no DISPLAY") {
		t.Errorf("output = %q, want the open failure as the reason", env.out.String())
	}
}

func TestFlow_AbortStopsWaiting(t *testing.T) {
	env := newTestFlow()
	env.flow.startServer = func(_, _, _ string) (<-chan platform.CallbackResult, string, func()) {
		return make(chan platform.CallbackResult), "http://127.0.0.1:4242/callback", func() {
			select {
			case env.fake.cleanupCh <- struct{}{}:
			default:
			}
		}
	}

	env.flow.Start("github", "https://example.com/authorize")
	env.flow.Abort()

	select {
	case <-env.fake.cleanupCh:
	case <-time.After(2 * time.Second):
		t.Fatal("flow did not stop after abort")
	}
	if len(env.ci.ch) != 0 {
		t.Error("abort must not send any CI frame")
	}
}

func TestFlow_AbortIsIdempotent(t *testing.T) {
	env := newTestFlow()
	env.flow.Abort()
	env.flow.Abort() // must not panic
}

func TestFlow_ConnectedCancelsFlow(t *testing.T) {
	env := newTestFlow()
	env.flow.startServer = func(_, _, _ string) (<-chan platform.CallbackResult, string, func()) {
		return make(chan platform.CallbackResult), "http://127.0.0.1:4242/callback", func() {
			select {
			case env.fake.cleanupCh <- struct{}{}:
			default:
			}
		}
	}

	env.flow.Start("github", "https://example.com/authorize")
	env.flow.Connected("github") // authorization completed via manual :mcp_confirm

	select {
	case <-env.fake.cleanupCh:
	case <-time.After(2 * time.Second):
		t.Fatal("flow did not stop after server connected")
	}
	if len(env.ci.ch) != 0 {
		t.Error("connected must not send any CI frame")
	}

	// A later abort must not double-close the run's cancel channel.
	env.flow.Abort()
}

func TestFlow_ConnectedUnknownServerIsNoop(t *testing.T) {
	env := newTestFlow()
	env.flow.Connected("nonexistent") // must not panic
	env.flow.Abort()
}

func TestFlow_TwoServersRunConcurrently(t *testing.T) {
	env := newTestFlow()
	env.flow.startServer = env.fake.start(platform.CallbackResult{Code: "code-v"})

	env.flow.Start("vercel", "https://v.example/authorize")
	env.flow.Start("github", "https://g.example/authorize")

	var got strings.Builder
	for i := 0; i < 2; i++ {
		select {
		case cmd := <-env.ci.ch:
			got.WriteString(cmd)
			got.WriteString("\n")
		case <-time.After(2 * time.Second):
			t.Fatal("expected 2 commands (one per server)")
		}
	}
	if !strings.Contains(got.String(), "mcp_confirm vercel code-v http://127.0.0.1:4242/callback") {
		t.Errorf("commands = %q, want vercel confirm", got.String())
	}
	if !strings.Contains(got.String(), "mcp_confirm github code-v http://127.0.0.1:4242/callback") {
		t.Errorf("commands = %q, want github confirm", got.String())
	}
}

func TestFlow_IgnoresDuplicateStartForSameServer(t *testing.T) {
	env := newTestFlow()
	env.flow.startServer = env.fake.start(platform.CallbackResult{Code: "code"})

	env.flow.Start("github", "https://g.example/authorize")
	env.flow.Start("github", "https://g.example/authorize2") // duplicate — ignored

	got := <-env.ci.ch
	if got != "mcp_confirm github code http://127.0.0.1:4242/callback" {
		t.Errorf("command = %q, want github confirm", got)
	}
	<-env.fake.cleanupCh

	// Give a wrongly-spawned duplicate a chance to send.
	time.Sleep(20 * time.Millisecond)
	if len(env.ci.ch) != 0 {
		t.Error("duplicate start must not spawn a second flow")
	}
}

// TestFlow_SendsConfirmWithIss verifies that the RFC 9207 iss parameter
// from the callback is forwarded as the 4th :mcp_confirm argument.
func TestFlow_SendsConfirmWithIss(t *testing.T) {
	env := newTestFlow()
	env.flow.startServer = env.fake.start(platform.CallbackResult{
		Code: "auth-code-123",
		Iss:  "https://auth.example.com",
	})

	env.flow.Start("github", "https://example.com/authorize")

	got := <-env.ci.ch
	want := "mcp_confirm github auth-code-123 http://127.0.0.1:4242/callback https://auth.example.com"
	if got != want {
		t.Errorf("command = %q, want %q", got, want)
	}
	<-env.fake.cleanupCh
}

// TestFlow_SendsConfirmWithoutIss verifies that no trailing iss argument is
// appended when the callback carried none.
func TestFlow_SendsConfirmWithoutIss(t *testing.T) {
	env := newTestFlow()
	env.flow.startServer = env.fake.start(platform.CallbackResult{Code: "auth-code-123"})

	env.flow.Start("github", "https://example.com/authorize")

	got := <-env.ci.ch
	want := "mcp_confirm github auth-code-123 http://127.0.0.1:4242/callback"
	if got != want {
		t.Errorf("command = %q, want %q", got, want)
	}
	<-env.fake.cleanupCh
}

// With the manual policy a browser-open failure must NOT abandon the
// server: the flow keeps waiting and still submits a code if the callback
// arrives (the interactive user may have opened the URL by hand).
func TestFlow_WaitManualOnBrowserOpenFailureStillWaitsForCallback(t *testing.T) {
	env := newTestFlow()
	resultCh := make(chan platform.CallbackResult, 1)
	env.flow.startServer = func(_, _, _ string) (<-chan platform.CallbackResult, string, func()) {
		return resultCh, "http://127.0.0.1:4242/callback", func() {}
	}
	env.flow.openURL = func(string) error { return errors.New("no DISPLAY") }
	env.flow.SetUnresolved(func(server, redirectURI, reason string) Action { return WaitManual })
	env.flow.timeout = 30 * time.Second

	env.flow.Start("github", "https://example.com/authorize")

	resultCh <- platform.CallbackResult{Code: "code-1"}

	select {
	case got := <-env.ci.ch:
		if got != "mcp_confirm github code-1 http://127.0.0.1:4242/callback" {
			t.Errorf("command = %q, want confirm after waiting past the open failure", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("flow did not submit the code after a browser-open failure with the manual policy")
	}
}
