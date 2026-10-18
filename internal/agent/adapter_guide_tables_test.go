package agent

// The protocol reference is what an adapter is implemented from, and nothing in
// the build read it: a command renamed in Go left the guide's table saying the
// old name, and a struct field renamed in Go left the guide's schema table
// saying the old key. Both were found by hand, twice. These checks compare the
// tables to the declarations, so the next one is found by `go test`.
//
// What is checked, and why it is exact:
//
//   - A command name in either table must be in the vocabulary (commands.Defs),
//     and that table is itself pinned to the session's handler table by
//     TestCommandVocabularyMatchesTheRegistry — so this compares the docs to the
//     declarations rather than to a list kept here.
//   - The arguments in docs/commands.md's command cell must equal the
//     vocabulary's usage string, character for character once the table's `\|`
//     is unescaped.
//   - An SM type in the guide's table must be one protocol declares.
//   - A field name in an SM table's schema column must be a JSON key of that
//     message's payload. "Field name" is exact: a backticked identifier
//     immediately followed by " (" — which is how the table writes a field's
//     type. Values inside those parentheses (`ready`, `queued`), nested object
//     keys (`{id:int, ...}`) and command names mentioned in prose are not
//     followed by " (", so they are not fields and are not checked here. The
//     nested model_list object is covered by
//     TestModelListSampleMatchesModelInfo, which compares the sample to the
//     struct itself.
//   - Every .txt named in the guide's sample column must exist.

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/commands"
	"github.com/alayacore/alayacore/internal/protocol"
)

// smPayloads maps an SM type to the payload struct that carries it, so the
// schema table can be checked against the real JSON tags. A type with no entry
// fails the test rather than being skipped: a new SM type must state its
// payload here, or its table row goes unchecked.
func smPayloads() map[string]any {
	return map[string]any{
		string(protocol.MsgTypeVersion):        messageVersionMsg{},
		string(protocol.MsgTypeModel):          modelMsg{},
		string(protocol.MsgTypeModelList):      modelListMsg{},
		string(protocol.MsgTypeTheme):          themeMsg{},
		string(protocol.MsgTypeThemeList):      themeListMsg{},
		string(protocol.MsgTypeReasoningLevel): reasoningMsg{},
		string(protocol.MsgTypeVideoConfig):    videoConfigMsg{},
		string(protocol.MsgTypeTask):           taskMsg{},
		string(protocol.MsgTypeError):          protocol.ErrorMsg{},
		string(protocol.MsgTypeNotify):         protocol.NotifyMsg{},
		string(protocol.MsgTypeToolConfirm):    protocol.ToolConfirmMsg{},
		string(protocol.MsgTypeMCP):            mcpMsg{},
		string(protocol.MsgTypeSession):        sessionMsg{},
		string(protocol.MsgTypeSteering):       steeringMsg{},
	}
}

// jsonTags returns the JSON keys a payload struct declares.
func jsonTags(v any) map[string]bool {
	tags := map[string]bool{}
	t := reflect.TypeOf(v)
	for i := 0; i < t.NumField(); i++ {
		tag := t.Field(i).Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		name, _, _ := strings.Cut(tag, ",")
		if name != "" && name != "-" {
			tags[name] = true
		}
	}
	return tags
}

// dispatchedCommands is the command names, taken from the vocabulary table
// (commands.Defs): every name an adapter may send and a help list may show.
// TestCommandVocabularyMatchesTheRegistry pins that table to the handler table.
func dispatchedCommands(t *testing.T) map[string]bool {
	t.Helper()
	known := map[string]bool{}
	for _, d := range commands.Defs {
		known[d.Name] = true
	}
	return known
}

// TestCommandVocabularyMatchesTheRegistry is the link between the two halves of
// a command: commands.Defs holds the name, the description and the usage; the
// agent's registry holds the handler and its policy. A name in one and not the
// other is a command that cannot run, or a handler nothing can reach.
//
// The two task commands are the one allowed difference: handleInputMsg starts
// them before it reaches the registry, so they have no handler entry. The set is
// asserted exactly, which is what makes a third one visible here.
func TestCommandVocabularyMatchesTheRegistry(t *testing.T) {
	inRegistry := map[string]bool{}
	for _, c := range defaultCommandDefs {
		inRegistry[c.Name] = true
		if _, ok := commands.DefinitionOf(c.Name); !ok {
			t.Errorf("the registry handles %q, which commands.Defs does not define", c.Name)
		}
	}

	unhandled := map[string]bool{}
	for _, d := range commands.Defs {
		if !inRegistry[d.Name] {
			unhandled[d.Name] = true
		}
	}
	for _, name := range []string{commands.CommandNameContinue, commands.CommandNameSummarize} {
		if !unhandled[name] {
			t.Errorf("%q is dispatched before the registry, so it must have no handler entry", name)
		}
		delete(unhandled, name)
	}
	for name := range unhandled {
		t.Errorf("%q is in commands.Defs with no handler in the registry", name)
	}
}

