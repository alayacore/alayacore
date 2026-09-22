package plainio

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/app"
	"github.com/alayacore/alayacore/internal/mcpauth"
	"github.com/alayacore/alayacore/internal/protocol"
	"github.com/alayacore/alayacore/internal/tlv"
)

// encodeTestTLV encodes a TLV frame for test input. Test payloads are
// tiny and never exceed tlv.MaxMessageSize, so the encode error is ignored.
func encodeTestTLV(tag, value string) []byte {
	msg, _ := tlv.EncodeTLV(tag, value)
	return msg
}

func TestNewlineBetweenDifferentStreamGroups(t *testing.T) {
	var buf bytes.Buffer
	o := &stdoutOutput{
		writer: &buf,
	}

	// Simulate: assistant text delta with NUL-delimited history IDs
	msg1 := encodeTestTLV(tlv.TagAssistantT, tlv.WrapID("1", "hello "))
	msg2 := encodeTestTLV(tlv.TagAssistantT, tlv.WrapID("1", "world"))
	// New step: different history ID
	msg3 := encodeTestTLV(tlv.TagAssistantT, tlv.WrapID("2", "new step"))

	o.Write(msg1)
	o.Write(msg2)
	o.Write(msg3)

	got := buf.String()
	want := "hello world\nnew step"
	if got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}

func TestDeltaCompleteFrameSkipped(t *testing.T) {
	var buf bytes.Buffer
	o := &stdoutOutput{
		writer:    &buf,
		seenDelta: make(map[string]bool),
	}

	// Delta mode: At carries the content, AT is an empty terminator. The
	// terminator must be skipped — processing it would print a spurious
	// separator (delta tag "At" vs complete tag "AT") and an empty line.
	o.Write(encodeTestTLV(tlv.TagAssistantTDelta, tlv.WrapID("1", "hello")))
	o.Write(encodeTestTLV(tlv.TagAssistantT, tlv.WrapID("1", "")))

	if got := buf.String(); got != "hello" {
		t.Errorf("output = %q, want %q (empty AT terminator must not add a separator)", got, "hello")
	}
}

func TestSeenDeltaBounded(t *testing.T) {
	var buf bytes.Buffer
	o := &stdoutOutput{
		writer:    &buf,
		seenDelta: make(map[string]bool),
	}

	// Stream more distinct delta IDs than the cap: the map must stay
	// bounded and the oldest (smallest) IDs must be evicted.
	total := maxSeenDeltaEntries + 50
	for i := 0; i < total; i++ {
		o.Write(encodeTestTLV(tlv.TagAssistantTDelta, tlv.WrapID(strconv.Itoa(i), "x")))
	}

	if len(o.seenDelta) > maxSeenDeltaEntries {
		t.Fatalf("seenDelta grew to %d entries, cap is %d", len(o.seenDelta), maxSeenDeltaEntries)
	}
	if _, ok := o.seenDelta["0"]; ok {
		t.Error("oldest delta ID was not evicted")
	}
	if _, ok := o.seenDelta[strconv.Itoa(total-1)]; !ok {
		t.Error("newest delta ID should be retained")
	}

	// The complete-frame skip must still work for a retained ID.
	buf.Reset()
	o.Write(encodeTestTLV(tlv.TagAssistantT, tlv.WrapID(strconv.Itoa(total-1), "")))
	if got := buf.String(); got != "" {
		t.Errorf("output = %q, want empty (retained ID terminator must be skipped)", got)
	}
}

func TestCommandOut_ErrorRenders(t *testing.T) {
	var buf bytes.Buffer
	o := &stdoutOutput{
		writer: &buf,
	}

	payload, _ := json.Marshal(protocol.CmdResultMsg{
		ID:      "x1",
		IsError: true,
		Output:  json.RawMessage(`{"code":"MODEL_NOT_FOUND","message":"model_set: model not found: 99"}`),
	})
	o.Write(encodeTestTLV(tlv.TagCommandOut, string(payload)))

	got := buf.String()
	if !strings.Contains(got, "model_set: model not found: 99") {
		t.Errorf("output = %q, want error message", got)
	}
}

func TestCommandOut_NoNameMappingSilent(t *testing.T) {
	var buf bytes.Buffer
	o := &stdoutOutput{
		writer: &buf,
	}

	payload, _ := json.Marshal(protocol.CmdResultMsg{
		ID:     "x2",
		Output: json.RawMessage(`{"path":"/tmp/x.alaya"}`),
	})
	o.Write(encodeTestTLV(tlv.TagCommandOut, string(payload)))

	got := buf.String()
	if got != "" {
		t.Errorf("output = %q, want empty (no generic confirmation)", got)
	}
}

