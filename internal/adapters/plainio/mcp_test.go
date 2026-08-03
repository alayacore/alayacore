package plainio

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/alayacore/alayacore/internal/platform"
)

// ciCapture collects TLV frames written by the auth flow. A channel is
// used instead of bytes.Buffer because the flow writes from a goroutine
// while the test reads from the main goroutine.
type ciCapture struct {
	ch chan string
}

func newCICapture() *ciCapture {
	return &ciCapture{ch: make(chan string, 4)}
}

func (c *ciCapture) Write(p []byte) (int, error) {
	c.ch <- string(p)
	return len(p), nil
}

// fakeCallbackServer is a stub for mcpAuthFlow.startServer that delivers
// a canned result immediately and records its cleanup on cleanupCh.
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

// testFlowEnv bundles the pieces of a test flow: the flow itself, the
// capturing output, the CI capture, and the fake callback server.
type testFlowEnv struct {
	flow *mcpAuthFlow
	out  *bytes.Buffer
	ci   *ciCapture
	fake *fakeCallbackServer
}

// newTestFlow builds a flow with a capturing output, a CI capture for the
// TLV input, and a fake callback server, all ready for flow.start.
func newTestFlow() *testFlowEnv {
	out := &bytes.Buffer{}
	ci := newCICapture()
	fake := &fakeCallbackServer{cleanupCh: make(chan struct{}, 1)}

	flow := newMCPAuthFlow(&stdoutOutput{writer: out})
	flow.openURL = func(string) error { return nil }
	flow.setInput(ci)
	return &testFlowEnv{flow: flow, out: out, ci: ci, fake: fake}
}

func TestMCPAuthFlow_SendsConfirmOnCallback(t *testing.T) {
	env := newTestFlow()
	env.flow.startServer = env.fake.start(platform.CallbackResult{Code: "auth-code-123"})

	env.flow.start("github", "https://example.com/authorize?redirect_uri={{redirect_uri}}&state={{state}}")

	frame := <-env.ci.ch
	want := `"name":"mcp_confirm","input":"github auth-code-123 http://127.0.0.1:4242/callback"`
	if !strings.Contains(frame, want) {
		t.Errorf("CI frame = %q, want substring %q", frame, want)
	}
	<-env.fake.cleanupCh

	text := env.out.String()
	if !strings.Contains(text, "https://example.com/authorize?redirect_uri=http%3A%2F%2F127.0.0.1%3A4242%2Fcallback") {
		t.Errorf("output = %q, want substituted URL", text)
	}
	if !strings.Contains(text, "manual: :mcp_confirm github <code> <redirect_uri>") {
		t.Errorf("output = %q, want manual fallback hint", text)
	}
}

func TestMCPAuthFlow_CallbackErrorDeclinesServer(t *testing.T) {
	env := newTestFlow()
	env.flow.startServer = env.fake.start(platform.CallbackResult{Err: errors.New("state mismatch")})

	env.flow.start("github", "https://example.com/authorize")

	frame := <-env.ci.ch
	if !strings.Contains(frame, `"name":"mcp_decline","input":"github"`) {
		t.Errorf("CI frame = %q, want :mcp_decline github", frame)
	}
	<-env.fake.cleanupCh

	if !strings.Contains(env.out.String(), "state mismatch") {
		t.Errorf("output = %q, want callback error text", env.out.String())
	}
}

func TestMCPAuthFlow_TimeoutPrintsHint(t *testing.T) {
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
	env.flow.authTimeout = 30 * time.Millisecond

	env.flow.start("github", "https://example.com/authorize")

	select {
	case <-env.fake.cleanupCh:
	case <-time.After(2 * time.Second):
		t.Fatal("flow did not clean up after timeout")
	}
	if !strings.Contains(env.out.String(), "timed out") {
		t.Errorf("output = %q, want timeout hint", env.out.String())
	}
}

func TestMCPAuthFlow_AbortStopsWaiting(t *testing.T) {
	env := newTestFlow()
	env.flow.startServer = func(_, _, _ string) (<-chan platform.CallbackResult, string, func()) {
		return make(chan platform.CallbackResult), "http://127.0.0.1:4242/callback", func() {
			select {
			case env.fake.cleanupCh <- struct{}{}:
			default:
			}
		}
	}

	env.flow.start("github", "https://example.com/authorize")
	env.flow.abort()

	select {
	case <-env.fake.cleanupCh:
	case <-time.After(2 * time.Second):
		t.Fatal("flow did not stop after abort")
	}
	if len(env.ci.ch) != 0 {
		t.Error("abort must not send any CI frame")
	}
}

func TestMCPAuthFlow_AbortIsIdempotent(t *testing.T) {
	env := newTestFlow()
	env.flow.abort()
	env.flow.abort() // must not panic
}

