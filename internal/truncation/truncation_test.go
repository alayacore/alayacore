package truncation

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Lines
// ---------------------------------------------------------------------------

func TestLines_NoTruncation(t *testing.T) {
	input := "line1\nline2\nline3\n"
	truncated, output := Lines(input, 10)
	if truncated {
		t.Error("expected no truncation")
	}
	if output != "line1\nline2\nline3" {
		t.Errorf("unexpected output: %q", output)
	}
}

func TestLines_Truncation(t *testing.T) {
	input := "line1\nline2\nline3\nline4\nline5\n"
	truncated, output := Lines(input, 3)
	if !truncated {
		t.Error("expected truncation")
	}
	if output != "line1\nline2\nline3" {
		t.Errorf("unexpected output: %q", output)
	}
}

func TestLines_SkipsEmptyLines(t *testing.T) {
	input := "line1\n\n\nline2\n\nline3\nline4\n"
	truncated, output := Lines(input, 2)
	if !truncated {
		t.Error("expected truncation")
	}
	if output != "line1\nline2" {
		t.Errorf("unexpected output: %q", output)
	}
}

func TestLines_CJK(t *testing.T) {
	input := "第一行\n第二行\n第三行\n第四行\n"
	truncated, output := Lines(input, 2)
	if !truncated {
		t.Error("expected truncation")
	}
	if output != "第一行\n第二行" {
		t.Errorf("unexpected output: %q", output)
	}
	for _, r := range output {
		if r == '\uFFFD' {
			t.Errorf("output contains replacement character: %q", output)
		}
	}
}

func TestLines_EmptyInput(t *testing.T) {
	truncated, output := Lines("", 5)
	if truncated {
		t.Error("expected no truncation for empty input")
	}
	if output != "" {
		t.Errorf("expected empty output, got %q", output)
	}
}

// ---------------------------------------------------------------------------
// Front
// ---------------------------------------------------------------------------

func TestFront_NoTruncation(t *testing.T) {
	text := "hello world"
	got := Front(text, 100, Marker)
	if got != text {
		t.Errorf("expected unchanged, got %q", got)
	}
}

func TestFront_Truncation(t *testing.T) {
	text := "abcdefghijklmnopqrstuvwxyz" // 26 bytes
	// budget=20 is less than text (26), so truncation happens.
	// marker=12 bytes. contentBudget = 20-12 = 8. 8 ASCII chars.
	got := Front(text, 20, Marker)
	if !strings.HasSuffix(got, Marker) {
		t.Errorf("expected marker suffix, got %q", got)
	}
	prefix := strings.TrimSuffix(got, Marker)
	if len(prefix) != 8 {
		t.Errorf("expected 8 bytes of content, got %d", len(prefix))
	}

	// Test with larger budget where content fits after marker.
	longText := strings.Repeat("x", 100) // 100 bytes
	// budget=50. marker=12. contentBudget=38. 38 ASCII chars.
	got = Front(longText, 50, Marker)
	if !strings.HasSuffix(got, Marker) {
		t.Errorf("expected marker suffix, got %q", got)
	}
	prefix = strings.TrimSuffix(got, Marker)
	if len(prefix) != 38 {
		t.Errorf("expected 38 bytes of content, got %d", len(prefix))
	}
}

func TestFront_BudgetTooSmallForMarker(t *testing.T) {
	text := "hello"
	// 5 bytes <= budget of 10, no truncation.
	got := Front(text, 10, Marker)
	if got != text {
		t.Errorf("expected original text, got %q", got)
	}
}

func TestFront_BudgetTooSmallForMarker_Overlong(t *testing.T) {
	text := strings.Repeat("x", 100)
	// budget=10, marker=12 bytes. contentBudget = 10-12 < 0 → marker only.
	got := Front(text, 10, Marker)
	if got != Marker {
		t.Errorf("expected marker-only output, got %q", got)
	}
}

func TestFront_CJK_NoTruncationWhenFits(t *testing.T) {
	// Pure CJK: 200 runes, 600 bytes. budget=600 fits exactly.
	text := strings.Repeat("你", 200)
	got := Front(text, 600, Marker)
	if got != text {
		t.Errorf("expected no truncation, got length %d want %d", len(got), len(text))
	}
}

func TestFront_CJKTruncation(t *testing.T) {
	text := strings.Repeat("你", 1000) // 1000 runes, 3000 bytes

	// budget=100 bytes. marker=12 bytes. contentBudget=88.
	// Each CJK rune is 3 bytes, so 88/3 = 29 runes.
	got := Front(text, 100, Marker)
	if got == text {
		t.Error("expected truncation for large CJK text")
	}
	if !strings.HasSuffix(got, Marker) {
		t.Errorf("expected marker suffix, got %q", got)
	}
	prefix := strings.TrimSuffix(got, Marker)
	runeCount := len([]rune(prefix))
	if runeCount != 29 {
		t.Errorf("expected 29 runes before marker, got %d", runeCount)
	}
}

func TestFront_MixedASCIIAndCJK(t *testing.T) {
	// "Hello " = 6 bytes. "世界" = 6 bytes each.
	text := "Hello " + strings.Repeat("世界", 50) // 6 + 300 = 306 bytes
	// budget=100. marker=12. contentBudget=88.
	// "Hello " (6) leaves 82 bytes for CJK. 82/3 = 27 CJK runes.
	got := Front(text, 100, Marker)
	if got == text {
		t.Error("expected truncation")
	}
	prefix := strings.TrimSuffix(got, Marker)
	if !strings.HasPrefix(prefix, "Hello ") {
		t.Errorf("expected 'Hello ' prefix, got %q", prefix)
	}
	// Verify byte count of output (minus marker) fits contentBudget.
	if len(prefix) > 88 {
		t.Errorf("prefix too long: %d bytes, budget is 88", len(prefix))
	}
}

func TestFront_CJKAtStart(t *testing.T) {
	// CJK at the beginning — each rune costs 3 bytes.
	text := strings.Repeat("你", 10) + "hello" // 30 + 5 = 35 bytes
	// budget=10. marker=12. contentBudget = -2 → marker only.
	got := Front(text, 10, Marker)
	if got != Marker {
		t.Errorf("expected marker-only when budget < marker, got %q", got)
	}

	// budget=30. marker=12. contentBudget=18.
	// 18 bytes can fit 6 CJK runes.
	got = Front(text, 30, Marker)
	prefix := strings.TrimSuffix(got, Marker)
	if len([]rune(prefix)) != 6 {
		t.Errorf("expected 6 CJK runes (18 bytes), got %d runes", len([]rune(prefix)))
	}
}
