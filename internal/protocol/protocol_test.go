package protocol

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestCmdMsgMarshal(t *testing.T) {
	tests := []struct {
		name string
		msg  CmdMsg
		want string
	}{
		{
			name: "with input",
			msg:  CmdMsg{ID: "a1b2", Name: "save", Input: "/tmp/x.alaya"},
			want: `{"id":"a1b2","name":"save","input":"/tmp/x.alaya"}`,
		},
		{
			name: "no input omitted",
			msg:  CmdMsg{ID: "c3d4", Name: "cancel"},
			want: `{"id":"c3d4","name":"cancel"}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := json.Marshal(tt.msg)
			if err != nil {
				t.Fatalf("Marshal error: %v", err)
			}
			if string(got) != tt.want {
				t.Errorf("Marshal = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestCmdMsgUnmarshal(t *testing.T) {
	raw := `{"id":"e5f6","name":"model_set","input":"3"}`
	var msg CmdMsg
	if err := json.Unmarshal([]byte(raw), &msg); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	want := CmdMsg{ID: "e5f6", Name: "model_set", Input: "3"}
	if !reflect.DeepEqual(msg, want) {
		t.Errorf("Unmarshal = %+v, want %+v", msg, want)
	}
}

func TestCmdResultMsgMarshal(t *testing.T) {
	tests := []struct {
		name string
		msg  CmdResultMsg
		want string
	}{
		{
			name: "success with result",
			msg: CmdResultMsg{
				ID:     "a1b2",
				Output: json.RawMessage(`{"path":"/tmp/x.alaya"}`),
			},
			want: `{"id":"a1b2","output":{"path":"/tmp/x.alaya"}}`,
		},
		{
			name: "success without result (fire-and-forget)",
			msg: CmdResultMsg{
				ID:     "zzz",
				Output: json.RawMessage(`null`),
			},
			want: `{"id":"zzz","output":null}`,
		},
		{
			name: "error with uniform error object",
			msg: CmdResultMsg{
				ID:      "c3d4",
				Output:  json.RawMessage(`{"code":"MODEL_NOT_FOUND","message":"model_set: model not found: 99"}`),
				IsError: true,
			},
			want: `{"id":"c3d4","output":{"code":"MODEL_NOT_FOUND","message":"model_set: model not found: 99"},"is_error":true}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := json.Marshal(tt.msg)
			if err != nil {
				t.Fatalf("Marshal error: %v", err)
			}
			if string(got) != tt.want {
				t.Errorf("Marshal = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestCmdResultMsgRoundTrip(t *testing.T) {
	raw := `{"id":"e5f6","output":{"path":"./extract.alaya","count":43},"is_error":true}`
	var msg CmdResultMsg
	if err := json.Unmarshal([]byte(raw), &msg); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	if msg.ID != "e5f6" || !msg.IsError {
		t.Fatalf("parsed fields wrong: %+v", msg)
	}
	var errObj CmdError
	if err := json.Unmarshal(msg.Output, &errObj); err != nil {
		t.Fatalf("Output is not a CmdError: %v", err)
	}
}

func TestCmdErrorMarshal(t *testing.T) {
	errObj := CmdError{Code: "UNKNOWN_COMMAND", Message: "unknown command: foo"}
	got, err := json.Marshal(errObj)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}
	want := `{"code":"UNKNOWN_COMMAND","message":"unknown command: foo"}`
	if string(got) != want {
		t.Errorf("Marshal = %s, want %s", got, want)
	}
}