func TestMCPAuthFlow_ConnectedCancelsFlow(t *testing.T) {
	env := newTestFlow()
	env.flow.startServer = func(_, _, _ string) (<-chan platform.CallbackResult, string, func()) {
		return make(chan platform.CallbackResult), "http://127.0.0.1:4242/callback", func() {
			select {
			case env.fake.cleanupCh <- struct{}{}:
			default:
			}
		}
	}

	env.flow.start("github", "https://example.com/authorize")
	env.flow.connected("github") // authorization completed via manual :mcp_confirm

	select {
	case <-env.fake.cleanupCh:
	case <-time.After(2 * time.Second):
		t.Fatal("flow did not stop after server connected")
	}
	if len(env.ci.ch) != 0 {
		t.Error("connected must not send any CI frame")
	}

	// A later abort must not double-close the run's cancel channel.
	env.flow.abort()
}

func TestMCPAuthFlow_ConnectedUnknownServerIsNoop(t *testing.T) {
	env := newTestFlow()
	env.flow.connected("nonexistent") // must not panic
	env.flow.abort()
}

func TestMCPAuthFlow_TwoServersRunConcurrently(t *testing.T) {
	env := newTestFlow()
	env.flow.startServer = env.fake.start(platform.CallbackResult{Code: "code-v"})

	env.flow.start("vercel", "https://v.example/authorize")
	env.flow.start("github", "https://g.example/authorize")

	var got strings.Builder
	for i := 0; i < 2; i++ {
		select {
		case frame := <-env.ci.ch:
			got.WriteString(frame)
		case <-time.After(2 * time.Second):
			t.Fatal("expected 2 CI frames (one per server)")
		}
	}
	if !strings.Contains(got.String(), `"input":"vercel code-v http://127.0.0.1:4242/callback"`) {
		t.Errorf("CI frames = %q, want vercel confirm", got.String())
	}
	if !strings.Contains(got.String(), `"input":"github code-v http://127.0.0.1:4242/callback"`) {
		t.Errorf("CI frames = %q, want github confirm", got.String())
	}
}

func TestMCPAuthFlow_IgnoresDuplicateStartForSameServer(t *testing.T) {
	env := newTestFlow()
	env.flow.startServer = env.fake.start(platform.CallbackResult{Code: "code"})

	env.flow.start("github", "https://g.example/authorize")
	env.flow.start("github", "https://g.example/authorize2") // duplicate — ignored

	frame := <-env.ci.ch
	if !strings.Contains(frame, `"input":"github code http://127.0.0.1:4242/callback"`) {
		t.Errorf("CI frame = %q, want github confirm", frame)
	}
	<-env.fake.cleanupCh

	// Give a wrongly-spawned duplicate a chance to send.
	time.Sleep(20 * time.Millisecond)
	if len(env.ci.ch) != 0 {
		t.Error("duplicate start must not spawn a second flow")
	}
}

// TestMCPAuthFlow_SendsConfirmWithIss verifies that the RFC 9207 iss
// parameter from the callback is forwarded as the 4th :mcp_confirm
// argument.
func TestMCPAuthFlow_SendsConfirmWithIss(t *testing.T) {
	env := newTestFlow()
	env.flow.startServer = env.fake.start(platform.CallbackResult{
		Code: "auth-code-123",
		Iss:  "https://auth.example.com",
	})

	env.flow.start("github", "https://example.com/authorize?redirect_uri={{redirect_uri}}&state={{state}}")

	frame := <-env.ci.ch
	want := `"name":"mcp_confirm","input":"github auth-code-123 http://127.0.0.1:4242/callback https://auth.example.com"`
	if !strings.Contains(frame, want) {
		t.Errorf("CI frame = %q, want substring %q", frame, want)
	}
	<-env.fake.cleanupCh
}

// TestMCPAuthFlow_SendsConfirmWithoutIss verifies that no trailing iss
// argument is appended when the callback carried none.
func TestMCPAuthFlow_SendsConfirmWithoutIss(t *testing.T) {
	env := newTestFlow()
	env.flow.startServer = env.fake.start(platform.CallbackResult{Code: "auth-code-123"})

	env.flow.start("github", "https://example.com/authorize")

	frame := <-env.ci.ch
	want := `"name":"mcp_confirm","input":"github auth-code-123 http://127.0.0.1:4242/callback"`
	if !strings.Contains(frame, want) {
		t.Errorf("CI frame = %q, want substring %q", frame, want)
	}
	if strings.Contains(frame, "callback  ") || strings.HasSuffix(frame, "callback ") {
		t.Errorf("CI frame = %q, want no trailing iss argument", frame)
	}
	<-env.fake.cleanupCh
}
