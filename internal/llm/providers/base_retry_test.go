package providers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alayacore/alayacore/internal/llm"
)

func TestParseRetryAfter(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want time.Duration
	}{
		{"empty", "", 0},
		{"seconds", "5", 5 * time.Second},
		{"with spaces", "  12 ", 12 * time.Second},
		{"zero", "0", 0},
		{"negative", "-3", 0},
		{"garbage", "soon", 0},
		{"http-date in the past", time.Now().Add(-time.Hour).UTC().Format(http.TimeFormat), 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseRetryAfter(tc.in); got != tc.want {
				t.Errorf("parseRetryAfter(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}

	t.Run("http-date in the future", func(t *testing.T) {
		in := time.Now().Add(10 * time.Second).UTC().Format(http.TimeFormat)
		if got := parseRetryAfter(in); got <= 0 || got > 10*time.Second {
			t.Errorf("parseRetryAfter(%q) = %v, want a positive duration <= 10s", in, got)
		}
	})
}

// A non-200 response must arrive as a typed *llm.APIError carrying the status
// and the Retry-After hint, so the agent can decide structurally whether to
// re-send.
func TestDoRequestReturnsTypedAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "7")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"slow down"}}`))
	}))
	defer srv.Close()

	p, err := NewOpenAI(BaseConfig{APIKey: "k", BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}

	_, err = p.StreamMessages(context.Background(),
		[]llm.ContentPart{&llm.TextPart{
			Text:            "hi",
			ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleUser},
		}}, nil, "", "")

	var apiErr *llm.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %v, want *llm.APIError", err)
	}
	if apiErr.StatusCode != http.StatusTooManyRequests {
		t.Errorf("StatusCode = %d, want %d", apiErr.StatusCode, http.StatusTooManyRequests)
	}
	if apiErr.RetryAfter != 7*time.Second {
		t.Errorf("RetryAfter = %v, want 7s", apiErr.RetryAfter)
	}
	if !strings.Contains(apiErr.Body, "slow down") {
		t.Errorf("Body = %q, want it to contain the server message", apiErr.Body)
	}
}
