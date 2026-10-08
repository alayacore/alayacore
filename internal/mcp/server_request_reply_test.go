package mcp

import (
	"context"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

// replyServer answers every POST with the given status and an empty body — the
// shape of the transport's HTTP response to a server-to-client response.
func replyServer(t *testing.T, status int) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(status)
	})}
	go func() { _ = srv.Serve(listener) }()
	t.Cleanup(func() { _ = srv.Close() })
	return "http://" + listener.Addr().String()
}

// An HTTP adapter's reply to the server used to throw its result away: neither a
// failed POST nor a status that is not 2xx was visible anywhere, so a ping the
// server never received looked exactly like one it did. 202 is what the spec
// asks for; any other 2xx still means the server took the reply, so only the
// rest is reported.
func TestHTTPAdapterReplyReportsWhatTheServerAnswered(t *testing.T) {
	for _, tc := range []struct {
		status  int
		wantErr bool
	}{
		{http.StatusAccepted, false},
		{http.StatusNoContent, false},
		{http.StatusBadRequest, true},
		{http.StatusInternalServerError, true},
	} {
		t.Run(strconv.Itoa(tc.status), func(t *testing.T) {
			adapter := NewAdapterV20250618()
			adapter.endpointURL = replyServer(t, tc.status)
			adapter.httpClient = &http.Client{}

			err := adapter.ServerRequestHandler(context.Background(), requestID("srv-1"), methodPing)

			if tc.wantErr {
				if err == nil {
					t.Fatalf("status %d: err = nil, want the reply reported as unaccepted", tc.status)
				}
				if !strings.Contains(err.Error(), strconv.Itoa(tc.status)) {
					t.Errorf("err = %v, want the status in it", err)
				}
				return
			}
			if err != nil {
				t.Errorf("status %d: err = %v, want nil (any 2xx means the reply landed)", tc.status, err)
			}
		})
	}
}

// A reply that cannot be sent at all is reported with the transport's own error.
func TestHTTPAdapterReplyReportsATransportFailure(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	_ = listener.Close() // nothing is listening now

	adapter := NewAdapterV20250618()
	adapter.endpointURL = "http://" + addr
	adapter.httpClient = &http.Client{}

	if err := adapter.ServerRequestHandler(context.Background(), requestID("srv-1"), methodPing); err == nil {
		t.Error("err = nil, want the connection failure reported")
	}
}
