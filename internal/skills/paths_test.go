package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/config"
)

// <location> is the only address the agent has for a skill's instructions, and
// the agent opens it with read_file — which resolves a relative path against the
// process's working directory, not against anything the prompt said. Handing out
// "../../misc/skills/weather/SKILL.md" alongside a "Current working directory:
// /abs/path" line gave the model two bases to combine, and the combination is
// what it gets wrong quietly.
func TestRelativeContainerAdvertisesAnAbsoluteLocation(t *testing.T) {
	root := t.TempDir()

	manifest := filepath.Join(root, "skills", "weather", manifestFileName)
	if err := os.MkdirAll(filepath.Dir(manifest), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifest, []byte("---\nname: weather\ndescription: d\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Chdir(root)
	m := NewManager([]string{"./skills"})

	if n := len(m.GetMetadata()); n != 1 {
		t.Fatalf("loaded %d skills from a relative container, want 1", n)
	}
	loc := m.GetMetadata()[0].Location
	if !filepath.IsAbs(loc) {
		t.Errorf("location = %s, want an absolute path", loc)
	}
	if strings.Contains(loc, "..") {
		t.Errorf("location = %s, want the relative parts resolved away", loc)
	}
	if want := filepath.Join(root, "skills", "weather", manifestFileName); loc != want {
		t.Errorf("location = %s, want %s", loc, want)
	}
}

// "~" is expanded by the shell in the common case, and not at all in others:
// quoted on any shell, unquoted on cmd.exe, or written into a wrapper script's
// argument list. A literal "~" is a path that does not exist, so the whole
// feature silently did nothing.
func TestTildeContainerIsExpanded(t *testing.T) {
	home := config.ExpandPath("~")
	if home == "~" {
		t.Skip("no home directory to expand against")
	}

	missing := filepath.Join(".alayacore", "skills", "nothing-here")
	m := NewManager([]string{"~/" + filepath.ToSlash(missing)})

	if errs := m.GetLoadErrors(); len(errs) != 0 {
		t.Fatalf("load errors = %v, want none", errs)
	}
	want := "skill container " + filepath.Join(home, missing) + " does not exist"
	found := false
	for _, n := range m.GetNotices() {
		if strings.Contains(n, want) {
			found = true
		}
		if strings.Contains(n, "~/") {
			t.Errorf("notice %q still carries a literal ~", n)
		}
	}
	if !found {
		t.Errorf("notices = %v, want %q", m.GetNotices(), want)
	}
}

// A container reached through a symlink is addressed through the link: the paths
// under it stay inside the layout the user arranged, and normalization does not
// quietly rewrite it into wherever the link points.
func TestNormalizationLeavesLinkedContainersAlone(t *testing.T) {
	root := t.TempDir()

	real := filepath.Join(root, "shared")
	manifest := filepath.Join(real, "pdf", manifestFileName)
	if err := os.MkdirAll(filepath.Dir(manifest), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifest, []byte("---\nname: pdf\ndescription: d\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "linked")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}

	m := NewManager([]string{link})
	if n := len(m.GetMetadata()); n != 1 {
		t.Fatalf("loaded %d skills through a symlinked container, want 1", n)
	}
	if got, want := m.GetMetadata()[0].Location, filepath.Join(link, "pdf", manifestFileName); got != want {
		t.Errorf("location = %s, want the path through the link (%s)", got, want)
	}
}
