package textutil

import "testing"

// FirstNonEmpty and FirstNonEmptyTrimmed consolidate the fourteen private
// firstNonEmpty copies of the 2026-11 audit (AR-17). The two variants are the
// two behaviors the copies actually had: keep the value verbatim, or trim it.

func TestFirstNonEmpty(t *testing.T) {
	tests := []struct {
		name   string
		values []string
		want   string
	}{
		{"no values", nil, ""},
		{"all empty", []string{"", ""}, ""},
		{"first wins", []string{"a", "b"}, "a"},
		{"skips empty", []string{"", "b", "c"}, "b"},
		{"whitespace is a value (verbatim variant)", []string{"  ", "b"}, "  "},
		{"keeps surrounding whitespace", []string{" a "}, " a "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FirstNonEmpty(tt.values...); got != tt.want {
				t.Fatalf("FirstNonEmpty(%q) = %q, want %q", tt.values, got, tt.want)
			}
		})
	}
}

func TestFirstNonEmptyTrimmed(t *testing.T) {
	tests := []struct {
		name   string
		values []string
		want   string
	}{
		{"no values", nil, ""},
		{"all blank", []string{"", "  ", "\t"}, ""},
		{"first wins trimmed", []string{" a ", "b"}, "a"},
		{"skips blank", []string{"  ", " b ", "c"}, "b"},
		{"trims the winner", []string{"\tname\n"}, "name"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FirstNonEmptyTrimmed(tt.values...); got != tt.want {
				t.Fatalf("FirstNonEmptyTrimmed(%q) = %q, want %q", tt.values, got, tt.want)
			}
		})
	}
}
