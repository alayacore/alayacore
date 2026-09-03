package skills

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// The manifest reader has one job: report the skill to the model exactly as the
// author wrote it. The description is the only signal the agent has for whether
// a skill applies, so a description that is silently shortened, rewritten, or
// that takes the file down with it is the worst failure this package can have.
// Each test below pins one form that failure used to take.

func manifest(description string) string {
	return "---\nname: x\ndescription: " + description + "\n---\nbody\n"
}

// A colon plus space ends a YAML scalar's plain form, so "description: Use this
// skill when: the user asks" was a parse failure and the skill vanished. The
// manifest format takes the rest of the line as the value, which is what the
// author wrote and what the agent needs to see.
func TestDescriptionKeepsItsColons(t *testing.T) {
	text := "Use this skill when: the user asks about PDFs"
	md, _, problems, err := ParseSkillMarkdown(manifest(text))
	if err != nil {
		t.Fatalf("a colon in the value lost the skill: %v", err)
	}
	if md.Description != text {
		t.Errorf("description = %q, want %q", md.Description, text)
	}
	if len(problems) != 0 {
		t.Errorf("a well-formed value should need no comment, got %v", problems)
	}
}

// "#" begins a comment only at the start of a line in this format. Under YAML it
// ended the scalar mid-sentence: the skill loaded and was advertised as
// "Count" with no error anywhere — a skill that never activates, and a user with
// nothing to look at.
func TestDescriptionKeepsItsHashMarks(t *testing.T) {
	text := "Count # of items, and handle C# interop"
	md, _, _, err := ParseSkillMarkdown(manifest(text))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if md.Description != text {
		t.Errorf("description was truncated to %q, want %q", md.Description, text)
	}
}

// A frontmatter block whose closing "---" was deleted used to run on until the
// next horizontal rule in the markdown, folding the document's own headings and
// prose into the description — sometimes with no error at all. A line that can
// only be body text is now reported as the missing delimiter it is.
func TestUnclosedFrontmatterIsReportedNotSwallowed(t *testing.T) {
	cases := map[string]string{
		"heading then prose": "---\nname: x\ndescription: y\n\n# Title\n\nprose\n\n---\n\nmore\n",
		"prose only":         "---\nname: x\ndescription: y\n\njust prose\n\n---\n\nmore\n",
		"never closed":       "---\nname: x\ndescription: y\nbody\n",
	}
	for label, content := range cases {
		_, _, _, err := ParseSkillMarkdown(content)
		if err == nil {
			t.Errorf("%s: no error; the reader guessed where the manifest ends", label)
			continue
		}
		if !strings.Contains(err.Error(), "not closed") && !strings.Contains(err.Error(), "never closed") {
			t.Errorf("%s: err = %v, want it to name the unclosed block", label, err)
		}
	}
}

