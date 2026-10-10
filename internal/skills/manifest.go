package skills

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	// frontmatterDelim opens and closes the manifest block.
	frontmatterDelim = "---"
	// maxFrontmatterLines bounds the search for the closing line. A SKILL.md is
	// a document, not a data stream: exactly one block belongs to the reader,
	// and it is the one that starts on the file's first non-blank line. The
	// bound is what keeps a deleted closing "---" from being searched for
	// through the whole document — within it the reader reports the first line
	// that can only be prose, and past it the file is reported as unclosed.
	maxFrontmatterLines = 200
)

// maxDescriptionRunes is the spec's description limit, counted in characters.
const maxDescriptionRunes = 1024

// ParseSkillMarkdown reads a SKILL.md file and returns its frontmatter
// metadata, its markdown body, and the problems found while reading.
//
// The error is reserved for a manifest that cannot be honored at all: a block
// that never closes, a block containing a line that can only be the markdown
// body under it (the usual shape of a deleted closing delimiter), an absent or
// malformed name, an absent or overlong description. What is returned in
// problems instead is a defect inside an otherwise sound block — a duplicate
// key, a quote left open, a block scalar with nothing indented under it, an
// explicit indentation the content does not reach — where the rest of the file
// is still readable, and a file with no frontmatter block at all, which is not a
// parse failure: the body is intact, and it is the caller that refuses the
// skill, for a name it had no way to read.
//
// The loader surfaces both, so no form of input is dropped or altered without
// saying where and why.
//
// The block is read with the same key-value shape as the project's config files,
// not a general YAML parser. It is a reader of its own because a frontmatter
// block has structure a config file does not: the delimiters that bound it, the
// body after it that must never be mistaken for more manifest, and values
// continued on indented lines. For an ordinary value the two formats agree, and
// the value is the rest of the line. Two of the places they do not are where a
// manifest must not be guessed at:
//
//   - `description: Use this skill when: the user asks about PDFs` is invalid
//     YAML ("mapping values are not allowed here"), so the whole skill
//     disappears; here the value is the rest of the line, as written.
//   - `description: Count # of items` ends at the " #" for YAML, so the skill
//     is advertised to the model as "Count" with no error raised; here "#"
//     only starts a comment at the beginning of a line.
//
// Values may still be quoted, folded (`>`), literal (`|`) or continued on
// indented lines, which is all of YAML the manifest format needs. A block
// scalar's chomping and indentation indicators are read, and a line indented
// deeper than its block stays a line of its own, so the value is the one YAML
// would have read rather than an approximation of it.
func ParseSkillMarkdown(content string) (Metadata, string, []string, error) {
	lines := splitLines(content)

	open := frontmatterOpen(lines)
	if open < 0 {
		return Metadata{}, strings.Join(lines, "\n"),
			[]string{`no frontmatter block: the file must start with a "` + frontmatterDelim + `" line`}, nil
	}

	end, closed := frontmatterEnd(lines, open)
	if !closed {
		// The scan stops either at the end of the file or at its own bound, and
		// the two say different things to the author: only one of them is a
		// block the file never closes.
		bounded := len(lines) > open+maxFrontmatterLines
		return Metadata{}, "", nil, unclosedFrontmatter(open, bounded)
	}

	meta, problems, err := parseManifestBlock(lines[open+1:end], open+2)
	if err != nil {
		return Metadata{}, "", problems, err
	}
	body := strings.TrimPrefix(strings.Join(lines[end+1:], "\n"), "\n")

	if meta.Name == "" {
		return meta, body, problems, fmt.Errorf("name is required")
	}
	if err := validateName(meta.Name); err != nil {
		return meta, body, problems, fmt.Errorf("invalid name: %w", err)
	}
	if meta.Description == "" {
		return meta, body, problems, fmt.Errorf("description is required")
	}
	if err := validateDescription(meta.Description); err != nil {
		return meta, body, problems, fmt.Errorf("invalid description: %w", err)
	}

	return meta, body, problems, nil
}

// splitLines normalizes line endings and a leading byte-order mark so the rest
// of the reader can compare lines exactly. A manifest saved by a Windows
// editor is the same manifest.
func splitLines(content string) []string {
	content = strings.TrimPrefix(content, "\ufeff")
	content = strings.ReplaceAll(content, "\r\n", "\n")
	return strings.Split(strings.TrimSuffix(content, "\r"), "\n")
}

