package terminal

// Tests for a field's line capacity: a single-line field holds no line break
// and strips one from any door that could introduce it, while a multi-line
// field keeps it. The capacity lives in InputField (see its type doc and
// stripLineBreaks) and is what keeps every text box that is not the prompt
// single-line without each owner remembering to flatten pastes — a filter
// holding a stranded newline matches no item, and the attachment box uses its
// value as a path.

import "testing"

// TestSingleLineFieldStripsBreaksFromEveryDoor walks the doors a line break can
// arrive by — bracketed paste, an editor block, a whole-value set, an explicit
// request — and pins that a single-line field comes out holding none of them.
// The rule is the field's, so it must not depend on which door the text used.
func TestSingleLineFieldStripsBreaksFromEveryDoor(t *testing.T) {
	f := NewInputField().WithWidth(40)

	// The paste door (block text, inserted at the cursor).
	if got := f.handlePaste(PasteMsg{Content: "alpha\nbeta\n"}).Value(); got != "alphabeta" {
		t.Errorf("paste kept a break: %q, want %q", got, "alphabeta")
	}

	// The editor door (an editor buffer, whole-value).
	if got := f.WithBlockValue("one\r\ntwo\r\n").Value(); got != "onetwo" {
		t.Errorf("editor block kept a break: %q, want %q", got, "onetwo")
	}

	// A whole-value set.
	if got := f.WithValue("a\nb\nc").Value(); got != "abc" {
		t.Errorf("WithValue kept a break: %q, want %q", got, "abc")
	}

	// An explicit break request is refused.
	if got := f.WithValue("ab").CursorEnd().insertNewline().Value(); got != "ab" {
		t.Errorf("insertNewline added a break: %q, want %q", got, "ab")
	}
}

// TestMultilineFieldKeepsBreaks is the other half: the same doors on a
// multi-line field must keep the breaks, so the stripping above is a property
// of the field and not of the doors.
func TestMultilineFieldKeepsBreaks(t *testing.T) {
	f := NewMultilineInputField().WithWidth(40)

	if got := f.handlePaste(PasteMsg{Content: "alpha\nbeta\n"}).Value(); got != "alpha\nbeta" {
		t.Errorf("paste lost a break: %q, want %q", got, "alpha\nbeta")
	}
	if got := f.WithBlockValue("one\r\ntwo\r\n").Value(); got != "one\ntwo" {
		t.Errorf("editor block lost a break: %q, want %q", got, "one\ntwo")
	}
	if got := f.WithValue("a\nb").Value(); got != "a\nb" {
		t.Errorf("WithValue lost a break: %q, want %q", got, "a\nb")
	}
	if got := f.WithValue("ab").CursorEnd().insertNewline().Value(); got != "ab\n" {
		t.Errorf("insertNewline did not add a break: %q, want %q", got, "ab\n")
	}
}

// TestSingleLineFieldLineCountStaysOne pins the observable the border color
// keys off (PromptInput.borderColor): a pasted block can never make a
// single-line field report more than one line, which is what makes the
// multi-line warning border exclusive to the prompt.
func TestSingleLineFieldLineCountStaysOne(t *testing.T) {
	f := NewInputField().WithWidth(40)
	f, _ = f.Update(PasteMsg{Content: "a\nb\nc"})
	if n := f.LineCount(); n != 1 {
		t.Errorf("LineCount = %d after a multi-line paste, want 1", n)
	}
}

// TestOverlayFilterIsSingleLine goes through the real door a paste takes into
// an overlay — FilteredListCore.InsertBlockText — and pins that the filter
// value holds no break. Before capacity was the field's, this was the leak: the
// filter kept "alpha\nbeta", a value that matches no item.
func TestOverlayFilterIsSingleLine(t *testing.T) {
	fl := FilteredListCore{Styles: DefaultStyles()}
	fl = fl.WithSize(80, 24)

	after, changed := fl.InsertBlockText("alpha\nbeta")
	if !changed {
		t.Fatal("paste did not change the filter")
	}
	if got := after.FilterInput.Value(); got != "alphabeta" {
		t.Errorf("filter value = %q, want %q", got, "alphabeta")
	}
	if n := after.FilterInput.LineCount(); n != 1 {
		t.Errorf("filter LineCount = %d, want 1", n)
	}
}

// TestPromptBorderWarnsOnlyForMultiline pins the prompt affordance itself: the
// rule turns warning only when the draft spans more than one line, and is the
// normal focused color otherwise.
func TestPromptBorderWarnsOnlyForMultiline(t *testing.T) {
	styles := DefaultStyles()

	one := NewPromptInput(styles).WithValue("one line")
	if got := one.borderColor(); got != styles.BorderFocused {
		t.Errorf("single-line prompt border = %v, want the focused color %v", got, styles.BorderFocused)
	}

	many := NewPromptInput(styles).WithValue("two\nlines")
	if got := many.borderColor(); got != styles.ColorWarning {
		t.Errorf("multi-line prompt border = %v, want the warning color %v", got, styles.ColorWarning)
	}
}

// TestOverlayFilterBorderNeverWarns is the consistency the capacity buys: a
// focused filter's border color does not change when a block is pasted into
// it, because the value it accepts never becomes multi-line. There is no second
// border rule to keep in step with the prompt's.
func TestOverlayFilterBorderNeverWarns(t *testing.T) {
	fl := FilteredListCore{Styles: DefaultStyles(), HasFocus: true, FilterInputFocused: true}
	fl = fl.WithSize(80, 24)

	before := fl.FilterBorderColor()
	fl, _ = fl.InsertBlockText("alpha\nbeta")
	if got := fl.FilterBorderColor(); got != before {
		t.Errorf("filter border changed on a pasted block: %v -> %v", before, got)
	}
	if got := fl.FilterBorderColor(); got != fl.Styles.BorderFocused {
		t.Errorf("focused filter border = %v, want %v", got, fl.Styles.BorderFocused)
	}
}
