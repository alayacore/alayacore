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
	"github.com/alayacore/alayacore/internal/wire"
	"github.com/alayacore/alayacore/internal/wiretest"
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
		tag, value, err := wiretest.NextFrame(r)
		if err != nil {
			break
		}
		if tag != wire.TagSystemMsg {
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

// A themes folder whose path glob cannot parse leaves the list unsent, and that
// used to be silent too: an unmatched bracket in the path is legal on Unix, and
// it is the user's own directory name, not a malformed theme.
func TestThemeListReportsUnparsableFolderPath(t *testing.T) {
	output := &syncOutput{}
	s := &Session{
		sessionConfig: sessionConfig{
			SessionConfig: SessionConfig{
				Output:       output,
				ThemesFolder: filepath.Join(t.TempDir(), "odd[name"),
			},
		},
	}

	s.sendThemeListMsg()

	if lists := systemMsgFrames(t, output.String(), string(protocol.MsgTypeThemeList)); len(lists) != 0 {
		t.Errorf("theme_list frames = %d, want none from a folder that cannot be listed", len(lists))
	}
	errFrames := systemMsgFrames(t, output.String(), string(protocol.MsgTypeError))
	if len(errFrames) != 1 {
		t.Fatalf("error frames = %d, want one for the unparsable path", len(errFrames))
	}
	if !strings.Contains(string(errFrames[0]), "odd[name") {
		t.Errorf("error frame = %s, want the folder named", errFrames[0])
	}
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
	if cmdError.Code != "IO_READ_FAILED" {
		t.Errorf("code = %q, want IO_READ_FAILED (message: %s)", cmdError.Code, cmdError.Message)
	}
	if got := s.modelService.runtimeMgr.getActiveTheme(); got != "" {
		t.Errorf("active theme = %q, want nothing persisted for a rejected switch", got)
	}
}

// ":theme_set <name>" trims its argument. A theme is named by its file
// (my theme.conf → "my theme"), so inner spaces are the name and surrounding
// whitespace is not: ":theme_set  my theme  " used to look for the file
// " my theme .conf" and answer NOT_FOUND for a theme that exists.
func TestThemeSetTrimsTheName(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "my theme.conf"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	s := &Session{
		sessionConfig: sessionConfig{
			SessionConfig: SessionConfig{Output: &syncOutput{}, ThemesFolder: dir},
		},
	}
	s.modelService = newModelService(newModelManager(""), newRuntimeManager(filepath.Join(t.TempDir(), "runtime.conf")))

	res, err := s.handleThemeSet("  my theme  ")
	if err != nil {
		t.Fatalf("handleThemeSet: %v", err)
	}
	if got := res.(map[string]any)["name"]; got != "my theme" {
		t.Errorf("reported name = %v, want %q", got, "my theme")
	}
	if got := s.modelService.runtimeMgr.getActiveTheme(); got != "my theme" {
		t.Errorf("persisted theme = %q, want %q", got, "my theme")
	}
}

// Whitespace is not a name: ":theme_set   " is the empty argument, refused the
// same way ":theme_set" is.
func TestThemeSetRejectsWhitespaceOnly(t *testing.T) {
	dir := t.TempDir()
	s := &Session{
		sessionConfig: sessionConfig{
			SessionConfig: SessionConfig{Output: &syncOutput{}, ThemesFolder: dir},
		},
	}
	s.modelService = newModelService(newModelManager(""), newRuntimeManager(filepath.Join(t.TempDir(), "runtime.conf")))

	_, err := s.handleThemeSet("   ")
	if err == nil {
		t.Fatal("handleThemeSet accepted a name made of spaces")
	}
	var cmdError *cmdErr
	if !errors.As(err, &cmdError) || cmdError.Code != "REQUEST_INVALID_ARGS" {
		t.Errorf("error = %v, want REQUEST_INVALID_ARGS", err)
	}
}