// tableAfter returns the rows of the Markdown table that follows the first line
// containing header, up to the first blank line. Failing to find it is an error:
// a silently missing table would silently stop checking.
func tableAfter(t *testing.T, text, header string) []string {
	t.Helper()
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if !strings.Contains(line, header) {
			continue
		}
		var rows []string
		for _, row := range lines[i+1:] {
			if !strings.HasPrefix(row, "|") {
				break
			}
			if strings.HasPrefix(row, "|---") || strings.Contains(row, "---|--") {
				continue
			}
			rows = append(rows, row)
		}
		if len(rows) == 0 {
			t.Fatalf("the table after %q has no rows", header)
		}
		return rows
	}
	t.Fatalf("no table after %q — the check cannot run, so the doc must have moved", header)
	return nil
}

// cells splits a table row on its cell separators. A `\|` inside a cell is an
// escaped pipe — Markdown's way of putting an alternation in a table — so it is
// protected before the split and restored after.
func cells(row string) []string {
	const escaped = "\x00pipe\x00"
	row = strings.ReplaceAll(row, `\|`, escaped)
	parts := strings.Split(strings.Trim(row, "|"), "|")
	for i := range parts {
		parts[i] = strings.TrimSpace(strings.ReplaceAll(parts[i], escaped, `\|`))
	}
	return parts
}

