package regression

// Adversarial audit: deterministic memory consolidation (no model call). The
// store applies the expiry window first, then the entry cap (oldest removed),
// reports per-policy counts and persists the pruned file; the settings Memory
// page loads the saved policy, writes a new normalized policy and surfaces
// invalid input as an unlimited (0) policy.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/modules/memory"
	settingsui "github.com/zeokeel/zeokeel/internal/ui/settings"
)

// consolidationSeed builds five entries: two expired-ish, three recent.
func consolidationSeed(now time.Time) []contracts.Memory {
	return []contracts.Memory{
		{ID: "old-1", Text: "very old", Scope: "global", Created: now.Add(-40 * 24 * time.Hour)},
		{ID: "old-2", Text: "older", Scope: "global", Created: now.Add(-20 * 24 * time.Hour)},
		{ID: "new-1", Text: "recent one", Scope: "global", Created: now.Add(-3 * 24 * time.Hour)},
		{ID: "new-2", Text: "recent two", Scope: "global", Created: now.Add(-2 * 24 * time.Hour)},
		{ID: "new-3", Text: "newest", Scope: "global", Created: now.Add(-1 * 24 * time.Hour)},
	}
}

// readMemoryIDs reads the persisted memory file's IDs.
func readMemoryIDs(t *testing.T, dataDir string) []string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(dataDir, memory.DefaultFileName))
	if err != nil {
		t.Fatalf("read memory file: %v", err)
	}
	var document struct {
		Entries []contracts.Memory `json:"entries"`
	}
	if err := json.Unmarshal(body, &document); err != nil {
		t.Fatalf("decode memory file: %v", err)
	}
	ids := make([]string, 0, len(document.Entries))
	for _, entry := range document.Entries {
		ids = append(ids, entry.ID)
	}
	return ids
}

// TestMemoryConsolidationStore covers the store policy: zero policy keeps all,
// expiry removes strictly-old entries, the cap removes the oldest, and the
// result is persisted; a canceled context removes nothing.
func TestMemoryConsolidationStore(t *testing.T) {
	h := newBareHarness(t)
	now := time.Now().UTC()
	seedMemoryFile(t, h.dataDir, consolidationSeed(now))
	activate(t, h.ctx, memory.Module(), memory.Config{})
	store := requireService(t, h.ctx, contracts.KeyMemory)
	consolidator, ok := store.(contracts.MemoryConsolidator)
	if !ok {
		t.Fatal("the memory store does not implement MemoryConsolidator")
	}
	ctx := context.Background()

	report, err := consolidator.Consolidate(ctx, contracts.MemoryPolicy{})
	if err != nil {
		t.Fatalf("zero policy consolidate: %v", err)
	}
	if report.RemovedExpired != 0 || report.RemovedOverCap != 0 || report.Remaining != 5 {
		t.Fatalf("zero policy report = %+v, want all five kept", report)
	}

	report, err = consolidator.Consolidate(ctx, contracts.MemoryPolicy{MaxAge: 30 * 24 * time.Hour})
	if err != nil {
		t.Fatalf("age policy consolidate: %v", err)
	}
	if report.RemovedExpired != 1 || report.RemovedOverCap != 0 || report.Remaining != 4 {
		t.Fatalf("age policy report = %+v, want 1 expired / 4 remaining", report)
	}

	// The cap then removes the oldest entries until two remain.
	report, err = consolidator.Consolidate(ctx, contracts.MemoryPolicy{MaxEntries: 2})
	if err != nil {
		t.Fatalf("cap policy consolidate: %v", err)
	}
	if report.RemovedExpired != 0 || report.RemovedOverCap != 2 || report.Remaining != 2 {
		t.Fatalf("cap policy report = %+v, want 2 over cap / 2 remaining", report)
	}
	ids := readMemoryIDs(t, h.dataDir)
	if len(ids) != 2 || ids[0] != "new-2" || ids[1] != "new-3" {
		t.Fatalf("persisted IDs = %v, want the two newest", ids)
	}

	// A canceled context leaves the store untouched.
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := consolidator.Consolidate(canceled, contracts.MemoryPolicy{MaxEntries: 1}); err == nil {
		t.Fatal("canceled consolidate succeeded")
	}
	if ids = readMemoryIDs(t, h.dataDir); len(ids) != 2 {
		t.Fatalf("canceled consolidate removed entries: %v", ids)
	}
}

