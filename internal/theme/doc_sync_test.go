package theme

import (
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// docPath is the user-facing configuration document. It enumerates the theme
// keys twice — the sample file and the color-role table — and nothing kept
// either (or the bundled .conf files) in step with the Theme struct: adding
// or removing a field means remembering four places, one of which is a
// comment-free sample users copy. The check below makes that automatic.
const docPath = "../../docs/configuration.md"

// TestThemeKeysMatchDocs asserts the three places that enumerate theme keys
// agree: the Theme struct's `config` tags, the bundled theme files, and
// docs/configuration.md (sample block + color-role table).
func TestThemeKeysMatchDocs(t *testing.T) {
	structKeys := themeKeys()
	if len(structKeys) == 0 {
		t.Fatal("no `config` keys found on Theme — the reflection helper broke, not the theme")
	}

	doc, err := os.ReadFile(docPath)
	if err != nil {
		t.Fatalf("read %s: %v", docPath, err)
	}
	text := string(doc)

	sample := sampleThemeKeys(t, text)
	roles := colorRoleKeys(t, text)
	bundled := bundledThemeKeys(t)

	// The three enumerations must be the same set as the struct's — a key
	// that exists in only one of them is either a stale document or an
	// undocumented field.
	for _, want := range []struct {
		name string
		keys []string
	}{
		{"the sample block in " + docPath, sample},
		{"the Color Roles table in " + docPath, roles},
		{"the bundled theme files", bundled},
	} {
		if diff := keyDiff(structKeys, want.keys); diff != "" {
			t.Errorf("theme keys in %s disagree with the Theme struct: %s", want.name, diff)
		}
	}
}

// themeKeys returns the `config` tag names of Theme's fields.
func themeKeys() []string {
	t := reflect.TypeOf(Theme{})
	keys := make([]string, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		if key, _, _ := strings.Cut(t.Field(i).Tag.Get("config"), ","); key != "" && key != "-" {
			keys = append(keys, key)
		}
	}
	return keys
}

// sampleThemeKeys reads the key names out of the fenced theme file under
// "### Theme File Format".
func sampleThemeKeys(t *testing.T, doc string) []string {
	t.Helper()
	block := fencedBlock(t, doc, "### Theme File Format")
	return keysInBlock(block)
}

// colorRoleKeys reads the first column of the "### Color Roles" table,
// whose rows are "| `key` | description |".
func colorRoleKeys(t *testing.T, doc string) []string {
	t.Helper()
	start := strings.Index(doc, "### Color Roles")
	if start < 0 {
		t.Fatal(`docs/configuration.md has no "### Color Roles" section`)
	}
	var keys []string
	for _, line := range strings.Split(doc[start:], "\n") {
		if strings.HasPrefix(line, "##") && !strings.HasPrefix(line, "### Color") {
			break // next section
		}
		first, _, ok := strings.Cut(strings.TrimPrefix(line, "|"), "|")
		if !ok {
			continue
		}
		first = strings.TrimSpace(first)
		if len(first) > 2 && strings.HasPrefix(first, "`") && strings.HasSuffix(first, "`") {
			keys = append(keys, strings.Trim(first, "`"))
		}
	}
	if len(keys) == 0 {
		t.Fatal("no `key` rows found in the Color Roles table")
	}
	return keys
}

// bundledThemeKeys checks the embedded files carry one identical key set —
// the themes users get on first run must not differ from each other.
func bundledThemeKeys(t *testing.T) []string {
	t.Helper()
	dark := keysInBlock(darkThemeContent)
	light := keysInBlock(lightThemeContent)
	sort.Strings(dark)
	sort.Strings(light)
	if len(dark) == 0 {
		t.Fatal("the bundled dark theme carries no keys")
	}
	if strings.Join(dark, ",") != strings.Join(light, ",") {
		t.Errorf("light.conf key set differs from dark.conf: %v vs %v", dark, light)
	}
	return dark
}

// fencedBlock returns the first fenced code block at or after the section
// marker, excluding the fences.
func fencedBlock(t *testing.T, doc, marker string) string {
	t.Helper()
	i := strings.Index(doc, marker)
	if i < 0 {
		t.Fatalf("docs/configuration.md has no %q section", marker)
	}
	rest := doc[i+len(marker):]
	open := strings.Index(rest, "```")
	if open < 0 {
		t.Fatalf("no fenced block after %q", marker)
	}
	rest = rest[open+3:]
	end := strings.Index(rest, "```")
	if end < 0 {
		t.Fatalf("unterminated fenced block after %q", marker)
	}
	return rest[:end]
}

// keysInBlock collects the "key: value" names of a theme-file body,
// skipping comments and the path line.
func keysInBlock(block string) []string {
	lines := strings.Split(block, "\n")
	keys := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, _, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		keys = append(keys, strings.TrimSpace(key))
	}
	return keys
}

// keyDiff reports what set(keys) and set(structKeys) disagree on, or "".
func keyDiff(structKeys, keys []string) string {
	known := make(map[string]bool, len(structKeys))
	for _, k := range structKeys {
		known[k] = true
	}
	present := make(map[string]bool, len(keys))
	var problems []string
	for _, k := range keys {
		present[k] = true
		if !known[k] {
			problems = append(problems, "documents/ships "+k+", which Theme has no field for")
		}
	}
	for _, k := range structKeys {
		if !present[k] {
			problems = append(problems, "Theme has "+k+", which is not documented/shipped")
		}
	}
	return strings.Join(problems, "; ")
}