func TestCommandOut_SuccessRendersByName(t *testing.T) {
	var buf bytes.Buffer
	o := &stdoutOutput{
		writer: &buf,
	}
	commandNames.Store("p1", "save")
	defer commandNames.Delete("p1")

	payload, _ := json.Marshal(protocol.CmdResultMsg{
		ID:     "p1",
		Output: json.RawMessage(`{"path":"/tmp/x.alaya"}`),
	})
	o.Write(encodeTestTLV(tlv.TagCommandOut, string(payload)))

	got := buf.String()
	if !strings.Contains(got, "Session saved to /tmp/x.alaya") {
		t.Errorf("output = %q, want rendered save result", got)
	}
	if strings.Contains(got, "Command completed") {
		t.Errorf("generic confirmation should not appear for a known command, got %q", got)
	}
	if _, ok := commandNames.Load("p1"); ok {
		t.Error("command name mapping should be consumed after the CO arrives")
	}
}

func TestCommandOut_MalformedIgnored(t *testing.T) {
	var buf bytes.Buffer
	o := &stdoutOutput{
		writer: &buf,
	}

	o.Write(encodeTestTLV(tlv.TagCommandOut, "{not json"))

	if buf.Len() != 0 {
		t.Errorf("malformed CO should be ignored, got %q", buf.String())
	}
}

func TestNewlineBetweenTextAndReasoning(t *testing.T) {
	var buf bytes.Buffer
	o := &stdoutOutput{
		writer: &buf,
	}

	msg1 := encodeTestTLV(tlv.TagAssistantT, tlv.WrapID("1", "some text"))
	msg2 := encodeTestTLV(tlv.TagAssistantR, tlv.WrapID("2", "some reasoning"))

	o.Write(msg1)
	o.Write(msg2)

	got := buf.String()
	want := "some text\nsome reasoning"
	if got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}

func TestNewlineBetweenReasoningAndText(t *testing.T) {
	var buf bytes.Buffer
	o := &stdoutOutput{
		writer: &buf,
	}

	msg1 := encodeTestTLV(tlv.TagAssistantR, tlv.WrapID("1", "thinking..."))
	msg2 := encodeTestTLV(tlv.TagAssistantT, tlv.WrapID("2", "answer"))

	o.Write(msg1)
	o.Write(msg2)

	got := buf.String()
	want := "thinking...\nanswer"
	if got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}

func TestNoPrefixNoNewline(t *testing.T) {
	var buf bytes.Buffer
	o := &stdoutOutput{
		writer: &buf,
	}

	// Messages without stream prefixes are malformed for stdout AT frames
	// (history ID is always required). They should be silently ignored.
	msg1 := encodeTestTLV(tlv.TagAssistantT, "hello ")
	msg2 := encodeTestTLV(tlv.TagAssistantT, "world")

	o.Write(msg1)
	o.Write(msg2)

	got := buf.String()
	want := ""
	if got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}

func TestToolCallResetsStreamPrefix(t *testing.T) {
	var buf bytes.Buffer
	o := &stdoutOutput{
		writer: &buf,
	}

	// Stream some text
	msg1 := encodeTestTLV(tlv.TagAssistantT, tlv.WrapID("1", "hello"))
	// Then a tool call (resets prefix)
	msg2 := encodeTestTLV(tlv.TagAssistantF, `{"id":"1","type":"call","name":"read_file","input":"{}"}`)
	// Then more text with different prefix — should NOT get extra newline since tool call reset it
	msg3 := encodeTestTLV(tlv.TagAssistantT, tlv.WrapID("2", "result"))

	o.Write(msg1)
	o.Write(msg2)
	o.Write(msg3)

	got := buf.String()
	// After tool call, lastHistoryID is "" so the new ID doesn't trigger separator
	if !contains(got, "hello") || !contains(got, "result") {
		t.Errorf("output = %q", got)
	}
}

