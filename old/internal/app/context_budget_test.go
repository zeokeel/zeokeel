package app

import "testing"

// fakeSettingGetter returns a getter over a key map; env overrides are keyed as
// "ENV:<name>" and only consulted when the caller passes an env name.
func fakeSettingGetter(values map[string]string) func(key, env, def string) string {
	return func(key, env, def string) string {
		if env != "" {
			if v, ok := values["ENV:"+env]; ok && v != "" {
				return v
			}
		}
		return values[key]
	}
}

// TestContextBudgetTokens is the FP-10 regression: the prompt budget is no
// longer hard-coded 24000, settings/env can tune it, invalid values fall back
// and out-of-range values clamp instead of silently disabling compaction.
func TestContextBudgetTokens(t *testing.T) {
	cases := []struct {
		name   string
		values map[string]string
		want   int
	}{
		{name: "default", values: map[string]string{}, want: defaultContextBudgetTokens},
		{name: "documented key", values: map[string]string{"context.budget_tokens": "8000"}, want: 8000},
		{name: "legacy namespaced key", values: map[string]string{"agent.context_budget_tokens": "12000"}, want: 12000},
		{
			name:   "documented key wins",
			values: map[string]string{"context.budget_tokens": "9000", "agent.context_budget_tokens": "12000"},
			want:   9000,
		},
		{
			name:   "env overrides the default",
			values: map[string]string{"ENV:ZEOKEEL_CONTEXT_BUDGET_TOKENS": "16000"},
			want:   16000,
		},
		{name: "invalid falls back", values: map[string]string{"context.budget_tokens": "not-a-number"}, want: defaultContextBudgetTokens},
		{name: "zero falls back", values: map[string]string{"context.budget_tokens": "0"}, want: defaultContextBudgetTokens},
		{name: "negative falls back", values: map[string]string{"context.budget_tokens": "-5"}, want: defaultContextBudgetTokens},
		{name: "too small clamps", values: map[string]string{"context.budget_tokens": "10"}, want: minContextBudgetTokens},
		{name: "too large clamps", values: map[string]string{"context.budget_tokens": "99999999"}, want: maxContextBudgetTokens},
	}
	for _, tc := range cases {
		if got := contextBudgetTokens(fakeSettingGetter(tc.values)); got != tc.want {
			t.Errorf("%s: contextBudgetTokens = %d, want %d", tc.name, got, tc.want)
		}
	}
}
