//go:build ignore

// gen_tlv_samples is a ONE-OFF tool that rewrites the adapter-guide sample
// frames from the v13 wire to the v14 wire (content frames become JSON content
// items; the NUL history-id prefix becomes a top-level "id"; the tool call id
// becomes "call_id"). It is kept for provenance — it documents exactly how the
// committed samples were transformed — and is not part of the build.
//
// Usage: go run ./misc/gen_tlv_samples.go
package main

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/alayacore/alayacore/internal/protocol"
	"github.com/alayacore/alayacore/internal/tlv"
)

const dir = "adapter-guide/tlv-samples"

// sampleTime is the fixed timestamp written onto authoritative frames. The
// samples are documentation, so a constant is clearer than a clock.
const sampleTime = "2026-10-14T10:00:00Z"

// renamed handles the samples whose new tag prefix would collide with another's.
var renamed = map[string]string{
	"AR-complete.bin": "AX-reasoning.bin",
	"AT-complete.bin": "AX-complete.bin",
}

func main() {
	entries, err := os.ReadDir(dir)
	if err != nil {
		panic(err)
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".bin") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			panic(err)
		}
		tag := string(raw[0:2])
		length := int(binary.BigEndian.Uint32(raw[2:6]))
		value := string(raw[6 : 6+length])

		id := ""
		if strings.HasPrefix(value, "\x00") {
			rest := value[1:]
			if i := strings.IndexByte(rest, 0); i >= 0 {
				id, value = rest[:i], rest[i+1:]
			}
		}

		newTag, newValue, newName, err := transform(tag, value, id, e.Name())
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", e.Name(), err)
			os.Exit(1)
		}
		encoded, err := tlv.EncodeTLV(newTag, newValue)
		if err != nil {
			panic(err)
		}
		if err := os.WriteFile(filepath.Join(dir, newName), encoded, 0o644); err != nil {
			panic(err)
		}
		if newName != e.Name() {
			if err := os.Remove(filepath.Join(dir, e.Name())); err != nil {
				panic(err)
			}
		}
		fmt.Printf("%-34s -> %-30s %s\n", e.Name(), newName, newValue)
	}
}

func transform(tag, value, id, name string) (string, string, string, error) {
	switch tag {
	case "UT", "UI", "UV", "UA", "UD", "AT", "AR", "At", "Ar":
		return transformContent(tag, value, id, name)
	case "AF":
		return transformToolInput(value, id, name)
	case "UF":
		return transformToolOutput(value, id, name)
	case "Af":
		return transformToolInputDelta(value, id, name)
	case "Uf":
		return transformToolOutputDelta(value, id, name)
	case "CI":
		return transformCmdIn(value, name)
	case "CO":
		return transformCmdOut(value, name)
	case "SM":
		return transformSystem(value, name)
	}
	return tag, value, name, nil
}

func contentKind(tag string) string {
	switch tag {
	case "UI":
		return protocol.TypeImage
	case "UV":
		return protocol.TypeVideo
	case "UA":
		return protocol.TypeAudio
	case "UD":
		return protocol.TypeDocument
	case "AR", "Ar":
		return protocol.TypeReasoning
	}
	return protocol.TypeText
}

func transformContent(tag, value, id, name string) (string, string, string, error) {
	kind := contentKind(tag)
	isDelta := tag == "At" || tag == "Ar"

	newTag := tlv.TagUserX
	switch tag {
	case "AT", "AR":
		newTag = tlv.TagAssistantX
	case "At", "Ar":
		newTag = tlv.TagAssistantXDelta
	}

	meta := protocol.ContentMeta{ID: id}
	if id != "" && !isDelta {
		meta.Time = sampleTime
	}
	item := protocol.ContentItem{Type: kind}
	if kind == protocol.TypeText || kind == protocol.TypeReasoning {
		item.Text = value
	} else {
		item.URI = value
	}
	body, err := json.Marshal(protocol.ContentData{ContentMeta: meta, ContentItem: item})
	if err != nil {
		return "", "", "", err
	}
	return newTag, string(body), renameTo(newTag, name), nil
}