func TestUserPromptResetsStreamPrefix(t *testing.T) {
	var buf bytes.Buffer
	o := &stdoutOutput{
		writer: &buf,
	}

	msg1 := encodeTestTLV(tlv.TagAssistantT, tlv.WrapID("1", "response"))
	// Echoed user prompts carry a NUL-delimited history ID (the agent
	// assigns one before echoing back).
	msg2 := encodeTestTLV(tlv.TagUserT, tlv.WrapID("9", "next prompt"))
	msg3 := encodeTestTLV(tlv.TagAssistantT, tlv.WrapID("2", "new response"))

	o.Write(msg1)
	o.Write(msg2)
	o.Write(msg3)

	// The User block is its own line with a blank line after it; the
	// prompt resets the stream prefix so the next assistant message gets
	// no extra separator.
	want := "response\nUser: next prompt\n\nnew response"
	if got := buf.String(); got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}

func TestUserPromptBlockFormat(t *testing.T) {
	var buf bytes.Buffer
	o := &stdoutOutput{
		writer: &buf,
	}

	// Tool result ends with a newline → the User block gets a blank line
	// before it.
	o.Write(encodeTestTLV(tlv.TagUserF, tlv.WrapID("3", `{"id":"c1","output":[],"is_error":false}`)))
	o.Write(encodeTestTLV(tlv.TagUserT, tlv.WrapID("1", "hello world")))
	o.Write(encodeTestTLV(tlv.TagAssistantT, tlv.WrapID("2", "answer")))

	want := "{\"id\":\"c1\",\"output\":[],\"is_error\":false}\n\nUser: hello world\n\nanswer"
	if got := buf.String(); got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}

// contains checks if s contains substr.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// mcpSMTLV encodes an "mcp" system message as a TagSystemMsg TLV frame.
func mcpSMTLV(status, server, url, errMsg string) []byte {
	env := protocol.SystemMsgEnvelope{
		Type: "mcp",
		Data: json.RawMessage(fmt.Sprintf(`{"status":%q,"server":%q,"url":%q,"error":%q}`,
			status, server, url, errMsg)),
	}
	v, err := json.Marshal(env)
	if err != nil {
		panic(err)
	}
	return encodeTestTLV(tlv.TagSystemMsg, string(v))
}

