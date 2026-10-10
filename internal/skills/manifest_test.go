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
	cases := []struct {
		label   string
		content string
		want    string
	}{
		{
			// The delimiter is gone, so the scan runs to the next rule and
			// the markdown inside it is what gives the answer away — the
			// heading itself now, rather than the prose under it.
			label:   "heading before any delimiter",
			content: "---\nname: x\ndescription: y\n\n# Title\n\nprose\n\n---\n\nmore\n",
			want:    `line 5 is neither a "key: value" entry`,
		},
		{
			label:   "body text, delimiter further down",
			content: "---\nname: x\ndescription: y\n\njust prose\n\n---\n\nmore\n",
			want:    `line 5 is neither a "key: value" entry`,
		},
		{
			// Nothing closes it, and the file says so.
			label:   "never closed",
			content: "---\nname: x\ndescription: y\nbody\n",
			want:    "is never closed by a",
		},
	}
	for _, tc := range cases {
		_, _, _, err := ParseSkillMarkdown(tc.content)
		if err == nil {
			t.Errorf("%s: no error; the reader guessed where the manifest ends", tc.label)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want it to say %q", tc.label, err, tc.want)
		}
	}
}

// The blank line between an entry and a "#" line is what decides whether that
// line is a comment or the markdown heading a deleted closing "---" let into the
// block. The reader used to advance past that blank while collecting the entry's
// continuation, so the heading read as a comment and the section under it left
// the body with no error anywhere: the skill loaded, minus part of the document.
func TestHeadingAfterEntryIsNotAComment(t *testing.T) {
	content := "---\nname: x\ndescription: d\nlicense: MIT\n\n# Title\n\n---\n\nmore\n"
	_, _, problems, err := ParseSkillMarkdown(content)
	if err == nil {
		t.Fatal("the heading was read as a comment; the block ran into the body and said nothing")
	}
	if len(problems) != 0 {
		t.Errorf("problems = %v, want a line that can only be the body to be the error it is", problems)
	}
	if !strings.Contains(err.Error(), "line 6") {
		t.Errorf("err = %v, want the heading's own line named", err)
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

// A block scalar's own line can be a "---" — indented, so it is content, not the
// end of the block. The closing delimiter used to be found by trimming the line
// first, so a rule inside `description: |` cut the description short and leaked
// `license: MIT` and the delimiter into the body — a wrong manifest with no
// error raised.
func TestIndentedRuleDoesNotCloseTheBlock(t *testing.T) {
	content := "---\nname: x\ndescription: |\n  first\n  ---\n  second\nlicense: MIT\n---\nbody\n"
	md, body, problems, err := ParseSkillMarkdown(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if md.Description != "first\n---\nsecond\n" {
		t.Errorf("description = %q, want the indented rule kept as content", md.Description)
	}
	if md.License != "MIT" {
		t.Errorf("license = %q, want the entry after the block still read", md.License)
	}
	if strings.Contains(body, "license:") || !strings.Contains(body, "body") {
		t.Errorf("body = %q, want only the document after the manifest", body)
	}
	if len(problems) != 0 {
		t.Errorf("unexpected problems: %v", problems)
	}

	// The same line under a free-form entry is part of what that entry skips.
	content = "---\nname: x\ndescription: d\nmetadata:\n  note: |\n    ---\nlicense: MIT\n---\nbody\n"
	md, _, _, err = ParseSkillMarkdown(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if md.License != "MIT" {
		t.Errorf("license = %q, want the entry after the metadata block still read", md.License)
	}
}

// An explicit indentation the content does not reach is an error for YAML. Here
// the author's text is kept — cut to the indentation each line actually carries,
// never into a word — and the line is named, because reading it as if the header
// were right is how `one` came back as `e`.
func TestExplicitIndentShorterThanContent(t *testing.T) {
	md, _, problems, err := ParseSkillMarkdown("---\nname: x\ndescription: |4\n  one\n  two\n---\nbody\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if md.Description != "one\ntwo\n" {
		t.Errorf("description = %q, want the text kept", md.Description)
	}
	if len(problems) != 1 || !strings.Contains(problems[0], "line 4") {
		t.Errorf("problems = %v, want the short line named", problems)
	}

	// An indicator the content does reach cuts exactly that much, leaving any
	// extra indentation as part of the value — YAML's own result for `|2` over
	// four-space content.
	md, _, problems, err = ParseSkillMarkdown("---\nname: x\ndescription: |2\n    one\n    two\n---\nbody\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if md.Description != "  one\n  two\n" {
		t.Errorf("description = %q, want the extra indentation kept", md.Description)
	}
	if len(problems) != 0 {
		t.Errorf("unexpected problems: %v", problems)
	}
}

// Folded and literal block scalars are how long descriptions are written.
func TestBlockScalars(t *testing.T) {
	// ">-" strips the trailing break.
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

	// "|" is the default (clip): the lines' breaks are kept and the value ends
	// with the one break the block itself ends with, as YAML defines it.
	md, _, _, err = ParseSkillMarkdown("---\nname: x\ndescription: |\n  one\n  two\n---\nbody\n")
	if err != nil {
		t.Fatalf("literal description: %v", err)
	}
	if md.Description != "one\ntwo\n" {
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

// The chomping indicator is honored rather than approximated. Reading every
// header as if it were the default silently rewrote the value: "|+" lost the
// blank lines the author left, and the three modes are not interchangeable.
func TestBlockScalarChomping(t *testing.T) {
	cases := []struct {
		header string
		want   string
	}{
		{"|", "one\ntwo\n"},    // clip: exactly one trailing break
		{"|-", "one\ntwo"},     // strip
		{"|+", "one\ntwo\n\n"}, // keep: the blank line the author left
		{">", "one two\n"},
		{">-", "one two"},
		{">+", "one two\n\n"},
	}
	for _, tc := range cases {
		md, _, _, err := ParseSkillMarkdown("---\nname: x\ndescription: " + tc.header + "\n  one\n  two\n\n---\nbody\n")
		if err != nil {
			t.Fatalf("%q: %v", tc.header, err)
		}
		if md.Description != tc.want {
			t.Errorf("%q = %q, want %q", tc.header, md.Description, tc.want)
		}
	}
}

// A line indented deeper than the block is content, not a continuation of the
// sentence above it: YAML keeps such lines as they are, and folding them joined
// words the author had separated. Each expectation is yaml.v3's own output for
// the same frontmatter.
func TestFoldedKeepsDeeperLines(t *testing.T) {
	cases := []struct{ block, want string }{
		{"  one\n   deeper\n  two", "one\n deeper\ntwo\n"},
		{"  one\n\n   deeper\n  two", "one\n\n deeper\ntwo\n"},
		{"  one\n   deeper\n\n  two", "one\n deeper\n\ntwo\n"},
	}
	for _, tc := range cases {
		md, _, _, err := ParseSkillMarkdown("---\nname: x\ndescription: >\n" + tc.block + "\n---\nbody\n")
		if err != nil {
			t.Fatalf("%q: %v", tc.block, err)
		}
		if md.Description != tc.want {
			t.Errorf(">\n%s = %q, want %q", tc.block, md.Description, tc.want)
		}
	}
}

// The spec's `metadata` is free-form and nothing in this build reads it, so the
// whole entry is skipped, nested or flat. It used to be recorded one level deep
// and the nesting reported: a valid manifest then printed an error at startup,
// and a `requires:` holding a map came back as `requires: ""` — a value the
// author never wrote.
func TestMetadataIsSkippedWholeWhateverItsShape(t *testing.T) {
	for _, content := range []string{
		"---\nname: x\ndescription: d\nmetadata:\n  team:\n    name: infra\n  author: jane\n---\nbody\n",
		"---\nname: x\ndescription: d\nmetadata:\n  author: jane\n  version: 2\n---\nbody\n",
		"---\nname: x\ndescription: d\nmetadata: {author: jane}\n---\nbody\n",
	} {
		md, body, problems, err := ParseSkillMarkdown(content)
		if err != nil {
			t.Fatalf("a metadata block lost the skill: %v", err)
		}
		if md.Name != "x" || md.Description != "d" {
			t.Errorf("fields read = %q/%q, want the entry skipped, not the file", md.Name, md.Description)
		}
		if !strings.Contains(body, "body") {
			t.Errorf("body = %q, want the document after the entry", body)
		}
		if len(problems) != 0 {
			t.Errorf("problems = %v, want a free-form field read past in silence", problems)
		}
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
