package terminal

// The model list's right-hand columns are a function of the terminal width
// alone: narrowing the terminal may shorten them, but must never move them.
// Before this was locked, dropping the provider column handed its cells to the
// name — a column to the LEFT of the context column — so the context jumped one
// place sideways and back as the terminal was resized. The provider is now
// dropped only together with the context, and only when the name can no longer
// keep its longest entry anyway.

import (
	"fmt"
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/protocol"
)

func columnFixture() []protocol.ModelInfo {
	models := make([]protocol.ModelInfo, 0, 20)
	for i := 1; i <= 6; i++ {
		models = append(models, protocol.ModelInfo{
			ID:           i,
			Name:         fmt.Sprintf("claude-%d", i),
			ProtocolType: "anthropic",
			ContextLimit: 200000,
		})
	}
	// Enough OpenAI rows that the last window (SelectorListRows tall) holds
	// no Anthropic row at all.
	for i := 7; i <= 20; i++ {
		models = append(models, protocol.ModelInfo{
			ID:           i,
			Name:         fmt.Sprintf("gpt-%d", i),
			ProtocolType: "openai",
			ContextLimit: 400000,
		})
	}
	return models
}

// columns returns the layout widths a selector would use.
func (ms ModelSelector) columns() (nameMaxWidth, ctxColWidth, provColWidth int) {
	return ms.measureColumns(ms.Width, ms.maxIDWidth())
}

// renderRowByID renders the row for one model under the selector's layout.
func renderRowByID(ms ModelSelector, id int) string {
	idWidth := ms.maxIDWidth()
	nameW, ctxW, provW := ms.columns()
	for i, m := range ms.filteredModels {
		if m.ID == id {
			return stripANSI(ms.renderModelRow(i, idWidth, nameW, ctxW, provW))
		}
	}
	return ""
}

// TestModelSelectorColumnsDoNotReflow locks the columns against the list
// contents: scrolling or filtering the list must not re-flow a row.
func TestModelSelectorColumnsDoNotReflow(t *testing.T) {
	styles := DefaultStyles()
	models := columnFixture()

	base := NewModelSelector(styles).WithSize(80, 50).Open().LoadModels(models, 1)
	nameW, ctxW, provW := base.columns()

	// The provider column is sized for the widest provider in the whole
	// config ("Anthropic"), not for whichever rows are on screen.
	if want := Width("Anthropic"); provW != want {
		t.Fatalf("provider column width = %d, want %d (widest provider over all models)", provW, want)
	}
	// A row that is present in every state, and is on screen in the last
	// window. Its rendered text must not move when the window changes.
	const openAIID = 13
	openAIRow := renderRowByID(base, openAIID)

	// Scrolled to the bottom: the window holds OpenAI rows only.
	scrolled := base
	scrolled.SelectedIdx = len(models) - 1
	scrolled.FilteredListCore = scrolled.FilteredListCore.EnsureVisible()
	if scrolled.ScrollIdx == 0 {
		t.Fatal("fixture did not scroll: need more models than SelectorListRows")
	}
	if n, c, p := scrolled.columns(); n != nameW || c != ctxW || p != provW {
		t.Errorf("scrolled widths = (%d,%d,%d), want (%d,%d,%d)", n, c, p, nameW, ctxW, provW)
	}
	if got := renderRowByID(scrolled, openAIID); got != openAIRow {
		t.Errorf("OpenAI row moved when scrolling to the bottom:\n got %q\nwant %q", got, openAIRow)
	}

	// Filtered down to OpenAI rows only.
	filtered := base
	filtered.FilterInput = filtered.FilterInput.WithValue("gpt")
	filtered = filtered.updateFilteredModels()
	if len(filtered.filteredModels) == 0 || len(filtered.filteredModels) == len(models) {
		t.Fatalf("filter did not narrow the list: %d of %d rows matched", len(filtered.filteredModels), len(models))
	}
	if n, c, p := filtered.columns(); n != nameW || c != ctxW || p != provW {
		t.Errorf("filtered widths = (%d,%d,%d), want (%d,%d,%d)", n, c, p, nameW, ctxW, provW)
	}
	if got := renderRowByID(filtered, openAIID); got != openAIRow {
		t.Errorf("OpenAI row moved when filtering:\n got %q\nwant %q", got, openAIRow)
	}
}

// TestModelSelectorColumnsShrinkInPlace locks the columns against the terminal
// width: the right pair is shown whole or not at all, every row fills the box,
// and a wider terminal only ever moves the context column to the right.
func TestModelSelectorColumnsShrinkInPlace(t *testing.T) {
	styles := DefaultStyles()
	base := NewModelSelector(styles).Open().LoadModels(columnFixture(), 1)

	prevStart, prevEnd := -1, -1
	shown := 0
	for w := 8; w <= 96; w++ {
		ms := base.WithSize(w, 50)
		idWidth := ms.maxIDWidth()
		nameW, ctxW, provW := ms.measureColumns(w, idWidth)

		// The right pair is dropped together or not at all — a lone drop is
		// what used to shove the survivor sideways.
		if (ctxW > 0) != (provW > 0) {
			t.Fatalf("W=%d: context=%d and provider=%d must appear together", w, ctxW, provW)
		}
		// Every row fills the box: no stray trailing cell, no overflow.
		row := stripANSI(ms.renderModelRow(0, idWidth, nameW, ctxW, provW))
		if got := Width(row); got != w {
			t.Errorf("W=%d: row is %d cells: %q", w, got, row)
		}
		if ctxW == 0 {
			continue
		}
		shown++
		// As the terminal widens the context column may only move right.
		start := (2 + idWidth) + nameW + 1
		end := start + ctxW - 1
		if prevStart >= 0 && (start < prevStart || end < prevEnd) {
			t.Errorf("W=%d: context column moved left, [%d..%d] after [%d..%d]",
				w, start, end, prevStart, prevEnd)
		}
		prevStart, prevEnd = start, end
	}
	if shown == 0 {
		t.Fatal("fixture never showed the context column")
	}
}

// TestHelpWindowTruncatedKeyFillsBox locks the help rows: a key that has to
// truncate takes the whole width once the description can no longer keep its
// gap and a cell. Reserving that gap anyway left the row one cell short, so a
// truncated key's ellipsis had a stray cell after it.
func TestHelpWindowTruncatedKeyFillsBox(t *testing.T) {
	styles := DefaultStyles()
	for w := 8; w <= 96; w++ {
		hw := NewHelpWindow(styles).WithSize(w, 50).Open()
		for _, item := range hw.items {
			if item.IsSection {
				continue
			}
			plain := stripANSI(hw.renderItem(item, false))
			if got := Width(plain); got > w {
				t.Fatalf("W=%d: row overflows the box (%d cells): %q", w, got, plain)
			}
			if strings.HasSuffix(plain, "…") && Width(plain) != w {
				t.Errorf("W=%d: ellipsis followed by a stray cell: %q", w, plain)
			}
		}
	}
}
