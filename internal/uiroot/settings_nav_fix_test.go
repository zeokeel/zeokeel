package uiroot

// Frame tests for UX-16 (2026-11 wave D, the settings IA review's deferred
// step): the settings navigation marks WHICH page holds an unsaved draft (a
// small dot with an "未保存" accessible name on the nav row, not only the
// modal title's global badge), and the navigation search reports how many
// pages match ("N 个匹配") instead of filtering silently.

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts/uinative"
)

// TestSettingsNavMarksDirtyPage registers two pages, one with an unsaved draft,
// and asserts the dirty page's navigation row carries the "未保存" mark while
// the clean page's does not.
func TestSettingsNavMarksDirtyPage(t *testing.T) {
	r := newTestRoot(t)
	mustScreen(t, r, textScreen("home", "首页", "body-home", 10))
	mustPage(t, r, uinative.SettingsPageSpec{
		ID: "model", Section: "常用", Title: "模型", Order: 10,
		Build: func(c *ui.Context) { ui.Text(c, "model-body") },
		Dirty: func() bool { return true },
	})
	mustPage(t, r, uinative.SettingsPageSpec{
		ID: "behavior", Section: "常用", Title: "行为", Order: 20,
		Build: func(c *ui.Context) { ui.Text(c, "behavior-body") },
		Dirty: func() bool { return false },
	})
	tt := newTestTester(t, r, 1400, 900)
	tt.Frame()
	r.Navigator().ToggleSettings()
	tt.Frame()
	tt.Frame()

	var modelItem, behaviorItem string
	for _, info := range tt.AccessAll() {
		if info.Role != ui.RoleTreeItem {
			continue
		}
		if strings.HasPrefix(info.Name, "模型") {
			modelItem = info.Name
		}
		if strings.HasPrefix(info.Name, "行为") {
			behaviorItem = info.Name
		}
	}
	if modelItem == "" || behaviorItem == "" {
		t.Fatalf("settings nav rows missing; tree items model=%q behavior=%q", modelItem, behaviorItem)
	}
	if !strings.Contains(modelItem, "未保存") {
		t.Fatalf("UX-16: the dirty page's nav row has no unsaved mark: %q", modelItem)
	}
	if strings.Contains(behaviorItem, "未保存") {
		t.Fatalf("UX-16: the clean page's nav row is marked unsaved: %q", behaviorItem)
	}
}

// TestSettingsSearchShowsMatchCount asserts the navigation search reports the
// number of matching pages, including zero.
func TestSettingsSearchShowsMatchCount(t *testing.T) {
	r := newTestRoot(t)
	settingsSearchPages(t, r)
	tt := newTestTester(t, r, 1400, 900)
	tt.Frame()
	openSettingsSearch(t, r, tt)

	// "字号" matches only the behavior page's message-size item.
	settingsSearch(tt, "字号")
	requireHasText(t, tt, "1 个匹配")

	// A query nothing matches reports zero rather than filtering silently.
	settingsSearch(tt, "zzz-no-such-setting")
	requireHasText(t, tt, "0 个匹配")
}
