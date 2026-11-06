package app

import "testing"

// TestBudgetWarnThresholdsParsing pins the agent-loop budget settings parsing:
// empty = runtime defaults, comma-separated positive ints = custom thresholds,
// 0/off/none = disabled, and any invalid entry falls back to the defaults.
func TestBudgetWarnThresholdsParsing(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want []int
	}{
		{"empty default", "", nil},
		{"spaces only default", "   ", nil},
		{"custom steps", "25,50,100", []int{25, 50, 100}},
		{"custom tokens", "200000,500000", []int{200000, 500000}},
		{"spaces around values", " 10 , 20 ", []int{10, 20}},
		{"single value", "7", []int{7}},
		{"disabled zero", "0", []int{}},
		{"disabled off", "off", []int{}},
		{"disabled none uppercase", "NONE", []int{}},
		{"invalid word", "abc", nil},
		{"invalid entry", "25,abc", nil},
		{"negative entry", "25,-1", nil},
		{"zero in list", "25,0", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := budgetWarnThresholds(tc.raw)
			if tc.want == nil {
				if got != nil {
					t.Fatalf("budgetWarnThresholds(%q) = %v, want nil (defaults)", tc.raw, got)
				}
				return
			}
			if got == nil || len(got) != len(tc.want) {
				t.Fatalf("budgetWarnThresholds(%q) = %v, want %v", tc.raw, got, tc.want)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Fatalf("budgetWarnThresholds(%q) = %v, want %v", tc.raw, got, tc.want)
				}
			}
		})
	}
}