// frontmatterOpen returns the index of the opening delimiter, or -1. Only the
// first non-blank line can open the block: a "---" further down is markdown.
func frontmatterOpen(lines []string) int {
	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if strings.TrimSpace(line) == frontmatterDelim {
			return i
		}
		return -1
	}
	return -1
}

// frontmatterEnd returns the index of the line closing the block that opens at
// start, or -1 when no such line is found within maxFrontmatterLines of it. The
// bool says whether one was found; unclosedFrontmatter is where the two ways a
// search can come up empty are told apart.
//
// The closing delimiter is a line of its own, at the start of the line. An
// indented "---" is content — a line of the literal or folded block above it —
// and indentation is already what decides ownership everywhere else in this
// reader, so it decides here too. Comparing with the line trimmed instead closed
// a `description: |` at its own rule, cutting the value short and leaking the
// rest of the manifest into the body with no error anywhere.
func frontmatterEnd(lines []string, start int) (int, bool) {
	limit := start + maxFrontmatterLines
	if limit > len(lines) {
		limit = len(lines)
	}
	for i := start + 1; i < limit; i++ {
		switch strings.TrimRight(lines[i], " \t") {
		case frontmatterDelim, "...":
			return i, true
		}
	}
	return -1, false
}

// parseManifestBlock reads the lines between the delimiters. firstLine is the
// 1-indexed file line number of block[0], so every problem can name its line.
//
// An error is returned when the block turns out not to be one: a line that can
// only belong to the markdown body means the closing delimiter is missing, and
// guessing where the manifest ends is how a heading used to be folded into the
// description while the body was discarded.
func parseManifestBlock(block []string, firstLine int) (Metadata, []string, error) {
	var meta Metadata
	var problems []string
	seen := make(map[string]bool, len(block))
	blankSeen := false

	for i := 0; i < len(block); {
		lineNo := firstLine + i
		raw := block[i]
		i++

		text := strings.TrimSpace(raw)
		if text == "" {
			blankSeen = true
			continue
		}
		if strings.HasPrefix(text, "#") {
			// A comment inside the block is a comment. The same line after a
			// blank line is a markdown heading, which is what a missing
			// closing "---" leaves behind.
			if blankSeen {
				return meta, problems, badManifestLine(lineNo)
			}
			continue
		}
		if isContinuation(raw) {
			problems = append(problems, fmt.Sprintf("line %d: unexpected indentation, line ignored: %s", lineNo, text))
			continue
		}

		key, rest, ok := splitKeyValue(text)
		if !ok {
			return meta, problems, badManifestLine(lineNo)
		}
		blankSeen = false

		if seen[key] {
			problems = append(problems, fmt.Sprintf("line %d: duplicate key %q: the first value stands", lineNo, key))
			i = skipEntry(block, i)
			continue
		}
		seen[key] = true

		switch {
		case isScalarField(key):
			var value string
			value, i = parseScalar(rest, block, i, lineNo, key, &problems)
			assignScalar(&meta, key, value)
		default:
			// Every other key is read past whole, not complained about: the
			// manifest format grows new fields, and a field this build does not
			// use must not cost the user the skill.
			//
			// That is the treatment the spec's `metadata` gets, and the reason it
			// is not a case of its own: it is free-form and nothing here reads
			// it, so there is no reading of it that can be right. Recording the
			// one level a scalar map could hold would hand back structure the
			// author did not write — a `requires:` that held a map came back as
			// `requires: ""` — and complaining about the rest made a valid
			// manifest, nested or flat, print an error at startup.
			i = skipEntry(block, i)
		}
	}

	return meta, problems, nil
}

// unclosedFrontmatter reports a block the reader found no closing delimiter for.
//
// There are two ways that happens and the reader knows which: the scan reached
// the end of the file, or it stopped at its own bound. "Never closed" is false
// for the second — the file may close the block on the line after the search
// stopped — and naming the bound for the first tells a five-line manifest about
// a line it never came near.
func unclosedFrontmatter(open int, bounded bool) error {
	if bounded {
		return fmt.Errorf(`frontmatter block opened on line %d has no closing "`+frontmatterDelim+`" line in the next %d lines`, open+1, maxFrontmatterLines)
	}
	return fmt.Errorf(`frontmatter block opened on line %d is never closed by a "`+frontmatterDelim+`" line`, open+1)
}

