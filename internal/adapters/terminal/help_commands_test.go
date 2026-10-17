package terminal

// The help overlay's Commands section is derived from the vocabulary
// (commands.Defs) — a hand-kept copy is how it came to disagree with the
// registry about `:tool_confirm`'s argument and to miss the short forms. These
// tests pin the derivation: the section is exactly the vocabulary plus the two
// adapter-local controls, each name appears once, and a command with a short
// form says so.

import (
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/commands"
)

func commandItems(t *testing.T) []HelpItem {
	t.Helper()
	id := 0
	nextID := func() int { id++; return id }
	return commandHelpItems(nextID)
}

func TestHelpCommandsAreTheVocabulary(t *testing.T) {
	items := commandItems(t)

	// ":name" up to the first space is the entry's command.
	nameOf := func(key string) string {
		key = strings.TrimPrefix(key, ":")
		name, _, _ := strings.Cut(key, " ")
		return name
	}

	got := map[string]HelpItem{}
	for _, item := range items {
		name := nameOf(item.Key)
		if _, dup := got[name]; dup {
			t.Errorf(":%s appears twice in the help list", name)
		}
		got[name] = item
	}

	for _, def := range commands.Defs {
		item, ok := got[def.Name]
		if !ok {
			t.Errorf("the help list does not show :%s", def.Name)
			continue
		}
		if item.Description != def.Description && !strings.HasPrefix(item.Description, def.Description) {
			t.Errorf(":%s says %q; the vocabulary says %q", def.Name, item.Description, def.Description)
		}
		if def.Usage != "" && !strings.Contains(item.Key, def.Usage) {
			t.Errorf(":%s shows key %q, which is missing the usage %q", def.Name, item.Key, def.Usage)
		}
	}

	// The adapter's own controls are the only entries the vocabulary does not
	// have: they never reach the session.
	for _, local := range []string{cmdSuspend, cmdHelp} {
		if _, ok := got[local]; !ok {
			t.Errorf("the help list does not show the adapter-local :%s", local)
		}
		delete(got, local)
	}
	for name := range got {
		if _, ok := commands.DefinitionOf(name); !ok {
			t.Errorf("the help list shows :%s, which is neither a command nor an adapter-local control", name)
		}
	}
}

func TestHelpCommandsAdvertiseShortForms(t *testing.T) {
	items := commandItems(t)
	described := map[string]string{}
	for _, item := range items {
		name := strings.TrimPrefix(item.Key, ":")
		name, _, _ = strings.Cut(name, " ")
		described[name] = item.Description
	}

	for short, name := range commands.ShortForms() {
		if !strings.Contains(described[name], "(or :"+short+")") {
			t.Errorf("the help list does not mention the short form :%s for %q (description: %q)",
				short, name, described[name])
		}
	}
}
