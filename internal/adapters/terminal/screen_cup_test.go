package terminal

// Tests for the shared CSI/CUP parser extracted in TERMINAL_AUDIT.md §B-4.

import "testing"

func TestParseCUP(t *testing.T) {
	cases := []struct {
		name    string
		seq     string
		wantRow int
		wantCol int
		wantOK  bool
	}{
		{"bare home", "[H", 0, 0, true},
		{"two-arg CUP", "[5;10H", 4, 9, true},
		{"one-arg CUP defaults col", "[7H", 6, 0, true},
		{"col-only CUP", "[;5H", 0, 4, true},
		{"row 1 col 1", "[1;1H", 0, 0, true},
		// Negative cases — should return ok=false.
		{"not CSI", "5;10H", 0, 0, false},
		{"empty", "", 0, 0, false},
		{"missing final H", "[5;10", 0, 0, false},
		{"final byte m not H", "[38;2;0;0;0m", 0, 0, false},
		{"non-digit inner", "[abcH", 0, 0, false},
		{"private mode prefix not allowed", "[?25H", 0, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			row, col, ok := parseCUP(tc.seq)
			if row != tc.wantRow || col != tc.wantCol || ok != tc.wantOK {
				t.Errorf("parseCUP(%q) = (%d, %d, %v), want (%d, %d, %v)",
					tc.seq, row, col, ok, tc.wantRow, tc.wantCol, tc.wantOK)
			}
		})
	}
}

func TestContainsCUP(t *testing.T) {
	if !containsCUP("\x1b[5;10Hhello") {
		t.Error("containsCUP should detect [5;10H")
	}
	if containsCUP("\x1b[?25h") {
		t.Error("containsCUP should not match cursor-visibility toggle")
	}
	if containsCUP("plain text") {
		t.Error("containsCUP should be false on plain text")
	}
	if !containsCUP("\x1b[38;2;0;0;0m\x1b[1;1Hdata") {
		t.Error("containsCUP should detect CUP after SGR")
	}
}