func readDoc(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// TestDocsCommandsTableMatchesTheRegistry checks docs/commands.md: every command
// it lists is dispatched, and its arguments are the registry's usage string.
func TestDocsCommandsTableMatchesTheRegistry(t *testing.T) {
	known := dispatchedCommands(t)
	usage := map[string]string{}
	for _, d := range commands.Defs {
		usage[d.Name] = d.Usage
	}

	doc := readDoc(t, "../../docs/commands.md")
	// The page splits its commands across two tables (the ones an adapter
	// intercepts, and the configuration ones), so every command-shaped row in
	// the file is checked rather than one table.
	rowPattern := regexp.MustCompile("^\\| `:([a-z_]+)([^`]*)`")
	checked := 0
	documented := map[string]bool{}
	for _, row := range strings.Split(doc, "\n") {
		m := rowPattern.FindStringSubmatch(row)
		if m == nil {
			continue
		}
		// A command-table row has three cells (command, action, CO result). The
		// page's other table has five — it says what each adapter does with a
		// command, and its `:help`/`:suspend` rows are TUI-local controls with no
		// session command behind them.
		if n := len(cells(row)); n != 3 {
			continue
		}
		name, args := m[1], strings.TrimSpace(m[2])
		// The table escapes the alternation's pipe so Markdown does not split the
		// cell; the registry writes it plainly.
		args = strings.ReplaceAll(args, `\|`, "|")
		canonical := commands.Canonical(name)
		if !known[canonical] {
			t.Errorf("docs/commands.md lists :%s, which the session does not dispatch", name)
			continue
		}
		if want := usage[canonical]; args != want {
			t.Errorf("docs/commands.md :%s says arguments %q; the registry says %q", name, args, want)
		}
		documented[canonical] = true
		checked++
	}
	for name := range known {
		if !documented[name] {
			t.Errorf("docs/commands.md does not document :%s", name)
		}
	}
	if checked == 0 {
		t.Error("no command rows were checked — the table's shape changed, so the check went quiet")
	}
}

// TestAdapterGuideCommandTableMatchesTheRegistry checks the guide's full command
// list, where a name is a CI `name` value and carries no colon.
func TestAdapterGuideCommandTableMatchesTheRegistry(t *testing.T) {
	known := dispatchedCommands(t)
	guide := readDoc(t, "../../adapter-guide/README.md")

	rowPattern := regexp.MustCompile("^\\| `([a-z_]+)`(?:\\s*/\\s*`([a-z_]+)`)?\\s*\\|")
	checked := 0
	for _, row := range tableAfter(t, guide, "| Command | args | CO result (success) |") {
		m := rowPattern.FindStringSubmatch(row)
		if m == nil {
			continue
		}
		for _, name := range []string{m[1], m[2]} {
			if name == "" {
				continue
			}
			if !known[commands.Canonical(name)] {
				t.Errorf("adapter-guide lists %q, which the session does not dispatch", name)
			}
			checked++
		}
	}
	if checked < len(defaultCommandDefs) {
		t.Errorf("adapter-guide documents %d commands; the registry has %d", checked, len(defaultCommandDefs))
	}
}

// TestAdapterGuideSMTablesMatchTheProtocol checks the system-message table: the
// type is one protocol declares, every field is a JSON key of its payload, and
// every sample it names exists.
func TestAdapterGuideSMTablesMatchTheProtocol(t *testing.T) {
	guide := readDoc(t, "../../adapter-guide/README.md")
	payloads := smPayloads()

	fieldPattern := regexp.MustCompile("`([a-z_][a-z0-9_]*)` \\(")
	samplePattern := regexp.MustCompile("`([A-Za-z0-9._-]+\\.txt)`")

	seen := map[string]bool{}
	fieldChecks := 0
	for _, row := range tableAfter(t, guide, "| Type | JSON Schema (data fields) |") {
		cols := cells(row)
		if len(cols) < 3 {
			t.Fatalf("system-message row has %d columns, want 3: %s", len(cols), row)
		}
		smType := strings.Trim(cols[0], "`")
		payload, ok := payloads[smType]
		if !ok {
			t.Errorf("adapter-guide documents SM type %q, which has no payload here — add it to smPayloads", smType)
			continue
		}
		seen[smType] = true

		tags := jsonTags(payload)
		for _, m := range fieldPattern.FindAllStringSubmatch(cols[1], -1) {
			fieldChecks++
			if !tags[m[1]] {
				t.Errorf("adapter-guide: SM %q lists field %q, which %T does not declare",
					smType, m[1], payload)
			}
		}
		for _, m := range samplePattern.FindAllStringSubmatch(cols[2], -1) {
			path := filepath.Join("../../adapter-guide/wire-samples", m[1])
			if _, err := os.Stat(path); err != nil {
				t.Errorf("adapter-guide names sample %s, which is not there: %v", m[1], err)
			}
		}
	}

	for smType := range payloads {
		if !seen[smType] {
			t.Errorf("protocol declares SM type %q, which the adapter-guide's table does not document", smType)
		}
	}
	if fieldChecks == 0 {
		t.Error("no field names were checked — the schema column's shape changed, so the check went quiet")
	}
}

// TestSMPayloadsAnnounceDeclaredTypes is the producer half of the guide check:
// the type a payload puts on the wire must be one protocol declares, and the
// same one the guide's table uses. It exists because reasoningMsg returned the
// literal "reasoning" while protocol's type became "reasoning_level": the
// session broadcast a frame no adapter could match, and the TUI's status bar
// stopped showing level changes.
func TestSMPayloadsAnnounceDeclaredTypes(t *testing.T) {
	declared := map[string]bool{}
	for _, v := range []string{
		string(protocol.MsgTypeError), string(protocol.MsgTypeNotify), string(protocol.MsgTypeTask),
		string(protocol.MsgTypeModel), string(protocol.MsgTypeModelList), string(protocol.MsgTypeTheme),
		string(protocol.MsgTypeThemeList), string(protocol.MsgTypeReasoningLevel),
		string(protocol.MsgTypeVideoConfig), string(protocol.MsgTypeToolConfirm),
		string(protocol.MsgTypeMCP), string(protocol.MsgTypeSession), string(protocol.MsgTypeVersion),
		string(protocol.MsgTypeSteering),
	} {
		declared[v] = true
	}

	for want, payload := range smPayloads() {
		msg, ok := payload.(protocol.SystemMsg)
		if !ok {
			t.Errorf("%T does not implement protocol.SystemMsg", payload)
			continue
		}
		got := msg.SystemMsgType()
		if !declared[got] {
			t.Errorf("%T announces %q, which protocol does not declare — the type must come from protocol.MsgType*", payload, got)
		}
		if got != want {
			t.Errorf("%T announces %q but is registered here as %q", payload, got, want)
		}
	}
}

// TestShortFormsResolveToDispatchedCommands: a short form is an adapter's
// convenience, so its target must be a command the session dispatches, and it
// must not shadow one — a short form equal to a command name would make that
// name untypeable.
func TestShortFormsResolveToDispatchedCommands(t *testing.T) {
	known := dispatchedCommands(t)
	forms := commands.ShortForms()
	if len(forms) == 0 {
		t.Fatal("no short forms — the table moved, so the check went quiet")
	}
	seen := map[string]string{}
	for short, name := range forms {
		if !known[name] {
			t.Errorf("short form %q expands to %q, which the session does not dispatch", short, name)
		}
		if known[short] {
			t.Errorf("short form %q shadows the command of the same name", short)
		}
		if prev, dup := seen[name]; dup {
			t.Errorf("%q and %q both expand to %q; the help list shows one short form per command", prev, short, name)
		}
		seen[name] = short
	}
}
