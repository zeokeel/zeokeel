package memory

import (
	"math"
	"strings"
	"testing"
)

func TestNormalizeMode(t *testing.T) {
	cases := map[string]Mode{
		"auto":    ModeAuto,
		"AUTO":    ModeAuto,
		" auto ":  ModeAuto,
		"suggest": ModeSuggest,
		"Suggest": ModeSuggest,
		"off":     ModeOff,
		"":        ModeOff,
		"enabled": ModeOff,
		"true":    ModeOff,
	}
	for raw, want := range cases {
		if got := NormalizeMode(raw); got != want {
			t.Errorf("NormalizeMode(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestStatusForMode(t *testing.T) {
	cases := []struct {
		mode        Mode
		wantStatus  string
		wantAllowed bool
	}{
		{ModeAuto, StatusActive, true},
		{ModeSuggest, StatusPending, true},
		{ModeOff, "", false},
		{Mode(""), "", false},
		{Mode("bogus"), "", false},
	}
	for _, tc := range cases {
		status, allowed := StatusForMode(tc.mode)
		if status != tc.wantStatus || allowed != tc.wantAllowed {
			t.Errorf("StatusForMode(%q) = (%q, %v), want (%q, %v)",
				tc.mode, status, allowed, tc.wantStatus, tc.wantAllowed)
		}
	}
}

func TestValidateScope(t *testing.T) {
	for _, scope := range []string{ScopeGlobal, ScopeWorkspace, " global ", "WORKSPACE"} {
		if err := ValidateScope(scope); err != nil {
			t.Errorf("ValidateScope(%q): %v", scope, err)
		}
	}
	for _, scope := range []string{"", "personal", "Global!", "workspace/global"} {
		if err := ValidateScope(scope); err == nil {
			t.Errorf("ValidateScope(%q) succeeded, want an error", scope)
		}
	}
	if err := ValidateScopes(ScopeGlobal, ScopeWorkspace); err != nil {
		t.Errorf("ValidateScopes(global, workspace): %v", err)
	}
	if err := ValidateScopes(ScopeGlobal, "bogus"); err == nil {
		t.Error("ValidateScopes(global, bogus) succeeded, want an error")
	}
}

func TestValidateKind(t *testing.T) {
	for _, kind := range []string{KindUser, KindFeedback, KindProject, KindReference} {
		if err := ValidateKind(kind); err != nil {
			t.Errorf("ValidateKind(%q): %v", kind, err)
		}
	}
	for _, kind := range []string{"", "other", "USER!", "project kind"} {
		if err := ValidateKind(kind); err == nil {
			t.Errorf("ValidateKind(%q) succeeded, want an error", kind)
		}
	}
}

func TestValidateCandidate(t *testing.T) {
	valid, err := ValidateCandidate(Candidate{
		Scope:      ScopeGlobal,
		Kind:       KindUser,
		Content:    "  Prefers small, focused commits.  ",
		Confidence: 0.9,
	})
	if err != nil {
		t.Fatalf("ValidateCandidate(valid): %v", err)
	}
	if valid.Scope != ScopeGlobal || valid.Kind != KindUser || valid.Content != "Prefers small, focused commits." || valid.Confidence != 0.9 {
		t.Fatalf("ValidateCandidate(valid) = %+v", valid)
	}

	// Missing scope/kind get their defaults; confidence is clamped.
	defaulted, err := ValidateCandidate(Candidate{Content: "release branch is release/2026"})
	if err != nil {
		t.Fatalf("ValidateCandidate(defaults): %v", err)
	}
	if defaulted.Scope != DefaultScope() || defaulted.Kind != DefaultKind() {
		t.Errorf("defaults = scope %q kind %q, want %q/%q",
			defaulted.Scope, defaulted.Kind, DefaultScope(), DefaultKind())
	}
	high, err := ValidateCandidate(Candidate{Content: "x", Confidence: 1.7})
	if err != nil || high.Confidence != 1 {
		t.Errorf("confidence clamp high = %+v, %v", high, err)
	}
	low, err := ValidateCandidate(Candidate{Content: "x", Confidence: -0.5})
	if err != nil || low.Confidence != 0 {
		t.Errorf("confidence clamp low = %+v, %v", low, err)
	}
	nan, err := ValidateCandidate(Candidate{Content: "x", Confidence: math.NaN()})
	if err != nil || nan.Confidence != 0 {
		t.Errorf("confidence NaN = %+v, %v", nan, err)
	}

	bad := []Candidate{
		{Content: ""},
		{Content: "   \n\t "},
		{Content: strings.Repeat("a", MaxContentBytes+1)},
		{Content: "ok", Scope: "personal"},
		{Content: "ok", Kind: "diary"},
		{Content: "nul\x00byte"},
	}
	for _, candidate := range bad {
		if _, err := ValidateCandidate(candidate); err == nil {
			t.Errorf("ValidateCandidate(%+v) succeeded, want an error", candidate)
		}
	}
}
