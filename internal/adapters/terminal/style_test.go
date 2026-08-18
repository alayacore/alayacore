package terminal

// Style layer tests (module 6, S3). The expected SGR bytes were locked
// against lipgloss v2 output by style_compat_test.go (now deleted): every
// assertion here is byte-for-byte what lipgloss produced for the same
// chain, so replacing lipgloss did not change any rendered output.

import (
	"bytes"
	"strings"
	"testing"
)

// TestStyleRenderSGR locks the exact SGR bytes for attribute chains.
func TestStyleRenderSGR(t *testing.T) {
	hex := Color("#585b70")

	cases := []struct {
		name  string
		style Style
		in    string
		want  string
	}{
		{
			name:  "empty style passthrough",
			style: NewStyle(),
			in:    "x",
			want:  "x",
		},
		{
			name:  "fg hex",
			style: NewStyle().Foreground(hex),
			in:    "x",
			want:  "\x1b[38;2;88;91;112mx\x1b[m",
		},
		{
			name:  "bold fg",
			style: NewStyle().Foreground(hex).Bold(true),
			in:    "x",
			want:  "\x1b[1;38;2;88;91;112mx\x1b[m",
		},
		{
			name:  "italic fg",
			style: NewStyle().Foreground(hex).Italic(true),
			in:    "x",
			want:  "\x1b[3;38;2;88;91;112mx\x1b[m",
		},
		{
			name:  "underline fg (early and late 4)",
			style: NewStyle().Foreground(hex).Underline(true),
			in:    "x",
			want:  "\x1b[4;38;2;88;91;112;4mx\x1b[m",
		},
		{
			name:  "strikethrough fg (9 after colors)",
			style: NewStyle().Foreground(hex).Strikethrough(true),
			in:    "x",
			want:  "\x1b[38;2;88;91;112;9mx\x1b[m",
		},
		{
			name:  "fg bg",
			style: NewStyle().Foreground(hex).Background(Color("#ffffff")),
			in:    "x",
			want:  "\x1b[38;2;88;91;112;48;2;255;255;255mx\x1b[m",
		},
		{
			name:  "full chain",
			style: NewStyle().Foreground(hex).Background(Color("#ffffff")).Bold(true).Italic(true),
			in:    "x",
			want:  "\x1b[1;3;38;2;88;91;112;48;2;255;255;255mx\x1b[m",
		},
		{
			name:  "empty color renders no fg",
			style: NewStyle().Foreground(Color("")).Bold(true),
			in:    "x",
			want:  "\x1b[1mx\x1b[m",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.style.Render(tc.in)
			if got != tc.want {
				t.Errorf("Render() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestStyleRenderMultiArgs locks variadic Render joining.
func TestStyleRenderMultiArgs(t *testing.T) {
	hex := Color("#585b70")
	got := NewStyle().Foreground(hex).Render("a", "b")
	want := "\x1b[38;2;88;91;112ma b\x1b[m"
	if got != want {
		t.Errorf("Render() = %q, want %q", got, want)
	}
}

// TestStyleRenderPerLineReset locks the self-contained-row behavior that
// the soft-wrap fragment pipeline relies on.
func TestStyleRenderPerLineReset(t *testing.T) {
	hex := Color("#585b70")
	got := NewStyle().Foreground(hex).Bold(true).Render("ab\ncd\nef")
	want := "\x1b[1;38;2;88;91;112mab\x1b[m\n" +
		"\x1b[1;38;2;88;91;112mcd\x1b[m\n" +
		"\x1b[1;38;2;88;91;112mef\x1b[m"
	if got != want {
		t.Errorf("Render() = %q, want %q", got, want)
	}
}

// TestStyleRenderInline locks the single-line mode (newlines stripped).
func TestStyleRenderInline(t *testing.T) {
	hex := Color("#585b70")
	got := NewStyle().Foreground(hex).Inline(true).Render("ab\ncd")
	want := "\x1b[38;2;88;91;112mabcd\x1b[m"
	if got != want {
		t.Errorf("Render() = %q, want %q", got, want)
	}
}

// TestStyleRenderWidthPad locks block-width padding (plain spaces unless
// a background is set).
func TestStyleRenderWidthPad(t *testing.T) {
	hex := Color("#585b70")
	got := NewStyle().Foreground(hex).Width(8).Render("ab")
	want := "\x1b[38;2;88;91;112mab\x1b[m      "
	if got != want {
		t.Errorf("Render() = %q, want %q", got, want)
	}

	// Width with background: padding carries the background color.
	got = NewStyle().Foreground(hex).Background(Color("#ffffff")).Width(6).Render("ab")
	want = "\x1b[38;2;88;91;112;48;2;255;255;255mab\x1b[m" +
		"\x1b[48;2;255;255;255m    \x1b[m"
	if got != want {
		t.Errorf("Render() = %q, want %q", got, want)
	}
}

// TestColorParsing locks Color's spec handling.
func TestColorParsing(t *testing.T) {
	if got := Color("#585b70"); got == nil {
		t.Fatal("Color(#585b70) = nil")
	} else if r, g, b, _ := got.RGBA(); r>>8 != 0x58 || g>>8 != 0x5b || b>>8 != 0x70 {
		t.Errorf("Color(#585b70) = %d,%d,%d, want 88,91,112", r>>8, g>>8, b>>8)
	}
	if got := Color("#abc"); got == nil {
		t.Fatal("Color(#abc) = nil")
	} else if r, g, b, _ := got.RGBA(); r>>8 != 0xaa || g>>8 != 0xbb || b>>8 != 0xcc {
		t.Errorf("Color(#abc) = %d,%d,%d, want 170,187,204", r>>8, g>>8, b>>8)
	}
	if got := Color(""); got != nil {
		t.Errorf("Color(\"\") = %v, want nil", got)
	}
	if got := Color("notacolor"); got != nil {
		t.Errorf("Color(notacolor) = %v, want nil", got)
	}
	if got := Color("#12345"); got != nil {
		t.Errorf("Color(#12345) = %v, want nil", got)
	}
	if got := Color("5"); got == nil {
		t.Error("Color(5) = nil, want ANSI color")
	}
}

// TestStyleWidthHeight locks Width/Height semantics.
func TestStyleWidthHeight(t *testing.T) {
	if got := Width(""); got != 0 {
		t.Errorf("Width(\"\") = %d, want 0", got)
	}
	if got := Width("abc"); got != 3 {
		t.Errorf("Width(abc) = %d, want 3", got)
	}
	if got := Width("a\nbcdef"); got != 5 {
		t.Errorf("Width(a\\nbcdef) = %d, want 5", got)
	}
	if got := Width("\x1b[31mred\x1b[0m"); got != 3 {
		t.Errorf("Width(ANSI red) = %d, want 3", got)
	}
	if got := Width("中文"); got != 4 {
		t.Errorf("Width(中文) = %d, want 4", got)
	}
	if got := Height(""); got != 1 {
		t.Errorf("Height(\"\") = %d, want 1", got)
	}
	if got := Height("a\nb\nc"); got != 3 {
		t.Errorf("Height(a\\nb\\nc) = %d, want 3", got)
	}
}

// TestWrapWriterRestyle locks the newline style re-application.
func TestWrapWriterRestyle(t *testing.T) {
	in := "\x1b[38;2;88;91;112mabc\ndef\x1b[0m"
	var buf bytes.Buffer
	w := NewWrapWriter(&buf)
	_, _ = w.Write([]byte(in))
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	// Style split across the newline boundary is re-applied to the second
	// line (self-contained rows), exactly like lipgloss's WrapWriter. The
	// input's own trailing reset is passed through verbatim.
	want := "\x1b[38;2;88;91;112mabc\x1b[m\n\x1b[38;2;88;91;112mdef\x1b[0m"
	if buf.String() != want {
		t.Errorf("restyled = %q, want %q", buf.String(), want)
	}
}

// TestWrapWordBoundary locks word-boundary wrap behavior.
func TestWrapWordBoundary(t *testing.T) {
	in := "aaa bbb ccc"
	got := Wrap(in, 7, " ")
	if got != "aaa bbb\nccc" {
		t.Errorf("Wrap() = %q, want %q", got, "aaa bbb\nccc")
	}
	if !strings.Contains(Wrap("hello world", 5, " "), "hello") {
		t.Errorf("Wrap(hello world, 5) = %q", Wrap("hello world", 5, " "))
	}
}

// TestGetForeground locks the getter used by the window border pipeline.
func TestGetForeground(t *testing.T) {
	hex := Color("#585b70")
	if got := NewStyle().GetForeground(); got != nil {
		t.Errorf("GetForeground() = %v, want nil", got)
	}
	if got := NewStyle().Foreground(hex).GetForeground(); got != hex {
		t.Errorf("GetForeground() = %v, want %v", got, hex)
	}
}
