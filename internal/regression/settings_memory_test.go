package regression

// Adversarial audit: the settings Memory page (global memory management). The
// page resolves the optional backend per frame: without it the empty-state copy
// is shown; with the real memory module it lists/searches/adds/deletes entries
// and keeps the count/empty states correct.

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/modules/memory"
	settingsui "github.com/zeokeel/zeokeel/internal/ui/settings"
)

// openMemorySettings opens the settings surface on the memory page.
func openMemorySettings(t *testing.T, withBackend bool) (*harness, *ui.Tester) {
	t.Helper()
	h := newBareHarness(t)
	provide(t, h.ctx, contracts.KeyChat, contracts.ChatService(newFakeChat()))
	if withBackend {
		activate(t, h.ctx, memory.Module(), memory.Config{})
	}
	activate(t, h.ctx, settingsui.Module(), settingsui.Config{})
	h.ui.Navigator().ToggleSettings()
	tester := h.tester(1280, 800)
	tester.Frame()
	if err := tester.Click("记忆"); err != nil {
		t.Fatalf("open memory page: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	return h, tester
}

// clickMemorySettings clicks one label and repaints.
func clickMemorySettings(t *testing.T, tester *ui.Tester, label string) {
	t.Helper()
	if err := tester.Click(label); err != nil {
		t.Fatalf("click %q: %v; texts = %v", label, err, tester.Texts())
	}
	tester.Frame()
}

// TestSettingsMemoryPage covers the page: no-backend empty state, add/list,
// search and delete through the real memory module.
func TestSettingsMemoryPage(t *testing.T) {
	t.Run("no backend", func(t *testing.T) {
		_, tester := openMemorySettings(t, false)
		requireHasText(t, tester, "未启用记忆后端")
		requireNoText(t, tester, "添加记忆")
	})

	t.Run("add list search delete", func(t *testing.T) {
		_, tester := openMemorySettings(t, true)
		requireHasTexts(t, tester, "还没有记忆", "0 条记忆", "添加", "记忆内容")

		// Add one memory through the form.
		clickMemorySettings(t, tester, "记忆内容 输入")
		tester.Type("记住这次结论")
		tester.Frame()
		clickMemorySettings(t, tester, "添加")
		requireHasTexts(t, tester, "已添加", "1 条记忆", "记住这次结论", "全局")

		// Search narrows the list; no-match has its own empty state.
		clickMemorySettings(t, tester, "搜索记忆")
		tester.Type("Alpha")
		tester.Frame()
		requireHasText(t, tester, "没有匹配的记忆")
		clickMemorySettings(t, tester, "搜索记忆")
		tester.Key(ui.Cmd, ui.KeyA)
		tester.Key(0, ui.KeyBackspace)
		tester.Frame()
		requireHasText(t, tester, "记住这次结论")

		// Delete removes the entry and the count returns to zero.
		clickMemorySettings(t, tester, "删除")
		requireHasTexts(t, tester, "0 条记忆", "还没有记忆")
	})
}