// parseID reads the old {"id":...} field into a scratch struct.
type oldToolInput struct {
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

type oldToolOutput struct {
	ID      string                 `json:"id"`
	Output  []protocol.ContentItem `json:"output"`
	IsError bool                   `json:"is_error"`
}

func transformToolInput(value, id, name string) (string, string, string, error) {
	var old oldToolInput
	if err := json.Unmarshal([]byte(value), &old); err != nil {
		return "", "", "", err
	}
	body, err := json.Marshal(protocol.ToolInputData{
		CallID: old.ID, Name: old.Name, Input: old.Input,
		ContentMeta: protocol.ContentMeta{ID: id, Time: sampleTime},
	})
	if err != nil {
		return "", "", "", err
	}
	return tlv.TagAssistantF, string(body), name, nil
}

func transformToolOutput(value, id, name string) (string, string, string, error) {
	var old oldToolOutput
	if err := json.Unmarshal([]byte(value), &old); err != nil {
		return "", "", "", err
	}
	body, err := json.Marshal(protocol.ToolOutputData{
		CallID: old.ID, Output: old.Output, IsError: old.IsError,
		ContentMeta: protocol.ContentMeta{ID: id, Time: sampleTime},
	})
	if err != nil {
		return "", "", "", err
	}
	return tlv.TagUserF, string(body), name, nil
}

func transformToolInputDelta(value, id, name string) (string, string, string, error) {
	var old struct {
		ID    string `json:"id"`
		Delta string `json:"delta"`
	}
	if err := json.Unmarshal([]byte(value), &old); err != nil {
		return "", "", "", err
	}
	body, err := json.Marshal(protocol.ToolInputDeltaData{
		CallID: old.ID, Delta: old.Delta,
		ContentMeta: protocol.ContentMeta{ID: id},
	})
	if err != nil {
		return "", "", "", err
	}
	return tlv.TagAssistantFDelta, string(body), name, nil
}

func transformToolOutputDelta(value, id, name string) (string, string, string, error) {
	var old struct {
		ID   string `json:"id"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal([]byte(value), &old); err != nil {
		return "", "", "", err
	}
	body, err := json.Marshal(protocol.ToolOutputDeltaData{
		CallID: old.ID, Text: old.Text,
		ContentMeta: protocol.ContentMeta{ID: id},
	})
	if err != nil {
		return "", "", "", err
	}
	return tlv.TagUserFDelta, string(body), name, nil
}

func transformCmdIn(value, name string) (string, string, string, error) {
	var old struct {
		ID    string `json:"id"`
		Name  string `json:"name"`
		Input string `json:"input"`
	}
	if err := json.Unmarshal([]byte(value), &old); err != nil {
		return "", "", "", err
	}
	body, err := json.Marshal(protocol.CmdMsg{CallID: old.ID, Name: old.Name, Input: old.Input})
	if err != nil {
		return "", "", "", err
	}
	return tlv.TagCommandIn, string(body), name, nil
}

func transformCmdOut(value, name string) (string, string, string, error) {
	var old struct {
		ID      string          `json:"id"`
		Output  json.RawMessage `json:"output"`
		IsError bool            `json:"is_error"`
	}
	if err := json.Unmarshal([]byte(value), &old); err != nil {
		return "", "", "", err
	}
	body, err := json.Marshal(protocol.CmdResultMsg{CallID: old.ID, Output: old.Output, IsError: old.IsError})
	if err != nil {
		return "", "", "", err
	}
	return tlv.TagCommandOut, string(body), name, nil
}

func transformSystem(value, name string) (string, string, string, error) {
	env, err := protocol.ParseSystemMsg(value)
	if err != nil {
		return "", "", "", err
	}
	switch protocol.SystemMsgType(env.Type) {
	case protocol.MsgTypeToolConfirm:
		var m struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(env.Data, &m); err != nil {
			return "", "", "", err
		}
		return writeSystem(protocol.ToolConfirmMsg{CallID: m.ID}, name)
	case protocol.MsgTypeVersion:
		var m struct {
			MessageVersion int    `json:"message_version"`
			CoreVersion    string `json:"core_version"`
		}
		if err := json.Unmarshal(env.Data, &m); err != nil {
			return "", "", "", err
		}
		m.MessageVersion = 14
		return writeSystem(m, name)
	}
	return tlv.TagSystemMsg, value, name, nil
}

func writeSystem(payload any, name string) (string, string, string, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return "", "", "", err
	}
	env, err := json.Marshal(protocol.SystemMsgEnvelope{Type: systemType(payload), Data: data})
	if err != nil {
		return "", "", "", err
	}
	return tlv.TagSystemMsg, string(env), name, nil
}

func systemType(payload any) string {
	switch payload.(type) {
	case protocol.ToolConfirmMsg:
		return string(protocol.MsgTypeToolConfirm)
	}
	return "version"
}

func renameTo(newTag, name string) string {
	if r, ok := renamed[name]; ok {
		return r
	}
	if i := strings.IndexByte(name, '-'); i >= 0 {
		return newTag + name[i:]
	}
	return newTag + ".bin"
}
