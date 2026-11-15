package agent

// Tests for the capability settings: the Subagent and Team member concurrency
// caps are read from contracts.SettingsService on every delegation (1–8), so a
// change applies to the next delegation without a restart, and an absent or
// invalid row keeps the module's own default.

import (
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// TestDelegationConcurrencyReadsLiveSettings covers the per-delegation read: the
// valid rows win, an invalid row falls back to the module constant, and a later
// settings change is visible to the next read (no cached snapshot).
func TestDelegationConcurrencyReadsLiveSettings(t *testing.T) {
	settings := newFakeSettings(map[string]string{
		contracts.SettingAgentSubagentMaxConcurrent: "6",
		contracts.SettingAgentTeamMaxConcurrent:     "1",
	})
	service := newTestService(t, Config{}, settings, nil)

	subagent, team := service.delegationConcurrency()
	if subagent != 6 || team != 1 {
		t.Fatalf("delegationConcurrency() = (%d, %d), want (6, 1) from the settings rows", subagent, team)
	}

	// A later change applies to the next delegation: nothing is cached.
	if err := settings.Set(contracts.SettingAgentSubagentMaxConcurrent, "3"); err != nil {
		t.Fatalf("Set subagent limit: %v", err)
	}
	if err := settings.Set(contracts.SettingAgentTeamMaxConcurrent, "7"); err != nil {
		t.Fatalf("Set team limit: %v", err)
	}
	subagent, team = service.delegationConcurrency()
	if subagent != 3 || team != 7 {
		t.Fatalf("delegationConcurrency() after change = (%d, %d), want (3, 7)", subagent, team)
	}
}

// TestDelegationConcurrencyFallsBack covers the absent and invalid rows: the
// module defaults stay in force instead of an out-of-range cap reaching rig.
func TestDelegationConcurrencyFallsBack(t *testing.T) {
	cases := []struct {
		name string
		rows map[string]string
	}{
		{name: "absent", rows: nil},
		{name: "empty", rows: map[string]string{
			contracts.SettingAgentSubagentMaxConcurrent: "",
			contracts.SettingAgentTeamMaxConcurrent:     "  ",
		}},
		{name: "out of range", rows: map[string]string{
			contracts.SettingAgentSubagentMaxConcurrent: "0",
			contracts.SettingAgentTeamMaxConcurrent:     "9",
		}},
		{name: "malformed", rows: map[string]string{
			contracts.SettingAgentSubagentMaxConcurrent: "many",
			contracts.SettingAgentTeamMaxConcurrent:     "-2",
		}},
	}
	for _, test := range cases {
		test := test
		t.Run(test.name, func(t *testing.T) {
			service := newTestService(t, Config{}, newFakeSettings(test.rows), nil)
			subagent, team := service.delegationConcurrency()
			if subagent != subagentMaxConcurrent || team != teamMaxConcurrent {
				t.Fatalf("delegationConcurrency() = (%d, %d), want the module defaults (%d, %d)",
					subagent, team, subagentMaxConcurrent, teamMaxConcurrent)
			}
		})
	}
}

// TestRunnerOptionsReadTheLiveCaps covers the two spawn paths: the runner
// options each delegation builds carry the live caps, and the depth/excluded
// tools stay as before.
func TestRunnerOptionsReadTheLiveCaps(t *testing.T) {
	settings := newFakeSettings(map[string]string{
		contracts.SettingAgentSubagentMaxConcurrent: "5",
		contracts.SettingAgentTeamMaxConcurrent:     "2",
	})
	service := newTestService(t, Config{}, settings, nil)

	subagentOptions := service.subagentRunnerOptions()
	if subagentOptions.MaxConcurrent != 5 {
		t.Fatalf("subagent MaxConcurrent = %d, want the live setting 5", subagentOptions.MaxConcurrent)
	}
	if subagentOptions.MaxDepth != subagentMaxDepth {
		t.Fatalf("subagent MaxDepth = %d, want %d", subagentOptions.MaxDepth, subagentMaxDepth)
	}
	if len(subagentOptions.ExcludedTools) == 0 {
		t.Fatalf("subagent runner options lost their excluded tools")
	}

	teamOptions := service.teamRunnerOptions()
	if teamOptions.MaxConcurrent != 2 {
		t.Fatalf("team member MaxConcurrent = %d, want the live setting 2", teamOptions.MaxConcurrent)
	}

	// The team and the synchronous subagent read independent rows.
	if err := settings.Set(contracts.SettingAgentSubagentMaxConcurrent, "1"); err != nil {
		t.Fatalf("Set subagent limit: %v", err)
	}
	if got := service.subagentRunnerOptions().MaxConcurrent; got != 1 {
		t.Fatalf("subagent MaxConcurrent after change = %d, want 1", got)
	}
	if got := service.teamRunnerOptions().MaxConcurrent; got != 2 {
		t.Fatalf("team MaxConcurrent followed the subagent row change: %d, want 2", got)
	}
}
