package terminal

import (
	"testing"

	"github.com/alayacore/alayacore/internal/theme"
)

func TestNewStylesWithTheme(t *testing.T) {
	customTheme := &theme.Theme{
		Primary:   "#custom1",
		Dim:       "#custom2",
		Muted:     "#custom3",
		Warning:   "#custom5",
		Error:     "#custom6",
		Selection: "#custom8",
	}

	styles := NewStyles(customTheme)
	if styles == nil {
		t.Fatal("NewStyles returned nil")
		return
	}

	_ = styles.Error.Render("test")

	_ = styles.ColorAccent
	_ = styles.ColorDim
}
