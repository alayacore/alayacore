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
	// search is bounded so a deleted closing "---" is reported rather than
	// swallowing the markdown body — a "---" under a heading is a horizontal
	// rule, not a delimiter.
	maxFrontmatterLines = 200
)

// maxDescriptionRunes is the spec's description limit, counted in characters.
const maxDescriptionRunes = 1024

// ParseSkillMarkdown reads a SKILL.md file and returns its frontmatter
// metadata, its markdown body, and the problems found while reading.
//
// The error is reserved for a manifest that cannot be honored at all: a
// missing or unclosed frontmatter block, an absent or malformed name, an absent
// or overlong description. Everything else — an unparseable line, a duplicate
// key, a nested metadata map — is returned in problems and leaves the rest of
// the file readable. The loader surfaces both, so no form of input is dropped
// or altered without saying where and why.
//
// The block is read with the project's key-value format (see
// config.ParseKeyValue), not a general YAML parser, because the two disagree
// exactly where a manifest must not be guessed at:
//
//   - `description: Use this skill when: the user asks about PDFs` is invalid
//     YAML ("mapping values are not allowed here"), so the whole skill
//     disappears; here the value is the rest of the line, as written.
//   - `description: Count # of items` ends at the " #" for YAML, so the skill
//     is advertised to the model as "Count" with no error raised; here "#"
//     only starts a comment at the beginning of a line.
//
// Values may still be quoted, folded (`>`), literal (`|`) or continued on
// indented lines, which is all of YAML the manifest format needs.
func ParseSkillMarkdown(content string) (Metadata, string, []string, error) {
	lines := splitLines(content)

	open := frontmatterOpen(lines)
	if open < 0 {
		return Metadata{}, strings.Join(lines, "\n"),
			[]string{`no frontmatter block: the file must start with a "` + frontmatterDelim + `" line`}, nil
	}

	end, closed := frontmatterEnd(lines, open)
	if !closed {
		return Metadata{}, "", nil, fmt.Errorf(
			`frontmatter block opened on line %d is never closed by a "`+frontmatterDelim+`" line`, open+1)
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
// start, or -1 when the block is never closed within its own bounds.
func frontmatterEnd(lines []string, start int) (int, bool) {
	limit := start + maxFrontmatterLines
	if limit > len(lines) {
		limit = len(lines)
	}
	for i := start + 1; i < limit; i++ {
		switch strings.TrimSpace(lines[i]) {
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
				return meta, problems, unclosedFrontmatter(lineNo)
			}
			continue
		}
		if isContinuation(raw) {
			problems = append(problems, fmt.Sprintf("line %d: unexpected indentation, line ignored: %s", lineNo, text))
			continue
		}

		key, rest, ok := splitKeyValue(text)
		if !ok {
			return meta, problems, unclosedFrontmatter(lineNo)
		}
		blankSeen = false

		if seen[key] {
			problems = append(problems, fmt.Sprintf("line %d: duplicate key %q: the first value stands", lineNo, key))
			i = skipEntry(block, i)
			continue
		}
		seen[key] = true

		switch {
		case key == "metadata":
			meta.Metadata, i = parseStringMap(block, i, lineNo, &problems)
		case isScalarField(key):
			var value string
			value, i = parseScalar(rest, block, i, lineNo, key, &problems)
			assignScalar(&meta, key, value)
		default:
			// An unrecognized key is read past, not complained about: the
			// manifest format grows new fields, and a field this build does
			// not know must not cost the user the skill.
			i = skipEntry(block, i)
		}
	}

	return meta, problems, nil
}

// unclosedFrontmatter reports the line that gave the manifest away.
func unclosedFrontmatter(lineNo int) error {
	return fmt.Errorf(`frontmatter block is not closed: line %d is neither an entry nor a closing "`+frontmatterDelim+`" line`, lineNo)
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
		if len(body) == 0 {
			*problems = append(*problems, fmt.Sprintf("line %d: key %q declares a block scalar with no indented content", lineNo, key))
		}
		if rest[0] == '|' {
			return literalScalar(body), next
		}
		return foldScalar(body), next

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

// parseStringMap reads the one-level map under a "metadata:" key. Anything
// deeper is reported and skipped: the field is informational, and a nested map
// must not cost the file its name and description the way a YAML parse failure
// used to.
func parseStringMap(block []string, i, lineNo int, problems *[]string) (map[string]string, int) {
	body, next := collectContinuation(block, i)
	if len(body) == 0 {
		return nil, next
	}

	out := make(map[string]string, len(body))
	base := -1
	nestedReported := false
	for j, line := range body {
		text := strings.TrimSpace(line)
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		indent := indentationOf(line)
		if base < 0 {
			base = indent
		}
		if indent > base {
			if !nestedReported {
				*problems = append(*problems, fmt.Sprintf("line %d: nested entries under %q are not read", lineNo+j, "metadata"))
				nestedReported = true
			}
			continue
		}
		key, value, ok := splitKeyValue(text)
		if !ok {
			*problems = append(*problems, fmt.Sprintf("line %d: not a key: value entry under %q, skipped: %s", lineNo+j, "metadata", text))
			continue
		}
		out[key] = unquote(value)
	}
	if len(out) == 0 {
		return nil, next
	}
	return out, next
}

// collectContinuation gathers the lines owned by the entry above the cursor:
// indented lines and the blank lines between them, stopping at the next
// top-level entry. Trailing blank lines are left behind — they separate this
// entry from the next, they are not part of its value.
func collectContinuation(block []string, i int) ([]string, int) {
	start := i
	for i < len(block) {
		if strings.TrimSpace(block[i]) == "" {
			i++
			continue
		}
		if !isContinuation(block[i]) {
			break
		}
		i++
	}
	body := block[start:i]
	for len(body) > 0 && strings.TrimSpace(body[len(body)-1]) == "" {
		body = body[:len(body)-1]
	}
	return body, i
}

// skipEntry moves the cursor past the continuation lines of an entry whose
// value is not read.
func skipEntry(block []string, i int) int {
	_, next := collectContinuation(block, i)
	return next
}

// foldScalar joins lines the way a folded YAML scalar does: one space between
// continued lines, one newline where the author left a blank line.
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

// literalScalar joins lines keeping their breaks, after cutting the indentation
// every line shares.
func literalScalar(body []string) string {
	indent := -1
	for _, line := range body {
		if strings.TrimSpace(line) == "" {
			continue
		}
		n := indentationOf(line)
		if indent < 0 || n < indent {
			indent = n
		}
	}
	out := make([]string, len(body))
	for k, line := range body {
		switch {
		case strings.TrimSpace(line) == "":
			out[k] = ""
		case len(line) >= indent:
			out[k] = line[indent:]
		default:
			out[k] = strings.TrimLeft(line, " \t")
		}
	}
	return strings.Join(out, "\n")
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
