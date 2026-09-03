package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func hasNotice(m *Manager, substr string) bool {
	for _, n := range m.GetNotices() {
		if strings.Contains(n, substr) {
			return true
		}
	}
	return false
}

func hasLoadError(m *Manager, substr string) bool {
	for _, e := range m.GetLoadErrors() {
		if strings.Contains(e, substr) {
			return true
		}
	}
	return false
}

// A container that cannot be read is a container problem, not a program problem.
// It used to be both: discovery returned the error, Setup wrapped it, and main
// exited before the first turn — so an optional feature that one path could not
// open stopped the whole agent, while the failures users actually hit (a folder
// with nothing in it) stayed silent.
func TestUnreadableContainerCostsOnlyItself(t *testing.T) {
	root := t.TempDir()

	notADirectory := filepath.Join(root, "afile")
	if err := os.WriteFile(notADirectory, []byte("I am a file"), 0o644); err != nil {
		t.Fatal(err)
	}

	unreadable := filepath.Join(root, "unreadable")
	if err := os.MkdirAll(filepath.Join(unreadable, "weather"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeManifest(unreadable, "weather", "---\nname: weather\ndescription: d\n---\nbody\n"); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(unreadable, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(unreadable, 0o755) })

	good := filepath.Join(root, "good")
	if err := writeManifest(good, "pdf", "---\nname: pdf\ndescription: d\n---\nbody\n"); err != nil {
		t.Fatal(err)
	}

	for _, container := range []string{notADirectory, unreadable} {
		m := NewManager([]string{container, good})
		if got := loadedNames(m); len(got) != 1 || got[0] != "pdf" {
			t.Errorf("%s: loaded %v, want the readable container to still load pdf", container, got)
		}
		if !hasLoadError(m, "skill container "+container) {
			t.Errorf("%s: load errors = %v, want the container named", container, m.GetLoadErrors())
		}
	}
}

// A container that is not there at all is the same shape: reported, survivable.
// It is a notice rather than a load error because a personal folder is routinely
// passed before it has been created.
func TestMissingContainerIsReportedAsANotice(t *testing.T) {
	good := filepath.Join(t.TempDir(), "good")
	if err := writeManifest(good, "pdf", "---\nname: pdf\ndescription: d\n---\nbody\n"); err != nil {
		t.Fatal(err)
	}

	missing := filepath.Join(t.TempDir(), "no-such-folder")
	m := NewManager([]string{missing, good})
	if n := len(m.GetMetadata()); n != 1 {
		t.Fatalf("loaded %d skills, want pdf still loaded", n)
	}
	if !hasNotice(m, "does not exist") {
		t.Errorf("notices = %v, want the missing container named", m.GetNotices())
	}
	if len(m.GetLoadErrors()) != 0 {
		t.Errorf("load errors = %v, want none: an absent folder is not a fault", m.GetLoadErrors())
	}
}

// The outcome line is what makes "loaded nothing" visible. It is printed whenever
// a container was configured, including when it worked.
func TestStartupReportsHowManySkillsLoaded(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "one")
	second := filepath.Join(root, "two")
	for _, container := range []string{first, second} {
		if err := writeManifest(container, filepath.Base(container)+"-skill", "---\nname: "+filepath.Base(container)+"-skill\ndescription: d\n---\nbody\n"); err != nil {
			t.Fatal(err)
		}
	}

	m := NewManager([]string{first, second})
	if !hasNotice(m, "skills: 2 skills loaded from 2 containers") {
		t.Errorf("notices = %v, want the count line", m.GetNotices())
	}

	// One container, one skill: no stray plural.
	only := filepath.Join(root, "only")
	if err := writeManifest(only, "solo", "---\nname: solo\ndescription: d\n---\nbody\n"); err != nil {
		t.Fatal(err)
	}
	m = NewManager([]string{only})
	if !hasNotice(m, "skills: 1 skill loaded from 1 container") {
		t.Errorf("notices = %v, want the count line without a plural", m.GetNotices())
	}

	// Nothing configured: nothing said. A feature that was not asked for has no
	// outcome to report.
	m = NewManager(nil)
	if len(m.GetNotices()) != 0 {
		t.Errorf("notices = %v, want none without --skill", m.GetNotices())
	}
}

// A manifest that loads with a line the reader could not represent still says
// so: the agent is being told less than the author wrote.
func TestManifestProblemsReachStartupErrors(t *testing.T) {
	container := t.TempDir()
	if err := writeManifest(container, "odd", "---\nname: odd\ndescription: d\nmetadata:\n  team:\n    name: infra\n---\nbody\n"); err != nil {
		t.Fatal(err)
	}

	m := NewManager([]string{container})
	if n := len(m.GetMetadata()); n != 1 {
		t.Fatalf("loaded %d skills, want the skill kept", n)
	}
	if !hasLoadError(m, "nested entries") {
		t.Errorf("load errors = %v, want the unread nesting reported with its line", m.GetLoadErrors())
	}
}