// The mirror of that test: a "---" after the block is a horizontal rule and must
// stay in the body untouched.
func TestBodyRuleIsNotAFrontmatterDelimiter(t *testing.T) {
	content := "---\nname: x\ndescription: y\n---\n\n# Title\n\n---\n\nmore\n"
	md, body, problems, err := ParseSkillMarkdown(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if md.Description != "y" {
		t.Errorf("description = %q, want the body left out of it", md.Description)
	}
	if !strings.Contains(body, "# Title") || !strings.Contains(body, "---") || !strings.Contains(body, "more") {
		t.Errorf("body = %q, want the heading, the rule and the text after it", body)
	}
	if len(problems) != 0 {
		t.Errorf("unexpected problems: %v", problems)
	}
}

// Folded and literal block scalars are how long descriptions are written.
func TestBlockScalars(t *testing.T) {
	md, _, problems, err := ParseSkillMarkdown("---\nname: x\ndescription: >-\n  First.\n\n  Second.\n---\nbody\n")
	if err != nil {
		t.Fatalf("folded description: %v", err)
	}
	if md.Description != "First.\nSecond." {
		t.Errorf("folded = %q, want the paragraphs folded with one break between them", md.Description)
	}
	if len(problems) != 0 {
		t.Errorf("folded: unexpected problems %v", problems)
	}

	md, _, _, err = ParseSkillMarkdown("---\nname: x\ndescription: |\n  one\n  two\n---\nbody\n")
	if err != nil {
		t.Fatalf("literal description: %v", err)
	}
	if md.Description != "one\ntwo" {
		t.Errorf("literal = %q, want the line breaks kept", md.Description)
	}

	// A block scalar with nothing in it is an empty description: the skill
	// cannot be advertised, so it is refused — but with the empty block named,
	// not just "description is required" as if the key were missing.
	_, _, problems, err = ParseSkillMarkdown("---\nname: x\ndescription: |\n---\nbody\n")
	if err == nil || !strings.Contains(err.Error(), "description is required") {
		t.Fatalf("err = %v, want the empty description to refuse the skill", err)
	}
	if len(problems) == 0 || !strings.Contains(problems[0], "block scalar") {
		t.Errorf("problems = %v, want the empty block scalar reported", problems)
	}
}

// metadata is not read by anything. It used to be able to kill a skill: a nested
// map failed the YAML parse of the whole file. Now it is reported and the file
// stands.
func TestNestedMetadataCannotLoseTheSkill(t *testing.T) {
	md, _, problems, err := ParseSkillMarkdown("---\nname: x\ndescription: d\nmetadata:\n  team:\n    name: infra\n---\nbody\n")
	if err != nil {
		t.Fatalf("a nested metadata map lost the skill: %v", err)
	}
	if md.Name != "x" || md.Description != "d" {
		t.Errorf("fields read = %q/%q", md.Name, md.Description)
	}
	if len(problems) == 0 || !strings.Contains(problems[0], "nested") {
		t.Errorf("problems = %v, want the unread nesting reported", problems)
	}

	// A flat map, which is what the field can hold, is read.
	md, _, problems, err = ParseSkillMarkdown("---\nname: x\ndescription: d\nmetadata:\n  author: jane\n  version: 2\n---\nbody\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if md.Metadata["author"] != "jane" || md.Metadata["version"] != "2" {
		t.Errorf("metadata = %v, want both entries", md.Metadata)
	}
	if len(problems) != 0 {
		t.Errorf("unexpected problems: %v", problems)
	}
}

// The format keeps the config file rule: the first occurrence of a key wins, and
// the discarded one is named.
func TestDuplicateKeyFirstValueStands(t *testing.T) {
	md, _, problems, err := ParseSkillMarkdown("---\nname: x\ndescription: written first\ndescription: written second\n---\nbody\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if md.Description != "written first" {
		t.Errorf("description = %q, want the first value", md.Description)
	}
	if len(problems) != 1 || !strings.Contains(problems[0], "duplicate key") || !strings.Contains(problems[0], "line 4") {
		t.Errorf("problems = %v, want one naming the duplicate and its line", problems)
	}
}

// A field this build does not know is not a defect: the manifest format grows,
// and `version: 1.2` must not cost the user a skill.
func TestUnknownKeysAreIgnored(t *testing.T) {
	md, _, problems, err := ParseSkillMarkdown("---\nname: x\ndescription: d\nversion: 1.2\nargument-hint: \"<file>\"\n---\nbody\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if md.Name != "x" {
		t.Errorf("name = %q", md.Name)
	}
	if len(problems) != 0 {
		t.Errorf("an unknown key should be read past in silence, got %v", problems)
	}
}

func TestQuotedValues(t *testing.T) {
	md, _, problems, err := ParseSkillMarkdown(manifest(`"when: quoted, and # hash"`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if md.Description != "when: quoted, and # hash" {
		t.Errorf("description = %q, want the quotes removed and the text kept", md.Description)
	}
	if len(problems) != 0 {
		t.Errorf("unexpected problems: %v", problems)
	}

	// An unterminated quote is a mistake worth naming; the text is still kept
	// rather than guessed away.
	md, _, problems, err = ParseSkillMarkdown("---\nname: x\ndescription: \"never closes\n---\nbody\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(md.Description, "never closes") {
		t.Errorf("description = %q, want the author's text", md.Description)
	}
	if len(problems) == 0 || !strings.Contains(problems[0], "never closes on its line") {
		t.Errorf("problems = %v, want the quote reported", problems)
	}
}

// A file with no manifest at all is not a parse error — the body is intact and
// the caller reports the absent name — but the reason must be the real one. It
// used to surface as `skill name ” does not match directory`, which points at
// the directory instead of the missing frontmatter.
func TestMissingFrontmatterNamesTheRealCause(t *testing.T) {
	_, _, problems, err := ParseSkillMarkdown("# just markdown\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(problems) != 1 || !strings.Contains(problems[0], "no frontmatter block") {
		t.Fatalf("problems = %v, want the missing block named", problems)
	}
}

// The spec's 1-1024 limit is in characters. Counting bytes instead left a
// Chinese description capped near 341 characters and rejected a manifest that
// satisfies the format.
func TestDescriptionLimitCountsCharactersNotBytes(t *testing.T) {
	d := strings.Repeat("天", 400) // 1200 bytes: rejected when the limit was bytes
	if _, _, _, err := ParseSkillMarkdown(manifest(d)); err != nil {
		t.Errorf("400 characters (%d bytes) rejected: %v", utf8.RuneCountInString(d), err)
	}

	if _, _, _, err := ParseSkillMarkdown(manifest(strings.Repeat("天", 1100))); err == nil {
		t.Error("1100 characters must still be rejected")
	}
}

// A manifest saved by a Windows editor is the same manifest.
func TestCRLFFrontmatter(t *testing.T) {
	md, body, _, err := ParseSkillMarkdown("---\r\nname: x\r\ndescription: d\r\n---\r\nbody\r\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if md.Name != "x" || md.Description != "d" {
		t.Errorf("fields = %q/%q", md.Name, md.Description)
	}
	if !strings.Contains(body, "body") {
		t.Errorf("body = %q", body)
	}
}

// The two required fields keep their messages: the loader shows them verbatim.
func TestRequiredFields(t *testing.T) {
	if _, _, _, err := ParseSkillMarkdown("---\ndescription: d\n---\nbody\n"); err == nil || !strings.Contains(err.Error(), "name is required") {
		t.Errorf("err = %v, want \"name is required\"", err)
	}
	if _, _, _, err := ParseSkillMarkdown("---\nname: x\n---\nbody\n"); err == nil || !strings.Contains(err.Error(), "description is required") {
		t.Errorf("err = %v, want \"description is required\"", err)
	}
}
