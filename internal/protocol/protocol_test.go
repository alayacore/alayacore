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
			name: "with args",
			msg:  CmdMsg{CommandID: "a1b2", Name: "save", Args: "/tmp/x.alaya"},
			want: `{"command_id":"a1b2","name":"save","args":"/tmp/x.alaya"}`,
		},
		{
			name: "no args omitted",
			msg:  CmdMsg{CommandID: "c3d4", Name: "cancel"},
			want: `{"command_id":"c3d4","name":"cancel"}`,
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
	raw := `{"command_id":"e5f6","name":"model_set","args":"3"}`
	var msg CmdMsg
	if err := json.Unmarshal([]byte(raw), &msg); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	want := CmdMsg{CommandID: "e5f6", Name: "model_set", Args: "3"}
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
				CommandID: "a1b2",
				Result:    json.RawMessage(`{"path":"/tmp/x.alaya"}`),
			},
			want: `{"command_id":"a1b2","result":{"path":"/tmp/x.alaya"}}`,
		},
		{
			name: "success without result (fire-and-forget)",
			msg: CmdResultMsg{
				CommandID: "zzz",
				Result:    json.RawMessage(`null`),
			},
			want: `{"command_id":"zzz","result":null}`,
		},
		{
			name: "error with uniform error object",
			msg: CmdResultMsg{
				CommandID: "c3d4",
				Result:    json.RawMessage(`{"code":"REQUEST_MODEL_UNKNOWN","message":"model_set: model not found: 99"}`),
				IsError:   true,
			},
			want: `{"command_id":"c3d4","result":{"code":"REQUEST_MODEL_UNKNOWN","message":"model_set: model not found: 99"},"is_error":true}`,
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
	raw := `{"command_id":"e5f6","result":{"path":"./extract.alaya","count":43},"is_error":true}`
	var msg CmdResultMsg
	if err := json.Unmarshal([]byte(raw), &msg); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	if msg.CommandID != "e5f6" || !msg.IsError {
		t.Fatalf("parsed fields wrong: %+v", msg)
	}
	var errObj CmdError
	if err := json.Unmarshal(msg.Result, &errObj); err != nil {
		t.Fatalf("Result is not a CmdError: %v", err)
	}
}
