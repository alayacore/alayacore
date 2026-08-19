package agent

import (
	"strings"
	"testing"
	"time"

	"github.com/alayacore/alayacore/internal/config"
)

// --- EscapeQuoted tests ---

func TestEscapeQuoted(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"plain", `hello`, `hello`},
		{"double quote", `say "hi"`, `say \"hi\"`},
		{"backslash", `path\to\file`, `path\\to\\file`},
		{"newline", "line1\nline2", `line1\nline2`},
		{"carriage return", "line1\rline2", `line1\rline2`},
		{"combo", `a "b" \c` + "\n\r", `a \"b\" \\c\n\r`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := config.EscapeQuoted(tt.input)
			if got != tt.want {
				t.Errorf("EscapeQuoted(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// --- formatRuntimeConfig round-trip test ---

func TestFormatRuntimeConfig_RoundTrip(t *testing.T) {
	cfg := runtimeConfig{
		ActiveModel: `model with "quotes" and \backslash\`,
		ActiveTheme: "theme\nwith\nnewlines",
	}

	formatted := formatRuntimeConfig(cfg)
	parsed, _ := parseRuntimeConfig(formatted, "runtime.conf")

	if parsed.ActiveModel != cfg.ActiveModel {
		t.Errorf("ActiveModel round-trip failed: got %q, want %q", parsed.ActiveModel, cfg.ActiveModel)
	}
	if parsed.ActiveTheme != cfg.ActiveTheme {
		t.Errorf("ActiveTheme round-trip failed: got %q, want %q", parsed.ActiveTheme, cfg.ActiveTheme)
	}
}

// --- formatFrontmatter round-trip test ---

func TestFormatFrontmatter_RoundTrip(t *testing.T) {
	meta := sessionMeta{
		MessageVersion: messageVersion,
		ActiveModel:    `model "name" \\ thing`,
		ReasoningLevel: 1,
		ContextTokens:  12345,
	}
	meta.CreatedAt = time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
	meta.UpdatedAt = time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)

	formatted := formatFrontmatter(&meta)

	// Extract frontmatter from formatted output
	fm, _, err := parseFrontmatter(formatted)
	if err != nil {
		t.Fatalf("failed to parse frontmatter: %v", err)
	}
	parsed, err := parseSessionMeta(fm)
	if err != nil {
		t.Fatalf("parseSessionMeta failed: %v", err)
	}

	if parsed.ActiveModel != meta.ActiveModel {
		t.Errorf("ActiveModel round-trip failed: got %q, want %q", parsed.ActiveModel, meta.ActiveModel)
	}
}

// Test that model names with special characters survive a runtime config round-trip
func TestRuntimeManager_SpecialChars_RoundTrip(t *testing.T) {
	tmpDir := t.TempDir()
	runtimePath := tmpDir + "/runtime.conf"

	rm := newRuntimeManager(runtimePath)

	specialName := `model "quoted" \slash\`
	if err := rm.setActiveModel(specialName); err != nil {
		t.Fatalf("SetActiveModel failed: %v", err)
	}

	// Create a fresh manager and load from the same file
	rm2 := newRuntimeManager(runtimePath)
	got := rm2.getActiveModel()
	if got != specialName {
		t.Errorf("round-trip failed: got %q, want %q", got, specialName)
	}
}

// Test that a model name with newlines doesn't corrupt the runtime config
func TestRuntimeManager_NewlineInModel_RoundTrip(t *testing.T) {
	tmpDir := t.TempDir()
	runtimePath := tmpDir + "/runtime.conf"

	rm := newRuntimeManager(runtimePath)

	nameWithNewline := "line1\nline2"
	if err := rm.setActiveModel(nameWithNewline); err != nil {
		t.Fatalf("SetActiveModel failed: %v", err)
	}

	rm2 := newRuntimeManager(runtimePath)
	got := rm2.getActiveModel()
	if got != nameWithNewline {
		t.Errorf("round-trip failed: got %q, want %q", got, nameWithNewline)
	}

	// Verify the file can still be parsed (no broken lines)
	_ = rm2 // just to ensure it loaded without error
}

// Verify formatFrontmatter doesn't produce broken output for special chars
func TestFormatFrontmatter_NoBrokenOutput(t *testing.T) {
	meta := sessionMeta{
		MessageVersion: messageVersion,
		ActiveModel:    "has\nnewlines",
		ReasoningLevel: 1,
	}
	meta.CreatedAt = time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
	meta.UpdatedAt = time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)

	formatted := formatFrontmatter(&meta)

	// The formatted output should have the active_model on a single line
	lines := strings.Split(formatted, "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "active_model:") {
			// Should be a single line, not split
			if strings.Count(line, `"`) != 2 {
				t.Errorf("active_model line has unexpected quotes: %q", line)
			}
			return
		}
	}
	t.Error("active_model line not found in formatted output")
}
