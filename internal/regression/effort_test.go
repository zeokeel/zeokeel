package regression

// Adversarial audit: reasoning-effort resolution. The effective effort is
// settings > module config > empty; invalid values (config or per-session
// setting) must omit the provider field rather than forward garbage. The
// provider boundary is asserted on the real httptest request body
// (top-level reasoning_effort).

import (
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/modules/agent"
)

// sendOne creates a session, sends one prompt and returns the provider request
// body of that run.
func sendOne(t *testing.T, harness *agentHarness, prompt string) []byte {
	t.Helper()
	sessionID := harness.newChatSession(t, "")
	marker := len(harness.model.callsSnapshot())
	harness.sendAndWait(t, sessionID, prompt)
	calls := harness.bodiesAfter(marker)
	if len(calls) != 1 {
		t.Fatalf("one run made %d provider calls, want 1", len(calls))
	}
	return calls[0].Body
}

// TestReasoningEffortFallbacks covers invalid/missing inputs: invalid config,
// invalid per-session values and absent settings all omit reasoning_effort.
func TestReasoningEffortFallbacks(t *testing.T) {
	t.Run("invalid module config is omitted", func(t *testing.T) {
		harness := startAgent(t, agent.Config{ReasoningEffort: "bogus"}, nil)
		body := sendOne(t, harness, "hello")
		if effort, found := requestReasoningEffort(t, body); found {
			t.Fatalf("reasoning_effort = %q, want the field omitted for an invalid config", effort)
		}
	})
	t.Run("empty config and no setting omit the field", func(t *testing.T) {
		harness := startAgent(t, agent.Config{}, nil)
		body := sendOne(t, harness, "hello")
		if effort, found := requestReasoningEffort(t, body); found {
			t.Fatalf("reasoning_effort = %q, want the field omitted with no config and no setting", effort)
		}
	})
	t.Run("invalid per-session setting omits the field", func(t *testing.T) {
		harness := startAgent(t, agent.Config{ReasoningEffort: "high"}, nil)
		sessionID := harness.newChatSession(t, "")
		if err := harness.h.settings.Set(contracts.SessionEffortSettingKey(sessionID), "ultra"); err != nil {
			t.Fatalf("set effort setting: %v", err)
		}
		marker := len(harness.model.callsSnapshot())
		harness.sendAndWait(t, sessionID, "hello")
		body := harness.bodiesAfter(marker)[0].Body
		if effort, found := requestReasoningEffort(t, body); found {
			t.Fatalf("reasoning_effort = %q for an invalid setting, want the field omitted", effort)
		}
	})
}

// TestReasoningEffortConfigDefault covers the module config default flowing to
// the provider request.
func TestReasoningEffortConfigDefault(t *testing.T) {
	for _, effort := range []string{"low", "medium", "high", "  HIGH  "} {
		harness := startAgent(t, agent.Config{ReasoningEffort: effort}, nil)
		body := sendOne(t, harness, "hello")
		got, found := requestReasoningEffort(t, body)
		if !found {
			t.Fatalf("reasoning_effort missing for config %q", effort)
		}
		want := effort
		if want == "  HIGH  " {
			want = "high"
		}
		if got != want {
			t.Fatalf("reasoning_effort = %q for config %q, want %q", got, effort, want)
		}
	}
}

// TestReasoningEffortSessionSettingOverride covers per-session settings winning
// over the module config, including trimming/case normalization.
func TestReasoningEffortSessionSettingOverride(t *testing.T) {
	cases := []struct {
		setting string
		want    string
	}{
		{"low", "low"},
		{"  MEDIUM  ", "medium"},
		{"High", "high"},
	}
	for _, testCase := range cases {
		harness := startAgent(t, agent.Config{ReasoningEffort: "low"}, nil)
		sessionID := harness.newChatSession(t, "")
		if err := harness.h.settings.Set(contracts.SessionEffortSettingKey(sessionID), testCase.setting); err != nil {
			t.Fatalf("set effort setting: %v", err)
		}
		marker := len(harness.model.callsSnapshot())
		harness.sendAndWait(t, sessionID, "hello")
		body := harness.bodiesAfter(marker)[0].Body
		got, found := requestReasoningEffort(t, body)
		if !found || got != testCase.want {
			t.Fatalf("setting %q produced reasoning_effort %q (found=%v), want %q", testCase.setting, got, found, testCase.want)
		}
	}
}

// TestReasoningEffortOtherSessionDoesNotLeak proves the override is scoped to
// one session: a second session keeps the module default.
func TestReasoningEffortOtherSessionDoesNotLeak(t *testing.T) {
	harness := startAgent(t, agent.Config{ReasoningEffort: "high"}, nil)
	first := harness.newChatSession(t, "")
	if err := harness.h.settings.Set(contracts.SessionEffortSettingKey(first), "low"); err != nil {
		t.Fatalf("set effort setting: %v", err)
	}
	marker := len(harness.model.callsSnapshot())
	harness.sendAndWait(t, first, "first")
	firstBody := harness.bodiesAfter(marker)[0].Body
	if got, _ := requestReasoningEffort(t, firstBody); got != "low" {
		t.Fatalf("first session reasoning_effort = %q, want low", got)
	}

	second := harness.newChatSession(t, "")
	marker = len(harness.model.callsSnapshot())
	harness.sendAndWait(t, second, "second")
	secondBody := harness.bodiesAfter(marker)[0].Body
	if got, _ := requestReasoningEffort(t, secondBody); got != "high" {
		t.Fatalf("second session reasoning_effort = %q, want the module default high", got)
	}
}
