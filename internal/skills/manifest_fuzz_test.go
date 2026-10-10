package skills

// Fuzzing the manifest reader.
//
// The table in manifest_test.go pins one exemplar per shape the reader accepts
// or refuses. What an exemplar cannot promise is that *any* input is safe, and
// this reader is hand-rolled: it takes `rest[0]`, slices `line[indent:]` and
// `value[1:len-1]`, tracks a cursor across lines and reads `body[k]`. Each of
// those is arithmetic over bytes nobody vouched for — a SKILL.md is shared as a
// git repository, so the text in it is not necessarily the user's own — and the
// space worth checking is every byte string, not the shapes someone thought of.
//
// The properties below are the ones a wrong reading can break, each of which
// this package promises somewhere: that a skill which loads is usable, that the
// body is the file's own tail, that a message names a line the file has, that
// reading is a pure function, and the shape of a file with no manifest at all.

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

// lineRef matches the "line N" a problem or error names its place with.
var lineRef = regexp.MustCompile(`line (\d+)`)

func FuzzParseSkillMarkdown(f *testing.F) {
	seeds := []string{
		"",
		"---",
		"---\n---\n",
		"---\nname: x\ndescription: d\n---\nbody\n",
		"---\nname: pdf-processing\ndescription: Use this skill when: the user asks about PDFs # not a comment\nlicense: Apache-2.0\n---\n\n# PDF Processing\n\n---\n\nmore\n",
		"---\nname: x\ndescription: \"> quoted: with # both\"\n---\nb\n",
		"---\nname: x\ndescription: >\n  one\n  two\n\n   deeper\n  three\n---\nbody\n",
		"---\nname: x\ndescription: |+\n  one\n  two\n\n---\nbody\n",
		"---\nname: x\ndescription: |4\n  one\n  two\n---\nbody\n",
		"---\nname: x\ndescription: d\nmetadata:\n  requires:\n    bins: [\"x\"]\n  cliHelp: \"x --help\"\n---\nbody\n",
		"\ufeff---\r\nname: x\r\ndescription: d\r\n---\r\nbody\r\n",
		"---\nname: x\ndescription: >\n\tone\n\ttwo\n---\nbody\n",
		"---\nname: x\ndescription: \"never closes\n---\nbody\n",
		"---\nname: x\ndescription: d\n  indented prose\n---\nbody\n",
		"---\nname: x\ndescription: d\n\n# Title\n\n---\n\nmore\n",
		"---   \nname: x\ndescription: d\n---   \nbody\n",
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, content string) {
		meta, body, problems, err := ParseSkillMarkdown(content)
		lines := splitLines(content)

		// A manifest that loads has to be one a caller can use: the prompt
		// advertises these two fields and nothing else, so an empty or
		// out-of-rule value with no error is a skill the model is told about
		// wrongly. A file with no manifest at all is the exception — its fields
		// are empty and its error is nil by design, and it is checked as its own
		// shape below.
		if err == nil && hasFrontmatter(lines) {
			if meta.Name == "" || meta.Description == "" {
				t.Fatalf("loaded with an empty field: name=%q description=%q", meta.Name, meta.Description)
			}
			if e := validateName(meta.Name); e != nil {
				t.Fatalf("loaded name %q: %v", meta.Name, e)
			}
			if e := validateDescription(meta.Description); e != nil {
				t.Fatalf("loaded description of %d runes: %v", utf8.RuneCountInString(meta.Description), e)
			}
		}

		// The body is the file's own tail, verbatim — the reader's one promise
		// about the document. Re-assembling, trimming or reordering its lines
		// breaks this, and the file's instructions are what a skill is for.
		if !strings.HasSuffix(strings.Join(lines, "\n"), body) {
			t.Fatalf("body is not a tail of the file: body=%q content=%q", body, content)
		}

		// A message may only name a line the file has, and every line the reader
		// names is one it looked at and found wrong: a blank line is skipped
		// wherever the reader counts, so naming one is an off-by-one that sends
		// the user to a line that says nothing about the problem.
		for _, msgs := range [][]string{problems, errStrings(err)} {
			for _, msg := range msgs {
				for _, m := range lineRef.FindAllStringSubmatch(msg, -1) {
					n, convErr := strconv.Atoi(m[1])
					if convErr != nil || n < 1 || n > len(lines) {
						t.Fatalf("%q names line %s of a %d-line file", msg, m[1], len(lines))
					}
					if strings.TrimSpace(lines[n-1]) == "" {
						t.Fatalf("%q names line %d, which is blank: %q", msg, n, content)
					}
				}
			}
		}

		// Reading is a pure function of the bytes: no globals, no map order, no
		// state carried between calls.
		meta2, body2, problems2, err2 := ParseSkillMarkdown(content)
		if meta != meta2 || body != body2 || errString(err) != errString(err2) || strings.Join(problems, "\x00") != strings.Join(problems2, "\x00") {
			t.Fatalf("two readings of one file disagree:\n%q\n%q", content, content)
		}

		// A file whose first non-blank line is not the delimiter has no manifest
		// at all: the body is the whole file, the reason is stated, and it is
		// not an error — the caller is the one that refuses a skill without a
		// name, for a name it had no way to read.
		if !hasFrontmatter(lines) {
			want := strings.Join(lines, "\n")
			if err != nil || body != want || len(problems) != 1 || !strings.Contains(problems[0], "no frontmatter block") {
				t.Fatalf("no frontmatter: err=%v body=%q want=%q problems=%v", err, body, want, problems)
			}
		}
	})
}

// hasFrontmatter mirrors the reader's opening rule: the first non-blank line is
// the delimiter, and a "---" further down is markdown.
func hasFrontmatter(lines []string) bool {
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		return strings.TrimSpace(line) == frontmatterDelim
	}
	return false
}

// errStrings renders an error as the one-element list the line-number check
// walks, or nothing when there is no error.
func errStrings(err error) []string {
	if err == nil {
		return nil
	}
	return []string{err.Error()}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
