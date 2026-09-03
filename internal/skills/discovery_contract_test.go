package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeSkill creates <root>/<dir>/SKILL.md with frontmatter whose name matches
// dir, which the loader requires.
func writeSkill(t *testing.T, root, dir string) string {
	t.Helper()
	path := filepath.Join(root, dir)
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: " + filepath.Base(dir) + "\ndescription: d\n---\n\n# x\n"
	if err := os.WriteFile(filepath.Join(path, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func loadedNames(m *Manager) []string {
	skills := m.GetMetadata()
	out := make([]string, 0, len(skills))
	for _, s := range skills {
		out = append(out, s.Name)
	}
	return out
}

// The documented contract of --skill: the value is a CONTAINER, and every
// immediate subdirectory holding a SKILL.md is loaded from that one flag. The
// docs previously taught one flag per skill, which loads nothing at all; this
// test is what keeps the corrected wording true.
func TestContainerFlagLoadsEverySkillBeneathIt(t *testing.T) {
	root := t.TempDir()
	container := filepath.Join(root, "skills")
	writeSkill(t, container, "weather")
	writeSkill(t, container, "pdf")
	writeSkill(t, container, "notes")

	m := NewManager([]string{container})
	got := strings.Join(loadedNames(m), ",")
	if got != "notes,pdf,weather" {
		t.Errorf("loaded %q, want all three skills from the one container path", got)
	}
	if errs := m.GetLoadErrors(); len(errs) != 0 {
		t.Errorf("unexpected load errors: %v", errs)
	}
}

// Pointing --skill at a skill's own directory treats it as a container and finds
// no sub-skills inside it: nothing loads. It no longer passes in silence — the
// run says which container gave nothing, because a user who configured a
// container and got no skills cannot tell that apart from success otherwise.
func TestSkillDirectoryAsPathLoadsNothing(t *testing.T) {
	root := t.TempDir()
	skill := writeSkill(t, filepath.Join(root, "skills"), "weather")

	m := NewManager([]string{skill})
	if n := len(m.GetMetadata()); n != 0 {
		t.Fatalf("loaded %d skills from the skill's own directory, want 0", n)
	}
	if errs := m.GetLoadErrors(); len(errs) != 0 {
		t.Errorf("an empty container is not a fault, got load errors: %v", errs)
	}
	if !hasNotice(m, "loaded no skills") {
		t.Errorf("notices = %v, want the empty container named", m.GetNotices())
	}
}

// Discovery is exactly one level deep: a skill grouped into a subdirectory of the
// container is not found. Recursive would be friendlier and has not been chosen,
// so the limit is pinned rather than left to be rediscovered as a bug.
func TestDiscoveryIsOneLevelDeep(t *testing.T) {
	root := t.TempDir()
	container := filepath.Join(root, "skills")
	writeSkill(t, container, "top")
	writeSkill(t, filepath.Join(container, "grouped"), "buried") // container/grouped/buried

	m := NewManager([]string{container})
	if got := strings.Join(loadedNames(m), ","); got != "top" {
		t.Errorf("loaded %q, want only the immediate child: the scan is not recursive", got)
	}
}

// A subdirectory of the container without a SKILL.md is just a folder, not a
// broken skill: it is skipped without a word, so a repo can keep docs, scripts,
// or a README next to its skills.
func TestSubdirectoryWithoutManifestIsSkippedQuietly(t *testing.T) {
	root := t.TempDir()
	container := filepath.Join(root, "skills")
	writeSkill(t, container, "weather")
	if err := os.MkdirAll(filepath.Join(container, "shared-assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(container, "README.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := NewManager([]string{container})
	if got := strings.Join(loadedNames(m), ","); got != "weather" {
		t.Errorf("loaded %q, want only weather", got)
	}
	if errs := m.GetLoadErrors(); len(errs) != 0 {
		t.Errorf("an asset folder must not be reported as a failed skill: %v", errs)
	}
}

// A SKILL.md whose frontmatter name disagrees with its directory is dropped and
// reported — the one discovery failure that is NOT silent. The name is what the
// system prompt advertises and the directory is what <location> points at, so a
// disagreement would leave the agent told one thing and reading another.
func TestNameMustMatchDirectoryName(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "pdf-export")
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: pdf\ndescription: d\n---\n\n# x\n"
	if err := os.WriteFile(filepath.Join(path, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	m := NewManager([]string{root})
	if n := len(m.GetMetadata()); n != 0 {
		t.Errorf("loaded %d skills, want 0: a mismatched name must not reach the prompt", n)
	}
	errs := m.GetLoadErrors()
	if len(errs) != 1 {
		t.Fatalf("got %d load errors, want 1: %v", len(errs), errs)
	}
	if !strings.Contains(errs[0], "does not match directory") {
		t.Errorf("error = %q, want it to name the mismatch", errs[0])
	}
}

// A skill folder kept somewhere else and symlinked into the container is the
// ordinary way to share one skill across projects, or to keep it under version
// control in a dotfiles repo. The directory listing reports a link as a link, so
// the entry used to be dropped — one of the silent shapes, the hardest kind to
// notice.
func TestSymlinkedSkillDirectoryIsFollowed(t *testing.T) {
	root := t.TempDir()

	// The real folder, outside the container.
	home := filepath.Join(root, "shared")
	if err := os.MkdirAll(filepath.Join(home, "pdf"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "pdf", "SKILL.md"), []byte("---\nname: pdf\ndescription: d\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	container := filepath.Join(root, "skills")
	if err := os.MkdirAll(container, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(home, "pdf"), filepath.Join(container, "pdf")); err != nil {
		t.Fatal(err)
	}

	m := NewManager([]string{container})
	if got := loadedNames(m); len(got) != 1 || got[0] != "pdf" {
		t.Fatalf("loaded %v, want the symlinked skill", got)
	}
	// <location> names the path through the container — the one the user arranged
	// — not the folder the link points at.
	if loc := m.GetMetadata()[0].Location; loc != filepath.Join(container, "pdf", "SKILL.md") {
		t.Errorf("location = %s, want the path through the container", loc)
	}
	if errs := m.GetLoadErrors(); len(errs) != 0 {
		t.Errorf("unexpected load errors: %v", errs)
	}
}

// A link that leads nowhere, or to a plain file, names no skill and says
// nothing: a container holds other things, and a broken link is not a manifest
// that failed.
func TestLinkThatIsNotADirectoryStaysInvisible(t *testing.T) {
	root := t.TempDir()
	container := filepath.Join(root, "skills")
	if err := os.MkdirAll(container, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "gone"), filepath.Join(container, "ghost")); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "afile")
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(container, "filelink")); err != nil {
		t.Fatal(err)
	}

	m := NewManager([]string{container})
	if n := len(m.GetMetadata()); n != 0 {
		t.Errorf("loaded %d skills, want 0", n)
	}
	if errs := m.GetLoadErrors(); len(errs) != 0 {
		t.Errorf("load errors = %v, want none for entries that are simply not skills", errs)
	}
}
