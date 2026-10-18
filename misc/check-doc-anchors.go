//go:build ignore

// check-doc-anchors — the fragment half of misc/check-docs.sh.
//
// check-docs.sh resolves the path half of a relative Markdown link: does
// ./docs/tui.md exist from the file that wrote it. The fragment half — the
// #heading on the end — was unread, so retitling a section (which is exactly how
// docs/tool-execution.md's "Same-File Writes Are Serialized" became "Writes
// Exclude All Access, and Order Nothing") left every link to it pointing at
// nothing, and no check noticed.
//
// Written in Go rather than sed/awk for one reason: the slug is a Unicode
// transform. README.zh-CN.md links to (#模式) and (#快速开始), and a heading such
// as "### Plain IO（`--plainio`）" mixes fullwidth punctuation with code spans.
// A byte-oriented or ASCII-class-based shell version would have to either drop
// the CJK headings (so those links look broken) or keep punctuation (so Latin
// ones do). unicode.IsLetter says the same thing in every locale.
//
// The rules reproduced here are GitHub's (github-slugger):
//
//   - ATX headings only ("## Title"). Setext (underlined) headings are not used
//     in this repo and are not recognized; a doc that adopts them would need
//     this file extended.
//   - Inline markup is unwrapped first: code backticks and emphasis markers
//     removed, [text](url) reduced to text, <html> dropped.
//   - Lowercase. Keep letters, digits, marks, spaces, hyphens and underscores;
//     drop every other rune. Then each space becomes a hyphen, one for one, so
//     "a  b" is "a--b".
//   - A repeated heading in one document gets -1, -2, … appended, which is what
//     makes the second "## Implementation" reachable at all.
//
// Usage: file paths come on stdin, one per line (the list check-docs.sh already
// computed); -report names a file to receive the number of fragments checked, so
// the shell can print one summary line rather than two.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

var (
	// An ATX heading: up to three leading spaces, one to six #, a space, then
	// the text, with an optional closing run of #.
	headingRE = regexp.MustCompile(`^ {0,3}(#{1,6})[ \t]+(.*?)[ \t]*#*[ \t]*$`)
	// ](target) — the same shape check-docs.sh matches for paths.
	linkRE = regexp.MustCompile(`\]\(([^)]+)\)`)
	// [text](url) and ![alt](url), reduced to their text, before slugging.
	inlineLinkRE = regexp.MustCompile(`!?\[([^\]]*)\]\([^)]*\)`)
	htmlTagRE    = regexp.MustCompile(`<[^>]*>`)
	// A fence opens or closes with ``` or ~~~ (three or more).
	fenceRE = regexp.MustCompile("^ {0,3}(?:```|~~~)")
)

func main() {
	report := flag.String("report", "", "write the count of fragments checked to this file")
	flag.Parse()

	files, err := readFileList()
	if err != nil {
		fmt.Fprintf(os.Stderr, "check-doc-anchors: %v\n", err)
		os.Exit(2)
	}

	// Slugs are per document, so cache them: a page linked from twenty others
	// is still read once.
	cache := make(map[string]map[string]bool, len(files))
	slugs := func(path string) (map[string]bool, bool) {
		if s, ok := cache[path]; ok {
			return s, s != nil
		}
		s := documentSlugs(path)
		cache[path] = s
		return s, s != nil
	}

	checked := 0
	broken := 0
	for _, file := range files {
		for _, target := range fragmentsOf(file) {
			frag := target
			resolved := file
			if path, rest, ok := strings.Cut(target, "#"); ok {
				if path != "" {
					resolved = filepath.Join(filepath.Dir(file), path)
				}
				frag = rest
			} else {
				continue // no fragment: class 1's business
			}
			// Only Markdown targets can be answered. A missing file is class
			// 1's report, not this one's, so it is skipped rather than repeated.
			if !strings.HasSuffix(strings.ToLower(resolved), ".md") {
				continue
			}
			known, ok := slugs(resolved)
			if !ok {
				continue
			}
			if frag == "" {
				continue // a bare "page.md#" asserts nothing
			}
			checked++
			if known[frag] {
				continue
			}
			broken++
			writeBroken(file, target, resolved, frag, known)
		}
	}

	if *report != "" {
		if err := os.WriteFile(*report, []byte(fmt.Sprintf("%d\n", checked)), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "check-doc-anchors: %v\n", err)
			os.Exit(2)
		}
	}
	if broken > 0 {
		os.Exit(1)
	}
}

// writeBroken prints one finding, naming the fix where the fix is obvious: a
// fragment that differs from a real anchor only by case is a one-character edit,
// not a re-title.
func writeBroken(file, target, resolved, frag string, known map[string]bool) {
	lower := strings.ToLower(frag)
	switch {
	case known[lower]:
		fmt.Printf("%s -> %s (the heading exists, but its anchor is all lowercase: #%s)\n", file, target, lower)
	default:
		fmt.Printf("%s -> %s (no heading in %s has the anchor #%s)\n", file, target, resolved, frag)
	}
}

// readFileList takes the file paths on stdin, one per line.
func readFileList() ([]string, error) {
	var files []string
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); line != "" {
			files = append(files, line)
		}
	}
	return files, sc.Err()
}

// documentSlugs returns every anchor the document defines. A nil map means the
// file could not be read, which is class 1's finding, not this one's.
func documentSlugs(path string) map[string]bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	slugs := make(map[string]bool)
	counts := make(map[string]int) // per-slug, for the -1/-2 suffix
	inFence := false
	for _, line := range strings.Split(string(data), "\n") {
		if fenceRE.MatchString(line) {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		m := headingRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		slug := slugOf(m[2])
		if slug == "" {
			continue
		}
		// GitHub dedupes by appending the count of prior occurrences.
		if n := counts[slug]; n > 0 {
			counts[slug] = n + 1
			slugs[fmt.Sprintf("%s-%d", slug, n)] = true
			continue
		}
		counts[slug] = 1
		slugs[slug] = true
	}
	return slugs
}

// fragmentsOf lists the fragment-bearing link targets in one document. Fenced
// blocks are skipped: sample text is not a link.
func fragmentsOf(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var targets []string
	inFence := false
	for _, line := range strings.Split(string(data), "\n") {
		if fenceRE.MatchString(line) {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		for _, m := range linkRE.FindAllStringSubmatch(line, -1) {
			target := strings.TrimSpace(m[1])
			if !strings.Contains(target, "#") {
				continue
			}
			// A scheme or a bare '#' fragment on its own is either external or
			// nothing to check.
			if strings.Contains(target, "://") || strings.HasPrefix(target, "mailto:") {
				continue
			}
			targets = append(targets, target)
		}
	}
	return targets
}

// slugOf is github-slugger's transform, in the order it applies them.
func slugOf(heading string) string {
	s := heading
	s = inlineLinkRE.ReplaceAllString(s, "$1") // [text](url) -> text
	s = htmlTagRE.ReplaceAllString(s, "")      // <span id=x> -> gone
	s = strings.ReplaceAll(s, "`", "")         // code spans
	// Emphasis markers are dropped; underscores are NOT, because GitHub keeps
	// them — "Tool-calling mode (`serial_tool_calls`)" anchors to
	// #tool-calling-mode-serial_tool_calls.
	s = strings.ReplaceAll(s, "*", "")
	s = strings.ToLower(s)

	var b strings.Builder
	for _, r := range s {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r), unicode.IsMark(r):
			b.WriteRune(r)
		case r == ' ', r == '-', r == '_':
			b.WriteRune(r)
		}
	}
	return strings.ReplaceAll(b.String(), " ", "-")
}