// badManifestLine reports the line that cannot belong to a frontmatter block.
//
// It is most often the symptom of a deleted closing delimiter — the reader is
// looking at markdown that was meant to be the body — but the reader cannot know
// that, and neither can the user, so the message says what was found rather than
// claiming the delimiter is missing when the block in fact does close further
// down. The fix is the same either way, and it is named.
func badManifestLine(lineNo int) error {
	return fmt.Errorf(`line %d is neither a "key: value" entry, a comment nor a blank; a "`+frontmatterDelim+`" line must close the block before it`, lineNo)
}

// isScalarField reports whether a manifest key holds a string this reader
// stores. A key outside the set is skipped whole: an unknown field must not
// cost the user the skill.
func isScalarField(key string) bool {
	switch key {
	case "name", "description", "license", "compatibility":
		return true
	}
	return false
}

func assignScalar(meta *Metadata, key, value string) {
	switch key {
	case "name":
		meta.Name = value
	case "description":
		meta.Description = value
	case "license":
		meta.License = value
	case "compatibility":
		meta.Compatibility = value
	}
}

// parseScalar resolves one field's value and returns the cursor after every
// line the value consumed.
func parseScalar(rest string, block []string, i, lineNo int, key string, problems *[]string) (string, int) {
	body, next := collectContinuation(block, i)

	switch {
	case rest == "":
		// A bare key over an indented block is a nested mapping in YAML. Every
		// field the reader knows is a string, so the block is folded into the
		// value rather than discarded: the model still gets the text.
		return foldScalar(body), next

	case rest[0] == '|' || rest[0] == '>':
		style, chomp, explicit, isHeader := blockHeader(rest)
		if !isHeader {
			// A "|" or ">" that does not head a block — `description: > 5
			// items` — is a plain value. Reading it as an empty block cost the
			// skill its description and said nothing about why.
			return foldScalar(append([]string{rest}, body...)), next
		}
		if len(body) == 0 {
			*problems = append(*problems, fmt.Sprintf("line %d: key %q declares a block scalar with no indented content", lineNo, key))
		}
		indent := blockIndent(body, explicit)
		if k := shortIndentedLine(body, explicit); k >= 0 {
			*problems = append(*problems, fmt.Sprintf(
				"line %d: key %q declares indentation %d but this line is indented %d, so the block is read from the line's own indentation",
				lineNo+1+k, key, explicit, indentationOf(body[k])))
		}
		var text string
		if style == '|' {
			text = literalScalar(body, indent)
		} else {
			text = foldedBlock(body, indent)
		}
		return chompBlock(text, chomp, countBlanks(block, next)), next

	case isQuote(rest[0]):
		if !closesQuote(rest) {
			*problems = append(*problems, fmt.Sprintf("line %d: key %q opens a quoted value that never closes on its line", lineNo, key))
		}
		value := unquote(rest)
		if len(body) == 0 {
			return value, next
		}
		return strings.TrimRight(value, " ") + " " + foldScalar(body), next

	default:
		return foldScalar(append([]string{rest}, body...)), next
	}
}

// blockHeader reads a block scalar header: "|" or ">", then an optional
// indentation indicator (1-9) and an optional chomping indicator ("-" strip,
// "+" keep) in either order, then optional spaces and an optional comment. It
// returns the style, the chomping byte (0 for the default, clip) and the
// explicit indentation (0 when the header carried none).
//
// The header must end there. YAML's three chomping modes are honored rather than
// approximated, because approximating them is a silent rewrite of the value:
// "|+" keeps the blank lines the author left, and "|" clips to one break.
func blockHeader(rest string) (style, chomp byte, indent int, ok bool) {
	if rest == "" || (rest[0] != '|' && rest[0] != '>') {
		return 0, 0, 0, false
	}
	style = rest[0]

	indicators := rest[1:]
	if i := strings.IndexByte(indicators, '#'); i >= 0 {
		// A comment follows whitespace, as it does anywhere else in YAML.
		if i > 0 && indicators[i-1] != ' ' && indicators[i-1] != '\t' {
			return 0, 0, 0, false
		}
		indicators = indicators[:i]
	}
	for _, c := range strings.Trim(indicators, " \t") {
		switch {
		case c == '-' || c == '+':
			if chomp != 0 {
				return 0, 0, 0, false
			}
			chomp = byte(c)
		case c >= '1' && c <= '9':
			if indent != 0 {
				return 0, 0, 0, false
			}
			indent = int(c - '0')
		default:
			return 0, 0, 0, false
		}
	}
	return style, chomp, indent, true
}