// startMemoryPolicyPage opens the settings Memory page with a saved policy.
func startMemoryPolicyPage(t *testing.T, seed []contracts.Memory, values map[string]string) (*harness, *ui.Tester) {
	t.Helper()
	h := newBareHarness(t)
	seedMemoryFile(t, h.dataDir, seed)
	activate(t, h.ctx, memory.Module(), memory.Config{})
	for key, value := range values {
		if err := h.settings.Set(key, value); err != nil {
			t.Fatalf("set %s: %v", key, err)
		}
	}
	provide(t, h.ctx, contracts.KeyChat, contracts.ChatService(newFakeChat()))
	activate(t, h.ctx, settingsui.Module(), settingsui.Config{})
	h.ui.Navigator().ToggleSettings()
	tester := h.tester(1280, 1100)
	tester.Frame()
	openSettingsPage(t, tester, 1280, 1100, "记忆")
	return h, tester
}

// TestMemoryConsolidationSettingsPage covers the page: a saved policy runs on
// demand and is persisted, and invalid numbers fall back to an unlimited policy
// with a warning.
func TestMemoryConsolidationSettingsPage(t *testing.T) {
	now := time.Now().UTC()
	h, tester := startMemoryPolicyPage(t, consolidationSeed(now), map[string]string{
		contracts.SettingMemoryMaxEntries: "2",
		contracts.SettingMemoryMaxAgeDays: "0",
	})
	requireHasTexts(t, tester, "整理策略", "最大条目数 输入", "保留天数 输入", "按策略整理")
	revealSettings(t, tester, "按策略整理")
	if err := tester.Click("按策略整理"); err != nil {
		t.Fatalf("consolidate: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	requireHasText(t, tester, "已整理：过期 0 / 超限 3 / 剩余 2")
	if got := h.settings.Get(contracts.SettingMemoryMaxEntries, ""); got != "2" {
		t.Fatalf("saved max entries = %q, want 2", got)
	}
	if got := len(readMemoryIDs(t, h.dataDir)); got != 2 {
		t.Fatalf("persisted entries after page consolidation = %d, want 2", got)
	}

	// The fields are official ui.NumberInputs: a typed number outside the
	// bounds is ignored (the field keeps its last valid value on blur), so the
	// page has no free-text "invalid draft" state and the saved policy stays
	// whatever the last valid value was.
	revealSettings(t, tester, "最大条目数 输入")
	rect, ok := tester.Find("最大条目数 输入")
	if !ok {
		t.Fatalf("max-entries number input not found; texts = %v", tester.Texts())
	}
	tester.ClickAt(rect.X+16, rect.Y+rect.H/2)
	tester.Frame()
	tester.Key(ui.Cmd, ui.KeyA)
	tester.Type("9999999") // above the 100000 bound: ignored
	tester.Frame()
	revealSettings(t, tester, "按策略整理")
	if err := tester.Click("按策略整理"); err != nil {
		t.Fatalf("consolidate after out-of-range typing: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	requireHasTexts(t, tester, "已整理：过期 0 / 超限 0 / 剩余 2")
	if got := h.settings.Get(contracts.SettingMemoryMaxEntries, ""); got != "2" {
		t.Fatalf("max entries saved as %q, want the last valid value 2", got)
	}
	if got := h.settings.Get(contracts.SettingMemoryMaxAgeDays, ""); got != "0" {
		t.Fatalf("max age saved as %q, want 0", got)
	}
}
