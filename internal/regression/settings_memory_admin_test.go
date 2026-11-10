package regression

// Adversarial audit: the settings Memory page's tidy and clear-all actions.
// Negative/boundary cases: dedupe keeps the earliest entry by normalized text and
// removes exactly the later ids, a clean store is a no-op, clear-all asks for
// confirmation (cancel keeps everything), and list/remove failures surface as
// readable errors instead of partial silent deletion.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/modules/memory"
	settingsui "github.com/zeokeel/zeokeel/internal/ui/settings"
)

// openMemoryAdmin opens the settings memory page over the supplied store (or a
// seeded real memory module when provided is nil).
func openMemoryAdmin(t *testing.T, seed []contracts.Memory, provided contracts.MemoryStore) (*harness, *ui.Tester) {
	t.Helper()
	h := newBareHarness(t)
	provide(t, h.ctx, contracts.KeyChat, contracts.ChatService(newFakeChat()))
	if provided != nil {
		provide(t, h.ctx, contracts.KeyMemory, provided)
	} else {
		if seed != nil {
			seedMemoryFile(t, h.dataDir, seed)
		}
		activate(t, h.ctx, memory.Module(), memory.Config{})
	}
	activate(t, h.ctx, settingsui.Module(), settingsui.Config{})
	h.ui.Navigator().ToggleSettings()
	tester := h.tester(1280, 800)
	tester.Frame()
	openSettingsPage(t, tester, 1280, 800, "记忆")
	return h, tester
}

// clickMemoryAdmin clicks one label and repaints.
func clickMemoryAdmin(t *testing.T, tester *ui.Tester, label string) {
	t.Helper()
	revealSettings(t, tester, label)
	if err := tester.Click(label); err != nil {
		t.Fatalf("click %q: %v; texts = %v", label, err, tester.Texts())
	}
	tester.Frame()
}

// memoryIDs returns the stored ids.
func memoryIDs(t *testing.T, store contracts.MemoryStore) []string {
	t.Helper()
	entries, err := store.List(context.Background())
	if err != nil {
		t.Fatalf("List memories: %v", err)
	}
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		ids = append(ids, entry.ID)
	}
	return ids
}

// memoryAdminSeed builds three entries: two normalized duplicates (earliest id
// wins) and one unique entry.
func memoryAdminSeed() []contracts.Memory {
	base := time.Unix(1_700_000_000, 0)
	return []contracts.Memory{
		{ID: "m-late", Text: "  BUY   milk ", Scope: "global", Created: base.Add(2 * time.Minute)},
		{ID: "m-early", Text: "Buy milk", Scope: "global", Created: base},
		{ID: "m-unique", Text: "Unique note", Scope: "global", Created: base.Add(time.Minute)},
	}
}

// TestSettingsMemoryTidyAndClear covers the successful admin flow: tidy removes
// exactly the later duplicates, a second tidy reports nothing to do, clear-all
// confirm/cancel behaves and the empty state returns.
func TestSettingsMemoryTidyAndClear(t *testing.T) {
	h, tester := openMemoryAdmin(t, memoryAdminSeed(), nil)
	store := requireService(t, h.ctx, contracts.KeyMemory)

	requireHasTexts(t, tester, "3 条记忆", "Buy milk", "BUY   milk", "Unique note")
	clickMemoryAdmin(t, tester, "去重并合并")
	requireHasText(t, tester, "已整理 1 条")
	ids := memoryIDs(t, store)
	if len(ids) != 2 {
		t.Fatalf("memories after tidy = %v, want 2", ids)
	}
	kept := map[string]bool{}
	for _, id := range ids {
		kept[id] = true
	}
	if !kept["m-early"] || !kept["m-unique"] || kept["m-late"] {
		t.Fatalf("tidy kept %v, want the earliest duplicate + the unique entry", ids)
	}

	// A second tidy has nothing to remove.
	clickMemoryAdmin(t, tester, "去重并合并")
	requireHasText(t, tester, "没有可整理的重复记忆")
	if got := len(memoryIDs(t, store)); got != 2 {
		t.Fatalf("memories after no-op tidy = %d, want 2", got)
	}

	// Clear-all: cancel keeps everything; confirm empties the store.
	clickMemoryAdmin(t, tester, "全部清空")
	requireHasTexts(t, tester, "清空全部记忆", "确认删除全部记忆？此操作无法撤销。")
	clickMemoryAdmin(t, tester, "取消")
	if got := len(memoryIDs(t, store)); got != 2 {
		t.Fatalf("memories after cancel = %d, want 2", got)
	}
	clickMemoryAdmin(t, tester, "全部清空")
	clickMemoryAdmin(t, tester, "清空")
	requireHasTexts(t, tester, "已清空", "0 条记忆", "还没有记忆")
	if got := memoryIDs(t, store); len(got) != 0 {
		t.Fatalf("memories after clear = %v, want none", got)
	}
}

// TestSettingsMemoryAdminErrors covers store failures: list errors surface on
// both actions, failed removes are counted and never look like success.
func TestSettingsMemoryAdminErrors(t *testing.T) {
	t.Run("list failure", func(t *testing.T) {
		fault := &faultMemoryStore{listErr: errors.New("memory read denied")}
		_, tester := openMemoryAdmin(t, nil, fault)
		requireHasText(t, tester, "读取记忆失败")
		clickMemoryAdmin(t, tester, "去重并合并")
		requireHasText(t, tester, "读取记忆失败")
		clickMemoryAdmin(t, tester, "全部清空")
		clickMemoryAdmin(t, tester, "清空")
		requireHasText(t, tester, "清空失败")
	})

	t.Run("remove failures counted", func(t *testing.T) {
		fault := &faultMemoryStore{entries: memoryAdminSeed(), failRemove: true}
		_, tester := openMemoryAdmin(t, nil, fault)
		clickMemoryAdmin(t, tester, "去重并合并")
		requireHasText(t, tester, "去重并合并失败：1")
		clickMemoryAdmin(t, tester, "全部清空")
		clickMemoryAdmin(t, tester, "清空")
		requireHasText(t, tester, "清空失败：3")
		if got := len(fault.entries); got != 3 {
			t.Fatalf("fault store entries after failed removes = %d, want 3", got)
		}
	})
}