// blockIndent returns the indentation the block scalar's content shares: the
// header's explicit indicator when it carried one, otherwise the smallest
// indentation among the body's non-blank lines.
func blockIndent(body []string, explicit int) int {
	if explicit > 0 {
		return explicit
	}
	indent := -1
	for _, line := range body {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if n := indentationOf(line); indent < 0 || n < indent {
			indent = n
		}
	}
	if indent < 0 {
		return 0
	}
	return indent
}

// cutIndent removes the block's indentation from one line, never more than the
// whitespace the line actually starts with. An explicit indicator can name an
// indentation the content does not reach, and cutting the count it asked for
// took characters out of the text: `|4` over two-space lines read `one` as `e`.
func cutIndent(line string, indent int) string {
	if strings.TrimSpace(line) == "" {
		return ""
	}
	n := indentationOf(line)
	if n > indent {
		n = indent
	}
	return line[n:]
}

// shortIndentedLine returns the index of the first body line indented less than
// the header's explicit indentation, or -1. YAML rejects such a block outright;
// this reader keeps the author's text instead, and the caller names the line so
// the difference is visible rather than silently reinterpreted.
func shortIndentedLine(body []string, explicit int) int {
	if explicit <= 0 {
		return -1
	}
	for k, line := range body {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if indentationOf(line) < explicit {
			return k
		}
	}
	return -1
}

// literalScalar keeps the lines' breaks, after cutting the indentation the block
// shares.
func literalScalar(body []string, indent int) string {
	out := make([]string, len(body))
	for k, line := range body {
		out[k] = cutIndent(line, indent)
	}
	return strings.Join(out, "\n")
}

// foldedBlock joins lines the way a folded YAML scalar does. A break between two
// lines at the block's own indentation folds to a space; a break is kept — as
// many breaks as there are empty lines between the two, plus one when either
// side is indented deeper than the block — when that is not the case.
//
// The deeper line is why: it is content, a run of it is not a continuation of
// the sentence above it, and joining it rewrote the author's text.
func foldedBlock(body []string, indent int) string {
	var sb strings.Builder
	blanks := 0
	prevMore := false
	started := false
	for _, line := range body {
		if strings.TrimSpace(line) == "" {
			blanks++
			continue
		}
		more := indentationOf(line) > indent
		switch {
		case !started && blanks > 0:
			sb.WriteString(strings.Repeat("\n", blanks))
		case started && (blanks > 0 || more || prevMore):
			breaks := blanks
			if more || prevMore {
				breaks++
			}
			sb.WriteString(strings.Repeat("\n", breaks))
		case started:
			sb.WriteString(" ")
		}
		sb.WriteString(cutIndent(line, indent))
		prevMore = more
		blanks = 0
		started = true
	}
	return sb.String()
}

// countBlanks counts the blank lines a block scalar left for the entry below it.
// The reader keeps them out of the body so that a heading after them is still
// seen as body; a "+" header is the one place they belong to the value, which is
// why they are counted here instead of being consumed.
func countBlanks(block []string, next int) int {
	n := 0
	for j := next; j < len(block) && strings.TrimSpace(block[j]) == ""; j++ {
		n++
	}
	return n
}

// chompBlock applies a block scalar's chomping indicator: "-" strips the
// trailing break, "+" keeps the blank lines the author left, and the default
// clips to exactly one break. YAML's three modes, not an approximation of them.
func chompBlock(text string, chomp byte, blanks int) string {
	if text == "" {
		return ""
	}
	switch chomp {
	case '-':
		return text
	case '+':
		return text + strings.Repeat("\n", 1+blanks)
	default:
		return text + "\n"
	}
}

// collectContinuation gathers the lines owned by the entry above the cursor:
// indented lines and the blank lines between them, stopping at the next
// top-level entry. The cursor it returns sits on the first line the entry does
// not own, so the blank line separating this entry from the next is left for the
// caller rather than skipped here — which is what the block's comment rule rests
// on, since a "#" line is a comment only while no blank precedes it. Advancing
// past that trailing blank hid it, so a markdown heading admitted by a deleted
// closing "---" was read as a comment and the text under it left the body with
// no error.
func collectContinuation(block []string, i int) ([]string, int) {
	start := i
	end := i
	for i < len(block) {
		if strings.TrimSpace(block[i]) == "" {
			// Hold the blank tentatively: it belongs to the entry only if
			// indented content follows it.
			i++
			continue
		}
		if !isContinuation(block[i]) {
			break
		}
		i++
		end = i
	}
	return block[start:end], end
}

