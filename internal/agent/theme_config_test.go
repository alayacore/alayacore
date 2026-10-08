package agent

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/protocol"
	"github.com/alayacore/alayacore/internal/tlv"
)

// brokenThemeFolder returns a themes folder holding one readable theme and one
// entry that cannot be read — a symlink to a file that is not there, which
// fails for every uid, so the test cannot pass vacuously under root the way a
// chmod-0 folder would.
func brokenThemeFolder(t *testing.T) string {
	t.Helper()
	folder := t.TempDir()
	if err := os.WriteFile(filepath.Join(folder, "good.conf"), []byte("primary: #ff0000\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(folder, "gone.conf"), filepath.Join(folder, "broken.conf")); err != nil {
		t.Fatal(err)
	}
	return folder
}

// systemMsgFrames returns the Data of every SM frame of the given type in the
// captured output, in write order.
func systemMsgFrames(t *testing.T, output string, want string) []json.RawMessage {
	t.Helper()
	var frames []json.RawMessage
	r := bytes.NewReader([]byte(output))
	for {
		tag, value, err := tlv.ReadTLV(r)
		if err != nil {
			break
		}
		if tag != tlv.TagSystemMsg {
			continue
		}
		env, err := protocol.ParseSystemMsg(value)
		if err != nil || env.Type != want {
			continue
		}
		frames = append(frames, env.Data)
	}
	return frames
}

// A theme file that cannot be read is named instead of silently vanishing from
// the list: the adapter offers what it was sent, so a missing theme used to be
// indistinguishable from a theme file that is not there.
func TestThemeListReportsUnreadableThemeFile(t *testing.T) {
	output := &syncOutput{}
	s := &Session{
		sessionConfig: sessionConfig{
			SessionConfig: SessionConfig{
				Output:       output,
				ThemesFolder: brokenThemeFolder(t),
			},
		},
	}

	s.sendThemeListMsg()

	errFrames := systemMsgFrames(t, output.String(), string(protocol.MsgTypeError))
	if len(errFrames) != 1 {
		t.Fatalf("error frames = %d, want one for the unreadable theme", len(errFrames))
	}
	if !strings.Contains(string(errFrames[0]), "broken.conf") {
		t.Errorf("error frame = %s, want the theme file named", errFrames[0])
	}

	lists := systemMsgFrames(t, output.String(), string(protocol.MsgTypeThemeList))
	if len(lists) != 1 {
		t.Fatalf("theme_list frames = %d, want one", len(lists))
	}
	if !strings.Contains(string(lists[0]), `"name":"good"`) {
		t.Errorf("theme_list = %s, want the readable theme offered", lists[0])
	}
	if strings.Contains(string(lists[0]), `"name":"broken"`) {
		t.Errorf("theme_list = %s, want the unreadable theme left out", lists[0])
	}
}

// A theme that cannot even be checked must not be reported as applied. The
// command used to return success, persist the name, and leave the UI on its
// previous colors — the adapter resolves a theme by name against the list it
// was sent, and an unreadable one is not in it.
func TestThemeSetRejectsUnreadableThemesFolder(t *testing.T) {
	notADirectory := filepath.Join(t.TempDir(), "themes")
	if err := os.WriteFile(notADirectory, []byte("I am a file"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := &Session{
		sessionConfig: sessionConfig{
			SessionConfig: SessionConfig{
				Output:       &syncOutput{},
				ThemesFolder: notADirectory,
			},
		},
	}
	s.modelService = newModelService(newModelManager(""), newRuntimeManager(filepath.Join(t.TempDir(), "runtime.conf")))

	_, err := s.handleThemeSet("theme-dark")
	if err == nil {
		t.Fatal("handleThemeSet succeeded for a themes folder it cannot read")
	}
	var cmdError *cmdErr
	if !errors.As(err, &cmdError) {
		t.Fatalf("error = %v, want a cmdErr", err)
	}
	if cmdError.Code != "THEME_ERROR" {
		t.Errorf("code = %q, want THEME_ERROR (message: %s)", cmdError.Code, cmdError.Message)
	}
	if got := s.modelService.runtimeMgr.getActiveTheme(); got != "" {
		t.Errorf("active theme = %q, want nothing persisted for a rejected switch", got)
	}
}
