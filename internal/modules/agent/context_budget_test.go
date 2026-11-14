package agent

// Tests for the context safety defaults: the model-window budget derivation,
// the explicit-setting precedence and /compact working without a hand-set
// budget.

import (
	"context"
	"testing"

	rig "github.com/zeokeel/zeokeel-rig"
	rigtools "github.com/zeokeel/zeokeel-rig/tools"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

func TestDeriveContextBudget(t *testing.T) {
	cases := []struct {
		name   string
		window int
		maxOut int
		want   int
	}{
		{name: "unknown window disables the budget", window: 0, maxOut: 8192, want: 0},
		{name: "negative window disables the budget", window: -5, want: 0},
		// min(0.8*128000, 128000-8192-65536) = min(102400, 54272)
		{name: "128k window with 8k completion", window: 128000, maxOut: 8192, want: 54272},
		// A small window cannot hold the reserve: the 0.8 cap keeps a usable budget.
		{name: "tiny window falls back to the 0.8 cap", window: 32000, maxOut: 4096, want: 25600},
		// min(0.8*200000, 200000-0-65536) = min(160000, 134464)
		{name: "200k window with provider default completion", window: 200000, want: 134464},
		// The 0.8 cap wins when the completion is huge.
		{name: "huge completion still caps at 0.8", window: 100000, maxOut: 100000, want: 80000},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := deriveContextBudget(test.window, test.maxOut); got != test.want {
				t.Fatalf("deriveContextBudget(%d, %d) = %d, want %d", test.window, test.maxOut, got, test.want)
			}
		})
	}
}

// budgetService builds a service whose settings carry model "m1" with the given
// context window/completion cap (0 disables the row's fields).
func budgetService(t *testing.T, window, maxOut int, explicitBudget int) *service {
	t.Helper()
	values := map[string]string{contracts.SettingsChatModel: "m1"}
	if window > 0 || maxOut > 0 {
		values[contracts.SettingModelCatalogModels] = `[{"id":"m1","contextWindow":` + itoaTest(window) +
			`,"maxTokens":` + itoaTest(maxOut) + `}]`
	}
	if explicitBudget > 0 {
		values[contracts.SettingsChatContextBudget] = itoaTest(explicitBudget)
	}
	settings := newFakeSettings(values)
	return newService(Config{}, rigtools.NewRegistry(), contracts.SettingsService(settings),
		&fakeLogger{}, newFakeStore(), fakeApprovals{})
}

// itoaTest renders a non-negative int for the JSON fixture.
func itoaTest(value int) string {
	if value == 0 {
		return "0"
	}
	digits := make([]byte, 0, 8)
	for value > 0 {
		digits = append([]byte{byte('0' + value%10)}, digits...)
		value /= 10
	}
	return string(digits)
}

func TestEffectiveContextBudget(t *testing.T) {
	// No model window and no setting: no automatic budget.
	svc := budgetService(t, 0, 0, 0)
	if got := svc.effectiveContextBudget(svc.Config()); got != 0 {
		t.Fatalf("budget without a window = %d, want 0", got)
	}

	// Derived from the model catalog when the user never set a budget.
	svc = budgetService(t, 128000, 8192, 0)
	if got := svc.effectiveContextBudget(svc.Config()); got != 54272 {
		t.Fatalf("derived budget = %d, want 54272", got)
	}

	// An explicit setting always wins over the derivation.
	svc = budgetService(t, 128000, 8192, 50000)
	if got := svc.effectiveContextBudget(svc.Config()); got != 50000 {
		t.Fatalf("explicit budget = %d, want 50000", got)
	}

	// An explicitly saved 0 disables the automatic budget (settings "0 = off"),
	// while an absent row still derives it.
	settings := newFakeSettings(map[string]string{
		contracts.SettingsChatModel:         "m1",
		contracts.SettingModelCatalogModels: `[{"id":"m1","contextWindow":128000,"maxTokens":8192}]`,
		contracts.SettingsChatContextBudget: "0",
	})
	disabled := newService(Config{}, rigtools.NewRegistry(), contracts.SettingsService(settings),
		&fakeLogger{}, newFakeStore(), fakeApprovals{})
	if got := disabled.effectiveContextBudget(disabled.Config()); got != 0 {
		t.Fatalf("explicit 0 budget = %d, want the automatic budget disabled", got)
	}
}

// TestCompactNowUsesADerivedBudget covers /compact for a user who never set
// chat.contextBudget: the model window alone makes the command available and
// the compactor receives the derived budget.
func TestCompactNowUsesADerivedBudget(t *testing.T) {
	compactor := &fakeCompactor{messages: compactTestMessages(2)}
	store := newFakeStore()
	settings := newFakeSettings(map[string]string{
		contracts.SettingsChatModel:         "m1",
		contracts.SettingModelCatalogModels: `[{"id":"m1","contextWindow":128000,"maxTokens":8192}]`,
	})
	svc := newService(Config{}, rigtools.NewRegistry(), contracts.SettingsService(settings),
		&fakeLogger{}, store, fakeApprovals{})
	svc.compactorFactory = func(contracts.ChatConfig) (rig.Compactor, error) { return compactor, nil }
	sessionID, err := svc.NewSession("")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := store.ReplaceHistory(context.Background(), sessionID, compactTestMessages(4)); err != nil {
		t.Fatalf("ReplaceHistory: %v", err)
	}
	result, err := svc.CompactNow(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("CompactNow with a derived budget: %v", err)
	}
	if result.Replaced != 4 || result.Saved != 2 {
		t.Fatalf("result = %+v, want 4 -> 2", result)
	}
	if compactor.calls != 1 || compactor.budget != 54272 {
		t.Fatalf("compactor calls/budget = %d/%d, want 1/54272", compactor.calls, compactor.budget)
	}
}

// TestCompactNowStillUnavailableWithoutAnyBudget keeps the disabled case: an
// unknown context window and no setting means no compaction.
func TestCompactNowStillUnavailableWithoutAnyBudget(t *testing.T) {
	compactor := &fakeCompactor{messages: compactTestMessages(1)}
	svc := budgetService(t, 0, 0, 0)
	svc.compactorFactory = func(contracts.ChatConfig) (rig.Compactor, error) { return compactor, nil }
	sessionID, err := svc.NewSession("")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if _, err := svc.CompactNow(context.Background(), sessionID); err == nil {
		t.Fatal("CompactNow without any budget succeeded")
	}
	if compactor.calls != 0 {
		t.Fatalf("compactor called %d times without a budget", compactor.calls)
	}
}