// skipEntry moves the cursor past the continuation lines of an entry whose
// value is not read.
func skipEntry(block []string, i int) int {
	_, next := collectContinuation(block, i)
	return next
}

// foldScalar joins the lines of a plain value — one continued on indented lines,
// or a bare key over the indented map YAML would have nested — with one space
// between them and one newline where the author left a blank line. A block
// scalar's `>` is not this: see foldedBlock.
func foldScalar(body []string) string {
	var sb strings.Builder
	pendingNewline := false
	for _, line := range body {
		text := strings.TrimSpace(line)
		if text == "" {
			pendingNewline = true
			continue
		}
		if sb.Len() > 0 {
			if pendingNewline {
				sb.WriteString("\n")
			} else {
				sb.WriteString(" ")
			}
		}
		sb.WriteString(text)
		pendingNewline = false
	}
	return sb.String()
}

// splitKeyValue cuts an entry at the FIRST colon. Requiring the key to look
// like a key is what lets the value keep its colons unquoted.
func splitKeyValue(text string) (key, value string, ok bool) {
	i := strings.IndexByte(text, ':')
	if i <= 0 {
		return "", "", false
	}
	key = text[:i]
	if !validManifestKey(key) {
		return "", "", false
	}
	return key, strings.TrimSpace(text[i+1:]), true
}

// validManifestKey accepts the keys the manifest format uses: a leading letter
// or underscore, then letters, digits, "-" and "_" (kebab-case keys such as
// "argument-hint" are part of the format).
func validManifestKey(key string) bool {
	for i := 0; i < len(key); i++ {
		c := key[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c == '_':
		case c >= '0' && c <= '9', c == '-':
			if i == 0 {
				return false
			}
		default:
			return false
		}
	}
	return key != ""
}

func isContinuation(line string) bool {
	return strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t")
}

func indentationOf(line string) int {
	return len(line) - len(strings.TrimLeft(line, " \t"))
}

func isQuote(c byte) bool { return c == '"' || c == '\'' }

// closesQuote reports whether a quoted value ends on its own line.
func closesQuote(value string) bool {
	if len(value) < 2 {
		return false
	}
	last := value[len(value)-1]
	return isQuote(last) && last == value[0]
}

// unquote removes one layer of surrounding quotes, resolving the escapes a
// double-quoted value can carry. A value that is not quoted is returned
// unchanged, so an unterminated quote costs nothing but its first character.
func unquote(value string) string {
	if !closesQuote(value) {
		return value
	}
	body := value[1 : len(value)-1]
	if value[0] == '\'' {
		return strings.ReplaceAll(body, "''", "'")
	}
	return strings.NewReplacer(`\n`, "\n", `\t`, "\t", `\"`, `"`, `\\`, `\`).Replace(body)
}

// validateName validates the skill name according to spec. The charset is
// ASCII-only, so counting bytes and counting characters agree; the count is
// still taken in characters to match how the limit is written.
func validateName(name string) error {
	if n := utf8.RuneCountInString(name); n < 1 || n > 64 {
		return fmt.Errorf("name must be 1-64 characters")
	}

	// Must be lowercase letters, numbers, and hyphens only
	for _, c := range name {
		if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-') {
			return fmt.Errorf("name must contain only lowercase letters, numbers, and hyphens")
		}
	}

	// Must not start or end with hyphen
	if strings.HasPrefix(name, "-") || strings.HasSuffix(name, "-") {
		return fmt.Errorf("name must not start or end with hyphen")
	}

	// Must not contain consecutive hyphens
	if strings.Contains(name, "--") {
		return fmt.Errorf("name must not contain consecutive hyphens")
	}

	return nil
}

// validateDescription validates the skill description according to spec, which
// counts the 1-1024 limit in characters. Counting bytes instead caps a Chinese
// or accented description at ~341 characters and rejects a perfectly good
// manifest, so the count is taken over runes.
func validateDescription(desc string) error {
	if n := utf8.RuneCountInString(desc); n < 1 || n > maxDescriptionRunes {
		return fmt.Errorf("description must be 1-1024 characters")
	}
	return nil
}