func TestSystemMsg_MCPStatusRendering(t *testing.T) {
	var buf bytes.Buffer
	o := &stdoutOutput{writer: &buf}

	cases := []struct {
		name   string
		status string
		want   string
	}{
		{"connecting", "connecting", `[mcp: connecting "db"]`},
		{"connected", "connected", `[mcp: connected "db"]`},
		{"failed", "failed", `[mcp: failed "db": connection timeout]`},
		{"auth_running", "auth_running", `[mcp: waiting for authorization for "db"…]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			buf.Reset()
			o.Write(mcpSMTLV(tc.status, "db", "", "connection timeout"))
			if got := buf.String(); !strings.Contains(got, tc.want) {
				t.Errorf("output = %q, want substring %q", got, tc.want)
			}
		})
	}

	// "done" renders nothing — completion is announced via the session's
	// notify ("MCP servers initialized: ...").
	buf.Reset()
	o.Write(mcpSMTLV("done", "", "", ""))
	if got := buf.String(); got != "" {
		t.Errorf("done output = %q, want empty", got)
	}
}

func TestSystemMsg_MCPAuthRequiredInvokesHook(t *testing.T) {
	var buf bytes.Buffer
	var gotServer, gotURL string
	o := &stdoutOutput{
		writer: &buf,
		mcpAuthRequired: func(server, url string) {
			gotServer, gotURL = server, url
		},
	}

	o.Write(mcpSMTLV("auth_required", "github", "https://example.com/authorize", ""))

	if got := buf.String(); !strings.Contains(got, `[mcp: server "github" requires authorization]`) {
		t.Errorf("output = %q, want auth_required status line", got)
	}
	if gotServer != "github" || gotURL != "https://example.com/authorize" {
		t.Errorf("hook = (%q, %q), want (github, https://example.com/authorize)", gotServer, gotURL)
	}
}

func TestSystemMsg_MCPDoneInvokesHook(t *testing.T) {
	var buf bytes.Buffer
	called := false
	o := &stdoutOutput{
		writer:    &buf,
		onMCPDone: func() { called = true },
	}
	o.Write(mcpSMTLV("done", "", "", ""))
	if !called {
		t.Error("onMCPDone hook not invoked on done")
	}
}

func TestSystemMsg_MCPConnectedInvokesHook(t *testing.T) {
	var buf bytes.Buffer
	var gotServer string
	o := &stdoutOutput{
		writer: &buf,
		onMCPConnected: func(server string) {
			gotServer = server
		},
	}
	o.Write(mcpSMTLV("connected", "github", "", ""))
	if gotServer != "github" {
		t.Errorf("onMCPConnected = %q, want github", gotServer)
	}
}

// TestSystemMsg_MCPHookCanPrint verifies the deferred-hook contract:
// hooks run AFTER Write releases the output lock, so a hook may safely
// call printLine (which re-acquires o.mu) without deadlocking. Under the
// old lock-inline implementation this test hangs.
func TestSystemMsg_MCPHookCanPrint(t *testing.T) {
	var buf bytes.Buffer
	o := &stdoutOutput{writer: &buf}
	o.mcpAuthRequired = func(server, url string) {
		o.printLine("[hook printed for %s]\n", server)
	}
	o.Write(mcpSMTLV("auth_required", "github", "https://example.com", ""))
	if !strings.Contains(buf.String(), "[hook printed for github]") {
		t.Errorf("output = %q, want hook print inside deferred hook", buf.String())
	}
}

// Session lifecycle frames are not user content: the adapter renders nothing
// for them. It does not gate prompts on them any more — the session holds a
// prompt that arrives before it is ready — so "ready" carries no meaning here;
// "closed" is the one it acts on, and it acts by marking the latch its Start
// waits on (see Closed()).
func TestSystemMsg_SessionFramesDriveTheLatch(t *testing.T) {
	var buf bytes.Buffer
	o := &stdoutOutput{writer: &buf, seenDelta: make(map[string]bool), closed: app.NewLatch()}

	o.Write(encodeTestTLV(tlv.TagSystemMsg, `{"type":"session","data":{"state":"ready"}}`))
	select {
	case <-o.Closed():
		t.Fatal("the ready frame must not report the session as over")
	default:
	}

	o.Write(encodeTestTLV(tlv.TagSystemMsg, `{"type":"session","data":{"state":"closed"}}`))
	select {
	case <-o.Closed():
	default:
		t.Fatal("the terminal frame should mark Closed()")
	}

	// Idempotent: a second terminal frame must not panic (close of a closed
	// channel would).
	o.Write(encodeTestTLV(tlv.TagSystemMsg, `{"type":"session","data":{"state":"closed"}}`))

	if got := buf.String(); got != "" {
		t.Errorf("session frames produced output %q, want none", got)
	}
}

// TestOutput_PrintManualFallback pins the manual-fallback format: the
// confirm command must be bare (selectable/copyable) on its own line, with
// the real redirect URI spelled out and no placeholder.
func TestOutput_PrintManualFallback(t *testing.T) {
	var buf bytes.Buffer
	o := &stdoutOutput{writer: &buf}
	o.printManualFallback("github", "http://127.0.0.1:4242/callback", `authorization for "github" timed out`)

	text := buf.String()
	if !strings.Contains(text, `timed out`) {
		t.Errorf("output = %q, want the reason line", text)
	}
	wantCmd := ":mcp_confirm github <code> http://127.0.0.1:4242/callback"
	if !strings.Contains(text, "\n"+wantCmd+"\n") {
		t.Errorf("output = %q, want the confirm command bare on its own line %q", text, wantCmd)
	}
	if !strings.Contains(text, "[mcp: to skip this server: :mcp_decline github]") {
		t.Errorf("output = %q, want the decline option", text)
	}
	if strings.Contains(text, "<redirect_uri>") {
		t.Errorf("output = %q, must not leave the redirect URI as a placeholder", text)
	}
}

// TestUnresolvedPolicy pins the two branches: a terminal keeps the manual
// path (WaitManual, instructions printed); a pipe declines so MCP init
// settles (there is nowhere to type a code).
func TestUnresolvedPolicy(t *testing.T) {
	var buf bytes.Buffer
	out := &stdoutOutput{writer: &buf}

	if got := unresolvedPolicy(out, true)("github", "http://127.0.0.1:4242/callback", "timed out"); got != mcpauth.WaitManual {
		t.Errorf("interactive policy = %v, want WaitManual", got)
	}
	if !strings.Contains(buf.String(), ":mcp_confirm github <code> http://127.0.0.1:4242/callback") {
		t.Errorf("interactive output = %q, want manual instructions", buf.String())
	}

	buf.Reset()
	if got := unresolvedPolicy(out, false)("github", "http://127.0.0.1:4242/callback", "timed out"); got != mcpauth.Decline {
		t.Errorf("non-interactive policy = %v, want Decline", got)
	}
	if !strings.Contains(buf.String(), "declining") || strings.Contains(buf.String(), ":mcp_confirm") {
		t.Errorf("non-interactive output = %q, want a decline notice and no manual command", buf.String())
	}
}
